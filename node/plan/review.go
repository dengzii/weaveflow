package plan

import (
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	executioncap "github.com/dengzii/weaveflow/capability/execution"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type ReviewNode struct {
	core.NodeBase
	MaxAttempts          int
	RetryExhaustedAction string
	FailureAction        string
	PlanPath             state.Path
	ExecutionPath        state.Path
	ConversationPath     state.Path
}

const (
	ReviewActionReplan   = "replan"
	ReviewActionFinalize = "finalize"
)

func NewReviewNode(options ...core.NodeOption) *ReviewNode {
	target := &ReviewNode{NodeBase: core.NewNodeBase(core.NodeSpec{
		Name:        NodeTypePlanReview,
		Description: "Record a step result and decide whether to continue, replan, or finish.",
	}), MaxAttempts: 2, RetryExhaustedAction: ReviewActionReplan, FailureAction: ReviewActionFinalize}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *ReviewNode) Validate() error {
	if n == nil {
		return fmt.Errorf("plan review node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.PlanPath.Empty() || n.ExecutionPath.Empty() || n.ConversationPath.Empty() {
		return fmt.Errorf("plan review node %q requires plan, execution, and conversation paths", n.ID())
	}
	if n.MaxAttempts <= 0 {
		return fmt.Errorf("plan review node %q max_attempts must be greater than zero", n.ID())
	}
	if !validReviewAction(n.RetryExhaustedAction) || !validReviewAction(n.FailureAction) {
		return fmt.Errorf("plan review node %q has invalid failure action", n.ID())
	}
	return nil
}

func (n *ReviewNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanReview, map[string]any{
		"max_attempts": n.MaxAttempts, "retry_exhausted_action": n.RetryExhaustedAction, "failure_action": n.FailureAction,
	}, map[string]state.Path{"plan": n.PlanPath, "execution": n.ExecutionPath, "conversation": n.ConversationPath})
}

func ReviewNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypePlanReview,
			Title:       "Plan Review",
			Description: "Record a step result and decide whether to continue, replan, or finish.",
			ConfigSchema: dsl.JSONSchema{"type": "object", "properties": dsl.JSONSchema{
				"max_attempts":           dsl.JSONSchema{"type": "integer", "minimum": 1},
				"retry_exhausted_action": dsl.JSONSchema{"type": "string", "enum": []string{ReviewActionReplan, ReviewActionFinalize}},
				"failure_action":         dsl.JSONSchema{"type": "string", "enum": []string{ReviewActionReplan, ReviewActionFinalize}},
			}, "additionalProperties": false},
			StatePorts: []dsl.StatePortDefinition{
				capabilityPort("plan", "Plan status and step results.", plancap.CapabilityID, true,
					capabilityField(plancap.FieldStatus, dsl.StateAccessWrite),
					capabilityField(plancap.FieldSteps, dsl.StateAccessReadWrite),
					capabilityField(plancap.FieldCurrentIndex, dsl.StateAccessReadWrite),
					capabilityField(plancap.FieldReplanCount, dsl.StateAccessRead),
					capabilityField(plancap.FieldMaxReplans, dsl.StateAccessRead),
					capabilityField(plancap.FieldReplanReason, dsl.StateAccessWrite)),
				capabilityPort("execution", "Execution result for the completed step.", executioncap.CapabilityID, true,
					capabilityField(executioncap.FieldStepResults, dsl.StateAccessWrite)),
				capabilityPort("conversation", "Conversation containing the completed step result.", conversationcap.CapabilityID, true,
					capabilityField(conversationcap.FieldFinalAnswer, dsl.StateAccessRead)),
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			planPath, err := resolvedPath(resolved, "plan")
			if err != nil {
				return nil, err
			}
			executionPath, err := resolvedPath(resolved, "execution")
			if err != nil {
				return nil, err
			}
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			target := NewReviewNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.PlanPath = planPath
			target.ExecutionPath = executionPath
			target.ConversationPath = conversationPath
			if value, ok := config.Int(spec.Config, "max_attempts"); ok {
				target.MaxAttempts = value
			}
			if value := config.String(spec.Config, "retry_exhausted_action"); value != "" {
				target.RetryExhaustedAction = value
			}
			if value := config.String(spec.Config, "failure_action"); value != "" {
				target.FailureAction = value
			}
			if !validReviewAction(target.RetryExhaustedAction) || !validReviewAction(target.FailureAction) {
				return nil, fmt.Errorf("build plan review node %q: invalid failure action", spec.ID)
			}
			return target, nil
		},
	}
}

func (n *ReviewNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *ReviewNode) execute(_ core.Context, access *state.Access) error {
	planner, err := plancap.Bind(access, n.PlanPath)
	if err != nil {
		return err
	}
	execution, err := executioncap.Bind(access, n.ExecutionPath)
	if err != nil {
		return err
	}
	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}

	planValue := planner.Value()
	steps := stepsFromValue(planValue[planFieldSteps])
	index := intValue(planValue[planFieldCurrentIndex])
	if index < 0 || index >= len(steps) {
		return planner.SetField(planFieldStatus, PlanStatusFinalizing)
	}
	step := steps[index]
	result := strings.TrimSpace(conversation.FinalAnswer())
	if result != "" {
		step.Result = result
	}
	switch step.VerificationStatus {
	case VerificationStatusPassed:
		step.Status = PlanStepStatusDone
		step.Error = ""
	case VerificationStatusRetry:
		step.Status = PlanStepStatusPending
		step.Error = step.VerificationSummary
	case VerificationStatusReplan:
		step.Status = PlanStepStatusFailed
		step.Error = step.VerificationSummary
	case VerificationStatusFailed:
		step.Status = PlanStepStatusFailed
		step.Error = step.VerificationSummary
	default:
		step.Status = PlanStepStatusFailed
		step.Error = "step has no verifier decision"
	}
	steps[index] = step
	if err := planner.SetField(planFieldSteps, stepMaps(steps)); err != nil {
		return err
	}
	stepResult := stepMaps([]plancap.Step{step})[0]
	if err := execution.SetStepResult(step.ID, stepResult); err != nil {
		return err
	}
	if step.VerificationStatus == VerificationStatusRetry && step.VerificationAttempts < n.MaxAttempts {
		return planner.SetField(planFieldStatus, PlanStatusExecuting)
	}
	shouldReplan := step.VerificationStatus == VerificationStatusReplan ||
		(step.VerificationStatus == VerificationStatusRetry && n.RetryExhaustedAction == ReviewActionReplan) ||
		(step.VerificationStatus == VerificationStatusFailed && n.FailureAction == ReviewActionReplan)
	if shouldReplan {
		replanCount := intValue(planValue[planFieldReplanCount])
		maxReplans := intValue(planValue[planFieldMaxReplans])
		if replanCount < maxReplans {
			if err := planner.SetField(planFieldReplanReason, fmt.Sprintf("step %s failed: %s", step.ID, step.Error)); err != nil {
				return err
			}
			return planner.SetField(planFieldStatus, PlanStatusReplan)
		}
		return planner.SetField(planFieldStatus, PlanStatusFinalizing)
	}
	if step.Status == PlanStepStatusFailed {
		return planner.SetField(planFieldStatus, PlanStatusFinalizing)
	}
	nextIndex := index + 1
	if err := planner.SetField(planFieldCurrentIndex, nextIndex); err != nil {
		return err
	}
	if nextIndex < len(steps) {
		return planner.SetField(planFieldStatus, PlanStatusExecuting)
	}
	return planner.SetField(planFieldStatus, PlanStatusFinalizing)
}

func validReviewAction(value string) bool {
	return value == ReviewActionReplan || value == ReviewActionFinalize
}
