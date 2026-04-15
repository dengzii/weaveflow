package weaveflow

import (
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
)

func resolveSubgraphStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	_ = spec

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:          "*",
				Mode:          dsl.StateAccessReadWrite,
				Description:   "The full graph state passed into the subgraph and merged back from the subgraph result.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveHumanMessageStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Conversation messages inspected and updated by the human message node.",
			},
			{
				Path:        scopedStatePath(scope, nodes.PendingHumanInputStateKey),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Pending human input consumed from state before resuming execution.",
			},
		},
	}, nil
}

func resolveContextReducerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Conversation messages read and compacted into a reduced message history.",
			},
		},
	}, nil
}

func resolveLLMStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Conversation messages sent to the model and extended with the model response.",
			},
			{
				Path:        scopedConversationPath(scope, "iteration_count"),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Iteration counter used to stop tool loops and incremented after each model turn.",
			},
			{
				Path:        scopedConversationPath(scope, "max_iterations"),
				Mode:        dsl.StateAccessRead,
				Description: "Maximum number of tool-using iterations allowed for the current conversation scope.",
			},
			{
				Path:        scopedConversationPath(scope, "final_answer"),
				Mode:        dsl.StateAccessWrite,
				Description: "Final answer written when the model finishes without further tool calls.",
			},
			{
				Path:          nodes.TokenUsageStateKey,
				Mode:          dsl.StateAccessWrite,
				Description:   "Accumulated token usage metrics emitted by the model node.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveToolsStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Conversation messages inspected for tool calls and extended with tool responses.",
			},
		},
	}, nil
}

func resolveIteratorStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	stateKey := strings.TrimSpace(stringConfig(spec.Config, "state_key"))
	nodeID := strings.TrimSpace(spec.ID)
	runtimePath := nodes.IteratorStateRootKey
	if nodeID != "" {
		runtimePath += "." + nodeID
	}

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:          stateKey,
				Mode:          dsl.StateAccessRead,
				Required:      true,
				Description:   "Source collection iterated by the iterator node.",
				Dynamic:       true,
				PathConfigKey: "state_key",
			},
			{
				Path:          runtimePath,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Iterator runtime state for the current node, including the current item and loop progress.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func scopedConversationPath(scope string, field string) string {
	return scopedStatePath(scope, field)
}

func scopedStatePath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if scope == "" {
		return field
	}
	if field == "" {
		return "scopes." + scope
	}
	return "scopes." + scope + "." + field
}
