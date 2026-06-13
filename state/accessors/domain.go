package accessors

import (
	"fmt"

	"github.com/dengzii/weaveflow/state"
)

const (
	KeyRequest       = "request"
	KeyAgent         = "agent"
	KeyToolPolicy    = "tool_policy"
	KeyEnvironment   = "environment"
	KeyOrchestration = "orchestration"
	KeyMemory        = "memory"
	KeyPlanner       = "planner"
	KeyExecution     = "execution"
	KeyObservations  = "observations"
	KeyEvidence      = "evidence"
	KeyVerification  = "verification"
	KeyFinal         = "final"
	KeyExplore       = "explore"
)

var (
	ConversationID  = state.NewAccessorID[Conversation]("conversation")
	RequestID       = state.NewAccessorID[Request]("request")
	AgentID         = state.NewAccessorID[Object]("agent")
	ToolPolicyID    = state.NewAccessorID[Object]("tool_policy")
	EnvironmentID   = state.NewAccessorID[Object]("environment")
	OrchestrationID = state.NewAccessorID[Object]("orchestration")
	MemoryID        = state.NewAccessorID[Object]("memory")
	PlannerID       = state.NewAccessorID[Object]("planner")
	ExecutionID     = state.NewAccessorID[Execution]("execution")
	ObservationsID  = state.NewAccessorID[Records]("observations")
	EvidenceID      = state.NewAccessorID[Records]("evidence")
	VerificationID  = state.NewAccessorID[Object]("verification")
	FinalID         = state.NewAccessorID[Final]("final")
	ExploreID       = state.NewAccessorID[Object]("explore")
)

func InstallDefaultAccessors(registry *state.Registry) error {
	if registry == nil {
		return fmt.Errorf("state accessor registry is nil")
	}
	registrations := []func(*state.Registry) error{
		registerConversation,
		registerRequest,
		func(r *state.Registry) error { return registerObject(r, AgentID.Name(), KeyAgent) },
		func(r *state.Registry) error { return registerObject(r, ToolPolicyID.Name(), KeyToolPolicy) },
		func(r *state.Registry) error { return registerObject(r, EnvironmentID.Name(), KeyEnvironment) },
		func(r *state.Registry) error { return registerObject(r, OrchestrationID.Name(), KeyOrchestration) },
		func(r *state.Registry) error { return registerObject(r, MemoryID.Name(), KeyMemory) },
		func(r *state.Registry) error { return registerObject(r, PlannerID.Name(), KeyPlanner) },
		registerExecution,
		func(r *state.Registry) error { return registerRecords(r, ObservationsID.Name(), KeyObservations) },
		func(r *state.Registry) error { return registerRecords(r, EvidenceID.Name(), KeyEvidence) },
		func(r *state.Registry) error { return registerObject(r, VerificationID.Name(), KeyVerification) },
		registerFinal,
		func(r *state.Registry) error { return registerObject(r, ExploreID.Name(), KeyExplore) },
	}
	for _, register := range registrations {
		if err := register(registry); err != nil {
			return err
		}
	}
	return nil
}

func sharedObjectRef(key string) state.Ref[map[string]any] {
	return state.NewRef[map[string]any](state.Shared(key)).WithMerge(state.MergeMerge)
}
