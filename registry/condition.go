package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type RouteDecision struct {
	Matched bool
	Targets []core.NodeRef
	Send    []core.Send
	Reason  string
	Details map[string]any
}

func (decision RouteDecision) Validate() error {
	if !decision.Matched && (len(decision.Targets) > 0 || len(decision.Send) > 0) {
		return fmt.Errorf("unmatched route decision cannot declare targets or sends")
	}
	if len(decision.Targets) > 0 && len(decision.Send) > 0 {
		return fmt.Errorf("route decision cannot combine targets and sends")
	}
	for _, target := range decision.Targets {
		if strings.TrimSpace(string(target)) == "" {
			return fmt.Errorf("route decision target is empty")
		}
	}
	for _, send := range decision.Send {
		if strings.TrimSpace(string(send.Target)) == "" {
			return fmt.Errorf("route decision send target is empty")
		}
		if issues := state.ValidateInputPatch(send.Input); len(issues) > 0 {
			return fmt.Errorf("route decision send input: %s", issues[0].Message)
		}
		if _, err := json.Marshal(send.Input); err != nil {
			return fmt.Errorf("route decision send input cannot be encoded: %w", err)
		}
	}
	if strings.TrimSpace(decision.Reason) == "" && len(decision.Details) > 0 {
		return fmt.Errorf("route decision details require a reason")
	}
	return nil
}

type EdgeConditionEvaluator func(context.Context, *state.State) (RouteDecision, error)

type EdgeCondition struct {
	Spec     dsl.GraphConditionSpec
	Evaluate EdgeConditionEvaluator
}

func NewEdgeCondition(spec dsl.GraphConditionSpec, evaluate EdgeConditionEvaluator) EdgeCondition {
	return EdgeCondition{Spec: dsl.NormalizeGraphConditionSpec(spec), Evaluate: evaluate}
}

func (condition EdgeCondition) Validate() error {
	spec := dsl.NormalizeGraphConditionSpec(condition.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if condition.Evaluate == nil {
		return fmt.Errorf("condition evaluator is nil")
	}
	return nil
}

func (condition EdgeCondition) EvaluateRoute(ctx context.Context, current *state.State) (RouteDecision, error) {
	decision, err := condition.Evaluate(ctx, current)
	if err != nil {
		return RouteDecision{}, err
	}
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.Targets = append([]core.NodeRef(nil), decision.Targets...)
	decision.Send = append([]core.Send(nil), decision.Send...)
	decision.Details, err = cloneRouteDetails(decision.Details)
	if err != nil {
		return RouteDecision{}, err
	}
	for index := range decision.Send {
		decision.Send[index].Input, err = cloneRoutePatch(decision.Send[index].Input)
		if err != nil {
			return RouteDecision{}, fmt.Errorf("route decision send %d input: %w", index, err)
		}
	}
	if err := decision.Validate(); err != nil {
		return RouteDecision{}, err
	}
	return decision, nil
}

func cloneRouteDetails(details map[string]any) (map[string]any, error) {
	if len(details) == 0 {
		return nil, nil
	}
	clonedValue, err := state.CloneValue(details)
	if err != nil {
		return nil, fmt.Errorf("route decision details cannot be safely cloned: %w", err)
	}
	cloned, ok := clonedValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("route decision details clone has type %T", clonedValue)
	}
	if _, err := json.Marshal(cloned); err != nil {
		return nil, fmt.Errorf("route decision details cannot be encoded: %w", err)
	}
	return cloned, nil
}

func cloneRoutePatch(patch state.Patch) (state.Patch, error) {
	operations := patch.Ops()
	for index := range operations {
		value, err := state.CloneValue(operations[index].Value)
		if err != nil {
			return state.Patch{}, fmt.Errorf("operation %d value cannot be safely cloned: %w", index, err)
		}
		operations[index].Value = value
	}
	return state.NewPatch(operations...), nil
}

func (condition EdgeCondition) WithSpec(spec dsl.GraphConditionSpec) EdgeCondition {
	condition.Spec = dsl.NormalizeGraphConditionSpec(spec)
	return condition
}

func (condition EdgeCondition) CloneSpec() dsl.GraphConditionSpec {
	return dsl.CloneGraphConditionSpec(condition.Spec)
}
