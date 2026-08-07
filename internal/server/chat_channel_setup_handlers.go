package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

const maxChatSetupPayloadBodyBytes int64 = 64 << 10

type chatSetupStartPayload struct {
	TriggerID string `json:"trigger_id,omitempty"`
}

type chatSetupPollPayload struct {
	VerificationCode string `json:"verification_code,omitempty"`
}

func (s *Server) handleStartChatChannelSetup(c *gin.Context) {
	if s == nil || s.chatSetup == nil {
		writeError(c, http.StatusServiceUnavailable, chatchannel.ErrSetupUnavailable)
		return
	}
	var payload chatSetupStartPayload
	if err := decodeOptionalChatSetupPayload(c, &payload); err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	channelID, ok := requirePathParam(c, "channel_id")
	if !ok {
		return
	}
	existingConfig, err := s.chatSetupExistingConfig(c.Request.Context(), channelID, payload.TriggerID)
	if err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	result, err := s.chatSetup.Start(c.Request.Context(), channelID, setupRequestOwner(c), existingConfig)
	if err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	if err := result.validate(); err != nil {
		writeError(c, http.StatusBadGateway, err)
		return
	}
	writeData(c, http.StatusCreated, result)
}

func (s *Server) handleGetChatChannelSetup(c *gin.Context) {
	if s == nil || s.chatSetup == nil {
		writeError(c, http.StatusServiceUnavailable, chatchannel.ErrSetupUnavailable)
		return
	}
	sessionID, ok := requirePathParam(c, "session_id")
	if !ok {
		return
	}
	channelID, ok := requirePathParam(c, "channel_id")
	if !ok {
		return
	}
	result, err := s.chatSetup.Poll(
		c.Request.Context(),
		sessionID,
		channelID,
		setupRequestOwner(c),
		chatchannel.SetupPollInput{},
	)
	if err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleSubmitChatChannelSetupVerification(c *gin.Context) {
	if s == nil || s.chatSetup == nil {
		writeError(c, http.StatusServiceUnavailable, chatchannel.ErrSetupUnavailable)
		return
	}
	var payload chatSetupPollPayload
	if err := decodeOptionalChatSetupPayload(c, &payload); err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	if strings.TrimSpace(payload.VerificationCode) == "" {
		writeError(c, http.StatusBadRequest, invalidRequestf("verification_code is required"))
		return
	}
	sessionID, ok := requirePathParam(c, "session_id")
	if !ok {
		return
	}
	channelID, ok := requirePathParam(c, "channel_id")
	if !ok {
		return
	}
	result, err := s.chatSetup.Poll(
		c.Request.Context(),
		sessionID,
		channelID,
		setupRequestOwner(c),
		chatchannel.SetupPollInput{VerificationCode: strings.TrimSpace(payload.VerificationCode)},
	)
	if err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleCancelChatChannelSetup(c *gin.Context) {
	if s == nil || s.chatSetup == nil {
		writeError(c, http.StatusServiceUnavailable, chatchannel.ErrSetupUnavailable)
		return
	}
	sessionID, ok := requirePathParam(c, "session_id")
	if !ok {
		return
	}
	channelID, ok := requirePathParam(c, "channel_id")
	if !ok {
		return
	}
	if err := s.chatSetup.Cancel(sessionID, channelID, setupRequestOwner(c)); err != nil {
		writeError(c, statusForChatSetupError(err), err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) chatSetupExistingConfig(ctx context.Context, channelID, triggerID string) (map[string]any, error) {
	triggerID = strings.TrimSpace(triggerID)
	if triggerID == "" {
		return nil, nil
	}
	service := s.TriggerService()
	if service == nil {
		return nil, errRunnerNotConfigured
	}
	item, err := service.Get(ctx, triggerID)
	if err != nil {
		return nil, err
	}
	if item.Type != trigger.TypeChat || item.Chat == nil || strings.TrimSpace(item.Chat.Channel) != channelID {
		return nil, fmt.Errorf("%w: trigger %q does not use chat channel %q", chatchannel.ErrInvalidSetupInput, triggerID, channelID)
	}
	return item.Chat.ChannelConfig, nil
}

func (s *Server) applyChatSetup(ctx context.Context, owner, sessionID string, item *trigger.Trigger) (func(bool), error) {
	noSetup := func(bool) {}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return noSetup, nil
	}
	if s == nil || s.chatSetup == nil {
		return nil, chatchannel.ErrSetupUnavailable
	}
	if item == nil || item.Type != trigger.TypeChat || item.Chat == nil {
		return nil, fmt.Errorf("%w: chat_setup_session_id requires a chat trigger", chatchannel.ErrInvalidSetupInput)
	}
	channelID := strings.TrimSpace(item.Chat.Channel)
	credentials, release, err := s.chatSetup.Claim(sessionID, channelID, owner)
	if err != nil {
		return nil, err
	}
	if item.Chat.ChannelConfig == nil {
		item.Chat.ChannelConfig = make(map[string]any)
	}
	for key, value := range credentials {
		item.Chat.ChannelConfig[key] = value
	}
	if err := s.ensureChatSetupCredentialAvailable(ctx, *item); err != nil {
		release(false)
		return nil, err
	}
	return release, nil
}

func (s *Server) ensureChatSetupCredentialAvailable(ctx context.Context, item trigger.Trigger) error {
	if s == nil || s.chatChannels == nil || s.triggers == nil || item.Chat == nil {
		return nil
	}
	channelID := strings.TrimSpace(item.Chat.Channel)
	credentialID := s.chatChannels.CredentialID(channelID, item.Chat.ChannelConfig)
	if credentialID == "" {
		return nil
	}
	items, err := s.triggers.List(ctx)
	if err != nil {
		return err
	}
	for _, existing := range items {
		if existing.ID == item.ID || existing.Type != trigger.TypeChat || existing.Chat == nil || strings.TrimSpace(existing.Chat.Channel) != channelID {
			continue
		}
		if s.chatChannels.CredentialID(channelID, existing.Chat.ChannelConfig) == credentialID {
			return errChatSetupCredentialInUse
		}
	}
	return nil
}

func decodeOptionalChatSetupPayload(c *gin.Context, target any) error {
	body, err := readRequestBody(c.Request.Body, maxChatSetupPayloadBodyBytes)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return decodeStrictJSON(body, target)
}

func setupRequestOwner(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return "unknown"
	}
	remoteAddress := strings.TrimSpace(c.Request.RemoteAddr)
	if host, _, err := net.SplitHostPort(remoteAddress); err == nil && host != "" {
		return host
	}
	if remoteAddress == "" {
		return "unknown"
	}
	return remoteAddress
}

func statusForChatSetupError(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, chatchannel.ErrChannelNotFound), errors.Is(err, errChatSetupSessionNotFound), errors.Is(err, trigger.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, chatchannel.ErrInvalidSetupInput):
		return http.StatusBadRequest
	case errors.Is(err, errChatSetupSessionBusy), errors.Is(err, errChatSetupSessionNotReady), errors.Is(err, errChatSetupSessionLimit), errors.Is(err, errChatSetupCredentialInUse):
		return http.StatusConflict
	case errors.Is(err, chatchannel.ErrSetupUnavailable), errors.Is(err, errRunnerNotConfigured):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
