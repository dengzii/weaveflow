package tinyscript

import (
	"fmt"
	"strconv"
)

// Parser implements a recursive-descent parser.
type Parser struct {
	lex    *Lexer
	cur    Token
	peek   Token
	errors []string
}

// NewParser creates a new parser from a lexer.
func NewParser(lex *Lexer) *Parser {
	p := &Parser{lex: lex}
	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) nextToken() {
	p.cur = p.peek
	p.peek = p.lex.Next()
}

func (p *Parser) expect(kind TokenKind) Token {
	if p.cur.Kind != kind {
		p.errorf(p.cur.Pos, "expected %s, got %s (%q)", kind, p.cur.Kind, p.cur.Literal)
	}
	tok := p.cur
	p.nextToken()
	return tok
}

func (p *Parser) errorf(pos Pos, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	p.errors = append(p.errors, fmt.Sprintf("at %s: %s", pos, msg))
}

// ParseProgram parses the entire input.
func (p *Parser) ParseProgram() *Program {
	prog := &Program{}

	// Parse function definitions and then the main block.
	for p.cur.Kind != TkEOF {
		if p.cur.Kind == TkFunc {
			fn := p.parseFuncDef()
			if fn != nil {
				prog.Functions = append(prog.Functions, fn)
			}
		} else {
			break
		}
	}

	// The remaining statements form the main block.
	stmts := p.parseBlockStatements()
	prog.Main = NewBlock(Pos{Line: 1, Col: 0}, stmts)

	if p.cur.Kind != TkEOF {
		p.errorf(p.cur.Pos, "unexpected token %q", p.cur.Literal)
	}

	return prog
}

func (p *Parser) parseFuncDef() *FuncDef {
	pos := p.cur.Pos
	p.nextToken() // consume "func"
	nameTok := p.expect(TkIdent)
	p.expect(TkLParen)
	params := p.parseParameters()
	p.expect(TkRParen)
	retType := p.parseType()
	body := p.parseBlock()
	return NewFuncDef(pos, nameTok.Literal, params, retType, body)
}

func (p *Parser) parseParameters() []Param {
	var params []Param
	if p.cur.Kind == TkRParen {
		return params
	}
	for {
		name := p.expect(TkIdent).Literal
		typ := p.parseType()
		params = append(params, Param{Name: name, Type: typ})
		if p.cur.Kind != TkComma {
			break
		}
		p.nextToken()
	}
	return params
}

func (p *Parser) parseType() Type {
	tok := p.cur
	switch tok.Kind {
	case TkIntType:
		p.nextToken()
		return TypeInt
	case TkBoolType:
		p.nextToken()
		return TypeBool
	case TkStringType:
		p.nextToken()
		return TypeString
	default:
		p.errorf(tok.Pos, "expected type (int, bool, string), got %s", tok.Kind)
		return TypeInvalid
	}
}

// parseBlock parses { ... }.
func (p *Parser) parseBlock() *Block {
	pos := p.cur.Pos
	p.expect(TkLBrace)
	stmts := p.parseBlockStatements()
	p.expect(TkRBrace)
	return NewBlock(pos, stmts)
}

// parseBlockStatements parses statements until } or EOF.
func (p *Parser) parseBlockStatements() []Stmt {
	var stmts []Stmt
	for p.cur.Kind != TkRBrace && p.cur.Kind != TkEOF {
		s := p.parseStmt()
		if s != nil {
			stmts = append(stmts, s)
		}
	}
	return stmts
}

func (p *Parser) parseStmt() Stmt {
	switch p.cur.Kind {
	case TkVar:
		return p.parseVarDecl()
	case TkIf:
		return p.parseIfStmt()
	case TkWhile:
		return p.parseWhileStmt()
	case TkReturn:
		return p.parseReturnStmt()
	case TkLBrace:
		return p.parseBlock()
	default:
		return p.parseExprOrAssignStmt()
	}
}

func (p *Parser) parseVarDecl() Stmt {
	pos := p.cur.Pos
	p.nextToken() // consume "var"
	name := p.expect(TkIdent).Literal
	typ := p.parseType()
	p.expect(TkAssign)
	init := p.parseExpr(0)
	p.expect(TkSemicolon)
	return NewVarDecl(pos, name, typ, init)
}

func (p *Parser) parseIfStmt() Stmt {
	pos := p.cur.Pos
	p.nextToken() // consume "if"
	cond := p.parseExpr(0)
	thenB := p.parseBlock()
	var elseB *Block
	if p.cur.Kind == TkElse {
		p.nextToken()
		if p.cur.Kind == TkIf {
			// else if: wrap as else { if ... }
			elseIf := p.parseIfStmt()
			elseB = NewBlock(pos, []Stmt{elseIf})
		} else {
			elseB = p.parseBlock()
		}
	}
	return NewIfStmt(pos, cond, thenB, elseB)
}

func (p *Parser) parseWhileStmt() Stmt {
	pos := p.cur.Pos
	p.nextToken() // consume "while"
	cond := p.parseExpr(0)
	body := p.parseBlock()
	return NewWhileStmt(pos, cond, body)
}

func (p *Parser) parseReturnStmt() Stmt {
	pos := p.cur.Pos
	p.nextToken() // consume "return"
	if p.cur.Kind == TkSemicolon || p.cur.Kind == TkRBrace || p.cur.Kind == TkEOF {
		p.errorf(pos, "return requires a value")
		return NewReturnStmt(pos, NewIntLit(pos, 0))
	}
	expr := p.parseExpr(0)
	p.expect(TkSemicolon)
	return NewReturnStmt(pos, expr)
}

// parseExprOrAssignStmt handles assignment or expression statements.
func (p *Parser) parseExprOrAssignStmt() Stmt {
	pos := p.cur.Pos
	expr := p.parseExpr(0)

	if p.cur.Kind == TkAssign {
		ident, ok := expr.(*IdentExpr)
		if !ok {
			p.errorf(pos, "left-hand side of assignment must be an identifier")
			p.nextToken()
			return NewExprStmt(pos, expr)
		}
		p.nextToken() // consume =
		val := p.parseExpr(0)
		if p.cur.Kind == TkSemicolon {
			p.nextToken()
		}
		return NewAssign(pos, ident.Name, val)
	}

	if p.cur.Kind == TkSemicolon {
		p.nextToken()
	}
	return NewExprStmt(pos, expr)
}

// Precedence levels.
const (
	PrecLowest = iota
	PrecOr     // ||
	PrecAnd    // &&
	PrecEq     // ==, !=
	PrecCmp    // <, <=, >, >=
	PrecAdd    // +, -
	PrecMul    // *, /
	PrecUnary  // !, -
	PrecPrimary
)

func precedence(kind TokenKind) int {
	switch kind {
	case TkOr:
		return PrecOr
	case TkAnd:
		return PrecAnd
	case TkEq, TkNe:
		return PrecEq
	case TkLt, TkLe, TkGt, TkGe:
		return PrecCmp
	case TkPlus, TkMinus:
		return PrecAdd
	case TkStar, TkSlash:
		return PrecMul
	default:
		return PrecLowest
	}
}

// parseExpr parses an expression with the given minimum precedence.
func (p *Parser) parseExpr(prec int) Expr {
	left := p.parsePrimary()

	for prec < precedence(p.cur.Kind) {
		op := p.cur.Kind
		pos := p.cur.Pos
		p.nextToken()
		right := p.parseExpr(precedence(op))
		left = NewBinaryOp(pos, left, tokenNames[op], right)
	}

	return left
}

func (p *Parser) parsePrimary() Expr {
	pos := p.cur.Pos
	switch p.cur.Kind {
	case TkInt:
		v, err := strconv.ParseInt(p.cur.Literal, 10, 64)
		if err != nil {
			p.errorf(pos, "invalid integer %q", p.cur.Literal)
			p.nextToken()
			return NewIntLit(pos, 0)
		}
		p.nextToken()
		return NewIntLit(pos, v)

	case TkBool:
		v := p.cur.Literal == "true"
		p.nextToken()
		return NewBoolLit(pos, v)

	case TkString:
		s := p.cur.Literal
		p.nextToken()
		return NewStringLit(pos, s)

	case TkIdent:
		name := p.cur.Literal
		p.nextToken()
		if p.cur.Kind == TkLParen {
			return p.parseCall(pos, name)
		}
		return NewIdentExpr(pos, name)

	case TkLParen:
		p.nextToken()
		inner := p.parseExpr(PrecLowest)
		p.expect(TkRParen)
		return NewGroupExpr(pos, inner)

	case TkNot, TkMinus:
		op := p.cur.Literal
		p.nextToken()
		right := p.parseExpr(PrecUnary)
		return NewUnaryOp(pos, op, right)

	case TkLBrace:
		// Block literal (treated as a block statement in statement context)
		// This should not happen in expression context but handle gracefully.
		p.errorf(pos, "unexpected '{' in expression")
		p.nextToken()
		return NewIntLit(pos, 0)

	default:
		p.errorf(pos, "unexpected token %q in expression", p.cur.Literal)
		p.nextToken()
		return NewIntLit(pos, 0)
	}
}

func (p *Parser) parseCall(pos Pos, name string) *CallExpr {
	p.nextToken() // consume (
	var args []Expr
	if p.cur.Kind != TkRParen {
		args = append(args, p.parseExpr(PrecLowest))
		for p.cur.Kind == TkComma {
			p.nextToken()
			args = append(args, p.parseExpr(PrecLowest))
		}
	}
	p.expect(TkRParen)
	return NewCallExpr(pos, name, args)
}

// Errors returns the list of error messages.
func (p *Parser) Errors() []string {
	return p.errors
}
