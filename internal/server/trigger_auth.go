package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

func (s *Server) graphTriggerCredentials(ctx context.Context, graphID string) (map[string]*dsl.SecretRef, error) {
	credentials := make(map[string]*dsl.SecretRef)
	items, err := s.triggers.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Target.GraphID != graphID || item.Credential == nil {
			continue
		}
		credential := *item.Credential
		credentials[item.ID] = &credential
	}
	return credentials, nil
}

func (s *Server) applyTriggerCredential(ctx context.Context, payload triggerPayload, item *trigger.Trigger, existing *dsl.SecretRef) (func(bool), error) {
	noChange := func(bool) {}
	value := strings.TrimSpace(payload.CredentialValue)
	if payload.CredentialClear && value != "" {
		return nil, invalidRequestf("trigger %q credential clear conflicts with a new credential", item.ID)
	}
	if value != "" {
		if s == nil || s.managedSecrets == nil {
			return nil, fmt.Errorf("managed trigger credential storage is unavailable")
		}
		credential, release, err := s.managedSecrets.Put(ctx, value)
		if err != nil {
			return nil, fmt.Errorf("store trigger %q credential: %w", item.ID, err)
		}
		item.Credential = &credential
		return release, nil
	}
	if payload.CredentialClear {
		item.Credential = nil
		return noChange, nil
	}
	if item.Credential == nil && existing != nil {
		credential := *existing
		item.Credential = &credential
	}
	if err := normalizeTriggerCredential(item); err != nil {
		return nil, err
	}
	return noChange, nil
}

var (
	errTriggerAuthenticationRequired  = errors.New("trigger authentication is required")
	errTriggerAuthenticationFailed    = errors.New("trigger authentication failed")
	errTriggerCredentialNotConfigured = errors.New("trigger credential is not configured")
	errTriggerCredentialUnavailable   = errors.New("trigger credential is unavailable")
)

func normalizeTriggerCredential(item *trigger.Trigger) error {
	if item == nil || item.Credential == nil {
		return nil
	}
	credential, err := normalizeSecretRef(*item.Credential)
	if err != nil {
		return fmt.Errorf("trigger credential: %w", err)
	}
	item.Credential = &credential
	return nil
}

func (s *Server) authorizeTriggerInvocation(c *gin.Context, item trigger.Trigger) bool {
	if item.Credential == nil {
		if item.Type == trigger.TypeWebhook {
			return true
		}
		writeError(c, http.StatusForbidden, errTriggerCredentialNotConfigured)
		return false
	}
	provided, ok := triggerBearerToken(c)
	if !ok {
		c.Header("WWW-Authenticate", `Bearer realm="weaveflow-trigger"`)
		writeError(c, http.StatusUnauthorized, errTriggerAuthenticationRequired)
		return false
	}
	expected, err := s.resolveSecret(c.Request.Context(), *item.Credential)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, errTriggerCredentialUnavailable)
		return false
	}
	expectedHash := sha256.Sum256([]byte(expected))
	providedHash := sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
		writeError(c, http.StatusForbidden, errTriggerAuthenticationFailed)
		return false
	}
	return true
}

func triggerBearerToken(c *gin.Context) (string, bool) {
	if c == nil {
		return "", false
	}
	parts := strings.Fields(strings.TrimSpace(c.GetHeader("Authorization")))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
