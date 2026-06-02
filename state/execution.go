package state

func (s State) Observations() []map[string]any {
	if s == nil {
		return nil
	}
	raw, ok := s[KeyObservations]
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

func (s State) Evidence() []map[string]any {
	if s == nil {
		return nil
	}
	raw, ok := s[KeyEvidence]
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

func (s State) StepResults() map[string]any {
	exec := s.Get(KeyExecution)
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
