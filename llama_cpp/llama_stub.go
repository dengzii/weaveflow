//go:build !cgo || !windows

package llama_cpp

import (
	"context"
	"errors"
	"time"
)

var errBackendUnavailable = errors.New("llama_cpp backend requires Windows + CGO_ENABLED=1 + a working gcc/clang toolchain in PATH")

type LoadOptions struct {
	ContextSize       int
	BatchSize         int
	Threads           int
	ThreadsBatch      int
	GPULayers         int
	UseMMap           bool
	UseMLock          bool
	StopOnContextFull bool
}

type GenerateOptions struct {
	MaxTokens   int
	Temperature float32
	TopP        float32
	TopK        int
	Seed        uint32
	AddSpecial  bool
	Stop        []string
}

type GenerateResult struct {
	StopReason   string
	PromptTokens int
	TokenCount   int
	Content      string
}

type GenerateSummary struct {
	StopReason   string
	PromptTokens int
	TokenCount   int
	Content      string
}

const (
	StopReasonNone            = ""
	StopReasonEndOfGeneration = "end_of_generation"
	StopReasonStopSequence    = "stop_sequence"
	StopReasonMaxTokens       = "max_tokens"
	StopReasonContextFull     = "context_full"
	StopReasonCancelled       = "cancelled"
)

type Model struct{}

func Load(path string, opts LoadOptions) (*Model, error) {
	return nil, errBackendUnavailable
}

func (m *Model) Path() string {
	return ""
}

func (m *Model) Name() string {
	return ""
}

func (m *Model) LoadedAt() time.Time {
	return time.Time{}
}

func (m *Model) ContextSize() int {
	return 0
}

func (m *Model) Release() error {
	return nil
}

func (m *Model) Generate(ctx context.Context, prompt string, options GenerateOptions) (<-chan GenerateResult, <-chan error) {
	resultCh := make(chan GenerateResult)
	errCh := make(chan error, 1)

	close(resultCh)
	errCh <- errBackendUnavailable
	close(errCh)

	return resultCh, errCh
}
