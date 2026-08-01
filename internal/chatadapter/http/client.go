package chathttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
)

type Client struct {
	HTTPClient *http.Client
}

func (c Client) Invoke(ctx context.Context, endpoint string, message chatchannel.InboundMessage, sink chatcap.ReplySink) error {
	if sink == nil {
		return chatcap.ErrReplySinkUnavailable
	}
	message = message.Normalize()
	if err := message.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(endpoint), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return fmt.Errorf("chat trigger returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if mediaType := strings.ToLower(response.Header.Get("Content-Type")); !strings.Contains(mediaType, "text/event-stream") {
		return fmt.Errorf("chat trigger returned unsupported content type %q", response.Header.Get("Content-Type"))
	}
	return consumeEventStream(ctx, response.Body, sink)
}

func consumeEventStream(ctx context.Context, reader io.Reader, sink chatcap.ReplySink) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	event := ""
	data := make([]string, 0, 1)
	dispatch := func() error {
		if event == "" && len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		defer func() {
			event = ""
			data = data[:0]
		}()
		switch event {
		case string(chatcap.ReplyUpdate), string(chatcap.ReplyMessage), string(chatcap.ReplyFinish):
			var reply chatcap.Reply
			if err := json.Unmarshal([]byte(payload), &reply); err != nil {
				return fmt.Errorf("decode chat reply event %q: %w", event, err)
			}
			return sink.Emit(ctx, reply)
		case "error":
			var value struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(payload), &value); err != nil {
				return fmt.Errorf("decode chat trigger error: %w", err)
			}
			if strings.TrimSpace(value.Error) == "" {
				value.Error = "chat trigger failed"
			}
			return fmt.Errorf("%s", value.Error)
		default:
			return nil
		}
	}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatch()
}
