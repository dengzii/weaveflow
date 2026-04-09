package runtime

const StateKeyPlanner = "planner"

func Planner(state State) State {
	return plannerState(state, false)
}

func EnsurePlanner(state State) State {
	return plannerState(state, true)
}

func plannerState(state State, create bool) State {
	if state == nil {
		return nil
	}

	switch typed := state[StateKeyPlanner].(type) {
	case State:
		return typed
	case map[string]any:
		planner := State(typed)
		state[StateKeyPlanner] = planner
		return planner
	}
	if !create {
		return nil
	}

	planner := State{}
	state[StateKeyPlanner] = planner
	return planner
}
