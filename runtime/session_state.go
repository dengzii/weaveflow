package runtime

const (
	StateKeyRequest    = "request"
	StateKeyAgent      = "agent"
	StateKeyToolPolicy = "tool_policy"
)

func Request(state State) State {
	return rootObjectState(state, StateKeyRequest, false)
}

func EnsureRequest(state State) State {
	return rootObjectState(state, StateKeyRequest, true)
}

func Agent(state State) State {
	return rootObjectState(state, StateKeyAgent, false)
}

func EnsureAgent(state State) State {
	return rootObjectState(state, StateKeyAgent, true)
}

func ToolPolicy(state State) State {
	return rootObjectState(state, StateKeyToolPolicy, false)
}

func EnsureToolPolicy(state State) State {
	return rootObjectState(state, StateKeyToolPolicy, true)
}

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
