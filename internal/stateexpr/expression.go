package stateexpr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

const (
	InputAliasPattern              = `[A-Za-z_][A-Za-z0-9_]*`
	MaxExpressionCodePoints        = 16 * 1024
	MaxInputBytes                  = 1024 * 1024
	MaxOutputBytes                 = 1024 * 1024
	MaxRuntimeCost          uint64 = 100_000
)

type CompileOptions struct {
	LegacyInput    bool
	RequireBoolean bool
}

type Program struct {
	program        cel.Program
	requireBoolean bool
}

func Compile(expression string, options CompileOptions) (*Program, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, fmt.Errorf("expression is required")
	}
	environmentOptions := []cel.EnvOption{
		cel.Variable("inputs", cel.MapType(cel.StringType, cel.DynType)),
		cel.OptionalTypes(),
		cel.CrossTypeNumericComparisons(true),
		cel.ParserExpressionSizeLimit(MaxExpressionCodePoints),
	}
	if options.LegacyInput {
		environmentOptions = append(environmentOptions, cel.Variable("input", cel.DynType))
	}
	environment, err := cel.NewEnv(environmentOptions...)
	if err != nil {
		return nil, fmt.Errorf("create CEL environment: %w", err)
	}
	ast, issues := environment.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile CEL expression: %w", issues.Err())
	}
	if options.RequireBoolean && ast.OutputType() != cel.BoolType && ast.OutputType() != cel.DynType {
		return nil, fmt.Errorf("CEL expression result type %q is not boolean", ast.OutputType())
	}
	compiled, err := environment.Program(ast,
		cel.CostLimit(MaxRuntimeCost),
		cel.InterruptCheckFrequency(100),
		cel.EvalOptions(cel.OptOptimize),
	)
	if err != nil {
		return nil, fmt.Errorf("create CEL program: %w", err)
	}
	return &Program{program: compiled, requireBoolean: options.RequireBoolean}, nil
}

func (p *Program) EvalJSON(ctx context.Context, inputs map[string]any, legacyInput any, hasLegacyInput bool) (any, error) {
	if p == nil || p.program == nil {
		return nil, fmt.Errorf("CEL expression is not compiled")
	}
	activation := map[string]any{"inputs": inputs}
	if activation["inputs"] == nil {
		activation["inputs"] = map[string]any{}
	}
	if hasLegacyInput {
		activation["input"] = legacyInput
	}
	normalizedActivation, size, err := normalizeJSONObject(activation)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	if size > MaxInputBytes {
		return nil, fmt.Errorf("input exceeds %d bytes", MaxInputBytes)
	}
	output, _, err := p.program.ContextEval(ctx, normalizedActivation)
	if err != nil {
		return nil, fmt.Errorf("evaluate expression: %w", err)
	}
	if types.IsError(output) {
		return nil, fmt.Errorf("evaluate expression: %s", output)
	}
	native, err := valueToJSON(output)
	if err != nil {
		return nil, fmt.Errorf("expression result: %w", err)
	}
	if p.requireBoolean {
		if _, ok := native.(bool); !ok {
			return nil, fmt.Errorf("expression result must be boolean, got %T", native)
		}
	}
	normalized, size, err := normalizeJSON(native)
	if err != nil {
		return nil, fmt.Errorf("expression result: %w", err)
	}
	if size > MaxOutputBytes {
		return nil, fmt.Errorf("output exceeds %d bytes", MaxOutputBytes)
	}
	return normalized, nil
}

func (p *Program) EvalBool(ctx context.Context, inputs map[string]any) (bool, error) {
	value, err := p.EvalJSON(ctx, inputs, nil, false)
	if err != nil {
		return false, err
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("expression result must be boolean, got %T", value)
	}
	return result, nil
}

func normalizeJSONObject(value map[string]any) (map[string]any, int, error) {
	normalized, size, err := normalizeJSON(value)
	if err != nil {
		return nil, 0, err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("activation must be a JSON object")
	}
	return object, size, nil
}

func normalizeJSON(value any) (any, int, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, 0, fmt.Errorf("value is not JSON compatible: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, 0, fmt.Errorf("normalize JSON value: %w", err)
	}
	return normalized, len(payload), nil
}

func valueToJSON(value ref.Val) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("CEL returned no value")
	}
	if types.IsError(value) {
		return nil, fmt.Errorf("CEL returned error: %s", value)
	}
	if mapper, ok := value.(traits.Mapper); ok {
		result := map[string]any{}
		iterator := mapper.Iterator()
		for iteratorHasNext(iterator) {
			key := iterator.Next()
			keyValue, err := valueToJSON(key)
			if err != nil {
				return nil, err
			}
			keyText, ok := keyValue.(string)
			if !ok {
				return nil, fmt.Errorf("CEL object key must be a string, got %T", keyValue)
			}
			item, found := mapper.Find(key)
			if !found {
				return nil, fmt.Errorf("CEL object key %q disappeared during evaluation", keyText)
			}
			converted, err := valueToJSON(item)
			if err != nil {
				return nil, fmt.Errorf("CEL object key %q: %w", keyText, err)
			}
			result[keyText] = converted
		}
		return result, nil
	}
	if list, ok := value.(traits.Lister); ok {
		result := make([]any, 0)
		iterator := list.Iterator()
		for iteratorHasNext(iterator) {
			converted, err := valueToJSON(iterator.Next())
			if err != nil {
				return nil, fmt.Errorf("CEL list item %d: %w", len(result), err)
			}
			result = append(result, converted)
		}
		return result, nil
	}
	switch value.Type() {
	case types.NullType:
		return nil, nil
	case types.BoolType, types.IntType, types.UintType, types.DoubleType, types.StringType:
		return value.Value(), nil
	default:
		return nil, fmt.Errorf("CEL result type %q is not JSON compatible", value.Type().TypeName())
	}
}

func iteratorHasNext(iterator traits.Iterator) bool {
	if iterator == nil {
		return false
	}
	next, ok := iterator.HasNext().Value().(bool)
	return ok && next
}
