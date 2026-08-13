package registry

import (
	"context"
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
		if issues := state.ValidatePatch(send.Input); len(issues) > 0 {
			return fmt.Errorf("route decision send input: %s", issues[0].Message)
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
	decision.Details = cloneRouteDetails(decision.Details)
	for index := range decision.Send {
		decision.Send[index].Input = state.NewPatch(decision.Send[index].Input.Ops()...)
	}
	if err := decision.Validate(); err != nil {
		return RouteDecision{}, err
	}
	return decision, nil
}

func cloneRouteDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}

func (condition EdgeCondition) WithSpec(spec dsl.GraphConditionSpec) EdgeCondition {
	condition.Spec = dsl.NormalizeGraphConditionSpec(spec)
	return condition
}

func (condition EdgeCondition) CloneSpec() dsl.GraphConditionSpec {
	return dsl.CloneGraphConditionSpec(condition.Spec)
}
