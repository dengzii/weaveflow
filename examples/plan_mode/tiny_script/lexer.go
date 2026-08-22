package tinyscript

import (
	"fmt"
	"unicode"
)

// Token types.
type TokenKind int

const (
	TkEOF TokenKind = iota
	TkInvalid
	TkIdent
	TkInt
	TkBool
	TkString
	TkVar
	TkFunc
	TkIf
	TkElse
	TkWhile
	TkReturn
	TkIntType
	TkBoolType
	TkStringType
	TkAssign    // =
	TkEq        // ==
	TkNe        // !=
	TkLt        // <
	TkLe        // <=
	TkGt        // >
	TkGe        // >=
	TkPlus      // +
	TkMinus     // -
	TkStar      // *
	TkSlash     // /
	TkAnd       // &&
	TkOr        // ||
	TkNot       // !
	TkLParen    // (
	TkRParen    // )
	TkLBrace    // {
	TkRBrace    // }
	TkComma     // ,
	TkSemicolon // ;
	TkColon     // :
)

var tokenNames = map[TokenKind]string{
	TkEOF:        "EOF",
	TkInvalid:    "invalid token",
	TkIdent:      "identifier",
	TkInt:        "int literal",
	TkBool:       "bool literal",
	TkString:     "string literal",
	TkVar:        "var",
	TkFunc:       "func",
	TkIf:         "if",
	TkElse:       "else",
	TkWhile:      "while",
	TkReturn:     "return",
	TkIntType:    "int",
	TkBoolType:   "bool",
	TkStringType: "string",
	TkAssign:     "=",
	TkEq:         "==",
	TkNe:         "!=",
	TkLt:         "<",
	TkLe:         "<=",
	TkGt:         ">",
	TkGe:         ">=",
	TkPlus:       "+",
	TkMinus:      "-",
	TkStar:       "*",
	TkSlash:      "/",
	TkAnd:        "&&",
	TkOr:         "||",
	TkNot:        "!",
	TkLParen:     "(",
	TkRParen:     ")",
	TkLBrace:     "{",
	TkRBrace:     "}",
	TkComma:      ",",
	TkSemicolon:  ";",
	TkColon:      ":",
}

func (k TokenKind) String() string {
	if s, ok := tokenNames[k]; ok {
		return s
	}
	return fmt.Sprintf("<tk %d>", int(k))
}

type Token struct {
	Kind    TokenKind
	Literal string
	Pos     Pos
}

type Lexer struct {
	input []rune
	pos   int
	line  int
	col   int
	tok   Token
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input: []rune(input),
		line:  1,
	}
}

func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()
	pos := Pos{Line: l.line, Col: l.col}
	if l.pos >= len(l.input) {
		l.tok = Token{Kind: TkEOF, Pos: pos}
		return l.tok
	}
	ch := l.input[l.pos]

	// Identifiers and keywords.
	if unicode.IsLetter(ch) || ch == '_' {
		start := l.pos
		for l.pos < len(l.input) && (unicode.IsLetter(l.input[l.pos]) || unicode.IsDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
			l.pos++
		}
		word := string(l.input[start:l.pos])
		l.col += l.pos - start
		kind := lookupKeyword(word)
		return Token{Kind: kind, Literal: word, Pos: pos}
	}

	// Numbers.
	if unicode.IsDigit(ch) {
		start := l.pos
		for l.pos < len(l.input) && unicode.IsDigit(l.input[l.pos]) {
			l.pos++
		}
		l.col += l.pos - start
		return Token{Kind: TkInt, Literal: string(l.input[start:l.pos]), Pos: pos}
	}

	// Strings.
	if ch == '"' {
		l.pos++ // skip opening quote
		l.col++
		var buf []rune
		for l.pos < len(l.input) {
			if l.input[l.pos] == '"' {
				l.pos++
				l.col++
				return Token{Kind: TkString, Literal: string(buf), Pos: pos}
			}
			if l.input[l.pos] == '\\' && l.pos+1 < len(l.input) {
				l.pos++
				l.col++
				switch l.input[l.pos] {
				case 'n':
					buf = append(buf, '\n')
				case 't':
					buf = append(buf, '\t')
				case '"':
					buf = append(buf, '"')
				case '\\':
					buf = append(buf, '\\')
				default:
					buf = append(buf, l.input[l.pos])
				}
				l.pos++
				l.col++
				continue
			}
			buf = append(buf, l.input[l.pos])
			l.pos++
			l.col++
		}
		return Token{Kind: TkInvalid, Literal: "unterminated string", Pos: pos}
	}

	// Multi-char operators.
	if ch == '=' && l.peek() == '=' {
		l.pos += 2
		l.col += 2
		return Token{Kind: TkEq, Literal: "==", Pos: pos}
	}
	if ch == '!' && l.peek() == '=' {
		l.pos += 2
		l.col += 2
		return Token{Kind: TkNe, Literal: "!=", Pos: pos}
	}
	if ch == '<' && l.peek() == '=' {
		l.pos += 2
		l.col += 2
		return Token{Kind: TkLe, Literal: "<=", Pos: pos}
	}
	if ch == '>' && l.peek() == '=' {
		l.pos += 2
		l.col += 2
		return Token{Kind: TkGe, Literal: ">=", Pos: pos}
	}
	if ch == '&' && l.peek() == '&' {
		l.pos += 2
		l.col += 2
		return Token{Kind: TkAnd, Literal: "&&", Pos: pos}
	}
	if ch == '|' && l.peek() == '|' {
		l.pos += 2
		l.col += 2
		return Token{Kind: TkOr, Literal: "||", Pos: pos}
	}

	// Single-char tokens.
	l.pos++
	l.col++
	switch ch {
	case '=':
		return Token{Kind: TkAssign, Literal: "=", Pos: pos}
	case '+':
		return Token{Kind: TkPlus, Literal: "+", Pos: pos}
	case '-':
		return Token{Kind: TkMinus, Literal: "-", Pos: pos}
	case '*':
		return Token{Kind: TkStar, Literal: "*", Pos: pos}
	case '/':
		return Token{Kind: TkSlash, Literal: "/", Pos: pos}
	case '!':
		return Token{Kind: TkNot, Literal: "!", Pos: pos}
	case '<':
		return Token{Kind: TkLt, Literal: "<", Pos: pos}
	case '>':
		return Token{Kind: TkGt, Literal: ">", Pos: pos}
	case '(':
		return Token{Kind: TkLParen, Literal: "(", Pos: pos}
	case ')':
		return Token{Kind: TkRParen, Literal: ")", Pos: pos}
	case '{':
		return Token{Kind: TkLBrace, Literal: "{", Pos: pos}
	case '}':
		return Token{Kind: TkRBrace, Literal: "}", Pos: pos}
	case ',':
		return Token{Kind: TkComma, Literal: ",", Pos: pos}
	case ';':
		return Token{Kind: TkSemicolon, Literal: ";", Pos: pos}
	case ':':
		return Token{Kind: TkColon, Literal: ":", Pos: pos}
	default:
		return Token{Kind: TkInvalid, Literal: fmt.Sprintf("unexpected character %q", ch), Pos: pos}
	}
}

func (l *Lexer) peek() rune {
	if l.pos+1 < len(l.input) {
		return l.input[l.pos+1]
	}
	return 0
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\n' {
			l.line++
			l.col = 0
			l.pos++
		} else if ch == ' ' || ch == '\t' || ch == '\r' {
			l.col++
			l.pos++
		} else if ch == '/' && l.peek() == '/' {
			// single-line comment
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.pos++
			}
			// don't skip the newline; it will be handled on next iteration
		} else {
			break
		}
	}
}

func lookupKeyword(word string) TokenKind {
	switch word {
	case "var":
		return TkVar
	case "func":
		return TkFunc
	case "if":
		return TkIf
	case "else":
		return TkElse
	case "while":
		return TkWhile
	case "return":
		return TkReturn
	case "int":
		return TkIntType
	case "bool":
		return TkBoolType
	case "string":
		return TkStringType
	case "true":
		return TkBool
	case "false":
		return TkBool
	default:
		return TkIdent
	}
}
