package registry

import (
	"sort"
	"strings"

	"weaveflow/dsl"
	"weaveflow/nodes"
	wfstate "weaveflow/state"
)

func ResolveHumanMessageStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected and updated by the human message node."},
			{Path: scopedStatePath(scope, nodes.PendingHumanInputStateKey), Mode: dsl.StateAccessReadWrite, Description: "Pending human input consumed from state before resuming execution."},
		},
	}, nil
}

func ResolveContextReducerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages read and compacted into a reduced message history."}},
	}, nil
}

func ResolveContextAssemblerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	memoryPath := canonicalContractPath(defaultIfBlank(StringConfig(spec.Config, "memory_state_path"), wfstate.StateKeyMemory))
	orchestrationPath := canonicalContractPath(defaultIfBlank(StringConfig(spec.Config, "orchestration_state_path"), wfstate.StateKeyOrchestration))
	plannerPath := canonicalContractPath(defaultIfBlank(StringConfig(spec.Config, "planner_state_path"), wfstate.StateKeyPlanner))
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages updated with assembled memory context."},
			{Path: memoryPath, Mode: dsl.StateAccessRead, Description: "Structured memory state consumed for prompt assembly."},
			{Path: orchestrationPath, Mode: dsl.StateAccessRead, Description: "Structured orchestration state consumed for prompt assembly."},
			{Path: plannerPath, Mode: dsl.StateAccessRead, Description: "Structured planner state consumed for prompt assembly."},
		},
	}, nil
}

func ResolveLLMStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages sent to the model and extended with the model response."},
			{Path: scopedConversationPath(scope, "iteration_count"), Mode: dsl.StateAccessReadWrite, Description: "Iteration counter used to stop tool loops and incremented after each model turn."},
			{Path: scopedConversationPath(scope, "max_iterations"), Mode: dsl.StateAccessRead, Description: "Maximum number of tool-using iterations allowed for the current conversation scope."},
			{Path: scopedConversationPath(scope, "final_answer"), Mode: dsl.StateAccessWrite, Description: "Final answer written when the model finishes without further tool calls."},
			{Path: canonicalContractPath(nodes.TokenUsageStateKey), Mode: dsl.StateAccessWrite, Description: "Accumulated token usage metrics emitted by the model node.", MergeStrategy: dsl.StateMergeMerge},
		},
	}, nil
}

func ResolveToolsStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected for tool calls and extended with tool responses."}},
	}, nil
}

func ResolveIteratorStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	stateKey := canonicalContractPath(strings.TrimSpace(StringConfig(spec.Config, "state_key")))
	nodeID := strings.TrimSpace(spec.ID)
	runtimePath := nodes.IteratorStateRootKey
	if nodeID != "" {
		runtimePath += "." + nodeID
	}
	runtimePath = canonicalContractPath(runtimePath)

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: stateKey, Mode: dsl.StateAccessRead, Required: true, Description: "Source collection iterated by the iterator node.", Dynamic: true, PathConfigKey: "state_key"},
			{Path: runtimePath, Mode: dsl.StateAccessReadWrite, Description: "Iterator runtime state for the current node, including the current item and loop progress.", MergeStrategy: dsl.StateMergeMerge},
		},
	}, nil
}

func ResolveMappedSubgraphStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	inputMap := MapStringConfig(spec.Config, "input_map")
	outputMap := MapStringConfig(spec.Config, "output_map")
	fields := make([]dsl.StateFieldRef, 0, len(inputMap)+len(outputMap))
	inputPaths := make([]string, 0, len(inputMap))
	for parentPath := range inputMap {
		inputPaths = append(inputPaths, parentPath)
	}
	sort.Strings(inputPaths)
	for _, parentPath := range inputPaths {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(parentPath), Mode: dsl.StateAccessRead, Description: "Input path mapped into the subgraph."})
	}
	outputPaths := make([]string, 0, len(outputMap))
	for _, parentPath := range outputMap {
		outputPaths = append(outputPaths, parentPath)
	}
	sort.Strings(outputPaths)
	for _, parentPath := range outputPaths {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(parentPath), Mode: dsl.StateAccessWrite, Description: "Output path mapped back from the subgraph.", MergeStrategy: dsl.StateMergeMerge})
	}
	return dsl.StateContract{Fields: fields}, nil
}

func scopedConversationPath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if field == "" {
		if scope == "" {
			return "conversation"
		}
		return "scopes." + scope
	}
	if scope == "" {
		return "conversation." + field
	}
	return "scopes." + scope + "." + field
}

func scopedStatePath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if scope == "" {
		return canonicalContractPath(field)
	}
	if field == "" {
		return "scopes." + scope
	}
	return "scopes." + scope + "." + field
}

func canonicalContractPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "*" {
		return path
	}
	return wfstate.NormalizeContractPath(path)
}

func defaultIfBlank(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
