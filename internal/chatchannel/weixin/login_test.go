package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/internal/chatchannel"
)

func TestQRLoginSetupReturnsCredentialOnlyAfterConfirmation(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))
	statusCalls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.Header.Get("iLink-App-Id") != apiID || request.Header.Get("iLink-App-ClientVersion") != apiClientVersion {
			t.Errorf("app headers = %#v", request.Header)
		}
		if request.Header.Get("Authorization") != "" {
			t.Errorf("QR login sent Authorization header")
		}
		switch request.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			if request.Method != http.MethodPost || request.URL.Query().Get("bot_type") != "3" {
				t.Errorf("QR request = %s %s", request.Method, request.URL.String())
			}
			var payload qrCodeRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode QR request: %v", err)
			}
			if len(payload.LocalTokenList) != 1 || payload.LocalTokenList[0] != "existing-token" {
				t.Errorf("local_token_list = %#v", payload.LocalTokenList)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"ret": 0, "qrcode": "opaque-qr", "qrcode_img_content": "weixin://qr-content",
			})
		case "/ilink/bot/get_qrcode_status":
			if request.Method != http.MethodGet || request.URL.Query().Get("qrcode") != "opaque-qr" {
				t.Errorf("status request = %s %s", request.Method, request.URL.String())
			}
			statusCalls++
			switch statusCalls {
			case 1:
				_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0, "status": "scaned"})
			case 2:
				_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0, "status": "need_verifycode"})
			default:
				if request.URL.Query().Get("verify_code") != "123456" {
					t.Errorf("verify_code = %q", request.URL.Query().Get("verify_code"))
				}
				_ = json.NewEncoder(response).Encode(map[string]any{
					"ret": 0, "status": "confirmed", "bot_token": "new-token", "ilink_bot_id": "bot-a", "baseurl": server.URL,
				})
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	factory := Factory{Logger: logger, HTTPClient: server.Client(), setupBaseURL: server.URL}
	session, initial, err := factory.StartSetup(context.Background(), chatchannel.SetupStartConfig{
		ExistingConfig: map[string]any{"bot_token": "existing-token", "base_url": server.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Status != chatchannel.SetupStatusWaiting || initial.QRCodeContent != "weixin://qr-content" || len(initial.CredentialConfig) != 0 {
		t.Fatalf("initial result = %#v", initial)
	}
	scanned, err := session.Poll(context.Background(), chatchannel.SetupPollInput{})
	if err != nil || scanned.Status != chatchannel.SetupStatusScanned {
		t.Fatalf("scanned result = %#v, err = %v", scanned, err)
	}
	verification, err := session.Poll(context.Background(), chatchannel.SetupPollInput{})
	if err != nil || verification.Status != chatchannel.SetupStatusVerificationRequired {
		t.Fatalf("verification result = %#v, err = %v", verification, err)
	}
	confirmed, err := session.Poll(context.Background(), chatchannel.SetupPollInput{VerificationCode: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != chatchannel.SetupStatusConfirmed || confirmed.Account == nil || confirmed.Account.ID != "bot-a" {
		t.Fatalf("confirmed result = %#v", confirmed)
	}
	if confirmed.CredentialConfig["bot_token"] != "new-token" || confirmed.CredentialConfig["base_url"] != server.URL {
		t.Fatalf("confirmed credentials = %#v", confirmed.CredentialConfig)
	}
	logs := logOutput.String()
	if !strings.Contains(logs, "level=DEBUG") || !strings.Contains(logs, "level=INFO") {
		t.Fatalf("QR setup logs are missing debug/info levels: %s", logs)
	}
	for _, sensitive := range []string{"existing-token", "new-token", "opaque-qr", "weixin://qr-content", "123456"} {
		if strings.Contains(logs, sensitive) {
			t.Fatalf("QR setup logs contain sensitive value %q: %s", sensitive, logs)
		}
	}
}

func TestQRLoginRejectsInvalidVerificationCodeAndUntrustedURL(t *testing.T) {
	session := &loginSession{expiresAt: time.Now().Add(time.Minute), logger: discardLogger()}
	if _, err := session.Poll(context.Background(), chatchannel.SetupPollInput{VerificationCode: "12-ab"}); !errors.Is(err, chatchannel.ErrInvalidSetupInput) {
		t.Fatalf("invalid verification code error = %v", err)
	}
	if _, err := validateSetupBaseURL("https://evil.example", false); err == nil {
		t.Fatal("expected untrusted host error")
	}
	if got, err := validateSetupBaseURL("ilinkai.weixin.qq.com", false); err != nil || got != DefaultBaseURL {
		t.Fatalf("validated URL = %q, err = %v", got, err)
	}
}

func TestCredentialIDDoesNotExposeToken(t *testing.T) {
	identifier := (Factory{}).CredentialID(map[string]any{"bot_token": "secret-token"})
	if identifier == "" || identifier == "secret-token" {
		t.Fatalf("credential ID = %q", identifier)
	}
	if identifier != (Factory{}).CredentialID(map[string]any{"bot_token": "secret-token"}) {
		t.Fatal("credential ID is not stable")
	}
}
