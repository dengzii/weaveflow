package codex

import (
	"strings"
	"testing"
)

func TestReadCodexOutputExtractsFinalMessageAndUsage(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"type":"reasoning","text":"checked repository"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"final answer"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":11,"cached_input_tokens":3,"output_tokens":7}}`,
	}, "\n") + "\n"
	var chunks []Chunk
	read := readCodexOutput(strings.NewReader(input), "review", 1024*1024, func(chunk Chunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if read.err != nil {
		t.Fatal(read.err)
	}
	if read.parser.threadID != "thread-1" || read.parser.output != "final answer" {
		t.Fatalf("parser = %#v", read.parser)
	}
	if !read.parser.completed {
		t.Fatal("turn was not marked completed")
	}
	if read.parser.usage.InputTokens != 11 || read.parser.usage.CachedInputTokens != 3 || read.parser.usage.OutputTokens != 7 {
		t.Fatalf("usage = %#v", read.parser.usage)
	}
	if len(chunks) != 3 || chunks[0].ModelID != "review" || chunks[2].Text != "final answer" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestReadCodexOutputAllowsRecoverableErrorBeforeCompletion(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"error","message":"Reconnecting... 5/5"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"fallback succeeded"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":7}}`,
	}, "\n") + "\n"
	read := readCodexOutput(strings.NewReader(input), "review", 1024*1024, nil)
	if read.err != nil {
		t.Fatal(read.err)
	}
	if read.parser.failed != nil || !read.parser.completed || read.parser.output != "fallback succeeded" {
		t.Fatalf("parser = %#v", read.parser)
	}
	if read.parser.diagnostic != "Reconnecting... 5/5" {
		t.Fatalf("diagnostic = %q", read.parser.diagnostic)
	}
}

func TestReadCodexOutputReportsProviderFailure(t *testing.T) {
	input := `{"type":"turn.failed","error":{"message":"authentication failed"}}` + "\n"
	read := readCodexOutput(strings.NewReader(input), "review", 1024, nil)
	if read.err != nil {
		t.Fatal(read.err)
	}
	if read.parser.failed == nil || !strings.Contains(read.parser.failed.Error(), "authentication failed") {
		t.Fatalf("failure = %v", read.parser.failed)
	}
	if read.parser.completed {
		t.Fatal("failed turn was marked completed")
	}
}

func TestReadCodexOutputRejectsMalformedJSONL(t *testing.T) {
	read := readCodexOutput(strings.NewReader("not-json\n"), "review", 1024, nil)
	if read.err == nil || !strings.Contains(read.err.Error(), "decode Codex JSONL event") {
		t.Fatalf("error = %v", read.err)
	}
}

func TestReadCodexOutputEnforcesLimit(t *testing.T) {
	input := `{"type":"thread.started","thread_id":"thread-1"}` + "\n"
	read := readCodexOutput(strings.NewReader(input), "review", 8, nil)
	if read.err == nil || !read.truncated {
		t.Fatalf("read = %#v", read)
	}
}
