package runtime

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/state"
)

const AgentResumePhaseToolCalls = "tool_calls"

var agentResumePath = state.Internal("agent", "resume")

type agentResumeState struct {
	InvocationID string `json:"invocation_id"`
	Phase        string `json:"phase"`
}

func StoreAgentResumeState(current *state.State, invocationID, phase string) error {
	if current == nil {
		return nil
	}
	if strings.TrimSpace(invocationID) == "" || strings.TrimSpace(phase) == "" {
		return fmt.Errorf("agent resume state requires invocation ID and phase")
	}
	return state.SetPath(current, agentResumePath.String(), agentResumeState{InvocationID: invocationID, Phase: phase})
}

func LoadAgentResumeState(current *state.State) (string, bool, error) {
	if current == nil {
		return "", false, nil
	}
	value, ok := state.ReadPath(current, agentResumePath.String())
	if !ok {
		return "", false, nil
	}
	switch typed := value.(type) {
	case agentResumeState:
		if typed.Phase == "" || typed.InvocationID == "" {
			return "", false, fmt.Errorf("agent resume state is incomplete")
		}
		return typed.Phase, true, nil
	case map[string]any:
		invocationID, _ := typed["invocation_id"].(string)
		phase, _ := typed["phase"].(string)
		if strings.TrimSpace(invocationID) == "" || strings.TrimSpace(phase) == "" {
			return "", false, fmt.Errorf("agent resume state is incomplete")
		}
		return phase, true, nil
	default:
		return "", false, fmt.Errorf("agent resume state has invalid type %T", value)
	}
}

func ClearAgentResumeState(current *state.State) error {
	if current == nil {
		return nil
	}
	return state.DeletePath(current, agentResumePath.String())
}
