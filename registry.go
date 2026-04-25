package weaveflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"weaveflow/dsl"
	"weaveflow/memory"
	"weaveflow/nodes"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type JSONSchema = dsl.JSONSchema
type GraphConditionSpec = dsl.GraphConditionSpec
type GraphNodeSpec = dsl.GraphNodeSpec
type StateFieldDefinition = dsl.StateFieldDefinition
type StateContract = dsl.StateContract
type StateFieldRef = dsl.StateFieldRef
type GraphDefinition = dsl.GraphDefinition
type NodeTypeSchema = dsl.NodeTypeSchema
type ConditionSchema = dsl.ConditionSchema

type GraphResolver func(graphRef string) (dsl.GraphDefinition, error)

type BuildContext struct {
	Model          llms.Model
	Memory         memory.Manager
	Tools          map[string]tools.Tool
	InstanceConfig *dsl.GraphInstanceConfig
	GraphResolver  GraphResolver
	graphBuildPath []string
}

type NodeTypeDefinition struct {
	dsl.NodeTypeSchema
	Build                func(*BuildContext, dsl.GraphNodeSpec) (nodes.Node[State], error) `json:"-"`
	ResolveStateContract func(dsl.GraphNodeSpec) (dsl.StateContract, error)                `json:"-"`
}

type ConditionDefinition struct {
	dsl.ConditionSchema
	Resolve func(GraphConditionSpec) (EdgeCondition, error) `json:"-"`
}

type Registry struct {
	StateFields map[string]StateFieldDefinition `json:"state_fields"`
	NodeTypes   map[string]NodeTypeDefinition   `json:"node_types"`
	Conditions  map[string]ConditionDefinition  `json:"conditions"`
}

func NewRegistry() *Registry {
	return &Registry{
		StateFields: map[string]StateFieldDefinition{},
		NodeTypes:   map[string]NodeTypeDefinition{},
		Conditions:  map[string]ConditionDefinition{},
	}
}

func DefaultRegistry() *Registry {
	r := NewRegistry()

	for _, field := range defaultStateFieldDefinitions() {
		r.RegisterStateField(field)
	}

	RegisterSessionBootstrapModule(r)
	RegisterIntentModule(r)
	RegisterOrchestrationModule(r)
	RegisterMemoryModule(r)
	RegisterContextModule(r)
	RegisterExecutionModule(r)
	RegisterVerificationModule(r)

	r.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "subgraph",
			Title:       "Subgraph Node",
			Description: "Invoke another graph resolved by graph_ref using the current state.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"graph_ref": JSONSchema{"type": "string"},
				},
				"required":             []string{"graph_ref"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: resolveSubgraphStateContract,
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			graphRef := stringConfig(spec.Config, "graph_ref")
			if graphRef == "" {
				return nil, fmt.Errorf("build subgraph nodes %q: graph_ref is required", spec.ID)
			}
			if ctx == nil || ctx.GraphResolver == nil {
				return nil, fmt.Errorf("build subgraph nodes %q: graph resolver is required", spec.ID)
			}
			if err := validateGraphBuildPath(ctx.graphBuildPath, graphRef); err != nil {
				return nil, fmt.Errorf("build subgraph nodes %q: %w", spec.ID, err)
			}

			def, err := ctx.GraphResolver(graphRef)
			if err != nil {
				return nil, fmt.Errorf("build subgraph nodes %q resolve %q: %w", spec.ID, graphRef, err)
			}

			subgraphCtx := cloneBuildContext(ctx)
			subgraphCtx.InstanceConfig = nil
			subgraphCtx.graphBuildPath = append(subgraphCtx.graphBuildPath, graphRef)

			subgraph, err := r.buildGraph(def, nil, subgraphCtx)
			if err != nil {
				return nil, fmt.Errorf("build subgraph nodes %q graph %q: %w", spec.ID, graphRef, err)
			}

			node := nodes.NewSubgraphNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.GraphRef = graphRef
			node.InvokeSubgraph = func(ctx context.Context, state State) (State, error) {
				return subgraph.Run(ctx, state)
			}
			return node, nil
		},
	})

	r.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "iterator",
			Title:       "Iterator Node",
			Description: "Iterate over a state array and inject the current iteration into temporary state.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_key":      JSONSchema{"type": "string"},
					"max_iterations": JSONSchema{"type": "integer", "minimum": 1},
					"continue_to":    JSONSchema{"type": "string"},
					"done_to":        JSONSchema{"type": "string"},
				},
				"required":             []string{"state_key", "max_iterations"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: resolveIteratorStateContract,
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			_ = ctx

			stateKey := stringConfig(spec.Config, "state_key")
			if stateKey == "" {
				return nil, fmt.Errorf("build iterator nodes %q: state_key is required", spec.ID)
			}
			maxIterations, ok := intConfig(spec.Config, "max_iterations")
			if !ok || maxIterations <= 0 {
				return nil, fmt.Errorf("build iterator nodes %q: max_iterations must be greater than 0", spec.ID)
			}

			node := nodes.NewIteratorNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateKey = stateKey
			node.MaxIterations = maxIterations
			node.ContinueTo = stringConfig(spec.Config, "continue_to")
			node.DoneTo = stringConfig(spec.Config, "done_to")
			return node, nil
		},
	})

	r.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "human_message",
			Title:       "Human Message Node",
			Description: "Pause the graph until the latest message in scope is a human message.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":       JSONSchema{"type": "string"},
					"interrupt_message": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: resolveHumanMessageStateContract,
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewHumanMessageNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			if scope := stringConfig(spec.Config, "state_scope"); scope != "" {
				node.StateScope = scope
			}
			if message := stringConfig(spec.Config, "interrupt_message"); message != "" {
				node.InterruptMessage = message
			}
			return node, nil
		},
	})

	r.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "context_reducer",
			Title:       "Context Reducer Node",
			Description: "Compact older conversation context into a summary message before the next model turn.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":     JSONSchema{"type": "string"},
					"max_messages":    JSONSchema{"type": "integer", "minimum": 2},
					"preserve_system": JSONSchema{"type": "boolean"},
					"preserve_recent": JSONSchema{"type": "integer", "minimum": 0},
					"summary_prefix":  JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: resolveContextReducerStateContract,
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			if ctx == nil || ctx.Model == nil {
				return nil, fmt.Errorf("build context_reducer nodes %q: model is required", spec.ID)
			}

			node := nodes.NewContextReducerNode(ctx.Model)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			if value, ok := intConfig(spec.Config, "max_messages"); ok {
				node.MaxMessages = value
			}
			if value, ok := boolConfig(spec.Config, "preserve_system"); ok {
				node.PreserveSystem = value
			}
			if value, ok := intConfig(spec.Config, "preserve_recent"); ok {
				node.PreserveRecent = value
			}
			if value := stringConfig(spec.Config, "summary_prefix"); value != "" {
				node.SummaryPrefix = value
			}
			return node, nil
		},
	})

	r.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "llm",
			Title:       "LLM Node",
			Description: "Built-in model inference nodes.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"tool_ids": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"state_scope": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: resolveLLMStateContract,
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			if ctx.Model == nil {
				return nil, fmt.Errorf("build llm nodes %q: model is required", spec.ID)
			}
			ts, err := resolveTools(ctx.Tools, stringSliceConfig(spec.Config, "tool_ids"))
			if err != nil {
				return nil, fmt.Errorf("build llm nodes %q: %w", spec.ID, err)
			}
			node := nodes.NewLLMNode(ctx.Model, ts)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			return node, nil
		},
	})

	r.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "tools",
			Title:       "Tools Node",
			Description: "Built-in tool execution nodes.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"tool_ids": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"state_scope": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: resolveToolsStateContract,
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			toolIDs := stringSliceConfig(spec.Config, "tool_ids")
			ts, err := resolveTools(ctx.Tools, toolIDs)
			if err != nil {
				return nil, fmt.Errorf("build tools nodes %q: %w", spec.ID, err)
			}
			node := nodes.NewToolCallNode(ts)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			return node, nil
		},
	})
	r.RegisterCondition(ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "last_message_has_tool_calls",
			Title:       "Last Message Has Tool Calls",
			Description: "Routes when the last AI message includes tool calls.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			return LastMessageHasToolCalls(stringConfig(spec.Config, "state_scope")), nil
		},
	})

	r.RegisterCondition(ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "has_final_answer",
			Title:       "Has Final Answer",
			Description: "Routes when the current state already contains a final answer.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			return HasFinalAnswer(stringConfig(spec.Config, "state_scope")), nil
		},
	})

	r.RegisterCondition(ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "expression_conditions",
			Title:       "Expression Conditions",
			Description: "Routes by evaluating serializable expressions against the current state.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope": JSONSchema{"type": "string"},
					"match": JSONSchema{
						"type": "string",
						"enum": []string{ExpressionMatchAll, ExpressionMatchAny},
					},
					"expressions": JSONSchema{
						"type": "array",
						"items": JSONSchema{
							"type": "object",
							"properties": JSONSchema{
								"value1": JSONSchema{"type": "string"},
								"op": JSONSchema{
									"type": "string",
									"enum": []string{
										OperationEqual,
										OperationNotEqual,
										OperationContains,
										OperationNotContain,
									},
								},
								"value2": JSONSchema{"type": "string"},
							},
							"required":             []string{"value1", "op", "value2"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"expressions"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			config, err := ParseExpressionConditionConfig(spec.Config)
			if err != nil {
				return EdgeCondition{}, fmt.Errorf("resolve expression condition: %w", err)
			}
			return ExpressionConditions(config)
		},
	})
	return r
}

func (r *Registry) RegisterStateField(def StateFieldDefinition) {
	r.StateFields[def.Name] = def
}

func (r *Registry) RegisterNodeType(def NodeTypeDefinition) {
	r.NodeTypes[def.Type] = def
}

func (r *Registry) RegisterCondition(def ConditionDefinition) {
	r.Conditions[def.Type] = def
}

func (r *Registry) ResolveCondition(spec GraphConditionSpec) (EdgeCondition, error) {
	if r == nil {
		return EdgeCondition{}, fmt.Errorf("registry is nil")
	}
	spec = dsl.NormalizeGraphConditionSpec(spec)
	if spec.Type == "" {
		return EdgeCondition{}, fmt.Errorf("condition type is required")
	}
	conditionDef, ok := r.Conditions[spec.Type]
	if !ok {
		return EdgeCondition{}, fmt.Errorf("condition %q is not registered", spec.Type)
	}
	condition, err := conditionDef.Resolve(spec)
	if err != nil {
		return EdgeCondition{}, err
	}
	return condition.withSpec(spec), nil
}

func (r *Registry) ResolveNodeStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	if r == nil {
		return dsl.StateContract{}, fmt.Errorf("registry is nil")
	}
	spec = dsl.NormalizeGraphDefinition(dsl.GraphDefinition{Nodes: []dsl.GraphNodeSpec{spec}}).Nodes[0]
	if spec.Type == "" {
		return dsl.StateContract{}, fmt.Errorf("node type is required")
	}

	nodeDef, ok := r.NodeTypes[spec.Type]
	if !ok {
		return dsl.StateContract{}, fmt.Errorf("node type %q is not registered", spec.Type)
	}
	if nodeDef.ResolveStateContract != nil {
		return nodeDef.ResolveStateContract(spec)
	}
	if nodeDef.StateContract == nil {
		return dsl.StateContract{}, nil
	}
	return nodeDef.StateContract.Clone(), nil
}

func (r *Registry) AddConditionalEdge(g *Graph, from, to string, spec GraphConditionSpec) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	condition, err := r.ResolveCondition(spec)
	if err != nil {
		return err
	}
	return g.AddConditionalEdge(from, to, condition)
}

func (r *Registry) BuildGraph(def GraphDefinition, ctx *BuildContext) (*Graph, error) {
	return r.buildGraph(def, nil, ctx)
}

func (r *Registry) BuildGraphInstance(def GraphDefinition, instance dsl.GraphInstanceConfig, ctx *BuildContext) (*Graph, error) {
	return r.buildGraph(def, &instance, ctx)
}

func (r *Registry) buildGraph(def GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *BuildContext) (*Graph, error) {
	def = dsl.NormalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return nil, err
	}
	if def.StateSchema != "" && def.StateSchema != dsl.CommonStateSchemaID {
		return nil, fmt.Errorf("unsupported state schema %q", def.StateSchema)
	}
	if instance != nil {
		normalized := *instance
		if err := normalized.Validate(); err != nil {
			return nil, err
		}
		applied, err := ApplyGraphInstanceConfig(def, normalized)
		if err != nil {
			return nil, err
		}
		def = applied
		if ctx != nil {
			clonedCtx := *ctx
			clonedInstance := normalized
			ctx = &clonedCtx
			ctx.InstanceConfig = &clonedInstance
		}
	}

	g := NewGraph()
	for _, nodeSpec := range def.Nodes {
		nodeDef, ok := r.NodeTypes[nodeSpec.Type]
		if !ok {
			return nil, fmt.Errorf("nodes type %q is not registered", nodeSpec.Type)
		}
		node, err := nodeDef.Build(ctx, nodeSpec)
		if err != nil {
			return nil, err
		}
		if err := g.AddNode(node); err != nil {
			return nil, err
		}
	}
	if err := r.applyBuiltInNodeEdges(g, def); err != nil {
		return nil, err
	}
	for _, edge := range def.Edges {
		if edge.Condition == nil {
			if err := g.AddEdge(edge.From, edge.To); err != nil {
				return nil, err
			}
			continue
		}
		condition, err := r.ResolveCondition(*edge.Condition)
		if err != nil {
			return nil, err
		}
		if err := g.AddConditionalEdge(edge.From, edge.To, condition); err != nil {
			return nil, err
		}
	}

	if def.EntryPoint != "" {
		if err := g.SetEntryPoint(def.EntryPoint); err != nil {
			return nil, err
		}
	}
	if def.FinishPoint != "" {
		if err := g.SetFinishPoint(def.FinishPoint); err != nil {
			return nil, err
		}
	}

	if err := g.Validate(); err != nil {
		return nil, err
	}

	return g, nil
}

func (r *Registry) applyBuiltInNodeEdges(g *Graph, def GraphDefinition) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}

	for _, nodeSpec := range def.Nodes {
		if nodeSpec.Type != "iterator" {
			continue
		}
		continueTo := stringConfig(nodeSpec.Config, "continue_to")
		doneTo := stringConfig(nodeSpec.Config, "done_to")
		if continueTo == "" && doneTo == "" {
			continue
		}
		if continueTo == "" || doneTo == "" {
			return fmt.Errorf("build iterator nodes %q: continue_to and done_to must be configured together", nodeSpec.ID)
		}
		if hasExplicitOutgoingEdge(def.Edges, nodeSpec.ID) {
			return fmt.Errorf("build iterator nodes %q: built-in iterator edges cannot be combined with explicit outgoing edges", nodeSpec.ID)
		}

		condition, err := ExpressionConditions(ExpressionConditionConfig{
			Expressions: []Expression{
				{
					Value1: nodes.IteratorStateRootKey + "." + nodeSpec.ID + ".done",
					Op:     OperationEqual,
					Value2: "false",
				},
			},
		})
		if err != nil {
			return fmt.Errorf("build iterator nodes %q built-in continue edge: %w", nodeSpec.ID, err)
		}
		if err := g.addRuntimeConditionalEdge(nodeSpec.ID, continueTo, condition); err != nil {
			return fmt.Errorf("build iterator nodes %q built-in continue edge: %w", nodeSpec.ID, err)
		}
		if err := g.addRuntimeEdge(nodeSpec.ID, doneTo); err != nil {
			return fmt.Errorf("build iterator nodes %q built-in done edge: %w", nodeSpec.ID, err)
		}
	}

	return nil
}

func hasExplicitOutgoingEdge(edges []dsl.GraphEdgeSpec, from string) bool {
	for _, edge := range edges {
		if strings.TrimSpace(edge.From) == from {
			return true
		}
	}
	return false
}

func (r *Registry) JSONSchema() JSONSchema {
	nodeTypes := make(map[string]dsl.NodeTypeSchema, len(r.NodeTypes))
	for key, def := range r.NodeTypes {
		nodeTypes[key] = def.NodeTypeSchema
	}
	conditions := make(map[string]dsl.ConditionSchema, len(r.Conditions))
	for key, def := range r.Conditions {
		conditions[key] = def.ConditionSchema
	}
	return dsl.BuildGraphDefinitionSchema(dsl.CommonStateSchemaID, r.StateFields, nodeTypes, conditions)
}

func resolveTools(all map[string]tools.Tool, ids []string) (map[string]tools.Tool, error) {
	if len(ids) == 0 {
		return all, nil
	}

	filtered := make(map[string]tools.Tool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("tool id is empty")
		}
		tool, ok := all[id]
		if !ok {
			return nil, fmt.Errorf("tool_id %q is not registered", id)
		}
		filtered[id] = tool
	}
	return filtered, nil
}

func stringSliceConfig(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	values, ok := raw.([]any)
	if ok {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}
	return nil
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func intConfig(config map[string]any, key string) (int, bool) {
	if len(config) == 0 {
		return 0, false
	}

	switch value := config[key].(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	if len(config) == 0 {
		return false, false
	}

	switch value := config[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}

	return false, false
}

func cloneBuildContext(ctx *BuildContext) *BuildContext {
	if ctx == nil {
		return &BuildContext{}
	}
	cloned := *ctx
	if len(ctx.graphBuildPath) > 0 {
		cloned.graphBuildPath = append([]string(nil), ctx.graphBuildPath...)
	}
	return &cloned
}

func validateGraphBuildPath(path []string, next string) error {
	next = strings.TrimSpace(next)
	if next == "" {
		return fmt.Errorf("graph_ref is required")
	}
	for _, existing := range path {
		if existing == next {
			cycle := append(append([]string(nil), path...), next)
			return fmt.Errorf("cyclic graph_ref dependency detected: %s", strings.Join(cycle, " -> "))
		}
	}
	return nil
}
