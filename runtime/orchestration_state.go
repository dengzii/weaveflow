package runtime

const StateKeyOrchestration = "orchestration"

func Orchestration(state State) State {
	return orchestrationState(state, false)
}

func EnsureOrchestration(state State) State {
	return orchestrationState(state, true)
}

func orchestrationState(state State, create bool) State {
	if state == nil {
		return nil
	}

	switch typed := state[StateKeyOrchestration].(type) {
	case State:
		return typed
	case map[string]any:
		orchestration := State(typed)
		state[StateKeyOrchestration] = orchestration
		return orchestration
	}
	if !create {
		return nil
	}

	orchestration := State{}
	state[StateKeyOrchestration] = orchestration
	return orchestration
}
