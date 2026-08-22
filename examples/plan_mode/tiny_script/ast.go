package tinyscript

import "fmt"

// Node is the interface for all AST nodes.
type Node interface {
	Pos() Pos
	String() string
}

// ---------- Expressions ----------

// Expr is the interface for expression nodes.
type Expr interface {
	Node
	exprNode()
}

// IdentExpr is a variable reference.
type IdentExpr struct {
	position Pos
	Name     string
}

func NewIdentExpr(pos Pos, name string) *IdentExpr {
	return &IdentExpr{position: pos, Name: name}
}
func (e *IdentExpr) Pos() Pos       { return e.position }
func (e *IdentExpr) String() string { return e.Name }
func (e *IdentExpr) exprNode()      {}

// IntLit is an integer literal.
type IntLit struct {
	position Pos
	Value    int64
}

func NewIntLit(pos Pos, value int64) *IntLit {
	return &IntLit{position: pos, Value: value}
}
func (e *IntLit) Pos() Pos       { return e.position }
func (e *IntLit) String() string { return fmt.Sprintf("%d", e.Value) }
func (e *IntLit) exprNode()      {}

// BoolLit is a boolean literal.
type BoolLit struct {
	position Pos
	Value    bool
}

func NewBoolLit(pos Pos, value bool) *BoolLit {
	return &BoolLit{position: pos, Value: value}
}
func (e *BoolLit) Pos() Pos       { return e.position }
func (e *BoolLit) String() string { return fmt.Sprintf("%t", e.Value) }
func (e *BoolLit) exprNode()      {}

// StringLit is a string literal.
type StringLit struct {
	position Pos
	Value    string
}

func NewStringLit(pos Pos, value string) *StringLit {
	return &StringLit{position: pos, Value: value}
}
func (e *StringLit) Pos() Pos       { return e.position }
func (e *StringLit) String() string { return fmt.Sprintf("%q", e.Value) }
func (e *StringLit) exprNode()      {}

// BinaryOp represents a binary operation.
type BinaryOp struct {
	position Pos
	Left     Expr
	Op       string // +, -, *, /, ==, !=, <, <=, >, >=, &&, ||
	Right    Expr
}

func NewBinaryOp(pos Pos, left Expr, op string, right Expr) *BinaryOp {
	return &BinaryOp{position: pos, Left: left, Op: op, Right: right}
}
func (e *BinaryOp) Pos() Pos       { return e.position }
func (e *BinaryOp) String() string { return fmt.Sprintf("(%s %s %s)", e.Left, e.Op, e.Right) }
func (e *BinaryOp) exprNode()      {}

// UnaryOp represents a unary operation (!, -).
type UnaryOp struct {
	position Pos
	Op       string // !, -
	Right    Expr
}

func NewUnaryOp(pos Pos, op string, right Expr) *UnaryOp {
	return &UnaryOp{position: pos, Op: op, Right: right}
}
func (e *UnaryOp) Pos() Pos       { return e.position }
func (e *UnaryOp) String() string { return fmt.Sprintf("(%s%s)", e.Op, e.Right) }
func (e *UnaryOp) exprNode()      {}

// GroupExpr is a parenthesised expression.
type GroupExpr struct {
	position Pos
	Inner    Expr
}

func NewGroupExpr(pos Pos, inner Expr) *GroupExpr {
	return &GroupExpr{position: pos, Inner: inner}
}
func (e *GroupExpr) Pos() Pos       { return e.position }
func (e *GroupExpr) String() string { return fmt.Sprintf("(%s)", e.Inner) }
func (e *GroupExpr) exprNode()      {}

// CallExpr is a function call.
type CallExpr struct {
	position  Pos
	FuncName  string
	Arguments []Expr
}

func NewCallExpr(pos Pos, name string, args []Expr) *CallExpr {
	return &CallExpr{position: pos, FuncName: name, Arguments: args}
}
func (e *CallExpr) Pos() Pos       { return e.position }
func (e *CallExpr) String() string { return fmt.Sprintf("%s(%v)", e.FuncName, e.Arguments) }
func (e *CallExpr) exprNode()      {}

// ---------- Statements ----------

// Stmt is the interface for statement nodes.
type Stmt interface {
	Node
	stmtNode()
}

// VarDecl is a typed variable declaration: var name type = expr.
type VarDecl struct {
	position Pos
	Name     string
	Type     Type
	Init     Expr
}

func NewVarDecl(pos Pos, name string, typ Type, init Expr) *VarDecl {
	return &VarDecl{position: pos, Name: name, Type: typ, Init: init}
}
func (s *VarDecl) Pos() Pos       { return s.position }
func (s *VarDecl) String() string { return fmt.Sprintf("var %s %s = %s", s.Name, s.Type, s.Init) }
func (s *VarDecl) stmtNode()      {}

// Assign is an assignment: name = expr.
type Assign struct {
	position Pos
	Name     string
	Value    Expr
}

func NewAssign(pos Pos, name string, value Expr) *Assign {
	return &Assign{position: pos, Name: name, Value: value}
}
func (s *Assign) Pos() Pos       { return s.position }
func (s *Assign) String() string { return fmt.Sprintf("%s = %s", s.Name, s.Value) }
func (s *Assign) stmtNode()      {}

// ExprStmt wraps an expression as a statement (for function calls as statements).
type ExprStmt struct {
	position Pos
	Expr     Expr
}

func NewExprStmt(pos Pos, expr Expr) *ExprStmt {
	return &ExprStmt{position: pos, Expr: expr}
}
func (s *ExprStmt) Pos() Pos       { return s.position }
func (s *ExprStmt) String() string { return s.Expr.String() }
func (s *ExprStmt) stmtNode()      {}

// ReturnStmt returns a value from a function.
type ReturnStmt struct {
	position Pos
	Value    Expr
}

func NewReturnStmt(pos Pos, value Expr) *ReturnStmt {
	return &ReturnStmt{position: pos, Value: value}
}
func (s *ReturnStmt) Pos() Pos       { return s.position }
func (s *ReturnStmt) String() string { return fmt.Sprintf("return %s", s.Value) }
func (s *ReturnStmt) stmtNode()      {}

// Block is a sequence of statements.
type Block struct {
	position   Pos
	Statements []Stmt
}

func NewBlock(pos Pos, stmts []Stmt) *Block {
	return &Block{position: pos, Statements: stmts}
}
func (s *Block) Pos() Pos       { return s.position }
func (s *Block) String() string { return fmt.Sprintf("{ %v }", s.Statements) }
func (s *Block) stmtNode()      {}

// IfStmt is an if/else statement.
type IfStmt struct {
	position  Pos
	Condition Expr
	ThenBlock *Block
	ElseBlock *Block // may be nil
}

func NewIfStmt(pos Pos, cond Expr, thenB, elseB *Block) *IfStmt {
	return &IfStmt{position: pos, Condition: cond, ThenBlock: thenB, ElseBlock: elseB}
}
func (s *IfStmt) Pos() Pos { return s.position }
func (s *IfStmt) String() string {
	return fmt.Sprintf("if %s %s else %s", s.Condition, s.ThenBlock, s.ElseBlock)
}
func (s *IfStmt) stmtNode() {}

// WhileStmt is a while loop.
type WhileStmt struct {
	position  Pos
	Condition Expr
	Body      *Block
}

func NewWhileStmt(pos Pos, cond Expr, body *Block) *WhileStmt {
	return &WhileStmt{position: pos, Condition: cond, Body: body}
}
func (s *WhileStmt) Pos() Pos       { return s.position }
func (s *WhileStmt) String() string { return fmt.Sprintf("while %s %s", s.Condition, s.Body) }
func (s *WhileStmt) stmtNode()      {}

// Program is the top-level node containing function definitions and a main block.
type Program struct {
	Functions []*FuncDef
	Main      *Block
}

// FuncDef is a function definition.
type FuncDef struct {
	position   Pos
	Name       string
	Parameters []Param
	ReturnType Type
	Body       *Block
}

type Param struct {
	Name string
	Type Type
}

func NewFuncDef(pos Pos, name string, params []Param, retType Type, body *Block) *FuncDef {
	return &FuncDef{position: pos, Name: name, Parameters: params, ReturnType: retType, Body: body}
}
func (f *FuncDef) Pos() Pos { return f.position }
func (f *FuncDef) String() string {
	return fmt.Sprintf("func %s(%v) %s %s", f.Name, f.Parameters, f.ReturnType, f.Body)
}
