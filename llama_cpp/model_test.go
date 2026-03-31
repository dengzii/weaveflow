package llama_cpp

import (
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestBuildPromptIncludesToolsAndMessages(t *testing.T) {
	t.Parallel()

	prompt, err := buildPrompt(
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
			llms.TextParts(llms.ChatMessageTypeHuman, "What time is it?"),
			{
				Role: llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.ToolCallResponse{
						ToolCallID: "call_1",
						Name:       "current_time",
						Content:    "2026-03-26T10:00:00+08:00",
					},
				},
			},
		},
		[]llms.Tool{
			{
				Type: "function",
				Function: &llms.FunctionDefinition{
					Name:        "current_time",
					Description: "Return the current time.",
					Parameters: map[string]any{
						"type": "object",
					},
				},
			},
		},
		thinkingSettings{},
	)
	if err != nil {
		t.Fatalf("buildPrompt() error = %v", err)
	}

	for _, want := range []string{
		"Available tools:",
		"current_time: Return the current time.",
		"System: You are helpful.",
		"User: What time is it?",
		`Tool: tool_result={"content":"2026-03-26T10:00:00+08:00","name":"current_time","tool_call_id":"call_1"}`,
		"Assistant:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestBuildPromptIncludesThinkInstructions(t *testing.T) {
	t.Parallel()

	prompt, err := buildPrompt(
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "Explain 2+2"),
		},
		nil,
		thinkingSettings{enabled: true, returnThinking: true},
	)
	if err != nil {
		t.Fatalf("buildPrompt() error = %v", err)
	}

	for _, want := range []string{
		"<think>...</think>",
		"User: Explain 2+2",
		"Assistant: <think>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestParseStructuredResponseParsesToolCalls(t *testing.T) {
	t.Parallel()

	raw := "```json\n{\"tool_calls\":[{\"name\":\"calculator\",\"arguments\":{\"expression\":\"2+2\"}}]}\n```"

	parsed, ok := parseStructuredResponse(raw)
	if !ok {
		t.Fatal("parseStructuredResponse() did not parse tool call payload")
	}
	if len(parsed.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(parsed.ToolCalls))
	}

	call := parsed.ToolCalls[0]
	if call.FunctionCall == nil {
		t.Fatal("expected function payload")
	}
	if call.FunctionCall.Name != "calculator" {
		t.Fatalf("unexpected tool name %q", call.FunctionCall.Name)
	}
	if call.FunctionCall.Arguments != "{\"expression\":\"2+2\"}" {
		t.Fatalf("unexpected arguments %q", call.FunctionCall.Arguments)
	}
}

func TestParseStructuredResponseParsesContent(t *testing.T) {
	t.Parallel()

	parsed, ok := parseStructuredResponse(`{"content":"hello"}`)
	if !ok {
		t.Fatal("parseStructuredResponse() did not parse content payload")
	}
	if parsed.Content != "hello" {
		t.Fatalf("unexpected content %q", parsed.Content)
	}
	if len(parsed.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(parsed.ToolCalls))
	}
}

func TestSplitThinkingContent(t *testing.T) {
	t.Parallel()

	reasoning, content := splitThinkingContent("<think>step 1\nstep 2</think>\nfinal answer")
	if reasoning != "step 1\nstep 2" {
		t.Fatalf("unexpected reasoning %q", reasoning)
	}
	if content != "final answer" {
		t.Fatalf("unexpected content %q", content)
	}
}

func TestCollectPromptToolsIncludesLegacyFunctionsWithoutDuplication(t *testing.T) {
	t.Parallel()

	tools := collectPromptTools(llms.CallOptions{
		Tools: []llms.Tool{
			{
				Type: "function",
				Function: &llms.FunctionDefinition{
					Name: "current_time",
				},
			},
		},
		Functions: []llms.FunctionDefinition{
			{
				Name: "calculator",
			},
		},
	})

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Function == nil || tools[0].Function.Name != "current_time" {
		t.Fatalf("unexpected first tool %#v", tools[0].Function)
	}
	if tools[1].Function == nil || tools[1].Function.Name != "calculator" {
		t.Fatalf("unexpected second tool %#v", tools[1].Function)
	}
}

func TestThinkStreamParserAcrossChunks(t *testing.T) {
	t.Parallel()

	parser := newThinkStreamParser()

	r1, c1 := parser.Write("<thi", false)
	if r1 != "" || c1 != "" {
		t.Fatalf("unexpected partial output reasoning=%q content=%q", r1, c1)
	}

	r2, c2 := parser.Write("nk>plan", false)
	if r2 != "" || c2 != "" {
		t.Fatalf("unexpected second partial output reasoning=%q content=%q", r2, c2)
	}

	r3, c3 := parser.Write("</think>done", true)
	if r3 != "plan" {
		t.Fatalf("unexpected reasoning %q", r3)
	}
	if c3 != "done" {
		t.Fatalf("unexpected content %q", c3)
	}
}
