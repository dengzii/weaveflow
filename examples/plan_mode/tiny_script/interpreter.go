package tinyscript

import (
	"errors"
	"fmt"
)

// Interpreter evaluates a type-checked program.
type Interpreter struct {
	prog    *Program
	info    TypeInfo
	funcs   map[string]*FuncDef
	scopes  []map[string]Value
	stdout  []string
	globals map[string]Value
}

// NewInterpreter creates a new interpreter.
func NewInterpreter(prog *Program, info TypeInfo) *Interpreter {
	return &Interpreter{
		prog:   prog,
		info:   info,
		funcs:  make(map[string]*FuncDef),
		stdout: []string{},
	}
}

// Run executes the program and returns collected stdout lines.
func (interp *Interpreter) Run() ([]string, error) {
	for _, fn := range interp.prog.Functions {
		interp.funcs[fn.Name] = fn
	}

	interp.enterScope()
	defer interp.exitScope()

	err := interp.execBlock(interp.prog.Main)
	if err != nil {
		return interp.stdout, err
	}
	interp.globals = cloneValues(interp.scopes[0])
	return interp.stdout, nil
}

func (interp *Interpreter) execBlock(block *Block) error {
	for _, stmt := range block.Statements {
		if err := interp.execStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (interp *Interpreter) execStmt(stmt Stmt) error {
	switch s := stmt.(type) {
	case *VarDecl:
		return interp.execVarDecl(s)
	case *Assign:
		return interp.execAssign(s)
	case *ExprStmt:
		return interp.execExprStmt(s)
	case *ReturnStmt:
		value, err := interp.evalExpr(s.Value)
		if err != nil {
			return err
		}
		return returnSignal{value: value}
	case *IfStmt:
		return interp.execIfStmt(s)
	case *WhileStmt:
		return interp.execWhileStmt(s)
	case *Block:
		interp.enterScope()
		defer interp.exitScope()
		return interp.execBlock(s)
	default:
		return fmt.Errorf("at %s: unknown statement type %T", stmt.Pos(), stmt)
	}
}

func (interp *Interpreter) execVarDecl(s *VarDecl) error {
	val, err := interp.evalExpr(s.Init)
	if err != nil {
		return err
	}
	if val.Type != s.Type {
		return fmt.Errorf("at %s: type mismatch for variable %q", s.Pos(), s.Name)
	}
	interp.scopeDeclare(s.Name, val)
	return nil
}

func (interp *Interpreter) execAssign(s *Assign) error {
	val, err := interp.evalExpr(s.Value)
	if err != nil {
		return err
	}
	if !interp.scopeAssign(s.Name, val) {
		return fmt.Errorf("at %s: undefined variable %q", s.Pos(), s.Name)
	}
	return nil
}

func (interp *Interpreter) execExprStmt(s *ExprStmt) error {
	_, err := interp.evalExpr(s.Expr)
	return err
}

func (interp *Interpreter) execIfStmt(s *IfStmt) error {
	cond, err := interp.evalExpr(s.Condition)
	if err != nil {
		return err
	}
	if cond.Type != TypeBool {
		return fmt.Errorf("at %s: if condition must be boolean", s.Condition.Pos())
	}
	if cond.Bool {
		interp.enterScope()
		defer interp.exitScope()
		return interp.execBlock(s.ThenBlock)
	} else if s.ElseBlock != nil {
		interp.enterScope()
		defer interp.exitScope()
		return interp.execBlock(s.ElseBlock)
	}
	return nil
}

func (interp *Interpreter) execWhileStmt(s *WhileStmt) error {
	for {
		cond, err := interp.evalExpr(s.Condition)
		if err != nil {
			return err
		}
		if cond.Type != TypeBool {
			return fmt.Errorf("at %s: while condition must be boolean", s.Condition.Pos())
		}
		if !cond.Bool {
			break
		}
		interp.enterScope()
		err = interp.execBlock(s.Body)
		interp.exitScope()
		if err != nil {
			return err
		}
	}
	return nil
}

func (interp *Interpreter) evalExpr(expr Expr) (Value, error) {
	switch e := expr.(type) {
	case *IntLit:
		return NewIntValue(e.Value), nil
	case *BoolLit:
		return NewBoolValue(e.Value), nil
	case *StringLit:
		return NewStringValue(e.Value), nil
	case *IdentExpr:
		val, ok := interp.scopeLookup(e.Name)
		if !ok {
			return Value{}, fmt.Errorf("at %s: undefined variable %q", e.Pos(), e.Name)
		}
		return val, nil
	case *UnaryOp:
		return interp.evalUnary(e)
	case *BinaryOp:
		return interp.evalBinary(e)
	case *GroupExpr:
		return interp.evalExpr(e.Inner)
	case *CallExpr:
		return interp.evalCall(e)
	default:
		return Value{}, fmt.Errorf("at %s: unknown expression type %T", expr.Pos(), expr)
	}
}

func (interp *Interpreter) evalUnary(e *UnaryOp) (Value, error) {
	right, err := interp.evalExpr(e.Right)
	if err != nil {
		return Value{}, err
	}
	switch e.Op {
	case "-":
		if right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: unary - expects int", e.Pos())
		}
		return NewIntValue(-right.Int), nil
	case "!":
		if right.Type != TypeBool {
			return Value{}, fmt.Errorf("at %s: unary ! expects bool", e.Pos())
		}
		return NewBoolValue(!right.Bool), nil
	default:
		return Value{}, fmt.Errorf("at %s: unknown unary operator %q", e.Pos(), e.Op)
	}
}

func (interp *Interpreter) evalBinary(e *BinaryOp) (Value, error) {
	left, err := interp.evalExpr(e.Left)
	if err != nil {
		return Value{}, err
	}
	if e.Op == "&&" && left.Type == TypeBool && !left.Bool {
		return NewBoolValue(false), nil
	}
	if e.Op == "||" && left.Type == TypeBool && left.Bool {
		return NewBoolValue(true), nil
	}
	right, err := interp.evalExpr(e.Right)
	if err != nil {
		return Value{}, err
	}

	switch e.Op {
	case "+":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: + expects int operands", e.Pos())
		}
		return NewIntValue(left.Int + right.Int), nil
	case "-":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: - expects int operands", e.Pos())
		}
		return NewIntValue(left.Int - right.Int), nil
	case "*":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: * expects int operands", e.Pos())
		}
		return NewIntValue(left.Int * right.Int), nil
	case "/":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: / expects int operands", e.Pos())
		}
		if right.Int == 0 {
			return Value{}, fmt.Errorf("at %s: division by zero", e.Pos())
		}
		return NewIntValue(left.Int / right.Int), nil
	case "==":
		if left.Type != right.Type {
			return Value{}, fmt.Errorf("at %s: == requires same types", e.Pos())
		}
		return NewBoolValue(valuesEqual(left, right)), nil
	case "!=":
		if left.Type != right.Type {
			return Value{}, fmt.Errorf("at %s: != requires same types", e.Pos())
		}
		return NewBoolValue(!valuesEqual(left, right)), nil
	case "<":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: < expects int operands", e.Pos())
		}
		return NewBoolValue(left.Int < right.Int), nil
	case "<=":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: <= expects int operands", e.Pos())
		}
		return NewBoolValue(left.Int <= right.Int), nil
	case ">":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: > expects int operands", e.Pos())
		}
		return NewBoolValue(left.Int > right.Int), nil
	case ">=":
		if left.Type != TypeInt || right.Type != TypeInt {
			return Value{}, fmt.Errorf("at %s: >= expects int operands", e.Pos())
		}
		return NewBoolValue(left.Int >= right.Int), nil
	case "&&":
		if left.Type != TypeBool || right.Type != TypeBool {
			return Value{}, fmt.Errorf("at %s: && expects bool operands", e.Pos())
		}
		return NewBoolValue(left.Bool && right.Bool), nil
	case "||":
		if left.Type != TypeBool || right.Type != TypeBool {
			return Value{}, fmt.Errorf("at %s: || expects bool operands", e.Pos())
		}
		return NewBoolValue(left.Bool || right.Bool), nil
	default:
		return Value{}, fmt.Errorf("at %s: unknown binary operator %q", e.Pos(), e.Op)
	}
}

func (interp *Interpreter) evalCall(e *CallExpr) (Value, error) {
	fn, ok := interp.funcs[e.FuncName]
	if !ok {
		return Value{}, fmt.Errorf("at %s: undefined function %q", e.Pos(), e.FuncName)
	}
	if len(e.Arguments) != len(fn.Parameters) {
		return Value{}, fmt.Errorf("at %s: function %q expects %d arguments, got %d", e.Pos(), e.FuncName, len(fn.Parameters), len(e.Arguments))
	}

	// Evaluate arguments.
	argVals := make([]Value, len(e.Arguments))
	for i, arg := range e.Arguments {
		val, err := interp.evalExpr(arg)
		if err != nil {
			return Value{}, err
		}
		argVals[i] = val
	}

	// Create new scope and bind parameters.
	interp.enterScope()
	defer interp.exitScope()
	for i, p := range fn.Parameters {
		interp.scopeDeclare(p.Name, argVals[i])
	}

	if err := interp.execBlock(fn.Body); err != nil {
		var returned returnSignal
		if errors.As(err, &returned) {
			if returned.value.Type != fn.ReturnType {
				return Value{}, fmt.Errorf("at %s: function %q returned %s, want %s", e.Pos(), e.FuncName, returned.value.Type, fn.ReturnType)
			}
			return returned.value, nil
		}
		return Value{}, err
	}
	return Value{}, fmt.Errorf("at %s: function %q completed without returning %s", e.Pos(), e.FuncName, fn.ReturnType)
}

type returnSignal struct{ value Value }

func (signal returnSignal) Error() string { return "function returned" }

// Scope management.
func (interp *Interpreter) enterScope() {
	interp.scopes = append(interp.scopes, make(map[string]Value))
}

func (interp *Interpreter) exitScope() {
	interp.scopes = interp.scopes[:len(interp.scopes)-1]
}

func (interp *Interpreter) scopeDeclare(name string, val Value) {
	interp.scopes[len(interp.scopes)-1][name] = val
}

func (interp *Interpreter) scopeAssign(name string, val Value) bool {
	for i := len(interp.scopes) - 1; i >= 0; i-- {
		if _, ok := interp.scopes[i][name]; ok {
			interp.scopes[i][name] = val
			return true
		}
	}
	return false
}

func (interp *Interpreter) scopeLookup(name string) (Value, bool) {
	for i := len(interp.scopes) - 1; i >= 0; i-- {
		if val, ok := interp.scopes[i][name]; ok {
			return val, true
		}
	}
	return Value{}, false
}

// Stdout returns captured output lines.
func (interp *Interpreter) Stdout() []string {
	return interp.stdout
}

// Globals returns the values left in the top-level scope after Run.
func (interp *Interpreter) Globals() map[string]Value {
	return cloneValues(interp.globals)
}

func valuesEqual(left, right Value) bool {
	switch left.Type {
	case TypeInt:
		return left.Int == right.Int
	case TypeBool:
		return left.Bool == right.Bool
	case TypeString:
		return left.Str == right.Str
	default:
		return false
	}
}

func cloneValues(values map[string]Value) map[string]Value {
	cloned := make(map[string]Value, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}
