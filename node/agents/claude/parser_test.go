package claude

import (
	"strings"
	"testing"
)

func TestReadClaudeOutputExtractsResultUsageAndProgress(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-sonnet-4-6"}`,
		`{"type":"stream_event","session_id":"session-1","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"checking"}}}`,
		`{"type":"stream_event","session_id":"session-1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"final answer"}}}`,
		`{"type":"assistant","session_id":"session-1","message":{"model":"claude-sonnet-4-6","content":[{"type":"thinking","thinking":"checking"},{"type":"text","text":"final answer"}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","result":"final answer","total_cost_usd":0.012,"num_turns":2,"usage":{"input_tokens":11,"cache_read_input_tokens":3,"output_tokens":7}}`,
	}, "\n") + "\n"
	var chunks []Chunk
	read := readClaudeOutput(strings.NewReader(input), 1024*1024, func(chunk Chunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if read.err != nil {
		t.Fatal(read.err)
	}
	if !read.parser.completed || read.parser.sessionID != "session-1" || read.parser.output != "final answer" || read.parser.model != "claude-sonnet-4-6" {
		t.Fatalf("parser = %#v", read.parser)
	}
	if read.parser.costUSD != 0.012 || read.parser.numTurns != 2 || read.parser.usage.InputTokens != 11 || read.parser.usage.OutputTokens != 7 {
		t.Fatalf("result metadata = %#v", read.parser)
	}
	if len(chunks) != 2 || chunks[0].Channel != "reasoning" || chunks[1].Channel != "content" || chunks[1].Text != "final answer" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestReadClaudeOutputUsesAssistantMessageWithoutPartialEvents(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"assistant","session_id":"session-1","message":{"model":"claude-opus-4-1","content":[{"type":"text","text":"answer"}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","result":"answer"}`,
	}, "\n") + "\n"
	var chunks []Chunk
	read := readClaudeOutput(strings.NewReader(input), 1024, func(chunk Chunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if read.err != nil {
		t.Fatal(read.err)
	}
	if len(chunks) != 1 || chunks[0].Model != "claude-opus-4-1" || chunks[0].Text != "answer" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestReadClaudeOutputResetsPartialDeduplicationPerAssistantMessage(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"stream_event","session_id":"session-1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"first"}}}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"first"}]}}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"second"}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","result":"second"}`,
	}, "\n") + "\n"
	var chunks []Chunk
	read := readClaudeOutput(strings.NewReader(input), 1024, func(chunk Chunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if read.err != nil {
		t.Fatal(read.err)
	}
	if len(chunks) != 2 || chunks[0].Text != "first" || chunks[1].Text != "second" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestReadClaudeOutputReportsProviderFailure(t *testing.T) {
	input := `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"result":"budget exceeded"}` + "\n"
	read := readClaudeOutput(strings.NewReader(input), 1024, nil)
	if read.err != nil {
		t.Fatal(read.err)
	}
	if read.parser.failed == nil || !strings.Contains(read.parser.failed.Error(), "budget exceeded") || read.parser.completed {
		t.Fatalf("parser = %#v", read.parser)
	}
}

func TestReadClaudeOutputRejectsMalformedJSONL(t *testing.T) {
	read := readClaudeOutput(strings.NewReader("not-json\n"), 1024, nil)
	if read.err == nil || !strings.Contains(read.err.Error(), "decode Claude stream-json event") {
		t.Fatalf("error = %v", read.err)
	}
}

func TestReadClaudeOutputEnforcesLimit(t *testing.T) {
	input := `{"type":"system","subtype":"init"}` + "\n"
	read := readClaudeOutput(strings.NewReader(input), 8, nil)
	if read.err == nil || !read.truncated {
		t.Fatalf("read = %#v", read)
	}
}
