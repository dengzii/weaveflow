package state

// Domain state keys are root-level fields in the State map.
// Grouped by module so additions are easy to audit.
const (
	// KeyRequest session
	KeyRequest     = "request"
	KeyAgent       = "agent"
	KeyToolPolicy  = "tool_policy"
	KeyEnvironment = "environment"

	KeyOrchestration = "orchestration"

	KeyMemory = "memory"

	KeyPlanner = "planner"

	KeyExecution    = "execution"
	KeyObservations = "observations"
	KeyEvidence     = "evidence"

	KeyVerification = "verification"
	KeyFinal        = "final"

	KeyExplore = "explore"
)

// Conversation field keys live inside the conversation namespace,
// not at the root level. Kept here for a single inventory.
const (
	KeyMessages       = "messages"
	KeyIterationCount = "iteration_count"
	KeyMaxIterations  = "max_iterations"
	KeyFinalAnswer    = "final_answer"
)

// Get returns the sub-map at the given root-level key, or nil if absent.
func (s State) Get(key string) State {
	return rootObjectState(s, key, false)
}

// Ensure returns the sub-map at the given root-level key, creating it if absent.
func (s State) Ensure(key string) State {
	return rootObjectState(s, key, true)
}

// rootObjectState is the shared accessor for root-level map fields.
// It returns the existing sub-map, coercing map[string]any to State,
// and optionally creates an empty one if [create] is true.
func rootObjectState(state State, key string, create bool) State {
	if state == nil || key == "" {
		return nil
	}

	switch typed := state[key].(type) {
	case State:
		return typed
	case map[string]any:
		nested := State(typed)
		state[key] = nested
		return nested
	}
	if !create {
		return nil
	}

	nested := State{}
	state[key] = nested
	return nested
}
