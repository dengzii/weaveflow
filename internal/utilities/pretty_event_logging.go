package utilities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	fruntime "weaveflow/runtime"
)

type PrettyEventLogging struct {
	mu          sync.Mutex
	w           io.Writer
	colors      bool
	truncateLen int // 0 means no truncation
}

type PrettyEventOption func(*PrettyEventLogging)

// WithTruncate sets max length for text truncation. 0 means no truncation.
func WithTruncate(maxLen int) PrettyEventOption {
	return func(p *PrettyEventLogging) {
		p.truncateLen = maxLen
	}
}

func WithColors(enabled bool) PrettyEventOption {
	return func(p *PrettyEventLogging) {
		p.colors = enabled
	}
}

func NewPrettyEventLogging(w io.Writer, opts ...PrettyEventOption) *PrettyEventLogging {
	if w == nil {
		w = os.Stdout
	}
	p := &PrettyEventLogging{
		w:           w,
		colors:      true,
		truncateLen: 0, // no truncation by default
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *PrettyEventLogging) Publish(ctx context.Context, event fruntime.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.printEvent(event)
	return nil
}

func (p *PrettyEventLogging) PublishBatch(ctx context.Context, events []fruntime.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range events {
		p.printEvent(e)
	}
	return nil
}

func (p *PrettyEventLogging) printEvent(e fruntime.Event) {
	ts := e.Timestamp.Format("15:04:05.000")

	switch e.Type {
	case fruntime.EventNodeStarted:
		p.printf("%s %s %s\n", p.dim(ts), p.green("▶"), p.nodeName(e.NodeID))

	case fruntime.EventNodeFinished:
		p.printf("%s %s %s\n", p.dim(ts), p.green("✓"), p.nodeName(e.NodeID))

	case fruntime.EventNodeFailed:
		p.printf("%s %s %s\n", p.dim(ts), p.red("✗"), p.nodeName(e.NodeID))

	case fruntime.EventNodeCustom:
		p.printCustomEvent(ts, e)

	case fruntime.EventLLMReasoning:
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err == nil {
			if text, ok := payload["text"].(string); ok {
				p.printf("%s %s %s\n", p.dim(ts), p.cyan("💭"), p.truncate(text))
			}
		}

	case fruntime.EventLLMContent:
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err == nil {
			if text, ok := payload["text"].(string); ok {
				p.printf("%s %s %s\n", p.dim(ts), p.blue("📝"), p.truncate(text))
			}
		}

	case fruntime.EventLLMReasoningChunk:
		// Skip chunk events for cleaner output
		return

	case fruntime.EventLLMContentChunk:
		// Skip chunk events for cleaner output
		return

	case fruntime.EventToolStarted:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		name, _ := payload["name"].(string)
		p.printf("%s %s %s\n", p.dim(ts), p.yellow("🔧"), name)

	case fruntime.EventToolCalled:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		name, _ := payload["name"].(string)
		p.printf("%s %s %s(%s)\n", p.dim(ts), p.yellow("⚡"), name, p.truncate(jsonArg(payload["arguments"])))

	case fruntime.EventToolReturned:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		name, _ := payload["name"].(string)
		p.printf("%s %s %s → %s\n", p.dim(ts), p.yellow("↩"), name, p.truncate(jsonArg(payload["result"])))

	case fruntime.EventToolFailed:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		name, _ := payload["name"].(string)
		errMsg, _ := payload["error"].(string)
		p.printf("%s %s %s: %s\n", p.dim(ts), p.red("⚠"), name, p.truncate(errMsg))

	case fruntime.EventRunStarted:
		p.printf("%s %s run started\n", p.dim(ts), p.bold("🚀"))

	case fruntime.EventRunFinished:
		p.printf("%s %s run finished\n", p.dim(ts), p.bold("🏁"))

	case fruntime.EventRunFailed:
		p.printf("%s %s run failed\n", p.dim(ts), p.bold("💥"))

	case fruntime.EventCheckpointCreated:
		p.printf("%s %s checkpoint\n", p.dim(ts), p.magenta("💾"))

	case fruntime.EventArtifactCreated:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		artifactType, _ := payload["type"].(string)
		p.printf("%s %s artifact: %s\n", p.dim(ts), p.magenta("📎"), artifactType)

	case fruntime.EventWarning:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		msg, _ := payload["message"].(string)
		p.printf("%s %s %s\n", p.dim(ts), p.yellow("⚠️"), p.truncate(msg))

	case fruntime.EventContractViolation:
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		msg, _ := payload["message"].(string)
		p.printf("%s %s contract violation: %s\n", p.dim(ts), p.red("❌"), p.truncate(msg))

	default:
		// Skip unknown events
	}
}

func (p *PrettyEventLogging) printCustomEvent(ts string, e fruntime.Event) {
	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		return
	}

	kind, _ := payload["kind"].(string)
	switch kind {
	case "planner_progress":
		p.printPlannerProgress(ts, payload)
	case "orchestration":
		p.printOrchestration(ts, payload)
	default:
		// Skip unknown custom events
	}
}

func (p *PrettyEventLogging) printPlannerProgress(ts string, payload map[string]any) {
	phase, _ := payload["phase"].(string)
	message, _ := payload["message"].(string)

	counts, _ := payload["counts"].(map[string]any)
	total := intFromAny(counts["total"])
	completed := intFromAny(counts["completed"])
	inProgress := intFromAny(counts["in_progress"])

	currentTitle := ""
	if step, ok := payload["current_step"].(map[string]any); ok {
		currentTitle, _ = step["title"].(string)
	}

	var progress string
	if total > 0 {
		if inProgress > 0 {
			progress = fmt.Sprintf("%d/%d (%d running)", completed, total, inProgress)
		} else {
			progress = fmt.Sprintf("%d/%d", completed, total)
		}
	} else {
		progress = "-"
	}

	var statusIcon string
	switch phase {
	case "planned":
		statusIcon = p.cyan("📋")
	case "step_started":
		statusIcon = p.blue("▶️")
	case "step_completed":
		statusIcon = p.green("✅")
	case "replanning":
		statusIcon = p.yellow("🔄")
	case "replanned":
		statusIcon = p.yellow("🔄")
	case "completed":
		statusIcon = p.green("✅")
	case "blocked":
		statusIcon = p.red("🚫")
	default:
		statusIcon = "•"
	}

	if currentTitle != "" {
		p.printf("%s %s [%s] %s \"%s\" %s\n", p.dim(ts), statusIcon, phase, p.bold(progress), currentTitle, p.dim(message))
	} else {
		p.printf("%s %s [%s] %s %s\n", p.dim(ts), statusIcon, phase, p.bold(progress), p.dim(message))
	}
}

func (p *PrettyEventLogging) printOrchestration(ts string, payload map[string]any) {
	mode, _ := payload["mode"].(string)
	useMemory, _ := payload["use_memory"].(bool)
	hasDirectAnswer, _ := payload["has_direct_answer"].(bool)

	parts := []string{p.bold(mode)}
	if useMemory {
		parts = append(parts, p.cyan("mem"))
	}
	if hasDirectAnswer {
		parts = append(parts, p.green("direct"))
	}

	p.printf("%s %s %s\n", p.dim(ts), p.magenta("🔀"), strings.Join(parts, " "))
}

func (p *PrettyEventLogging) nodeName(nodeID string) string {
	// Extract node name from ID like "Planner_abc123" -> "Planner"
	if idx := strings.Index(nodeID, "_"); idx > 0 {
		return p.bold(nodeID[:idx])
	}
	return p.bold(nodeID)
}

func (p *PrettyEventLogging) truncate(s string) string {
	s = strings.TrimSpace(s)
	if p.truncateLen == 0 || len(s) <= p.truncateLen {
		return s
	}
	return s[:p.truncateLen-3] + "..."
}

func jsonArg(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// Color helpers

func (p *PrettyEventLogging) printf(format string, args ...any) {
	fmt.Fprintf(p.w, format, args...)
}

func (p *PrettyEventLogging) bold(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) dim(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) green(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[32m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) red(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[31m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) yellow(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[33m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) blue(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[34m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) magenta(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[35m" + s + "\x1b[0m"
}

func (p *PrettyEventLogging) cyan(s string) string {
	if !p.colors {
		return s
	}
	return "\x1b[36m" + s + "\x1b[0m"
}
