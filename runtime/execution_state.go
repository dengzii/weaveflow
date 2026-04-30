package runtime

func (s State) Observations() []map[string]any {
	if s == nil {
		return nil
	}
	raw, ok := s[StateKeyObservations]
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

func (s State) AppendObservation(obs map[string]any) {
	if s == nil || obs == nil {
		return
	}
	existing := s.Observations()
	s[StateKeyObservations] = append(existing, obs)
}

func (s State) Evidence() []map[string]any {
	if s == nil {
		return nil
	}
	raw, ok := s[StateKeyEvidence]
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

func (s State) AppendEvidence(ev map[string]any) {
	if s == nil || ev == nil {
		return
	}
	existing := s.Evidence()
	s[StateKeyEvidence] = append(existing, ev)
}

func (s State) StepResults() map[string]any {
	exec := s.Get(StateKeyExecution)
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

func (s State) SetStepResult(stepID string, result map[string]any) {
	if s == nil || stepID == "" {
		return
	}
	exec := s.Ensure(StateKeyExecution)
	results, ok := exec["step_results"].(map[string]any)
	if !ok {
		results = map[string]any{}
		exec["step_results"] = results
	}
	results[stepID] = result
}
