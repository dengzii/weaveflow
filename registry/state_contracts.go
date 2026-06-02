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

func ResolveLLMStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages sent to the model and extended with the model response."},
			{Path: scopedConversationPath(scope, "iteration_count"), Mode: dsl.StateAccessReadWrite, Description: "Iteration counter used to stop tool loops and incremented after each model turn."},
			{Path: scopedConversationPath(scope, "max_iterations"), Mode: dsl.StateAccessRead, Description: "Maximum number of tool-using iterations allowed for the current conversation scope."},
			{Path: scopedConversationPath(scope, "final_answer"), Mode: dsl.StateAccessWrite, Description: "Final answer written when the model finishes without further tool calls."},
			{Path: canonicalContractPath(wfstate.KeyExecution), Mode: dsl.StateAccessReadWrite, Description: "Plan step execution state: read current_step.id and track last_llm_step_id to scrub prior-step messages at step boundary."},
		},
	}, nil
}

func ResolveToolsStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected for tool calls and extended with tool responses."}},
	}, nil
}

func ResolveAgentStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := StringConfig(spec.Config, "state_scope")
	fields := []dsl.StateFieldRef{
		{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages the agent reads and extends across each internal iteration."},
		{Path: scopedConversationPath(scope, "iteration_count"), Mode: dsl.StateAccessReadWrite, Description: "Iteration counter bumped after every internal model turn."},
		{Path: scopedConversationPath(scope, "max_iterations"), Mode: dsl.StateAccessReadWrite, Description: "Maximum iteration cap for the agent loop, applied if not already higher."},
		{Path: scopedConversationPath(scope, "final_answer"), Mode: dsl.StateAccessWrite, Description: "Final answer written when the agent stops without further tool calls."},
	}
	if inputPath := strings.TrimSpace(StringConfig(spec.Config, "input_path")); inputPath != "" {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(inputPath), Mode: dsl.StateAccessRead, Description: "State path the agent reads its initial task from.", Dynamic: true, PathConfigKey: "input_path"})
	}
	if outputPath := strings.TrimSpace(StringConfig(spec.Config, "output_path")); outputPath != "" {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(outputPath), Mode: dsl.StateAccessWrite, Description: "State path the agent writes its final answer to.", Dynamic: true, PathConfigKey: "output_path"})
	}
	return dsl.StateContract{Fields: fields}, nil
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
