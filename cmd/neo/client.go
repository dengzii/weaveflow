package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type neoClient struct {
	baseURL string
	http    *http.Client
}

func newNeoClient(addr string) *neoClient {
	return &neoClient{
		baseURL: fmt.Sprintf("http://%s", addr),
		http:    &http.Client{},
	}
}

func (c *neoClient) Chat(ctx context.Context, message string) (string, error) {
	body, _ := json.Marshal(map[string]string{"message": message})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/neo/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot connect to neo server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "" && !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Msg != "" {
			return "", fmt.Errorf("server error: %s", result.Msg)
		}
		return "", fmt.Errorf("unexpected response (status %d)", resp.StatusCode)
	}

	renderer := &consoleRenderer{}
	var answer string

	scanner := bufio.NewScanner(resp.Body)
	var eventType, dataLines string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLines = strings.TrimPrefix(line, "data: ")
		case line == "":
			if eventType != "" && dataLines != "" {
				if eventType == "done" {
					renderer.endStream()
					return answer, nil
				}
				if eventType == "error" {
					renderer.endStream()
					msg := parseField(dataLines, "msg")
					return "", fmt.Errorf("server error: %s", msg)
				}
				if eventType == "run.finished" {
					answer = parseField(dataLines, "answer")
				}
				renderer.render(eventType, dataLines)
			}
			eventType = ""
			dataLines = ""
		}
	}
	renderer.endStream()
	if err := scanner.Err(); err != nil {
		return answer, err
	}
	return answer, nil
}

type consoleRenderer struct {
	streaming bool
}

func (r *consoleRenderer) render(eventType, data string) {
	switch eventType {
	case "llm.reasoning_chunk":
		text := parseField(data, "text")
		if text != "" {
			if !r.streaming {
				r.streaming = true
				fmt.Fprint(os.Stderr, "  \033[2m")
			}
			fmt.Fprint(os.Stderr, text)
		}
	case "llm.content_chunk":
		text := parseField(data, "text")
		if text != "" {
			r.endStream()
			fmt.Print(text)
		}
	default:
		r.endStream()
		r.renderProgress(eventType, data)
	}
}

func (r *consoleRenderer) renderProgress(eventType, data string) {
	switch eventType {
	case "nodes.started":
		name := parseField(data, "node_name")
		if name != "" {
			fmt.Fprintf(os.Stderr, "  \033[2m▸ %s\033[0m\n", name)
		}
	case "tool.called":
		name := parseField(data, "name")
		if name != "" {
			fmt.Fprintf(os.Stderr, "  \033[2m⚡ tool: %s\033[0m\n", name)
		}
	case "tool.failed":
		name := parseField(data, "name")
		errMsg := parseField(data, "error")
		fmt.Fprintf(os.Stderr, "  \033[31m✗ tool %s: %s\033[0m\n", name, errMsg)
	case "nodes.failed":
		name := parseField(data, "node_name")
		fmt.Fprintf(os.Stderr, "  \033[31m✗ %s failed\033[0m\n", name)
	case "run.failed":
		fmt.Fprint(os.Stderr, "  \033[31m✗ run failed\033[0m\n")
	}
}

func (r *consoleRenderer) endStream() {
	if r.streaming {
		fmt.Fprint(os.Stderr, "\033[0m")
		fmt.Println()
		r.streaming = false
	}
}

func parseField(jsonData string, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(jsonData), &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}
