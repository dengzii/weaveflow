package tinyscript

import (
	"fmt"
	"strings"
)

// ExecutionResult contains the observable state after a successful execution.
type ExecutionResult struct {
	Globals map[string]Value
	Stdout  []string
}

// Check parses and statically checks source without executing it.
func Check(source string) (*Program, TypeInfo, error) {
	lex := NewLexer(source)
	parser := NewParser(lex)
	prog := parser.ParseProgram()

	if parserErrors := parser.Errors(); len(parserErrors) > 0 {
		return nil, nil, fmt.Errorf("parse errors:\n%s", strings.Join(parserErrors, "\n"))
	}

	// Type-check.
	tc := NewTypeChecker(prog)
	info, tcErrors := tc.Check()
	if len(tcErrors) > 0 {
		return nil, nil, fmt.Errorf("type errors:\n%s", strings.Join(tcErrors, "\n"))
	}
	return prog, info, nil
}

// Execute parses, statically checks, and executes source.
func Execute(source string) (*ExecutionResult, error) {
	prog, info, err := Check(source)
	if err != nil {
		return nil, err
	}

	interp := NewInterpreter(prog, info)
	stdout, err := interp.Run()
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{Globals: interp.Globals(), Stdout: stdout}, nil
}

// RunSource executes source and returns captured output for compatibility with
// callers that do not need the final global variables.
func RunSource(source string) ([]string, error) {
	result, err := Execute(source)
	if err != nil {
		return nil, err
	}
	return result.Stdout, nil
}

// MustRun is like RunSource but panics on error.
func MustRun(source string) []string {
	stdout, err := RunSource(source)
	if err != nil {
		panic(err)
	}
	return stdout
}
