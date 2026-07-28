package node

import (
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	executioncap "github.com/dengzii/weaveflow/capability/execution"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type PlanReviewNode struct {
	Base
	PlanPath         state.Path
	ExecutionPath    state.Path
	ConversationPath state.Path
}

func NewPlanReviewNode(options ...NodeOption) *PlanReviewNode {
	target := &PlanReviewNode{Base: NewBase(Spec{
		Name:        NodeTypePlanReview,
		Description: "Record a step result and decide whether to continue, replan, or finish.",
	})}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *PlanReviewNode) Validate() error {
	if n == nil {
		return fmt.Errorf("plan review node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.PlanPath.Empty() || n.ExecutionPath.Empty() || n.ConversationPath.Empty() {
		return fmt.Errorf("plan review node %q requires plan, execution, and conversation paths", n.ID())
	}
	return nil
}

func (n *PlanReviewNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.Base, NodeTypePlanReview, nil, map[string]state.Path{"plan": n.PlanPath, "execution": n.ExecutionPath, "conversation": n.ConversationPath})
}

func PlanReviewNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:         NodeTypePlanReview,
			Title:        "Plan Review",
			Description:  "Record a step result and decide whether to continue, replan, or finish.",
			ConfigSchema: dsl.JSONSchema{"type": "object", "additionalProperties": false},
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
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
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
			target := NewPlanReviewNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.PlanPath = planPath
			target.ExecutionPath = executionPath
			target.ConversationPath = conversationPath
			return target, nil
		},
	}
}

func (n *PlanReviewNode) Execute(_ core.Context, access *state.Access) error {
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

	plan := planner.Value()
	steps := planStepsFromValue(plan[planFieldSteps])
	index := planInt(plan[planFieldCurrentIndex])
	if index < 0 || index >= len(steps) {
		return planner.SetField(planFieldStatus, PlanStatusFinalizing)
	}
	step := steps[index]
	result := strings.TrimSpace(conversation.FinalAnswer())
	if result == "" {
		step.Status = PlanStepStatusFailed
		step.Error = "step completed without a result"
		step.Result = ""
	} else {
		step.Status = PlanStepStatusDone
		step.Result = result
		step.Error = ""
	}
	steps[index] = step
	if err := planner.SetField(planFieldSteps, planStepMaps(steps)); err != nil {
		return err
	}
	stepResult := planStepMaps([]PlanStep{step})[0]
	if err := execution.SetStepResult(step.ID, stepResult); err != nil {
		return err
	}
	nextIndex := index + 1
	if err := planner.SetField(planFieldCurrentIndex, nextIndex); err != nil {
		return err
	}

	if step.Status == PlanStepStatusFailed {
		replanCount := planInt(plan[planFieldReplanCount])
		maxReplans := planInt(plan[planFieldMaxReplans])
		if replanCount < maxReplans {
			if err := planner.SetField(planFieldReplanReason, fmt.Sprintf("step %s failed: %s", step.ID, step.Error)); err != nil {
				return err
			}
			return planner.SetField(planFieldStatus, PlanStatusReplan)
		}
		return planner.SetField(planFieldStatus, PlanStatusFinalizing)
	}
	if nextIndex < len(steps) {
		return planner.SetField(planFieldStatus, PlanStatusExecuting)
	}
	return planner.SetField(planFieldStatus, PlanStatusFinalizing)
}
