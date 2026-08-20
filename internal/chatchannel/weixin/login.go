package weixin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/internal/chatchannel"
)

func (Factory) CredentialID(config map[string]any) string {
	token := configString(config, "bot_token")
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

const (
	setupSessionLifetime = 5 * time.Minute
	maxSetupRedirects    = 3
)

type loginSession struct {
	mu            sync.Mutex
	client        *http.Client
	baseURL       string
	allowInsecure bool
	qrCode        string
	expiresAt     time.Time
	existingToken string
	existingURL   string
	finished      bool
	lastResult    chatchannel.SetupResult
	logger        *slog.Logger
}

type qrCodeRequest struct {
	LocalTokenList []string `json:"local_token_list"`
}

type qrCodeResponse struct {
	apiResponse
	QRCode           string `json:"qrcode"`
	QRCodeImgContent string `json:"qrcode_img_content"`
}

type qrCodeStatusResponse struct {
	apiResponse
	Status       string `json:"status"`
	BotToken     string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	BaseURL      string `json:"baseurl"`
	UserID       string `json:"ilink_user_id"`
	RedirectHost string `json:"redirect_host"`
}

func (factory Factory) StartSetup(ctx context.Context, config chatchannel.SetupStartConfig) (chatchannel.SetupSession, chatchannel.SetupResult, error) {
	logger := factory.loggerFor("").With("operation", "qr_setup")
	existingToken := configString(config.ExistingConfig, "bot_token")
	logger.Info("WeChat QR setup starting", "has_existing_credential", existingToken != "")
	baseURL := strings.TrimSpace(factory.setupBaseURL)
	allowInsecure := baseURL != ""
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL, err := validateSetupBaseURL(baseURL, allowInsecure)
	if err != nil {
		logger.Error("WeChat QR setup URL validation failed", "error", err)
		return nil, chatchannel.SetupResult{}, err
	}
	client := factory.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: pollTimeout}
	}
	tokens := []string{}
	if existingToken != "" {
		tokens = append(tokens, existingToken)
	}
	var response qrCodeResponse
	if err := doSetupJSON(ctx, client, http.MethodPost, baseURL, "/ilink/bot/get_bot_qrcode", url.Values{"bot_type": {"3"}}, qrCodeRequest{LocalTokenList: tokens}, &response); err != nil {
		logger.Error("WeChat QR code request failed", "error", err)
		return nil, chatchannel.SetupResult{}, err
	}
	if err := response.businessError("get_bot_qrcode"); err != nil {
		logger.Error("WeChat QR code request rejected", "error", err)
		return nil, chatchannel.SetupResult{}, err
	}
	if strings.TrimSpace(response.QRCode) == "" || strings.TrimSpace(response.QRCodeImgContent) == "" {
		logger.Error("WeChat QR code response was incomplete")
		return nil, chatchannel.SetupResult{}, errors.New("iLink get_bot_qrcode returned an incomplete QR code")
	}
	expiresAt := time.Now().Add(setupSessionLifetime)
	result := chatchannel.SetupResult{
		Status:        chatchannel.SetupStatusWaiting,
		QRCodeContent: response.QRCodeImgContent,
		ExpiresAt:     expiresAt,
	}
	session := &loginSession{
		client:        client,
		baseURL:       baseURL,
		allowInsecure: allowInsecure,
		qrCode:        response.QRCode,
		expiresAt:     expiresAt,
		existingToken: existingToken,
		existingURL:   configString(config.ExistingConfig, "base_url"),
		lastResult:    result,
		logger:        logger,
	}
	logger.Info("WeChat QR setup session started", "expires_in", setupSessionLifetime)
	return session, result, nil
}

func (session *loginSession) Poll(ctx context.Context, input chatchannel.SetupPollInput) (chatchannel.SetupResult, error) {
	if session == nil {
		return chatchannel.SetupResult{}, errors.New("WeChat setup session is unavailable")
	}
	logger := session.logger
	if logger == nil {
		logger = slog.Default().With("component", "chat_channel", "channel", ChannelID, "operation", "qr_setup")
	}
	verificationCode := strings.TrimSpace(input.VerificationCode)
	if verificationCode != "" && !validVerificationCode(verificationCode) {
		logger.Error("WeChat QR verification code rejected", "reason", "invalid_format")
		return chatchannel.SetupResult{}, fmt.Errorf("%w: verification_code must contain 1 to 32 digits", chatchannel.ErrInvalidSetupInput)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.finished {
		logger.Debug("WeChat QR setup polled after completion", "status", session.lastResult.Status)
		return session.lastResult, nil
	}
	if time.Now().After(session.expiresAt) {
		logger.Info("WeChat QR setup expired")
		return session.finish(chatchannel.SetupResult{
			Status:    chatchannel.SetupStatusExpired,
			ExpiresAt: session.expiresAt,
		}), nil
	}

	for redirects := 0; redirects <= maxSetupRedirects; redirects++ {
		logger.Debug("WeChat QR status polling", "has_verification_code", verificationCode != "", "redirect_count", redirects)
		query := url.Values{"qrcode": {session.qrCode}}
		if verificationCode != "" {
			query.Set("verify_code", verificationCode)
		}
		var response qrCodeStatusResponse
		if err := doSetupJSON(ctx, session.client, http.MethodGet, session.baseURL, "/ilink/bot/get_qrcode_status", query, nil, &response); err != nil {
			logger.Error("WeChat QR status poll failed", "error", err)
			return chatchannel.SetupResult{}, err
		}
		if err := response.businessError("get_qrcode_status"); err != nil {
			logger.Error("WeChat QR status poll rejected", "error", err)
			return chatchannel.SetupResult{}, err
		}
		result, redirect, err := session.mapStatus(response)
		if err != nil {
			logger.Error("WeChat QR status handling failed", "error", err)
			return chatchannel.SetupResult{}, err
		}
		if redirect != "" {
			if redirects == maxSetupRedirects {
				logger.Error("WeChat QR setup exceeded redirect limit", "redirect_count", redirects)
				return chatchannel.SetupResult{}, errors.New("iLink QR login returned too many redirects")
			}
			logger.Info("WeChat QR setup redirect accepted", "redirect_count", redirects+1)
			session.baseURL = redirect
			verificationCode = ""
			continue
		}
		session.lastResult = result
		if result.Status == chatchannel.SetupStatusConfirmed || result.Status == chatchannel.SetupStatusExpired || result.Status == chatchannel.SetupStatusFailed {
			session.finished = true
		}
		if result.Status == chatchannel.SetupStatusWaiting {
			logger.Debug("WeChat QR setup status updated", "status", result.Status)
		} else {
			logger.Info("WeChat QR setup status updated", "status", result.Status)
		}
		return result, nil
	}
	logger.Error("WeChat QR setup redirect failed")
	return chatchannel.SetupResult{}, errors.New("iLink QR login redirect failed")
}

func (session *loginSession) mapStatus(response qrCodeStatusResponse) (chatchannel.SetupResult, string, error) {
	result := chatchannel.SetupResult{ExpiresAt: session.expiresAt}
	switch strings.TrimSpace(response.Status) {
	case "wait":
		result.Status = chatchannel.SetupStatusWaiting
	case "scaned":
		result.Status = chatchannel.SetupStatusScanned
	case "need_verifycode":
		result.Status = chatchannel.SetupStatusVerificationRequired
	case "verify_code_blocked":
		result.Status = chatchannel.SetupStatusFailed
		result.Message = "Verification attempts were blocked. Generate a new QR code."
	case "expired":
		result.Status = chatchannel.SetupStatusExpired
	case "scaned_but_redirect":
		redirect, err := validateSetupBaseURL(response.RedirectHost, session.allowInsecure)
		if err != nil {
			return chatchannel.SetupResult{}, "", fmt.Errorf("validate iLink redirect_host: %w", err)
		}
		return chatchannel.SetupResult{}, redirect, nil
	case "binded_redirect":
		if session.existingToken == "" {
			result.Status = chatchannel.SetupStatusFailed
			result.Message = "This WeChat bot is already bound and no reusable credential is available."
			break
		}
		result.Status = chatchannel.SetupStatusConfirmed
		result.CredentialConfig = map[string]any{
			"bot_token": session.existingToken,
			"base_url":  firstNonEmpty(session.existingURL, DefaultBaseURL),
		}
	case "confirmed":
		botToken := strings.TrimSpace(response.BotToken)
		if botToken == "" {
			return chatchannel.SetupResult{}, "", errors.New("iLink confirmed QR login without bot_token")
		}
		baseURL := strings.TrimSpace(response.BaseURL)
		if baseURL == "" {
			baseURL = DefaultBaseURL
		}
		baseURL, err := validateSetupBaseURL(baseURL, session.allowInsecure)
		if err != nil {
			return chatchannel.SetupResult{}, "", fmt.Errorf("validate iLink baseurl: %w", err)
		}
		result.Status = chatchannel.SetupStatusConfirmed
		result.CredentialConfig = map[string]any{"bot_token": botToken, "base_url": baseURL}
		if botID := strings.TrimSpace(response.BotID); botID != "" {
			result.Account = &chatchannel.SetupAccount{ID: botID, Label: botID}
		}
	default:
		return chatchannel.SetupResult{}, "", fmt.Errorf("unsupported iLink QR login status %q", response.Status)
	}
	return result, "", nil
}

func (session *loginSession) finish(result chatchannel.SetupResult) chatchannel.SetupResult {
	session.finished = true
	session.lastResult = result
	return result
}

func validVerificationCode(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validateSetupBaseURL(raw string, allowInsecure bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("URL must contain only an origin: %q", raw)
	}
	if allowInsecure {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
		}
	} else {
		if parsed.Scheme != "https" {
			return "", errors.New("URL must use https")
		}
		hostname := strings.ToLower(parsed.Hostname())
		if hostname != "weixin.qq.com" && !strings.HasSuffix(hostname, ".weixin.qq.com") {
			return "", fmt.Errorf("host %q is not an allowed Tencent WeChat host", hostname)
		}
		if parsed.Port() != "" && parsed.Port() != "443" {
			return "", fmt.Errorf("port %q is not allowed", parsed.Port())
		}
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func doSetupJSON(ctx context.Context, client *http.Client, method, baseURL, path string, query url.Values, body any, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + path)
	if err != nil {
		return fmt.Errorf("build %s URL: %w", path, err)
	}
	endpoint.RawQuery = query.Encode()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", path, err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return fmt.Errorf("create %s request: %w", path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("iLink-App-Id", apiID)
	request.Header.Set("iLink-App-ClientVersion", apiClientVersion)
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s request failed", path)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s returned HTTP %d", path, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxMessageSize))
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}
