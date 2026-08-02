package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/gin-gonic/gin"
)

const (
	maxWebhookBodyBytes        = 1 << 20
	maxTriggerPayloadBodyBytes = 1 << 20
)

type triggerPayload struct {
	ID                 string                    `json:"id,omitempty"`
	Name               string                    `json:"name,omitempty"`
	Type               trigger.Type              `json:"type"`
	Enabled            *bool                     `json:"enabled,omitempty"`
	Target             trigger.Target            `json:"target,omitempty"`
	Concurrency        trigger.ConcurrencyPolicy `json:"concurrency,omitempty"`
	InitialState       map[string]any            `json:"initial_state,omitempty"`
	Webhook            *triggerWebhookPayload    `json:"webhook,omitempty"`
	Schedule           *trigger.ScheduleSpec     `json:"schedule,omitempty"`
	Chat               *trigger.ChatSpec         `json:"chat,omitempty"`
	ChatSetupSessionID string                    `json:"chat_setup_session_id,omitempty"`
}

type triggerWebhookPayload struct {
	APIKey        string                        `json:"api_key,omitempty"`
	StateMappings []trigger.WebhookStateMapping `json:"state_mappings,omitempty"`
}

type triggerInvocationResponse struct {
	Run runtime.RunRecord `json:"run"`
}

func (p triggerPayload) toTrigger(defaultEnabled bool) trigger.Trigger {
	enabled := defaultEnabled
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	var webhook *trigger.WebhookSpec
	if p.Webhook != nil {
		webhook = &trigger.WebhookSpec{
			APIKey:        p.Webhook.APIKey,
			StateMappings: append([]trigger.WebhookStateMapping(nil), p.Webhook.StateMappings...),
		}
	}
	return trigger.Trigger{
		ID:           strings.TrimSpace(p.ID),
		Name:         strings.TrimSpace(p.Name),
		Type:         p.Type,
		Enabled:      enabled,
		Target:       p.Target,
		Concurrency:  p.Concurrency,
		InitialState: p.InitialState,
		Webhook:      webhook,
		Schedule:     p.Schedule,
		Chat:         p.Chat,
	}
}

func (s *Server) handleCreateTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	payload, err := decodeTriggerPayload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	item := payload.toTrigger(true)
	if item.Target == (trigger.Target{}) {
		item.Target = s.defaultTriggerTarget()
	}
	if strings.TrimSpace(payload.ChatSetupSessionID) != "" {
		s.chatSetupSaveMu.Lock()
		defer s.chatSetupSaveMu.Unlock()
	}
	releaseSetup, err := s.applyChatSetup(c.Request.Context(), setupRequestOwner(c), payload.ChatSetupSessionID, &item)
	if err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	setupCommitted := false
	defer func() { releaseSetup(setupCommitted) }()
	item, err = service.Create(c.Request.Context(), item)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	setupCommitted = true
	writeData(c, http.StatusCreated, s.publicTrigger(item))
}

func (s *Server) handleListTriggers(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	items, err := service.List(c.Request.Context())
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	result := make([]trigger.Trigger, 0, len(items))
	for _, item := range items {
		result = append(result, s.publicTrigger(item))
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleGetTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	triggerID, ok := requirePathParam(c, "trigger_id")
	if !ok {
		return
	}
	item, err := service.Get(c.Request.Context(), triggerID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, s.publicTrigger(item))
}

func (s *Server) handleUpdateTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	id, ok := requirePathParam(c, "trigger_id")
	if !ok {
		return
	}
	existing, err := service.Get(c.Request.Context(), id)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	payload, err := decodeTriggerPayload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	item := payload.toTrigger(existing.Enabled)
	item.ID = id
	if item.Webhook != nil && item.Webhook.APIKey == "" && existing.Webhook != nil {
		item.Webhook.APIKey = existing.Webhook.APIKey
	}
	if item.Target == (trigger.Target{}) {
		item.Target = existing.Target
	}
	if strings.TrimSpace(payload.ChatSetupSessionID) != "" {
		s.chatSetupSaveMu.Lock()
		defer s.chatSetupSaveMu.Unlock()
	}
	releaseSetup, err := s.applyChatSetup(c.Request.Context(), setupRequestOwner(c), payload.ChatSetupSessionID, &item)
	if err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	setupCommitted := false
	defer func() { releaseSetup(setupCommitted) }()
	item, err = service.Update(c.Request.Context(), item)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	setupCommitted = true
	writeData(c, http.StatusOK, s.publicTrigger(item))
}

func (s *Server) handleDeleteTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	triggerID, ok := requirePathParam(c, "trigger_id")
	if !ok {
		return
	}
	if err := service.Delete(c.Request.Context(), triggerID); err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleCreateTriggerInvocation(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	triggerID, ok := requirePathParam(c, "trigger_id")
	if !ok {
		return
	}
	item, err := service.Get(c.Request.Context(), triggerID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()
	apiKey, err := optionalStringQuery(c, trigger.APIKeyQueryParameter)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}

	var (
		run    runtime.RunRecord
		runErr error
	)
	switch item.Type {
	case trigger.TypeWebhook:
		body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes+1))
		if readErr != nil {
			writeError(c, http.StatusBadRequest, readErr)
			return
		}
		if len(body) > maxWebhookBodyBytes {
			writeError(c, http.StatusRequestEntityTooLarge, errWebhookBodyTooLarge)
			return
		}
		run, runErr = service.InvokeWebhook(ctx, triggerID, body, apiKey, requestHeaders(c))
	case trigger.TypeSchedule:
		run, runErr = service.InvokeSchedule(ctx, triggerID)
	default:
		runErr = trigger.ErrTypeMismatch
	}
	if runErr != nil {
		writeError(c, statusForError(runErr), runErr)
		return
	}
	writeData(c, http.StatusOK, triggerInvocationResponse{Run: run})
}

func (s *Server) handleWebhookTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	triggerID, ok := requirePathParam(c, "trigger_id")
	if !ok {
		return
	}
	apiKey, err := optionalStringQuery(c, trigger.APIKeyQueryParameter)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	run, err := service.InvokeWebhookInput(
		ctx,
		triggerID,
		webhookQueryInput(c),
		apiKey,
		requestHeaders(c),
	)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, triggerInvocationResponse{Run: run})
}

func webhookQueryInput(c *gin.Context) map[string]any {
	query := c.Request.URL.Query()
	query.Del(trigger.APIKeyQueryParameter)
	input := make(map[string]any, len(query))
	for key, values := range query {
		if len(values) == 1 {
			input[key] = values[0]
			continue
		}
		input[key] = append([]string(nil), values...)
	}
	return input
}

func (s *Server) handleListTriggerInvocations(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	triggerID := optionalPathParam(c, "trigger_id")
	queryTriggerID, err := optionalStringQuery(c, "trigger_id")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	if triggerID != "" && queryTriggerID != "" {
		writeError(c, http.StatusBadRequest, invalidRequestf("trigger_id query is not allowed on a scoped invocation route"))
		return
	}
	if triggerID == "" {
		triggerID = queryTriggerID
	}
	limit, err := positiveIntQuery(c, "limit", trigger.DefaultRecordLimit, trigger.MaxRecordLimit)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	items, err := service.ListRecords(c.Request.Context(), triggerID, limit)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	for index := range items {
		run, err := s.triggerRecordRun(c.Request.Context(), items[index])
		if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			continue
		}
		if err != nil {
			writeError(c, statusForError(err), err)
			return
		}
		runCopy := run
		items[index].Run = &runCopy
		items[index].Status = run.Status
		items[index].ErrorMessage = run.ErrorMessage
		items[index].UpdatedAt = run.UpdatedAt
	}
	writeData(c, http.StatusOK, items)
}

func requestHeaders(c *gin.Context) map[string]string {
	headers := make(map[string]string, len(c.Request.Header))
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return headers
}

func (s *Server) triggerRecordRun(ctx context.Context, record trigger.Record) (runtime.RunRecord, error) {
	if record.Run == nil || strings.TrimSpace(record.Run.RunID) == "" {
		return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
	}
	runID := record.Run.RunID
	if runner := s.currentRunner(); triggerTargetMatchesRunner(record.Target.GraphID, runner) {
		run, err := runner.GetRun(ctx, runID)
		if err == nil {
			return run, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.RunRecord{}, err
		}
	}
	reader, err := s.openGraphCache(record.Target.GraphID)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	return reader.GetRun(ctx, runID)
}
func decodeTriggerPayload(c *gin.Context) (triggerPayload, error) {
	body, err := readRequestBody(c.Request.Body, maxTriggerPayloadBodyBytes)
	if err != nil {
		return triggerPayload{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return triggerPayload{}, errTriggerPayloadRequired
	}
	var payload triggerPayload
	if err := decodeStrictJSON(body, &payload); err != nil {
		return triggerPayload{}, err
	}
	return payload, nil
}

func (s *Server) publicTrigger(item trigger.Trigger) trigger.Trigger {
	if item.Webhook != nil {
		copy := *item.Webhook
		copy.APIKey = ""
		item.Webhook = &copy
	}
	if s != nil && s.triggers != nil {
		item = s.triggers.RedactChatChannelConfig(item)
	}
	return item
}
