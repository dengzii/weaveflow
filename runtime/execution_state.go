package runtime

func Observations(state State) []map[string]any {
	if state == nil {
		return nil
	}
	raw, ok := state[StateKeyObservations]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	default:
		return nil
	}
}

func AppendObservation(state State, obs map[string]any) {
	if state == nil || obs == nil {
		return
	}
	existing := Observations(state)
	state[StateKeyObservations] = append(existing, obs)
}

func Evidence(state State) []map[string]any {
	if state == nil {
		return nil
	}
	raw, ok := state[StateKeyEvidence]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	default:
		return nil
	}
}

func AppendEvidence(state State, ev map[string]any) {
	if state == nil || ev == nil {
		return
	}
	existing := Evidence(state)
	state[StateKeyEvidence] = append(existing, ev)
}

func StepResults(state State) map[string]any {
	exec := state.Get(StateKeyExecution)
	if exec == nil {
		return nil
	}
	raw, ok := exec["step_results"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case State:
		return typed
	case map[string]any:
		return typed
	default:
		return nil
	}
}

func SetStepResult(state State, stepID string, result map[string]any) {
	if state == nil || stepID == "" {
		return
	}
	exec := state.Ensure(StateKeyExecution)
	results, ok := exec["step_results"].(map[string]any)
	if !ok {
		results = map[string]any{}
		exec["step_results"] = results
	}
	results[stepID] = result
}
