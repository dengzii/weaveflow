package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/state"
)

var ErrToolApprovalRequired = errors.New("tool approval is required")

type ToolHandler func(context.Context, llms.ToolCall) (llms.ToolResult, error)

type ToolExecutionMode string

const (
	ToolExecutionLeaf      ToolExecutionMode = "leaf"
	ToolExecutionComposite ToolExecutionMode = "composite"
)

type ToolApprovalMode string

const (
	ToolApprovalNever    ToolApprovalMode = "never"
	ToolApprovalRequired ToolApprovalMode = "required"
)

type Tool struct {
	Function      *llms.FunctionDefinition
	Handler       ToolHandler
	ExecutionMode ToolExecutionMode
	Permissions   []string
	Approval      ToolApprovalMode
}

func NewTool(function *llms.FunctionDefinition, handler ToolHandler) Tool {
	return Tool{Function: cloneFunctionDefinition(function), Handler: handler}
}

func (tool Tool) Name() string {
	if tool.Function == nil {
		return ""
	}
	return tool.Function.Name
}

func (tool Tool) Definition() llms.ToolDefinition {
	return llms.ToolDefinition{Type: "function", Function: cloneFunctionDefinition(tool.Function)}
}

type ToolApprovalRequest struct {
	ToolCall    llms.ToolCall `json:"tool_call"`
	Permissions []string      `json:"permissions,omitempty"`
}

type ToolApprovalDecision struct {
	ApprovalID string `json:"approval_id,omitempty"`
	Approved   bool   `json:"approved"`
	Actor      string `json:"actor,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ToolApprover interface {
	Approve(context.Context, ToolApprovalRequest) (ToolApprovalDecision, error)
}

type ToolApproverFunc func(context.Context, ToolApprovalRequest) (ToolApprovalDecision, error)

func (approver ToolApproverFunc) Approve(ctx context.Context, request ToolApprovalRequest) (ToolApprovalDecision, error) {
	return approver(ctx, request)
}

type toolPermissions struct {
	enabled bool
	values  map[string]struct{}
}

type toolPermissionsKey struct{}
type toolApproverKey struct{}

func WithToolPermissions(ctx context.Context, permissions ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	values := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		if normalized := normalizePermission(permission); normalized != "" {
			values[normalized] = struct{}{}
		}
	}
	return context.WithValue(ctx, toolPermissionsKey{}, toolPermissions{enabled: true, values: values})
}

func ToolPermissionsFromContext(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	grants, configured := ctx.Value(toolPermissionsKey{}).(toolPermissions)
	if !configured || !grants.enabled {
		return nil, false
	}
	permissions := make([]string, 0, len(grants.values))
	for permission := range grants.values {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions, true
}

func WithToolApprover(ctx context.Context, approver ToolApprover) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if approver == nil {
		return ctx
	}
	return context.WithValue(ctx, toolApproverKey{}, approver)
}

func ToolApproverFromContext(ctx context.Context) ToolApprover {
	if ctx == nil {
		return nil
	}
	approver, _ := ctx.Value(toolApproverKey{}).(ToolApprover)
	return approver
}

type ToolExecutionStage string

const (
	ToolExecutionRequested      ToolExecutionStage = "requested"
	ToolExecutionApprovalNeeded ToolExecutionStage = "approval_needed"
	ToolExecutionApproved       ToolExecutionStage = "approved"
	ToolExecutionStarted        ToolExecutionStage = "started"
	ToolExecutionReturned       ToolExecutionStage = "returned"
	ToolExecutionFailed         ToolExecutionStage = "failed"
	ToolExecutionDenied         ToolExecutionStage = "denied"
)

type ToolExecutionEvent struct {
	Stage      ToolExecutionStage
	Tool       Tool
	Call       llms.ToolCall
	Result     llms.ToolResult
	Approval   *ToolApprovalDecision
	Err        error
	StartedAt  time.Time
	FinishedAt time.Time
	// CloneError reports observer fields omitted because they could not be
	// safely deep-cloned. It never changes the tool's actual returned value.
	CloneError error
}

type ToolExecutionObserver func(context.Context, ToolExecutionEvent)

type toolExecutionObserverKey struct{}

func WithToolExecutionObserver(ctx context.Context, observer ToolExecutionObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, toolExecutionObserverKey{}, observer)
}

func ExecuteTool(ctx context.Context, tool Tool, call llms.ToolCall) (llms.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tool = cloneTool(tool)
	call = normalizeToolCall(tool, cloneToolCall(call))
	startedAt := time.Now().UTC()
	notifyToolObserver(ctx, ToolExecutionEvent{Stage: ToolExecutionRequested, Tool: tool, Call: call, StartedAt: startedAt})
	if tool.Function == nil || strings.TrimSpace(tool.Function.Name) == "" {
		return failToolExecution(ctx, tool, call, startedAt, ErrorInvalidInput, "tool definition is missing", nil)
	}
	if tool.Handler == nil {
		return failToolExecution(ctx, tool, call, startedAt, ErrorInvalidInput, fmt.Sprintf("tool handler %q not found", tool.Name()), nil)
	}
	if err := validateToolArguments(tool, call); err != nil {
		return failToolExecution(ctx, tool, call, startedAt, ErrorInvalidInput, err.Error(), err)
	}
	if err := authorizeToolPermissions(ctx, tool); err != nil {
		return denyToolExecution(ctx, tool, call, startedAt, err, nil)
	}
	if tool.Approval == ToolApprovalRequired {
		notifyToolObserver(ctx, ToolExecutionEvent{Stage: ToolExecutionApprovalNeeded, Tool: tool, Call: call, StartedAt: startedAt})
	}
	approval, err := approveToolExecution(ctx, tool, call)
	if err != nil {
		return denyToolExecution(ctx, tool, call, startedAt, err, approval)
	}
	if approval != nil {
		notifyToolObserver(ctx, ToolExecutionEvent{Stage: ToolExecutionApproved, Tool: tool, Call: call, Approval: approval, StartedAt: startedAt})
	}
	executionCtx, release, err := AcquireToolExecution(ctx, tool.ExecutionMode)
	if err != nil {
		return failToolExecution(ctx, tool, call, startedAt, ClassifyError(err), err.Error(), err)
	}
	defer release()
	notifyToolObserver(executionCtx, ToolExecutionEvent{Stage: ToolExecutionStarted, Tool: tool, Call: call, Approval: approval, StartedAt: startedAt})
	result, err := tool.Handler(executionCtx, call)
	result = normalizeToolResult(call, result)
	if err != nil {
		finishedAt := time.Now().UTC()
		notifyToolObserver(executionCtx, ToolExecutionEvent{Stage: ToolExecutionFailed, Tool: tool, Call: call, Result: result, Approval: approval, Err: err, StartedAt: startedAt, FinishedAt: finishedAt})
		return result, err
	}
	if err := validateToolResult(tool, result); err != nil {
		return failToolExecution(executionCtx, tool, call, startedAt, ErrorNonRetryable, err.Error(), err)
	}
	finishedAt := time.Now().UTC()
	notifyToolObserver(executionCtx, ToolExecutionEvent{Stage: ToolExecutionReturned, Tool: tool, Call: call, Result: result, Approval: approval, StartedAt: startedAt, FinishedAt: finishedAt})
	return result, nil
}

func DecodeToolArguments(call llms.ToolCall, target any) error {
	arguments := strings.TrimSpace(string(callArguments(call)))
	if arguments == "" {
		arguments = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode tool %q arguments: %w", toolCallName(call), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode tool %q arguments: multiple JSON values", toolCallName(call))
		}
		return fmt.Errorf("decode tool %q arguments: trailing JSON: %w", toolCallName(call), err)
	}
	return nil
}

func FindTool(available map[string]Tool, name string) (Tool, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Tool{}, false
	}
	if tool, ok := available[name]; ok {
		return tool, true
	}
	var matched Tool
	found := false
	for key, tool := range available {
		if strings.EqualFold(strings.TrimSpace(key), name) || strings.EqualFold(strings.TrimSpace(tool.Name()), name) {
			if found {
				return Tool{}, false
			}
			matched = tool
			found = true
		}
	}
	return matched, found
}

func normalizeToolCall(tool Tool, call llms.ToolCall) llms.ToolCall {
	if call.Type == "" {
		call.Type = "function"
	}
	if call.FunctionCall == nil {
		call.FunctionCall = &llms.FunctionCall{}
	}
	if strings.TrimSpace(call.FunctionCall.Name) == "" {
		call.FunctionCall.Name = strings.TrimSpace(tool.Name())
	}
	if len(call.FunctionCall.Arguments) == 0 {
		call.FunctionCall.Arguments = json.RawMessage("{}")
	}
	return call
}

func normalizeToolResult(call llms.ToolCall, result llms.ToolResult) llms.ToolResult {
	if result.ToolCallID == "" {
		result.ToolCallID = call.ID
	}
	if result.Name == "" {
		result.Name = toolCallName(call)
	}
	return result
}

func validateToolArguments(tool Tool, call llms.ToolCall) error {
	if tool.Function == nil || len(tool.Function.Parameters) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(callArguments(call), &value); err != nil {
		return fmt.Errorf("tool %q arguments are not valid JSON: %w", tool.Name(), err)
	}
	issues := state.ValidateJSONSchemaValue(value, tool.Function.Parameters, "arguments")
	if len(issues) == 0 {
		return nil
	}
	return state.NewValidationError("tool input", issues)
}

func validateToolResult(tool Tool, result llms.ToolResult) error {
	if tool.Function == nil || len(tool.Function.OutputSchema) == 0 {
		return nil
	}
	value := result.Value
	if value == nil {
		value = result.Content
	}
	issues := state.ValidateJSONSchemaValue(value, tool.Function.OutputSchema, "result")
	if len(issues) == 0 {
		return nil
	}
	return state.NewValidationError("tool output", issues)
}

func authorizeToolPermissions(ctx context.Context, tool Tool) error {
	required := normalizedPermissions(tool.Permissions)
	if len(required) == 0 {
		return nil
	}
	grants, configured := ctx.Value(toolPermissionsKey{}).(toolPermissions)
	missing := make([]string, 0)
	for _, permission := range required {
		if !configured || !grants.enabled {
			missing = append(missing, permission)
			continue
		}
		if _, ok := grants.values[permission]; !ok {
			missing = append(missing, permission)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return NewExecutionError(ErrorPermissionDenied, fmt.Sprintf("tool %q requires permissions: %s", tool.Name(), strings.Join(missing, ", ")), nil, map[string]any{"missing_permissions": missing})
}

func approveToolExecution(ctx context.Context, tool Tool, call llms.ToolCall) (*ToolApprovalDecision, error) {
	if tool.Approval == "" || tool.Approval == ToolApprovalNever {
		return nil, nil
	}
	if tool.Approval != ToolApprovalRequired {
		return nil, NewExecutionError(ErrorInvalidInput, fmt.Sprintf("tool %q has unsupported approval mode %q", tool.Name(), tool.Approval), nil, nil)
	}
	approver, _ := ctx.Value(toolApproverKey{}).(ToolApprover)
	if approver == nil {
		return nil, NewExecutionError(ErrorPermissionDenied, fmt.Sprintf("%s for tool %q", ErrToolApprovalRequired, tool.Name()), ErrToolApprovalRequired, nil)
	}
	decision, err := approver.Approve(ctx, ToolApprovalRequest{ToolCall: cloneToolCall(call), Permissions: normalizedPermissions(tool.Permissions)})
	if err != nil {
		return &decision, NewExecutionError(ErrorPermissionDenied, fmt.Sprintf("tool %q approval failed: %v", tool.Name(), err), err, nil)
	}
	if !decision.Approved {
		message := fmt.Sprintf("tool %q approval was denied", tool.Name())
		if reason := strings.TrimSpace(decision.Reason); reason != "" {
			message += ": " + reason
		}
		return &decision, NewExecutionError(ErrorPermissionDenied, message, nil, map[string]any{"approval": decision})
	}
	return &decision, nil
}

func failToolExecution(ctx context.Context, tool Tool, call llms.ToolCall, startedAt time.Time, class ErrorClass, message string, cause error) (llms.ToolResult, error) {
	err := NewExecutionError(class, message, cause, nil)
	result := normalizeToolResult(call, llms.ToolResult{IsError: true, ErrorCode: string(class), ErrorMessage: message})
	notifyToolObserver(ctx, ToolExecutionEvent{Stage: ToolExecutionFailed, Tool: tool, Call: call, Result: result, Err: err, StartedAt: startedAt, FinishedAt: time.Now().UTC()})
	return result, err
}

func denyToolExecution(ctx context.Context, tool Tool, call llms.ToolCall, startedAt time.Time, err error, approval *ToolApprovalDecision) (llms.ToolResult, error) {
	result := normalizeToolResult(call, llms.ToolResult{IsError: true, ErrorCode: string(ErrorPermissionDenied), ErrorMessage: err.Error()})
	notifyToolObserver(ctx, ToolExecutionEvent{Stage: ToolExecutionDenied, Tool: tool, Call: call, Result: result, Approval: approval, Err: err, StartedAt: startedAt, FinishedAt: time.Now().UTC()})
	return result, err
}

func notifyToolObserver(ctx context.Context, event ToolExecutionEvent) {
	observer, _ := ctx.Value(toolExecutionObserverKey{}).(ToolExecutionObserver)
	if observer != nil {
		clonedTool, toolErr := cloneToolForObserver(event.Tool)
		event.Tool = clonedTool
		event.Call = cloneToolCall(event.Call)
		clonedResult, resultErr := cloneToolResult(event.Result)
		event.Result = clonedResult
		clonedErr, errorCloneErr := cloneObserverError(event.Err)
		event.Err = clonedErr
		if event.Approval != nil {
			approval := *event.Approval
			event.Approval = &approval
		}
		event.CloneError = errors.Join(event.CloneError, toolErr, resultErr, errorCloneErr)
		observer(ctx, event)
	}
}

func cloneToolForObserver(tool Tool) (Tool, error) {
	tool.Handler = nil
	clonedValue, err := cloneAnyValue(tool)
	if err != nil {
		return Tool{}, fmt.Errorf("omit tool observer definition: %w", err)
	}
	cloned, ok := clonedValue.(Tool)
	if !ok {
		return Tool{}, fmt.Errorf("clone tool observer definition returned %T", clonedValue)
	}
	return cloned, nil
}

func cloneToolCall(call llms.ToolCall) llms.ToolCall {
	if call.FunctionCall == nil {
		return call
	}
	functionCall := *call.FunctionCall
	functionCall.Arguments = append(json.RawMessage(nil), call.FunctionCall.Arguments...)
	call.FunctionCall = &functionCall
	return call
}

func cloneToolResult(result llms.ToolResult) (llms.ToolResult, error) {
	clonedValue, err := cloneAnyValue(result)
	if err != nil {
		result.Value = nil
		return result, fmt.Errorf("omit tool observer result value: %w", err)
	}
	cloned, ok := clonedValue.(llms.ToolResult)
	if !ok {
		return llms.ToolResult{}, fmt.Errorf("clone tool observer result returned %T", clonedValue)
	}
	return cloned, nil
}

func callArguments(call llms.ToolCall) json.RawMessage {
	if call.FunctionCall == nil || len(call.FunctionCall.Arguments) == 0 {
		return json.RawMessage("{}")
	}
	return call.FunctionCall.Arguments
}

func toolCallName(call llms.ToolCall) string {
	if call.FunctionCall == nil {
		return ""
	}
	return strings.TrimSpace(call.FunctionCall.Name)
}

func normalizedPermissions(permissions []string) []string {
	seen := make(map[string]struct{}, len(permissions))
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		normalized := normalizePermission(permission)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result
}

func normalizePermission(permission string) string {
	return strings.ToLower(strings.TrimSpace(permission))
}

func cloneFunctionDefinition(function *llms.FunctionDefinition) *llms.FunctionDefinition {
	if function == nil {
		return nil
	}
	cloned := *function
	cloned.Parameters = function.Parameters.Clone()
	cloned.OutputSchema = function.OutputSchema.Clone()
	return &cloned
}
