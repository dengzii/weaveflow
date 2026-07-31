package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/google/uuid"
)

const (
	defaultChatSetupLifetime     = 5 * time.Minute
	maxChatSetupSessions         = 64
	maxChatSetupSessionsPerOwner = 4
)

var (
	errChatSetupSessionNotFound = errors.New("chat channel setup session not found")
	errChatSetupSessionBusy     = errors.New("chat channel setup session is busy")
	errChatSetupSessionNotReady = errors.New("chat channel setup session is not confirmed")
	errChatSetupSessionLimit    = errors.New("too many chat channel setup sessions")
	errChatSetupCredentialInUse = errors.New("chat channel credential is already attached to another trigger")
)

type chatSetupManager struct {
	mu       sync.Mutex
	registry *chatchannel.Registry
	sessions map[string]*chatSetupSession
	now      func() time.Time
}

type chatSetupSession struct {
	id               string
	channelID        string
	owner            string
	provider         chatchannel.SetupSession
	status           chatchannel.SetupStatus
	qrCodeContent    string
	expiresAt        time.Time
	account          *chatchannel.SetupAccount
	message          string
	credentialConfig map[string]any
	polling          bool
	claimed          bool
}

type chatSetupPublicResult struct {
	SessionID     string                    `json:"session_id"`
	ChannelID     string                    `json:"channel_id"`
	Status        chatchannel.SetupStatus   `json:"status"`
	QRCodeContent string                    `json:"qr_code_content,omitempty"`
	ExpiresAt     time.Time                 `json:"expires_at"`
	Account       *chatchannel.SetupAccount `json:"account,omitempty"`
	Message       string                    `json:"message,omitempty"`
}

func newChatSetupManager(registry *chatchannel.Registry) *chatSetupManager {
	return &chatSetupManager{
		registry: registry,
		sessions: make(map[string]*chatSetupSession),
		now:      time.Now,
	}
}

func (manager *chatSetupManager) Start(ctx context.Context, channelID, owner string, existingConfig map[string]any) (chatSetupPublicResult, error) {
	if manager == nil || manager.registry == nil {
		return chatSetupPublicResult{}, chatchannel.ErrSetupUnavailable
	}
	channelID = strings.TrimSpace(channelID)
	owner = strings.TrimSpace(owner)
	manager.mu.Lock()
	manager.pruneLocked()
	if len(manager.sessions) >= maxChatSetupSessions || manager.ownerSessionCountLocked(channelID, owner) >= maxChatSetupSessionsPerOwner {
		manager.mu.Unlock()
		return chatSetupPublicResult{}, errChatSetupSessionLimit
	}
	manager.mu.Unlock()

	provider, result, err := manager.registry.StartSetup(ctx, channelID, chatchannel.SetupStartConfig{ExistingConfig: existingConfig})
	if err != nil {
		return chatSetupPublicResult{}, err
	}
	now := manager.now()
	expiresAt := result.ExpiresAt
	maximumExpiry := now.Add(defaultChatSetupLifetime)
	if expiresAt.IsZero() || expiresAt.After(maximumExpiry) {
		expiresAt = maximumExpiry
	}
	if !expiresAt.After(now) {
		return chatSetupPublicResult{}, errors.New("chat channel setup returned an expired session")
	}
	session := &chatSetupSession{
		id:               uuid.NewString(),
		channelID:        channelID,
		owner:            owner,
		provider:         provider,
		status:           result.Status,
		qrCodeContent:    strings.TrimSpace(result.QRCodeContent),
		expiresAt:        expiresAt,
		account:          cloneSetupAccount(result.Account),
		message:          strings.TrimSpace(result.Message),
		credentialConfig: cloneSetupConfig(result.CredentialConfig),
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneLocked()
	if len(manager.sessions) >= maxChatSetupSessions || manager.ownerSessionCountLocked(channelID, owner) >= maxChatSetupSessionsPerOwner {
		return chatSetupPublicResult{}, errChatSetupSessionLimit
	}
	manager.sessions[session.id] = session
	return session.publicResult(), nil
}

func (manager *chatSetupManager) Poll(ctx context.Context, sessionID, channelID, owner string, input chatchannel.SetupPollInput) (chatSetupPublicResult, error) {
	if manager == nil {
		return chatSetupPublicResult{}, errChatSetupSessionNotFound
	}
	sessionID = strings.TrimSpace(sessionID)
	channelID = strings.TrimSpace(channelID)
	owner = strings.TrimSpace(owner)
	manager.mu.Lock()
	manager.pruneLocked()
	session := manager.sessions[sessionID]
	if session == nil || session.channelID != channelID || session.owner != owner {
		manager.mu.Unlock()
		return chatSetupPublicResult{}, errChatSetupSessionNotFound
	}
	if session.polling || session.claimed {
		manager.mu.Unlock()
		return chatSetupPublicResult{}, errChatSetupSessionBusy
	}
	if isTerminalSetupStatus(session.status) {
		result := session.publicResult()
		manager.mu.Unlock()
		return result, nil
	}
	session.polling = true
	manager.mu.Unlock()

	result, err := session.provider.Poll(ctx, input)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current := manager.sessions[sessionID]
	if current != session || session.owner != owner {
		return chatSetupPublicResult{}, errChatSetupSessionNotFound
	}
	session.polling = false
	if err != nil {
		return chatSetupPublicResult{}, err
	}
	if result.Status == "" {
		return chatSetupPublicResult{}, errors.New("chat channel setup returned an empty status")
	}
	session.status = result.Status
	if content := strings.TrimSpace(result.QRCodeContent); content != "" {
		session.qrCodeContent = content
	}
	if !result.ExpiresAt.IsZero() && result.ExpiresAt.Before(session.expiresAt) {
		session.expiresAt = result.ExpiresAt
	}
	session.account = cloneSetupAccount(result.Account)
	session.message = strings.TrimSpace(result.Message)
	if result.Status == chatchannel.SetupStatusConfirmed {
		if len(result.CredentialConfig) == 0 {
			return chatSetupPublicResult{}, errors.New("confirmed chat channel setup returned no credentials")
		}
		session.credentialConfig = cloneSetupConfig(result.CredentialConfig)
	}
	return session.publicResult(), nil
}

func (manager *chatSetupManager) Cancel(sessionID, channelID, owner string) error {
	if manager == nil {
		return errChatSetupSessionNotFound
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	session := manager.sessions[strings.TrimSpace(sessionID)]
	if session == nil || session.channelID != strings.TrimSpace(channelID) || session.owner != strings.TrimSpace(owner) {
		return errChatSetupSessionNotFound
	}
	delete(manager.sessions, session.id)
	return nil
}

func (manager *chatSetupManager) Claim(sessionID, channelID, owner string) (map[string]any, func(bool), error) {
	if manager == nil {
		return nil, nil, errChatSetupSessionNotFound
	}
	manager.mu.Lock()
	manager.pruneLocked()
	session := manager.sessions[strings.TrimSpace(sessionID)]
	if session == nil || session.owner != strings.TrimSpace(owner) || session.channelID != strings.TrimSpace(channelID) {
		manager.mu.Unlock()
		return nil, nil, errChatSetupSessionNotFound
	}
	if session.polling || session.claimed {
		manager.mu.Unlock()
		return nil, nil, errChatSetupSessionBusy
	}
	if session.status != chatchannel.SetupStatusConfirmed || len(session.credentialConfig) == 0 {
		manager.mu.Unlock()
		return nil, nil, errChatSetupSessionNotReady
	}
	session.claimed = true
	credentials := cloneSetupConfig(session.credentialConfig)
	manager.mu.Unlock()

	var once sync.Once
	release := func(commit bool) {
		once.Do(func() {
			manager.mu.Lock()
			defer manager.mu.Unlock()
			if manager.sessions[session.id] != session {
				return
			}
			if commit {
				delete(manager.sessions, session.id)
				return
			}
			session.claimed = false
		})
	}
	return credentials, release, nil
}

func (manager *chatSetupManager) pruneLocked() {
	now := manager.now()
	for id, session := range manager.sessions {
		if now.After(session.expiresAt) && !session.polling && !session.claimed {
			delete(manager.sessions, id)
		}
	}
}

func (manager *chatSetupManager) ownerSessionCountLocked(channelID, owner string) int {
	count := 0
	for _, session := range manager.sessions {
		if session.channelID == channelID && session.owner == owner {
			count++
		}
	}
	return count
}

func (session *chatSetupSession) publicResult() chatSetupPublicResult {
	qrCodeContent := session.qrCodeContent
	if isTerminalSetupStatus(session.status) {
		qrCodeContent = ""
	}
	return chatSetupPublicResult{
		SessionID:     session.id,
		ChannelID:     session.channelID,
		Status:        session.status,
		QRCodeContent: qrCodeContent,
		ExpiresAt:     session.expiresAt,
		Account:       cloneSetupAccount(session.account),
		Message:       session.message,
	}
}

func isTerminalSetupStatus(status chatchannel.SetupStatus) bool {
	return status == chatchannel.SetupStatusConfirmed || status == chatchannel.SetupStatusExpired || status == chatchannel.SetupStatusFailed
}

func cloneSetupConfig(config map[string]any) map[string]any {
	result := make(map[string]any, len(config))
	for key, value := range config {
		result[key] = value
	}
	return result
}

func cloneSetupAccount(account *chatchannel.SetupAccount) *chatchannel.SetupAccount {
	if account == nil {
		return nil
	}
	copy := *account
	return &copy
}

func (result chatSetupPublicResult) validate() error {
	if result.SessionID == "" || result.ChannelID == "" || result.Status == "" || result.ExpiresAt.IsZero() {
		return fmt.Errorf("invalid chat channel setup result")
	}
	return nil
}
