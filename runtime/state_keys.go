package runtime

// Domain state keys — root-level fields in the State map.
// Grouped by module so additions are easy to audit.
const (
	// StateKeyRequest session
	StateKeyRequest    = "request"
	StateKeyAgent      = "agent"
	StateKeyToolPolicy = "tool_policy"

	// StateKeyIntent intent
	StateKeyIntent = "intent"

	// StateKeyOrchestration orchestration
	StateKeyOrchestration = "orchestration"

	// StateKeyMemory memory
	StateKeyMemory = "memory"

	// StateKeyPlanner planner
	StateKeyPlanner = "planner"

	// StateKeyExecution execution
	StateKeyExecution    = "execution"
	StateKeyObservations = "observations"
	StateKeyEvidence     = "evidence"

	// StateKeyVerification verification
	StateKeyVerification = "verification"
	StateKeyFinal        = "final"

	// StateKeyToolPolicyCheck safety
	StateKeyToolPolicyCheck = "tool_policy_check"
	StateKeyApproval        = "approval"
	StateKeyBudget          = "budget"

	// StateKeyReasoningBlocks holds []string of reasoning/thinking texts,
	// one entry per LLM invocation, for history display purposes.
	StateKeyReasoningBlocks = "reasoning_blocks"
)

// Conversation field keys — live inside the conversation namespace,
// not at the root level. Kept here for a single inventory.
const (
	StateKeyMessages       = "messages"
	StateKeyIterationCount = "iteration_count"
	StateKeyMaxIterations  = "max_iterations"
	StateKeyFinalAnswer    = "final_answer"
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
// It returns the existing sub-map, coercing map[string]any → State,
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
