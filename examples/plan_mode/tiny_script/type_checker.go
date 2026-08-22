package tinyscript

import (
	"fmt"
)

// TypeInfo holds resolved type information for expressions.
// It maps each expression node to the type it evaluates to.
type TypeInfo map[Expr]Type

// TypeChecker performs static type checking.
type TypeChecker struct {
	prog              *Program
	info              TypeInfo
	errors            []string
	funcs             map[string]*FuncDef
	scopes            []map[string]Type // variable name -> type
	currentReturnType Type
	functionHasReturn bool
}

// NewTypeChecker creates a new type checker.
func NewTypeChecker(prog *Program) *TypeChecker {
	return &TypeChecker{
		prog:   prog,
		info:   make(TypeInfo),
		errors: []string{},
		funcs:  make(map[string]*FuncDef),
	}
}

// Check runs type checking and returns type information and errors.
func (tc *TypeChecker) Check() (TypeInfo, []string) {
	// Register functions.
	for _, fn := range tc.prog.Functions {
		if _, ok := tc.funcs[fn.Name]; ok {
			tc.errorf(fn.Pos(), "duplicate function %q", fn.Name)
		}
		tc.funcs[fn.Name] = fn
	}

	// Type-check each function body.
	for _, fn := range tc.prog.Functions {
		tc.typeCheckFuncDef(fn)
	}

	// Type-check main block.
	tc.enterScope()
	tc.typeCheckBlock(tc.prog.Main)
	tc.exitScope()

	return tc.info, tc.errors
}

func (tc *TypeChecker) typeCheckFuncDef(fn *FuncDef) {
	previousReturnType := tc.currentReturnType
	previousHasReturn := tc.functionHasReturn
	tc.currentReturnType = fn.ReturnType
	tc.functionHasReturn = false
	tc.enterScope()
	// Add parameters to scope.
	for _, p := range fn.Parameters {
		tc.scopeSet(p.Name, p.Type)
	}
	tc.typeCheckBlock(fn.Body)
	tc.exitScope()
	if !tc.functionHasReturn {
		tc.errorf(fn.Pos(), "function %q must return %s", fn.Name, fn.ReturnType)
	}
	tc.currentReturnType = previousReturnType
	tc.functionHasReturn = previousHasReturn
}

func (tc *TypeChecker) typeCheckBlock(block *Block) {
	for _, stmt := range block.Statements {
		tc.typeCheckStmt(stmt)
	}
}

func (tc *TypeChecker) typeCheckStmt(stmt Stmt) {
	switch s := stmt.(type) {
	case *VarDecl:
		tc.typeCheckVarDecl(s)
	case *Assign:
		tc.typeCheckAssign(s)
	case *ExprStmt:
		tc.typeCheckExpr(s.Expr)
	case *ReturnStmt:
		tc.typeCheckReturn(s)
	case *IfStmt:
		tc.typeCheckIfStmt(s)
	case *WhileStmt:
		tc.typeCheckWhileStmt(s)
	case *Block:
		tc.enterScope()
		tc.typeCheckBlock(s)
		tc.exitScope()
	default:
		tc.errorf(stmt.Pos(), "unknown statement type %T", stmt)
	}
}

func (tc *TypeChecker) typeCheckVarDecl(s *VarDecl) {
	initType := tc.typeCheckExpr(s.Init)
	if initType == TypeInvalid {
		return
	}
	if initType != s.Type {
		tc.errorf(s.Pos(), "type mismatch in variable %q: declared %s, initializer is %s", s.Name, s.Type, initType)
	}
	if _, exists := tc.scopes[len(tc.scopes)-1][s.Name]; exists {
		tc.errorf(s.Pos(), "duplicate variable %q", s.Name)
		return
	}
	tc.scopeSet(s.Name, s.Type)
}

func (tc *TypeChecker) typeCheckReturn(s *ReturnStmt) {
	if tc.currentReturnType == TypeInvalid {
		tc.errorf(s.Pos(), "return is only valid inside a function")
		return
	}
	actual := tc.typeCheckExpr(s.Value)
	if actual != TypeInvalid && actual != tc.currentReturnType {
		tc.errorf(s.Pos(), "return type mismatch: expected %s, got %s", tc.currentReturnType, actual)
	}
	tc.functionHasReturn = true
}

func (tc *TypeChecker) typeCheckAssign(s *Assign) {
	typ, ok := tc.scopeLookup(s.Name)
	if !ok {
		tc.errorf(s.Pos(), "undefined variable %q", s.Name)
		return
	}
	valType := tc.typeCheckExpr(s.Value)
	if valType != TypeInvalid && valType != typ {
		tc.errorf(s.Pos(), "type mismatch in assignment to %q: variable is %s, value is %s", s.Name, typ, valType)
	}
}

func (tc *TypeChecker) typeCheckIfStmt(s *IfStmt) {
	condType := tc.typeCheckExpr(s.Condition)
	if condType != TypeInvalid && condType != TypeBool {
		tc.errorf(s.Condition.Pos(), "if condition must be boolean, got %s", condType)
	}
	tc.enterScope()
	tc.typeCheckBlock(s.ThenBlock)
	tc.exitScope()
	if s.ElseBlock != nil {
		tc.enterScope()
		tc.typeCheckBlock(s.ElseBlock)
		tc.exitScope()
	}
}

func (tc *TypeChecker) typeCheckWhileStmt(s *WhileStmt) {
	condType := tc.typeCheckExpr(s.Condition)
	if condType != TypeInvalid && condType != TypeBool {
		tc.errorf(s.Condition.Pos(), "while condition must be boolean, got %s", condType)
	}
	tc.enterScope()
	tc.typeCheckBlock(s.Body)
	tc.exitScope()
}

// typeCheckExpr returns the type of the expression, or TypeInvalid on error.
func (tc *TypeChecker) typeCheckExpr(expr Expr) Type {
	switch e := expr.(type) {
	case *IntLit:
		tc.info[e] = TypeInt
		return TypeInt
	case *BoolLit:
		tc.info[e] = TypeBool
		return TypeBool
	case *StringLit:
		tc.info[e] = TypeString
		return TypeString
	case *IdentExpr:
		typ, ok := tc.scopeLookup(e.Name)
		if !ok {
			tc.errorf(e.Pos(), "undefined variable %q", e.Name)
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = typ
		return typ
	case *UnaryOp:
		return tc.typeCheckUnary(e)
	case *BinaryOp:
		return tc.typeCheckBinary(e)
	case *GroupExpr:
		inner := tc.typeCheckExpr(e.Inner)
		tc.info[e] = inner
		return inner
	case *CallExpr:
		return tc.typeCheckCall(e)
	default:
		tc.errorf(expr.Pos(), "unknown expression type %T", expr)
		tc.info[expr] = TypeInvalid
		return TypeInvalid
	}
}

func (tc *TypeChecker) typeCheckUnary(e *UnaryOp) Type {
	rightType := tc.typeCheckExpr(e.Right)
	switch e.Op {
	case "-":
		if rightType != TypeInt && rightType != TypeInvalid {
			tc.errorf(e.Pos(), "unary - expects int, got %s", rightType)
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = TypeInt
		return TypeInt
	case "!":
		if rightType != TypeBool && rightType != TypeInvalid {
			tc.errorf(e.Pos(), "unary ! expects bool, got %s", rightType)
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = TypeBool
		return TypeBool
	default:
		tc.errorf(e.Pos(), "unknown unary operator %q", e.Op)
		tc.info[e] = TypeInvalid
		return TypeInvalid
	}
}

func (tc *TypeChecker) typeCheckBinary(e *BinaryOp) Type {
	leftType := tc.typeCheckExpr(e.Left)
	rightType := tc.typeCheckExpr(e.Right)

	switch e.Op {
	case "+", "-", "*", "/":
		if leftType != TypeInt || rightType != TypeInt {
			if leftType != TypeInvalid && rightType != TypeInvalid {
				tc.errorf(e.Pos(), "operator %q expects int operands, got %s and %s", e.Op, leftType, rightType)
			}
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = TypeInt
		return TypeInt

	case "==", "!=":
		if leftType != rightType && leftType != TypeInvalid && rightType != TypeInvalid {
			tc.errorf(e.Pos(), "operator %q requires operands of the same type, got %s and %s", e.Op, leftType, rightType)
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = TypeBool
		return TypeBool

	case "<", "<=", ">", ">=":
		if leftType != TypeInt || rightType != TypeInt {
			if leftType != TypeInvalid && rightType != TypeInvalid {
				tc.errorf(e.Pos(), "operator %q expects int operands, got %s and %s", e.Op, leftType, rightType)
			}
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = TypeBool
		return TypeBool

	case "&&", "||":
		if leftType != TypeBool || rightType != TypeBool {
			if leftType != TypeInvalid && rightType != TypeInvalid {
				tc.errorf(e.Pos(), "operator %q expects bool operands, got %s and %s", e.Op, leftType, rightType)
			}
			tc.info[e] = TypeInvalid
			return TypeInvalid
		}
		tc.info[e] = TypeBool
		return TypeBool

	default:
		tc.errorf(e.Pos(), "unknown binary operator %q", e.Op)
		tc.info[e] = TypeInvalid
		return TypeInvalid
	}
}

func (tc *TypeChecker) typeCheckCall(e *CallExpr) Type {
	fn, ok := tc.funcs[e.FuncName]
	if !ok {
		tc.errorf(e.Pos(), "undefined function %q", e.FuncName)
		tc.info[e] = TypeInvalid
		return TypeInvalid
	}
	if len(e.Arguments) != len(fn.Parameters) {
		tc.errorf(e.Pos(), "function %q expects %d arguments, got %d", e.FuncName, len(fn.Parameters), len(e.Arguments))
		tc.info[e] = TypeInvalid
		return TypeInvalid
	}
	for i, arg := range e.Arguments {
		argType := tc.typeCheckExpr(arg)
		paramType := fn.Parameters[i].Type
		if argType != TypeInvalid && argType != paramType {
			tc.errorf(arg.Pos(), "argument %d of function %q: expected %s, got %s", i+1, e.FuncName, paramType, argType)
		}
	}
	tc.info[e] = fn.ReturnType
	return fn.ReturnType
}

// Scope management.
func (tc *TypeChecker) enterScope() {
	tc.scopes = append(tc.scopes, make(map[string]Type))
}

func (tc *TypeChecker) exitScope() {
	tc.scopes = tc.scopes[:len(tc.scopes)-1]
}

func (tc *TypeChecker) scopeSet(name string, typ Type) {
	tc.scopes[len(tc.scopes)-1][name] = typ
}

func (tc *TypeChecker) scopeLookup(name string) (Type, bool) {
	for i := len(tc.scopes) - 1; i >= 0; i-- {
		if typ, ok := tc.scopes[i][name]; ok {
			return typ, true
		}
	}
	return TypeInvalid, false
}

func (tc *TypeChecker) errorf(pos Pos, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	tc.errors = append(tc.errors, fmt.Sprintf("at %s: %s", pos, msg))
}
