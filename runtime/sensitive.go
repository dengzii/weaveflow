package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
)

const redactedSensitiveValue = "[REDACTED]"

func isJSONMIMEType(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if separator := strings.IndexByte(mimeType, ';'); separator >= 0 {
		mimeType = strings.TrimSpace(mimeType[:separator])
	}
	return mimeType == "application/json" || strings.HasSuffix(mimeType, "+json")
}

func marshalRedactedJSON(ctx context.Context, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return redactJSONBytes(ctx, data), nil
}

func redactJSONBytes(ctx context.Context, data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	secrets := sensitiveContextValues(ctx)

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err == nil {
		if err := decoder.Decode(&struct{}{}); err == io.EOF {
			redacted := redactJSONValue(value, secrets)
			if encoded, marshalErr := json.Marshal(redacted); marshalErr == nil {
				return encoded
			}
		}
	}
	return []byte(redactSensitiveText(string(data), secrets))
}

func redactJSONValue(value any, secrets []string) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveFieldName(key) {
				redacted[key] = redactedSensitiveValue
				continue
			}
			redacted[key] = redactJSONValue(item, secrets)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactJSONValue(item, secrets)
		}
		return redacted
	case string:
		return redactSensitiveText(typed, secrets)
	default:
		return value
	}
}

func redactSensitiveText(value string, secrets []string) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, redactedSensitiveValue)
	}
	return value
}

func sensitiveContextValues(ctx context.Context) []string {
	values := make([]string, 0)
	seen := make(map[string]struct{})
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) < 4 || value == redactedSensitiveValue {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}

	for _, config := range core.ModelConfigsFromContext(ctx) {
		appendValue(config.APIKey)
	}
	for key, value := range core.EnvironmentFromContext(ctx) {
		if isSensitiveFieldName(key) {
			appendValue(value)
		}
	}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && isSensitiveFieldName(key) {
			appendValue(value)
		}
	}

	sort.SliceStable(values, func(left, right int) bool {
		return len(values[left]) > len(values[right])
	})
	return values
}

func isSensitiveFieldName(name string) bool {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(name)), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(parts) == 0 {
		return false
	}
	compact := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(strings.TrimSpace(name)))
	for _, marker := range []string{
		"password", "passwd", "secret", "credential", "authorization", "cookie",
		"apikey", "apitoken", "accesstoken", "refreshtoken", "authtoken", "clientsecret",
		"privatekey",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	has := func(target string) bool {
		for _, part := range parts {
			if part == target {
				return true
			}
		}
		return false
	}
	for _, part := range parts {
		switch part {
		case "password", "passwd", "secret", "credential", "authorization", "cookie", "apikey":
			return true
		case "token":
			if !has("count") && !has("usage") && !has("budget") && !has("limit") && !has("input") && !has("output") && !has("prompt") && !has("completion") && !has("reasoning") {
				return true
			}
		}
	}
	return has("api") && has("key") || has("access") && has("key") || has("private") && has("key") || has("client") && has("key")
}

func sanitizeEventPayload(ctx context.Context, event Event) Event {
	event.Payload = redactJSONBytes(ctx, event.Payload)
	return event
}

func sanitizeEvents(ctx context.Context, events []Event) []Event {
	if len(events) == 0 {
		return nil
	}
	sanitized := make([]Event, len(events))
	for index, event := range events {
		sanitized[index] = sanitizeEventPayload(ctx, cloneEvent(event))
	}
	return sanitized
}

func sanitizeCommit(ctx context.Context, commit Commit) Commit {
	if commit.Run != nil {
		runWrite := *commit.Run
		runWrite.Run = sanitizeRunRecord(ctx, runWrite.Run)
		commit.Run = &runWrite
	}
	if len(commit.Steps) > 0 {
		steps := make([]StepWrite, len(commit.Steps))
		for index, write := range commit.Steps {
			write.Step = sanitizeStepRecord(ctx, write.Step)
			steps[index] = write
		}
		commit.Steps = steps
	}
	commit.Events = sanitizeEvents(ctx, commit.Events)
	return commit
}

func sanitizeRunRecord(ctx context.Context, run RunRecord) RunRecord {
	run.ErrorMessage = redactSensitiveString(ctx, run.ErrorMessage)
	if run.ReturnValue != nil {
		run.ReturnValue = redactPersistedValue(ctx, run.ReturnValue)
	}
	return run
}

func redactPersistedValue(ctx context.Context, value any) any {
	data, err := marshalRedactedJSON(ctx, value)
	if err != nil {
		return redactedSensitiveValue
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var redacted any
	if err := decoder.Decode(&redacted); err != nil {
		return redactedSensitiveValue
	}
	return redacted
}

func sanitizeStepRecord(ctx context.Context, step StepRecord) StepRecord {
	step.ErrorMessage = redactSensitiveString(ctx, step.ErrorMessage)
	return step
}

func sanitizeArtifact(ctx context.Context, artifact Artifact) Artifact {
	mimeType := strings.ToLower(strings.TrimSpace(artifact.MIMEType))
	if separator := strings.IndexByte(mimeType, ';'); separator >= 0 {
		mimeType = strings.TrimSpace(mimeType[:separator])
	}
	if isJSONMIMEType(mimeType) || json.Valid(artifact.Data) {
		artifact.Data = redactJSONBytes(ctx, artifact.Data)
	} else if strings.HasPrefix(mimeType, "text/") {
		artifact.Data = []byte(redactSensitiveString(ctx, string(artifact.Data)))
	}
	return artifact
}

func redactSensitiveString(ctx context.Context, value string) string {
	return redactSensitiveText(value, sensitiveContextValues(ctx))
}
