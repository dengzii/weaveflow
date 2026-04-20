package runtime

const StateKeyIntent = "intent"

func Intent(state State) State {
	return intentState(state, false)
}

func EnsureIntent(state State) State {
	return intentState(state, true)
}

func intentState(state State, create bool) State {
	if state == nil {
		return nil
	}

	switch typed := state[StateKeyIntent].(type) {
	case State:
		return typed
	case map[string]any:
		intent := State(typed)
		state[StateKeyIntent] = intent
		return intent
	}
	if !create {
		return nil
	}

	intent := State{}
	state[StateKeyIntent] = intent
	return intent
}
