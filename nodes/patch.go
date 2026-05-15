package nodes

import wfstate "weaveflow/state"

func executeStatePatch(input wfstate.State, transition func(wfstate.State) (wfstate.State, error)) (wfstate.StatePatch, error) {
	if transition == nil {
		return wfstate.StatePatch{}, nil
	}
	before := input
	if before == nil {
		before = wfstate.State{}
	}
	next, err := transition(before.CloneState())
	if err != nil {
		return nil, err
	}
	patch, err := wfstate.DiffState(before, next)
	if err != nil {
		return nil, err
	}
	return wfstate.StatePatch(patch), nil
}
