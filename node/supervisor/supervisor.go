package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

const (
	NodeTypeSupervisor          = "supervisor"
	NodeTypeSupervisorWorker    = "supervisor_worker"
	NodeTypeSupervisorSynthesis = "supervisor_synthesis"

	ConditionTypeSupervisorRouteEquals = "supervisor_route_equals"

	SupervisorRouteFinish = "__finish__"

	SupervisorStatusRouting    = "routing"
	SupervisorStatusDelegating = "delegating"
	SupervisorStatusFinalizing = "finalizing"
	SupervisorStatusDone       = "done"

	SupervisorFieldObjective  = supervisorcap.FieldObjective
	SupervisorFieldRoute      = supervisorcap.FieldRoute
	SupervisorFieldTask       = supervisorcap.FieldTask
	SupervisorFieldReason     = supervisorcap.FieldReason
	SupervisorFieldStatus     = supervisorcap.FieldStatus
	SupervisorFieldTurnCount  = supervisorcap.FieldTurnCount
	SupervisorFieldMaxTurns   = supervisorcap.FieldMaxTurns
	SupervisorFieldHistory    = supervisorcap.FieldHistory
	SupervisorFieldLastResult = supervisorcap.FieldLastResult
)

const (
	defaultSupervisorMaxTurns      = 8
	defaultSupervisorRouteAttempts = 2
)

const defaultSupervisorSystemPrompt = `You are a supervisor coordinating specialist workers.
Choose exactly one worker for the next useful task. Do not do the worker's task yourself.
When the objective is fully addressed, choose __finish__.
Return JSON only: {"next_worker":"<worker id or __finish__>","task":"<specific delegated task>","reason":"<brief reason>"}.
The task must be self-contained and should tell the worker what result to return.`

type SupervisorMember struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description"`
}

type SupervisorTurn = supervisorcap.Turn

type SupervisorNode struct {
	Base
	ModelID        string
	SystemPrompt   string
	Members        []SupervisorMember
	MaxTurns       int
	RouteAttempts  int
	ObjectivePath  state.Path
	SupervisorPath state.Path
}

type supervisorRouteOutput struct {
	NextWorker string `json:"next_worker"`
	Task       string `json:"task"`
	Reason     string `json:"reason"`
}

func NewSupervisorNode(options ...NodeOption) *SupervisorNode {
	target := &SupervisorNode{
		Base: NewBase(Spec{
			Name:        NodeTypeSupervisor,
			Description: "Route work among specialist workers until the objective is ready for final synthesis.",
		}),
		SystemPrompt:  defaultSupervisorSystemPrompt,
		MaxTurns:      defaultSupervisorMaxTurns,
		RouteAttempts: defaultSupervisorRouteAttempts,
	}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *SupervisorNode) Validate() error {
	if n == nil {
		return fmt.Errorf("supervisor node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if len(n.Members) == 0 {
		return fmt.Errorf("supervisor node %q requires at least one member", n.ID())
	}
	seen := map[string]struct{}{}
	for index, member := range n.Members {
		member = normalizeSupervisorMember(member)
		if member.ID == "" {
			return fmt.Errorf("supervisor node %q member %d requires id", n.ID(), index)
		}
		if strings.EqualFold(member.ID, SupervisorRouteFinish) {
			return fmt.Errorf("supervisor node %q member id %q is reserved", n.ID(), member.ID)
		}
		if member.Description == "" {
			return fmt.Errorf("supervisor node %q member %q requires description", n.ID(), member.ID)
		}
		key := strings.ToLower(member.ID)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("supervisor node %q has duplicate member id %q", n.ID(), member.ID)
		}
		seen[key] = struct{}{}
	}
	if n.effectiveMaxTurns() < 1 {
		return fmt.Errorf("supervisor node %q max_turns must be positive", n.ID())
	}
	if n.effectiveRouteAttempts() < 1 {
		return fmt.Errorf("supervisor node %q route_attempts must be positive", n.ID())
	}
	if n.ObjectivePath.Empty() || n.SupervisorPath.Empty() {
		return fmt.Errorf("supervisor node %q requires objective and supervisor paths", n.ID())
	}
	return nil
}

func (n *SupervisorNode) GraphNodeSpec() dsl.GraphNodeSpec {
	members := make([]map[string]any, 0, len(n.Members))
	for _, member := range n.Members {
		member = normalizeSupervisorMember(member)
		item := map[string]any{"id": member.ID, "description": member.Description}
		if member.Name != "" {
			item["name"] = member.Name
		}
		members = append(members, item)
	}
	return newGraphNodeSpec(n.Base, NodeTypeSupervisor, map[string]any{
		"model_id":       n.ModelID,
		"system_prompt":  n.SystemPrompt,
		"members":        members,
		"max_turns":      n.effectiveMaxTurns(),
		"route_attempts": n.effectiveRouteAttempts(),
	}, map[string]state.Path{"objective": n.ObjectivePath, "supervisor": n.SupervisorPath})
}

func SupervisorNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeSupervisor,
			Title:       "Supervisor",
			Description: "Select a specialist worker for each turn, record the delegation, and route to final synthesis when the objective is complete.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"system_prompt": dsl.JSONSchema{
						"type": "string", "title": "System Prompt", "x-control": "textarea", "default": defaultSupervisorSystemPrompt,
					},
					"members": dsl.JSONSchema{
						"type": "array", "title": "Members", "minItems": 1, "x-control": "object-list", "x-item-title": "Member",
						"items": dsl.JSONSchema{
							"type": "object",
							"properties": dsl.JSONSchema{
								"id":          dsl.JSONSchema{"type": "string", "title": "Worker ID"},
								"name":        dsl.JSONSchema{"type": "string", "title": "Display Name"},
								"description": dsl.JSONSchema{"type": "string", "title": "Capability", "x-control": "textarea"},
							},
							"required":             []string{"id", "description"},
							"additionalProperties": false,
						},
					},
					"max_turns":      dsl.JSONSchema{"type": "integer", "title": "Max Delegations", "minimum": 1, "default": defaultSupervisorMaxTurns},
					"route_attempts": dsl.JSONSchema{"type": "integer", "title": "Route Attempts", "minimum": 1, "maximum": 5, "default": defaultSupervisorRouteAttempts},
				},
				"required":             []string{"members"},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			primitivePort("objective", "Objective coordinated by the supervisor.", "string", dsl.StateAccessRead, true),
			capabilityPort("supervisor", "Routing state and worker history.", supervisorcap.CapabilityID, true,
				capabilityField(supervisorcap.FieldObjective, dsl.StateAccessReadWrite),
				capabilityField(supervisorcap.FieldRoute, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldTask, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldReason, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldStatus, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldTurnCount, dsl.StateAccessReadWrite),
				capabilityField(supervisorcap.FieldMaxTurns, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldHistory, dsl.StateAccessRead)),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			objectivePath, err := resolvedPath(resolved, "objective")
			if err != nil {
				return nil, err
			}
			supervisorPath, err := resolvedPath(resolved, "supervisor")
			if err != nil {
				return nil, err
			}
			target := NewSupervisorNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			if prompt := config.String(spec.Config, "system_prompt"); strings.TrimSpace(prompt) != "" {
				target.SystemPrompt = prompt
			}
			members, err := parseSupervisorMembers(spec.Config["members"])
			if err != nil {
				return nil, fmt.Errorf("build supervisor node %q members: %w", spec.ID, err)
			}
			target.Members = members
			if value, ok := config.Int(spec.Config, "max_turns"); ok {
				target.MaxTurns = value
			}
			if value, ok := config.Int(spec.Config, "route_attempts"); ok {
				target.RouteAttempts = value
			}
			target.ObjectivePath = objectivePath
			target.SupervisorPath = supervisorPath
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (n *SupervisorNode) Execute(ctx core.Context, access *state.Access) error {
	if err := n.Validate(); err != nil {
		return err
	}
	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("supervisor node: model %q not available", effectiveModelID(n.ModelID))
	}
	supervisor, err := supervisorcap.Bind(access, n.SupervisorPath)
	if err != nil {
		return err
	}
	objective := strings.TrimSpace(supervisorString(supervisor.Value(), SupervisorFieldObjective))
	if objective == "" {
		objectiveInput, requestErr := state.Get(access, state.NewRef[string](n.ObjectivePath))
		if requestErr != nil {
			return requestErr
		}
		objective = strings.TrimSpace(objectiveInput)
		if objective == "" {
			return fmt.Errorf("supervisor node %q requires shared.request.input or shared.supervisor.objective", n.ID())
		}
		if err := supervisor.SetField(SupervisorFieldObjective, objective); err != nil {
			return err
		}
	}

	current := supervisor.Value()
	turnCount := supervisorInt(current, SupervisorFieldTurnCount)
	maxTurns := n.effectiveMaxTurns()
	if err := supervisor.SetField(SupervisorFieldMaxTurns, maxTurns); err != nil {
		return err
	}
	if turnCount >= maxTurns {
		return n.setRoute(ctx, supervisor, supervisorRouteOutput{
			NextWorker: SupervisorRouteFinish,
			Reason:     fmt.Sprintf("maximum delegation count %d reached", maxTurns),
		}, turnCount)
	}

	history := supervisorHistoryFromValue(current[SupervisorFieldHistory])
	route, err := n.selectRoute(ctx, objective, history, turnCount, maxTurns)
	if err != nil {
		return err
	}
	if !strings.EqualFold(route.NextWorker, SupervisorRouteFinish) {
		turnCount++
	}
	return n.setRoute(ctx, supervisor, route, turnCount)
}

func (n *SupervisorNode) selectRoute(ctx core.Context, objective string, history []SupervisorTurn, turnCount, maxTurns int) (supervisorRouteOutput, error) {
	membersJSON, _ := json.MarshalIndent(n.normalizedMembers(), "", "  ")
	historyJSON, _ := json.MarshalIndent(history, "", "  ")
	humanPrompt := fmt.Sprintf("Objective:\n%s\n\nAvailable workers:\n%s\n\nCompleted delegations (%d/%d):\n%s", objective, membersJSON, turnCount, maxTurns, historyJSON)
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
		llms.TextParts(llms.ChatMessageTypeHuman, humanPrompt),
	}
	var lastErr error
	for attempt := 1; attempt <= n.effectiveRouteAttempts(); attempt++ {
		if serialized, err := conversationcap.SerializeMessages(messages); err == nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "supervisor.route.prompt", map[string]any{
				"attempt": attempt, "turn_count": turnCount, "messages": serialized,
			})
		}
		response, err := ctx.Model(n.ModelID).GenerateContent(ctx, messages, llms.WithThinkingMode(llms.ThinkingModeHigh))
		if err != nil {
			return supervisorRouteOutput{}, err
		}
		if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
			lastErr = fmt.Errorf("llm returned no choices")
		} else {
			content := response.Choices[0].Content
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "supervisor.route.response", map[string]any{
				"attempt": attempt, "turn_count": turnCount, "content": content,
			})
			route, parseErr := parseSupervisorRoute(content, n.Members)
			if parseErr == nil {
				return route, nil
			}
			lastErr = parseErr
			messages = append(messages,
				llms.TextParts(llms.ChatMessageTypeAI, content),
				llms.TextParts(llms.ChatMessageTypeHuman, "The route was invalid: "+parseErr.Error()+". Return one corrected JSON object using an available worker id or __finish__."),
			)
		}
	}
	return supervisorRouteOutput{}, fmt.Errorf("supervisor node %q could not select a valid route after %d attempts: %w", n.ID(), n.effectiveRouteAttempts(), lastErr)
}

func (n *SupervisorNode) setRoute(ctx context.Context, supervisor *supervisorcap.View, route supervisorRouteOutput, turnCount int) error {
	route.NextWorker = canonicalSupervisorRoute(route.NextWorker, n.Members)
	status := SupervisorStatusDelegating
	if route.NextWorker == SupervisorRouteFinish {
		status = SupervisorStatusFinalizing
		route.Task = ""
	}
	if err := supervisor.Merge(map[string]any{
		SupervisorFieldRoute:     route.NextWorker,
		SupervisorFieldTask:      strings.TrimSpace(route.Task),
		SupervisorFieldReason:    strings.TrimSpace(route.Reason),
		SupervisorFieldStatus:    status,
		SupervisorFieldTurnCount: turnCount,
	}); err != nil {
		return err
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"event": "supervisor.route_selected", "next_worker": route.NextWorker, "task": strings.TrimSpace(route.Task), "reason": strings.TrimSpace(route.Reason), "turn_count": turnCount,
	})
	return nil
}

func (n *SupervisorNode) normalizedMembers() []SupervisorMember {
	members := make([]SupervisorMember, 0, len(n.Members))
	for _, member := range n.Members {
		members = append(members, normalizeSupervisorMember(member))
	}
	return members
}

func (n *SupervisorNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return defaultSupervisorSystemPrompt
	}
	return strings.TrimSpace(n.SystemPrompt)
}

func (n *SupervisorNode) effectiveMaxTurns() int {
	if n == nil || n.MaxTurns <= 0 {
		return defaultSupervisorMaxTurns
	}
	return n.MaxTurns
}

func (n *SupervisorNode) effectiveRouteAttempts() int {
	if n == nil || n.RouteAttempts <= 0 {
		return defaultSupervisorRouteAttempts
	}
	return n.RouteAttempts
}

func SupervisorRouteEquals(supervisorPath state.Path, workerID string) registry.EdgeCondition {
	workerID = strings.TrimSpace(workerID)
	return registry.NewEdgeCondition(dsl.GraphConditionSpec{
		Type:   ConditionTypeSupervisorRouteEquals,
		Config: map[string]any{"worker_id": workerID},
		State:  map[string]dsl.StateBinding{"supervisor": {Path: supervisorPath.String()}},
	}, func(_ context.Context, current *state.State) bool {
		value, ok := state.ReadPath(current, supervisorPath.MustChild(supervisorcap.FieldRoute).String())
		return ok && strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), workerID)
	})
}

func parseSupervisorMembers(raw any) ([]SupervisorMember, error) {
	var values []any
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		values = typed
	case []map[string]any:
		values = make([]any, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
	case []SupervisorMember:
		members := make([]SupervisorMember, len(typed))
		for index, member := range typed {
			members[index] = normalizeSupervisorMember(member)
		}
		return members, nil
	default:
		return nil, fmt.Errorf("members must be an array")
	}
	members := make([]SupervisorMember, 0, len(values))
	for index, rawMember := range values {
		mapped, ok := rawMember.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("member %d must be an object", index)
		}
		member := normalizeSupervisorMember(SupervisorMember{
			ID: config.String(mapped, "id"), Name: config.String(mapped, "name"), Description: config.String(mapped, "description"),
		})
		members = append(members, member)
	}
	return members, nil
}

func parseSupervisorRoute(content string, members []SupervisorMember) (supervisorRouteOutput, error) {
	content = stripSupervisorJSONFence(content)
	var output supervisorRouteOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start < 0 || end <= start || json.Unmarshal([]byte(content[start:end+1]), &output) != nil {
			return supervisorRouteOutput{}, fmt.Errorf("route must be valid JSON")
		}
	}
	output.NextWorker = canonicalSupervisorRoute(output.NextWorker, members)
	output.Task = strings.TrimSpace(output.Task)
	output.Reason = strings.TrimSpace(output.Reason)
	if output.NextWorker == "" {
		return supervisorRouteOutput{}, fmt.Errorf("next_worker must name an available worker or %s", SupervisorRouteFinish)
	}
	if output.NextWorker != SupervisorRouteFinish && output.Task == "" {
		return supervisorRouteOutput{}, fmt.Errorf("task is required when routing to worker %q", output.NextWorker)
	}
	return output, nil
}

func canonicalSupervisorRoute(route string, members []SupervisorMember) string {
	route = strings.TrimSpace(route)
	switch strings.ToLower(route) {
	case "finish", "done", "end", "__end__", SupervisorRouteFinish:
		return SupervisorRouteFinish
	}
	for _, member := range members {
		memberID := strings.TrimSpace(member.ID)
		if strings.EqualFold(route, memberID) {
			return memberID
		}
	}
	return ""
}

func normalizeSupervisorMember(member SupervisorMember) SupervisorMember {
	member.ID = strings.TrimSpace(member.ID)
	member.Name = strings.TrimSpace(member.Name)
	member.Description = strings.TrimSpace(member.Description)
	if member.Name == "" {
		member.Name = member.ID
	}
	return member
}

func stripSupervisorJSONFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func supervisorHistoryFromValue(raw any) []SupervisorTurn {
	return supervisorcap.DecodeHistory(raw)
}

func supervisorTurnMaps(turns []SupervisorTurn) []map[string]any {
	return supervisorcap.EncodeHistory(turns)
}

func supervisorString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func supervisorInt(values map[string]any, key string) int {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}
