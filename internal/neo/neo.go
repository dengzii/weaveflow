package neo

import (
	"path/filepath"

	"weaveflow"
	"weaveflow/core"
	"weaveflow/memory"
	fruntime "weaveflow/runtime"
	"weaveflow/state"
	"weaveflow/state/accessors"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

func NewServices(model llms.Model, baseDir string) *core.Services {
	model = fruntime.WrapLLM(model)
	repo := memory.NewFileMemoryRepository(filepath.Join(baseDir, "memory"))
	return &core.Services{
		Model:  model,
		Memory: memory.New(&memory.Options{Repository: repo, Retriever: memory.NewBM25Retriever(repo, nil)}),
		Tools: map[string]tools.Tool{
			"read":  tools.NewRead(),
			"write": tools.NewWrite(),
			"edit":  tools.NewEdit(),
			"glob":  tools.NewGlob(),
			"grep":  tools.NewGrep(),
			//"web_fetch": tools.NewWebFetch(),
			// "web_search": tools.NewWebSearch(),
		},
	}
}

const stateScope = "agent"

// Config todo move to run context or initial state
type Config struct {
	StateScope             string
	SystemPrompt           string
	MaxIterations          int
	RequestTimeoutSeconds  int
	PlannerMaxSteps        int
	MemoryRecallLimit      int
	HistoryRecentTurns     int
	HistorySummaryMaxChars int
	PromptMaxChars         int
	MemoryRecallTags       []string
	// Mode controls routing: "auto" lets the router decide, "direct" forces
	// single-turn execution, "planner" forces multi-step plan execution.
	Mode string
	// MaxRetries is the maximum number of retries per step before marking it blocked
	MaxRetries int
}

func DefaultConfig() Config {
	return Config{
		StateScope:             stateScope,
		SystemPrompt:           "You are Neo, a pragmatic general-purpose task agent. Use tools when they improve accuracy. Plan only when the task needs decomposition or verification. Ask for clarification when required and avoid guessing.",
		MaxIterations:          16,
		RequestTimeoutSeconds:  180,
		PlannerMaxSteps:        12,
		MemoryRecallLimit:      5,
		HistoryRecentTurns:     defaultPromptRecentTurns,
		HistorySummaryMaxChars: defaultPromptSummaryMaxChars,
		PromptMaxChars:         20000,
		MemoryRecallTags:       []string{"final_answer", "assistant_output", "user_input"},
		Mode:                   "auto",
		MaxRetries:             3,
	}
}

func NewGraph(cfg Config) (*weaveflow.Graph, error) {
	return nil, nil
}

func NewInitialState(input string, history []llms.MessageContent) *state.State {
	currentState := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: input,
		},
		accessors.KeyPlanner: map[string]any{
			"objective": input,
		},
	})
	if len(history) > 0 {
		msgs := make([]llms.MessageContent, len(history), len(history)+1)
		copy(msgs, history)
		msgs = append(msgs, llms.TextParts(llms.ChatMessageTypeHuman, input))
		_ = state.SetPath(currentState, state.Scope(stateScope, accessors.KeyConversation, accessors.ConversationFieldMessages).String(), msgs)
	}
	return currentState
}
