package builtin

import (
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
)

func resolveHumanMessageStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := graphNodeStateScope(spec.Config)
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected and updated by the human message node."},
			{Path: scopedStatePath(scope, node.PendingHumanInputStateKey), Mode: dsl.StateAccessReadWrite, Description: "Pending human input consumed from state before resuming execution."},
		},
	}, nil
}

func resolveContextReducerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := graphNodeStateScope(spec.Config)
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages read and compacted into a reduced message history."}},
	}, nil
}

func resolveLLMStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := graphNodeStateScope(spec.Config)
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages sent to the model and extended with the model response."},
			{Path: scopedConversationPath(scope, "iteration_count"), Mode: dsl.StateAccessReadWrite, Description: "Iteration counter used to stop tool loops and incremented after each model turn."},
			{Path: scopedConversationPath(scope, "max_iterations"), Mode: dsl.StateAccessRead, Description: "Maximum number of tool-using iterations allowed for the current conversation scope."},
			{Path: scopedConversationPath(scope, "final_answer"), Mode: dsl.StateAccessWrite, Description: "Final answer written when the model finishes without further tool calls."},
			{Path: state.Shared(accessors.KeyExecution).String(), Mode: dsl.StateAccessReadWrite, Description: "Plan step execution state: read current_step.id and track last_llm_step_id to scrub prior-step messages at step boundary."},
		},
	}, nil
}

func resolveToolsStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := graphNodeStateScope(spec.Config)
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected for tool calls and extended with tool responses."}},
	}, nil
}

func resolveAgentStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := graphNodeStateScope(spec.Config)
	fields := []dsl.StateFieldRef{
		{Path: scopedConversationPath(scope, "messages"), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages the agent reads and extends across each internal iteration."},
		{Path: scopedConversationPath(scope, "iteration_count"), Mode: dsl.StateAccessReadWrite, Description: "Iteration counter bumped after every internal model turn."},
		{Path: scopedConversationPath(scope, "max_iterations"), Mode: dsl.StateAccessReadWrite, Description: "Maximum iteration cap for the agent loop, applied if not already higher."},
		{Path: scopedConversationPath(scope, "final_answer"), Mode: dsl.StateAccessWrite, Description: "Final answer written when the agent stops without further tool calls."},
	}
	if inputPath := strings.TrimSpace(config.String(spec.Config, "input_path")); inputPath != "" {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(inputPath), Mode: dsl.StateAccessRead, Required: true, Description: "State path the agent reads its initial task from.", Dynamic: true, PathConfigKey: "input_path"})
	}
	if outputPath := strings.TrimSpace(config.String(spec.Config, "output_path")); outputPath != "" {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(outputPath), Mode: dsl.StateAccessWrite, Description: "State path the agent writes its final answer to.", Dynamic: true, PathConfigKey: "output_path"})
	}
	return dsl.StateContract{Fields: fields}, nil
}

func resolveMappedSubgraphStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	inputMap := config.StringMap(spec.Config, "input_map")
	outputMap := config.StringMap(spec.Config, "output_map")
	fields := make([]dsl.StateFieldRef, 0, len(inputMap)+len(outputMap))
	inputPaths := make([]string, 0, len(inputMap))
	for parentPath := range inputMap {
		inputPaths = append(inputPaths, parentPath)
	}
	sort.Strings(inputPaths)
	for _, parentPath := range inputPaths {
		fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(parentPath), Mode: dsl.StateAccessRead, Required: true, Description: "Input path mapped into the subgraph."})
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

func graphNodeStateScope(configMap map[string]any) string {
	if _, ok := configMap["state_scope"]; ok {
		return config.String(configMap, "state_scope")
	}
	return node.DefaultScope
}

func scopedConversationPath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if field == "" {
		if scope == "" {
			return state.Shared(accessors.KeyConversation).String()
		}
		return state.Scope(scope, accessors.KeyConversation).String()
	}
	if scope == "" {
		return state.Shared(accessors.KeyConversation, field).String()
	}
	return state.Scope(scope, accessors.KeyConversation, field).String()
}

func scopedStatePath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if scope == "" {
		return state.Shared(field).String()
	}
	if field == "" {
		return state.Scope(scope).String()
	}
	return state.Scope(scope, field).String()
}

func canonicalContractPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "*" {
		return path
	}
	if parsed, err := state.ParsePath(path); err == nil {
		return parsed.String()
	}
	return path
}
