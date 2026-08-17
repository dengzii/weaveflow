package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/gin-gonic/gin"
)

const (
	maxWebhookBodyBytes        = 1 << 20
	maxTriggerPayloadBodyBytes = 1 << 20
)

type triggerPayload struct {
	ID                 string                    `json:"id"`
	Name               string                    `json:"name,omitempty"`
	Type               trigger.Type              `json:"type"`
	Enabled            *bool                     `json:"enabled,omitempty"`
	Concurrency        trigger.ConcurrencyPolicy `json:"concurrency,omitempty"`
	Credential         *dsl.SecretRef            `json:"credential,omitempty"`
	InitialState       map[string]any            `json:"initial_state,omitempty"`
	Webhook            *triggerWebhookPayload    `json:"webhook,omitempty"`
	Schedule           *trigger.ScheduleSpec     `json:"schedule,omitempty"`
	Chat               *trigger.ChatSpec         `json:"chat,omitempty"`
	ChatSetupSessionID string                    `json:"chat_setup_session_id,omitempty"`
}

type triggerWebhookPayload struct {
	StateBindings *trigger.WebhookStateBindings `json:"state_bindings,omitempty"`
	StateMappings []trigger.WebhookStateMapping `json:"state_mappings,omitempty"`
}

type triggerReplacementPayload struct {
	Triggers []triggerPayload `json:"triggers"`
}

type triggerInvocationResponse struct {
	Run runtime.RunRecord `json:"run"`
}

func (payload triggerPayload) toTrigger(graphID string) trigger.Trigger {
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	var webhook *trigger.WebhookSpec
	if payload.Webhook != nil {
		webhook = &trigger.WebhookSpec{
			StateBindings: payload.Webhook.StateBindings,
			StateMappings: append([]trigger.WebhookStateMapping(nil), payload.Webhook.StateMappings...),
		}
	}
	return trigger.Trigger{
		ID:           strings.TrimSpace(payload.ID),
		Name:         strings.TrimSpace(payload.Name),
		Type:         payload.Type,
		Enabled:      enabled,
		Target:       trigger.Target{GraphID: graphID},
		Concurrency:  payload.Concurrency,
		Credential:   payload.Credential,
		InitialState: payload.InitialState,
		Webhook:      webhook,
		Schedule:     payload.Schedule,
		Chat:         payload.Chat,
	}
}

func (s *Server) handleListTriggers(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	items, err := service.List(c.Request.Context())
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	result := make([]trigger.Trigger, 0, len(items))
	for _, item := range items {
		if item.Target.GraphID == graphID {
			result = append(result, s.publicTrigger(item))
		}
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleReplaceTriggers(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	payload, err := decodeTriggerReplacementPayload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}

	s.chatSetupSaveMu.Lock()
	defer s.chatSetupSaveMu.Unlock()
	items := make([]trigger.Trigger, 0, len(payload.Triggers))
	releases := make([]func(bool), 0, len(payload.Triggers))
	committed := false
	defer func() {
		for _, release := range releases {
			release(committed)
		}
	}()
	for _, itemPayload := range payload.Triggers {
		item := itemPayload.toTrigger(graphID)
		if err := normalizeTriggerCredential(&item); err != nil {
			writeError(c, http.StatusBadRequest, err)
			return
		}
		setupRelease, err := s.applyChatSetup(
			c.Request.Context(),
			setupRequestOwner(c),
			itemPayload.ChatSetupSessionID,
			&item,
		)
		if err != nil {
			writeError(c, statusForChatSetupError(err), err)
			return
		}
		releases = append(releases, setupRelease)
		secretRelease, err := s.externalizeChatChannelSecrets(c.Request.Context(), &item)
		if err != nil {
			writeError(c, statusForError(err), err)
			return
		}
		releases = append(releases, secretRelease)
		items = append(items, item)
	}
	session, err := s.loadTriggerSession(graphID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	if err := validateGraphTriggerState(session.graph, items); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	for index := range items {
		items[index].Target.GraphSessionID = session.runner.GraphSessionID()
	}
	items, err = service.ReplaceGraph(c.Request.Context(), graphID, items)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	committed = true
	for _, release := range releases {
		release(true)
	}
	if err := s.sweepManagedSecrets(context.WithoutCancel(c.Request.Context())); err != nil {
		slog.Warn("managed secret cleanup failed after trigger replacement", "graph_id", graphID, "error", err)
	}
	result := make([]trigger.Trigger, 0, len(items))
	for _, item := range items {
		result = append(result, s.publicTrigger(item))
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleCreateTriggerInvocation(c *gin.Context) {
	service, item, ok := s.scopedTrigger(c)
	if !ok {
		return
	}
	if !s.authorizeTriggerInvocation(c, item) {
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	var (
		run runtime.RunRecord
		err error
	)
	switch item.Type {
	case trigger.TypeWebhook:
		body, readErr := readRequestBody(c.Request.Body, maxWebhookBodyBytes)
		if readErr != nil {
			writeError(c, statusForRequestError(readErr), readErr)
			return
		}
		run, err = service.InvokeWebhookTrigger(ctx, item, body, requestHeaders(c))
	case trigger.TypeSchedule:
		run, err = service.InvokeScheduleTrigger(ctx, item)
	default:
		err = trigger.ErrTypeMismatch
	}
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusAccepted, triggerInvocationResponse{Run: run})
}

func (s *Server) handleWebhookTrigger(c *gin.Context) {
	service, item, ok := s.scopedTrigger(c)
	if !ok {
		return
	}
	if !s.authorizeTriggerInvocation(c, item) {
		return
	}
	if item.Type != trigger.TypeWebhook {
		writeError(c, http.StatusBadRequest, trigger.ErrTypeMismatch)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	if len(body) > maxWebhookBodyBytes {
		writeError(c, http.StatusRequestEntityTooLarge, errWebhookBodyTooLarge)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()
	run, err := service.InvokeWebhookTrigger(ctx, item, body, requestHeaders(c))
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusAccepted, triggerInvocationResponse{Run: run})
}

func (s *Server) scopedTrigger(c *gin.Context) (*trigger.Service, trigger.Trigger, bool) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return nil, trigger.Trigger{}, false
	}
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return nil, trigger.Trigger{}, false
	}
	triggerID, ok := requirePathParam(c, "trigger_id")
	if !ok {
		return nil, trigger.Trigger{}, false
	}
	item, err := service.Get(c.Request.Context(), triggerID)
	if err != nil || item.Target.GraphID != graphID {
		if err == nil {
			err = trigger.ErrNotFound
		}
		writeError(c, statusForError(err), err)
		return nil, trigger.Trigger{}, false
	}
	return service, item, true
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

func decodeTriggerReplacementPayload(c *gin.Context) (triggerReplacementPayload, error) {
	body, err := readRequestBody(c.Request.Body, maxTriggerPayloadBodyBytes)
	if err != nil {
		return triggerReplacementPayload{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return triggerReplacementPayload{}, errTriggerPayloadRequired
	}
	var payload triggerReplacementPayload
	if err := decodeStrictJSON(body, &payload); err != nil {
		return triggerReplacementPayload{}, err
	}
	if payload.Triggers == nil {
		return triggerReplacementPayload{}, invalidRequestf("triggers is required")
	}
	return payload, nil
}

func (s *Server) publicTrigger(item trigger.Trigger) trigger.Trigger {
	if s != nil && s.triggers != nil {
		item = s.triggers.RedactChatChannelConfig(item)
	}
	return item
}
