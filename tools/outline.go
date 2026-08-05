package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const outlineMaxLineLen = 160

type outlineRequest struct {
	FilePath string `json:"file_path"`
	Grouped  bool   `json:"grouped,omitempty"`
}

// outlineKind classifies a top-level declaration so the outline can be
// rendered grouped (types / interfaces / functions / vars / consts) or
// indented (method nested under its receiver type).
type outlineKind int

const (
	kindOther outlineKind = iota
	kindType
	kindStruct
	kindInterface
	kindEnum
	kindClass
	kindTrait
	kindMixin
	kindExtension
	kindProtocol
	kindModule
	kindNamespace
	kindFunc
	kindMethod
	kindConst
	kindVar
	kindProperty
	kindImpl
)

func (k outlineKind) groupLabel() string {
	switch k {
	case kindType, kindStruct, kindInterface, kindEnum, kindClass, kindTrait, kindMixin, kindExtension, kindProtocol, kindModule, kindNamespace:
		return "types"
	case kindFunc, kindMethod:
		return "functions"
	case kindConst:
		return "consts"
	case kindVar:
		return "vars"
	case kindProperty:
		return "properties"
	case kindImpl:
		return "impls"
	default:
		return "other"
	}
}

// NewOutline returns a tool that emits a compact top-level structure outline
// (functions, types, classes, etc. with line numbers) for a single source file.
// Use for fast triage instead of reading the whole file.
func NewOutline() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "outline",
			Description: "Return a code-structure outline of a single source file: " +
				"top-level declarations (functions, types, classes, interfaces, enums, methods) with line numbers. " +
				"Much cheaper than 'read' for triage. " +
				"Go uses AST (precise, includes methods nested under their receiver, const/var groups). " +
				"Pattern matching covers: py, js/ts/tsx/jsx/mjs/cjs/vue/svelte, dart, java, kt, rs, rb, swift, c/cpp/h/hpp, " +
				"php, lua, scala, zig, sh/bash, sql, ex/exs, html.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to a source file (not a directory).",
					},
					"grouped": map[string]any{
						"type":        "boolean",
						"description": "If true, group output by category (types / functions / vars / consts). Default false = ordered by line.",
					},
				},
				"required":             []string{"file_path"},
				"additionalProperties": false,
			},
		},
		Handler: outlineTool,
	}
}

func outlineTool(ctx context.Context, input string) (string, error) {
	var req outlineRequest
	if err := decodeToolRequest(input, "outline", &req); err != nil {
		return "", err
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	_, target, relativePath, err := resolveToolPath(ctx, req.FilePath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory; outline accepts files only")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(target))
	var entries []outlineEntry
	lang := languageForExt(ext)
	if ext == ".go" {
		entries = outlineGo(target, data)
	}
	if len(entries) == 0 {
		entries = outlineRegex(data, ext)
	}

	return formatOutline(relativePath, lang, data, entries, req.Grouped), nil
}

type outlineEntry struct {
	Line   int
	Text   string
	Kind   outlineKind
	Parent string // for methods/fields nested under a type
	Indent int    // structural depth for indented rendering
}

// ---------------------------------------------------------------------------
// Go (AST)
// ---------------------------------------------------------------------------

func outlineGo(filename string, data []byte) []outlineEntry {
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, filename, data, parser.AllErrors)
	if file == nil {
		return nil
	}
	var entries []outlineEntry

	// Pass 1: types and top-level vars/consts.
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch gd.Tok {
		case token.TYPE:
			for _, spec := range gd.Specs {
				s := spec.(*ast.TypeSpec)
				pos := fset.Position(s.Pos())
				kind := kindType
				text := "type " + s.Name.Name
				switch tt := s.Type.(type) {
				case *ast.StructType:
					kind = kindStruct
					text = "struct " + s.Name.Name + fieldSummary(tt)
				case *ast.InterfaceType:
					kind = kindInterface
					text = "interface " + s.Name.Name + interfaceMethodSummary(tt)
				}
				entries = append(entries, outlineEntry{Line: pos.Line, Text: text, Kind: kind})
			}
		case token.CONST, token.VAR:
			kind := kindConst
			if gd.Tok == token.VAR {
				kind = kindVar
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					pos := fset.Position(n.Pos())
					entries = append(entries, outlineEntry{
						Line: pos.Line,
						Text: gd.Tok.String() + " " + n.Name,
						Kind: kind,
					})
				}
			}
		default:
			continue
		}
	}

	// Pass 2: functions and methods (methods carry their receiver as Parent).
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		pos := fset.Position(fd.Pos())
		kind := kindFunc
		parent := ""
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			kind = kindMethod
			parent = receiverTypeName(fd.Recv.List[0].Type)
		}
		entries = append(entries, outlineEntry{
			Line:   pos.Line,
			Text:   formatGoFunc(fd),
			Kind:   kind,
			Parent: parent,
			Indent: indentForKind(kind),
		})
	}
	return entries
}

func indentForKind(k outlineKind) int {
	if k == kindMethod {
		return 1
	}
	return 0
}

func receiverTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
}

func fieldSummary(st *ast.StructType) string {
	if st.Fields == nil || len(st.Fields.List) == 0 {
		return " {}"
	}
	n := 0
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			n++
			continue
		}
		n += len(f.Names)
	}
	return fmt.Sprintf(" { %d fields }", n)
}

func interfaceMethodSummary(it *ast.InterfaceType) string {
	if it.Methods == nil || len(it.Methods.List) == 0 {
		return " {}"
	}
	return fmt.Sprintf(" { %d methods }", len(it.Methods.List))
}

func formatGoFunc(fd *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		b.WriteString("(")
		b.WriteString(exprString(fd.Recv.List[0].Type))
		b.WriteString(") ")
	}
	b.WriteString(fd.Name.Name)
	b.WriteString("(")
	if fd.Type.Params != nil {
		first := true
		for _, p := range fd.Type.Params.List {
			if !first {
				b.WriteString(", ")
			}
			first = false
			for j, n := range p.Names {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(n.Name)
			}
			if len(p.Names) > 0 {
				b.WriteString(" ")
			}
			b.WriteString(exprString(p.Type))
		}
	}
	b.WriteString(")")
	if fd.Type.Results != nil && len(fd.Type.Results.List) > 0 {
		b.WriteString(" ")
		multi := len(fd.Type.Results.List) > 1
		if multi {
			b.WriteString("(")
		}
		for i, r := range fd.Type.Results.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(r.Type))
		}
		if multi {
			b.WriteString(")")
		}
	}
	return b.String()
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func(...)"
	case *ast.Ellipsis:
		return "..." + exprString(t.Elt)
	case *ast.ChanType:
		return "chan " + exprString(t.Value)
	case *ast.IndexExpr:
		return exprString(t.X) + "[" + exprString(t.Index) + "]"
	case *ast.IndexListExpr:
		parts := make([]string, 0, len(t.Indices))
		for _, idx := range t.Indices {
			parts = append(parts, exprString(idx))
		}
		return exprString(t.X) + "[" + strings.Join(parts, ", ") + "]"
	}
	return "..."
}

// ---------------------------------------------------------------------------
// Regex-based language patterns
// ---------------------------------------------------------------------------

type patternRule struct {
	re   *regexp.Regexp
	kind outlineKind
}

var (
	jsRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?(?:abstract\s+)?class\s+(\w+)`), kindClass},
		{regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s*\*?\s*(\w+)`), kindFunc},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?\(`), kindFunc},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?function`), kindFunc},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=`), kindVar},
		// class methods (must be inside a class — we mark them as method, indented)
		{regexp.MustCompile(`^\s{2,}(?:public\s+|private\s+|protected\s+|static\s+|async\s+|#)?(?:get\s+|set\s+)?(\w+)\s*\([^)]*\)\s*\{`), kindMethod},
	}
	tsRules = append([]patternRule{
		{regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?(?:abstract\s+)?interface\s+(\w+)`), kindInterface},
		{regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?type\s+(\w+)`), kindType},
		{regexp.MustCompile(`^\s*(?:export\s+(?:default\s+)?)?enum\s+(\w+)`), kindEnum},
		{regexp.MustCompile(`^\s*(?:export\s+)?namespace\s+(\w+)`), kindNamespace},
	}, jsRules...)
	cRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:typedef\s+)?struct\s+\w+`), kindStruct},
		{regexp.MustCompile(`^\s*(?:typedef\s+)?(?:enum|union)\s+\w+`), kindEnum},
		{regexp.MustCompile(`^\s*class\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*namespace\s+\w+`), kindNamespace},
		{regexp.MustCompile(`^\s*template\s*<[^>]+>\s*$`), kindOther},
		{regexp.MustCompile(`^\s*(?:static\s+|extern\s+|inline\s+|virtual\s+|explicit\s+)*[\w\*\s:&<>,]{1,60}\s+\w+\s*\([^;]*\)\s*(?:const\s*)?(?:override\s*)?(?:noexcept\s*)?\{?\s*$`), kindFunc},
	}
	goRegexRules = []patternRule{
		{regexp.MustCompile(`^\s*func\s+\([^)]+\)\s+\w+`), kindMethod},
		{regexp.MustCompile(`^\s*func\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*type\s+\w+\s+struct\b`), kindStruct},
		{regexp.MustCompile(`^\s*type\s+\w+\s+interface\b`), kindInterface},
		{regexp.MustCompile(`^\s*type\s+\w+`), kindType},
		{regexp.MustCompile(`^\s*const\s+\w+`), kindConst},
		{regexp.MustCompile(`^\s*var\s+\w+`), kindVar},
	}
	pyRules = []patternRule{
		{regexp.MustCompile(`^\s*class\s+\w+`), kindClass},
		// decorators on the previous line are accepted by being looser about leading whitespace
		{regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+\w+`), kindFunc}, // indented = method, will be relabeled below
	}
	dartRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:abstract\s+|sealed\s+|base\s+|final\s+|interface\s+)?class\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*mixin\s+\w+`), kindMixin},
		{regexp.MustCompile(`^\s*enum\s+\w+`), kindEnum},
		{regexp.MustCompile(`^\s*extension\s+\w+`), kindExtension},
		{regexp.MustCompile(`^\s*typedef\s+\w+`), kindType},
		{regexp.MustCompile(`^\s*(?:Future<[^>]*>|Stream<[^>]*>|void|[A-Z]\w*(?:<[^>]*>)?|\w+)\s+(?:get\s+)?\w+\s*\(`), kindFunc},
	}
	javaRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:abstract\s+)?(?:final\s+)?(?:sealed\s+)?(?:class|record)\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?interface\s+\w+`), kindInterface},
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?enum\s+\w+`), kindEnum},
		{regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|private|protected)\s+(?:static\s+)?(?:final\s+)?(?:synchronized\s+)?(?:<[^>]+>\s+)?[\w<>\[\],?\s]+\s+\w+\s*\(`), kindFunc},
	}
	ktRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:public|private|internal|protected)?\s*(?:open\s+|abstract\s+|sealed\s+|data\s+|inner\s+|value\s+)?(?:class|interface|object|enum\s+class)\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*typealias\s+\w+`), kindType},
		{regexp.MustCompile(`^\s*(?:public|private|internal|protected)?\s*(?:override\s+|suspend\s+|inline\s+|operator\s+|infix\s+)*fun\s+(?:<[^>]+>\s+)?\w+`), kindFunc},
		{regexp.MustCompile(`^\s*(?:public|private|internal|protected)?\s*(?:val|var)\s+\w+`), kindVar},
	}
	rustRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:pub\s+(?:\([^)]*\)\s+)?)?(?:async\s+)?(?:unsafe\s+)?(?:const\s+)?fn\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+\w+`), kindStruct},
		{regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+\w+`), kindEnum},
		{regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+\w+`), kindTrait},
		{regexp.MustCompile(`^\s*(?:pub\s+)?(?:type|mod|union)\s+\w+`), kindType},
		{regexp.MustCompile(`^\s*(?:pub\s+)?const\s+\w+`), kindConst},
		{regexp.MustCompile(`^\s*(?:pub\s+)?static\s+\w+`), kindVar},
		{regexp.MustCompile(`^\s*impl(?:<[^>]+>)?\s+`), kindImpl},
		{regexp.MustCompile(`^\s*macro_rules!\s+\w+`), kindFunc},
	}
	rubyRules = []patternRule{
		{regexp.MustCompile(`^\s*class\s+\w[\w:]*`), kindClass},
		{regexp.MustCompile(`^\s*module\s+\w[\w:]*`), kindModule},
		{regexp.MustCompile(`^\s*def\s+(?:self\.)?\w[\w?!=]*`), kindFunc},
	}
	swiftRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*(?:final\s+)?class\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*struct\s+\w+`), kindStruct},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*enum\s+\w+`), kindEnum},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*protocol\s+\w+`), kindProtocol},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*extension\s+\w+`), kindExtension},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*actor\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*(?:static\s+|class\s+)?func\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*(?:public|private|internal|fileprivate|open)?\s*(?:static\s+)?(?:let|var)\s+\w+`), kindVar},
	}
	phpRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?class\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*interface\s+\w+`), kindInterface},
		{regexp.MustCompile(`^\s*trait\s+\w+`), kindTrait},
		{regexp.MustCompile(`^\s*enum\s+\w+`), kindEnum},
		{regexp.MustCompile(`^\s*namespace\s+[\w\\]+`), kindNamespace},
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?function\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*(?:public|private|protected)\s+(?:static\s+)?(?:readonly\s+)?(?:\?\w+\s+)?\$\w+`), kindProperty},
	}
	luaRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:local\s+)?function\s+[\w.:]+`), kindFunc},
		{regexp.MustCompile(`^\s*[\w.]+\s*=\s*function\s*\(`), kindFunc},
		{regexp.MustCompile(`^\s*(?:local\s+)?(\w+)\s*=\s*\{`), kindVar},
	}
	scalaRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:final\s+|abstract\s+|sealed\s+|case\s+)*class\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*(?:case\s+)?object\s+\w+`), kindClass},
		{regexp.MustCompile(`^\s*trait\s+\w+`), kindTrait},
		{regexp.MustCompile(`^\s*(?:enum|type)\s+\w+`), kindType},
		{regexp.MustCompile(`^\s*(?:override\s+|implicit\s+|private\s+|protected\s+)*(?:def|val|var)\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*package\s+[\w.]+`), kindNamespace},
	}
	zigRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*(?:pub\s+)?const\s+\w+\s*=\s*(?:struct|enum|union)`), kindStruct},
		{regexp.MustCompile(`^\s*(?:pub\s+)?const\s+\w+`), kindConst},
		{regexp.MustCompile(`^\s*(?:pub\s+)?var\s+\w+`), kindVar},
	}
	shellRules = []patternRule{
		{regexp.MustCompile(`^\s*(?:function\s+)?(\w+)\s*\(\)\s*\{`), kindFunc},
		{regexp.MustCompile(`^\s*function\s+(\w+)`), kindFunc},
	}
	sqlRules = []patternRule{
		{regexp.MustCompile(`(?i)^\s*create\s+(?:or\s+replace\s+)?(?:temporary\s+)?(?:table|view|materialized\s+view)\s+[\w\.]+`), kindType},
		{regexp.MustCompile(`(?i)^\s*create\s+(?:or\s+replace\s+)?(?:function|procedure|trigger)\s+[\w\.]+`), kindFunc},
		{regexp.MustCompile(`(?i)^\s*create\s+(?:unique\s+)?index\s+[\w\.]+`), kindOther},
	}
	elixirRules = []patternRule{
		{regexp.MustCompile(`^\s*defmodule\s+[\w.]+`), kindModule},
		{regexp.MustCompile(`^\s*defprotocol\s+[\w.]+`), kindProtocol},
		{regexp.MustCompile(`^\s*defimpl\s+[\w.]+`), kindImpl},
		{regexp.MustCompile(`^\s*defstruct\s+`), kindStruct},
		{regexp.MustCompile(`^\s*defmacro\s+\w+`), kindFunc},
		{regexp.MustCompile(`^\s*defp?\s+\w[\w?!]*`), kindFunc},
	}
	vueRules = []patternRule{
		{regexp.MustCompile(`^\s*<script[^>]*>`), kindOther},
		{regexp.MustCompile(`^\s*<template[^>]*>`), kindOther},
		{regexp.MustCompile(`^\s*<style[^>]*>`), kindOther},
		{regexp.MustCompile(`^\s*(?:export\s+default\s+)?defineComponent\b`), kindClass},
		{regexp.MustCompile(`^\s*function\s+(\w+)`), kindFunc},
		{regexp.MustCompile(`^\s*const\s+(\w+)\s*=\s*(?:defineProps|defineEmits|computed|ref|reactive)\b`), kindVar},
	}
	htmlRules = []patternRule{
		{regexp.MustCompile(`(?i)^\s*<(?:section|article|nav|main|header|footer|aside)\b[^>]*(?:id|class)=`), kindOther},
		{regexp.MustCompile(`(?i)^\s*<script\b`), kindOther},
		{regexp.MustCompile(`(?i)^\s*<style\b`), kindOther},
	}
)

var outlinePatterns = map[string][]patternRule{
	".go":     goRegexRules,
	".py":     pyRules,
	".pyi":    pyRules,
	".js":     jsRules,
	".jsx":    jsRules,
	".mjs":    jsRules,
	".cjs":    jsRules,
	".ts":     tsRules,
	".tsx":    tsRules,
	".mts":    tsRules,
	".cts":    tsRules,
	".dart":   dartRules,
	".java":   javaRules,
	".kt":     ktRules,
	".kts":    ktRules,
	".rs":     rustRules,
	".rb":     rubyRules,
	".swift":  swiftRules,
	".c":      cRules,
	".cpp":    cRules,
	".cc":     cRules,
	".cxx":    cRules,
	".h":      cRules,
	".hpp":    cRules,
	".hh":     cRules,
	".php":    phpRules,
	".lua":    luaRules,
	".scala":  scalaRules,
	".sc":     scalaRules,
	".zig":    zigRules,
	".sh":     shellRules,
	".bash":   shellRules,
	".zsh":    shellRules,
	".ksh":    shellRules,
	".sql":    sqlRules,
	".ex":     elixirRules,
	".exs":    elixirRules,
	".vue":    vueRules,
	".svelte": vueRules,
	".html":   htmlRules,
	".htm":    htmlRules,
}

func outlineRegex(data []byte, ext string) []outlineEntry {
	patterns, ok := outlinePatterns[ext]
	if !ok {
		return nil
	}

	commentPrefixes := commentPrefixesFor(ext)
	indentTracksScope := indentScopeFor(ext)
	scopeStack := []scopeFrame{}

	var entries []outlineEntry
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || hasAnyPrefix(trimmed, commentPrefixes) {
			continue
		}

		curIndent := leadingIndent(text)
		if indentTracksScope {
			for len(scopeStack) > 0 && curIndent <= scopeStack[len(scopeStack)-1].indent {
				scopeStack = scopeStack[:len(scopeStack)-1]
			}
		}

		for _, p := range patterns {
			if !p.re.MatchString(text) {
				continue
			}
			kind := p.kind
			parent := ""
			indent := 0
			if indentTracksScope && len(scopeStack) > 0 {
				parent = scopeStack[len(scopeStack)-1].name
				indent = scopeStack[len(scopeStack)-1].depth + 1
				if kind == kindFunc {
					kind = kindMethod
				}
			}
			entries = append(entries, outlineEntry{
				Line:   line,
				Text:   trimOutlineLine(text),
				Kind:   kind,
				Parent: parent,
				Indent: indent,
			})
			if indentTracksScope && isScopeOpening(kind) {
				scopeStack = append(scopeStack, scopeFrame{
					indent: curIndent,
					depth:  len(scopeStack),
					name:   extractName(trimmed),
				})
			}
			break
		}
	}
	return entries
}

type scopeFrame struct {
	indent int
	depth  int
	name   string
}

func isScopeOpening(k outlineKind) bool {
	switch k {
	case kindClass, kindStruct, kindInterface, kindEnum, kindTrait, kindMixin, kindExtension, kindProtocol, kindModule, kindNamespace, kindImpl:
		return true
	}
	return false
}

func indentScopeFor(ext string) bool {
	switch ext {
	case ".py", ".pyi":
		return true
	}
	return false
}

func commentPrefixesFor(ext string) []string {
	switch ext {
	case ".py", ".pyi", ".rb", ".sh", ".bash", ".zsh", ".ksh", ".ex", ".exs":
		return []string{"#"}
	case ".lua":
		return []string{"--"}
	case ".sql":
		return []string{"--"}
	case ".html", ".htm":
		return []string{"<!--"}
	case ".vue", ".svelte":
		return []string{"<!--", "//"}
	}
	return []string{"//", "#"}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func leadingIndent(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

var nameAfterKeyword = regexp.MustCompile(`(?:class|struct|interface|enum|trait|mixin|extension|protocol|module|namespace|impl|def|fn|func|function|defmodule|defprotocol|defimpl)\s+([\w:]+)`)

func extractName(s string) string {
	m := nameAfterKeyword.FindStringSubmatch(s)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func trimOutlineLine(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > outlineMaxLineLen {
		s = s[:outlineMaxLineLen] + "…"
	}
	return s
}

func languageForExt(ext string) string {
	m := map[string]string{
		".go": "Go", ".py": "Python", ".pyi": "Python (stub)",
		".js": "JavaScript", ".jsx": "JavaScript (JSX)",
		".ts": "TypeScript", ".tsx": "TypeScript (TSX)",
		".mjs": "JavaScript (ESM)", ".cjs": "JavaScript (CJS)",
		".mts": "TypeScript (ESM)", ".cts": "TypeScript (CJS)",
		".dart": "Dart", ".java": "Java", ".kt": "Kotlin", ".kts": "Kotlin script",
		".rs": "Rust", ".rb": "Ruby", ".swift": "Swift",
		".c": "C", ".cpp": "C++", ".cc": "C++", ".cxx": "C++",
		".h": "C/C++ header", ".hpp": "C++ header", ".hh": "C++ header",
		".php": "PHP", ".lua": "Lua",
		".scala": "Scala", ".sc": "Scala script",
		".zig": "Zig",
		".sh":  "Shell", ".bash": "Bash", ".zsh": "Zsh", ".ksh": "Ksh",
		".sql": "SQL",
		".ex":  "Elixir", ".exs": "Elixir script",
		".vue": "Vue SFC", ".svelte": "Svelte",
		".html": "HTML", ".htm": "HTML",
	}
	if v, ok := m[ext]; ok {
		return v
	}
	return "Unknown"
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func formatOutline(path, lang string, data []byte, entries []outlineEntry, grouped bool) string {
	totalLines := bytes.Count(data, []byte{'\n'}) + 1
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s (%s, %d lines)\n", path, lang, totalLines)
	if len(entries) == 0 {
		b.WriteString("(no top-level declarations detected)\n")
		return b.String()
	}
	if grouped {
		renderGrouped(&b, entries)
	} else {
		renderFlat(&b, entries)
	}
	return b.String()
}

func renderFlat(b *strings.Builder, entries []outlineEntry) {
	for _, e := range entries {
		prefix := strings.Repeat("  ", e.Indent)
		_, _ = fmt.Fprintf(b, "%d\t%s%s\n", e.Line, prefix, e.Text)
	}
}

func renderGrouped(b *strings.Builder, entries []outlineEntry) {
	groups := map[string][]outlineEntry{}
	order := []string{"types", "impls", "functions", "properties", "vars", "consts", "other"}
	for _, e := range entries {
		g := e.Kind.groupLabel()
		groups[g] = append(groups[g], e)
	}
	for _, g := range order {
		items := groups[g]
		if len(items) == 0 {
			continue
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].Line < items[j].Line })
		_, _ = fmt.Fprintf(b, "# %s\n", g)
		for _, e := range items {
			prefix := strings.Repeat("  ", e.Indent)
			_, _ = fmt.Fprintf(b, "%d\t%s%s\n", e.Line, prefix, e.Text)
		}
	}
}
