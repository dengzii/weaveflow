package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/trigger"
	"github.com/gin-gonic/gin"
)

const (
	maxWebhookBodyBytes        = 1 << 20
	maxTriggerPayloadBodyBytes = 1 << 20
)

type triggerPayload struct {
	ID          string                    `json:"id,omitempty"`
	Name        string                    `json:"name,omitempty"`
	Type        trigger.Type              `json:"type"`
	Enabled     *bool                     `json:"enabled,omitempty"`
	Target      trigger.Target            `json:"target,omitempty"`
	Concurrency trigger.ConcurrencyPolicy `json:"concurrency,omitempty"`
	Webhook     *trigger.WebhookSpec      `json:"webhook,omitempty"`
	Schedule    *trigger.ScheduleSpec     `json:"schedule,omitempty"`
}

func (p triggerPayload) toTrigger(defaultEnabled bool) trigger.Trigger {
	enabled := defaultEnabled
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	return trigger.Trigger{
		ID:          strings.TrimSpace(p.ID),
		Name:        strings.TrimSpace(p.Name),
		Type:        p.Type,
		Enabled:     enabled,
		Target:      p.Target,
		Concurrency: p.Concurrency,
		Webhook:     p.Webhook,
		Schedule:    p.Schedule,
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
	item, err = service.Create(c.Request.Context(), item)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusCreated, publicTrigger(item))
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
		result = append(result, publicTrigger(item))
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleGetTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	item, err := service.Get(c.Request.Context(), strings.TrimSpace(c.Param("trigger_id")))
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, publicTrigger(item))
}

func (s *Server) handleUpdateTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	id := strings.TrimSpace(c.Param("trigger_id"))
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
	if item.Webhook != nil && item.Webhook.Secret == "" && existing.Webhook != nil {
		item.Webhook.Secret = existing.Webhook.Secret
	}
	if item.Target == (trigger.Target{}) {
		item.Target = existing.Target
	}
	item, err = service.Update(c.Request.Context(), item)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, publicTrigger(item))
}

func (s *Server) handleDeleteTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	if err := service.Delete(c.Request.Context(), strings.TrimSpace(c.Param("trigger_id"))); err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleInvokeTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	triggerID := strings.TrimSpace(c.Param("trigger_id"))
	item, err := service.Get(c.Request.Context(), triggerID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

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
		headers := make(map[string]string, len(c.Request.Header))
		for key, values := range c.Request.Header {
			if len(values) > 0 {
				headers[key] = values[0]
			}
		}
		run, runErr = service.InvokeWebhook(ctx, triggerID, body, headers)
	case trigger.TypeSchedule:
		run, runErr = service.InvokeSchedule(ctx, triggerID)
	default:
		runErr = trigger.ErrTypeMismatch
	}
	if runErr != nil {
		writeError(c, statusForError(runErr), runErr)
		return
	}
	writeData(c, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleWebhookTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	headers := make(map[string]string, len(c.Request.Header))
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	run, err := service.InvokeWebhookInput(
		ctx,
		strings.TrimSpace(c.Param("trigger_id")),
		webhookQueryInput(c),
		[]byte(c.Request.URL.RawQuery),
		headers,
	)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, map[string]any{"run": run})
}

func webhookQueryInput(c *gin.Context) map[string]any {
	query := c.Request.URL.Query()
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

func (s *Server) handleListTriggerRecords(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	limit := trigger.DefaultRecordLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(c, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = parsed
	}
	items, err := service.ListRecords(c.Request.Context(), strings.TrimSpace(c.Query("trigger_id")), limit)
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

func publicTrigger(item trigger.Trigger) trigger.Trigger {
	if item.Webhook != nil {
		copy := *item.Webhook
		copy.Secret = ""
		item.Webhook = &copy
	}
	return item
}
