package falcon

import (
	"context"
	"errors"
	"strings"
	"sync"

	"falcon/llama_cpp"
)

type llm interface {
	Generate(ctx context.Context, prompt string, options llama_cpp.GenerateOptions) (<-chan llama_cpp.GenerateResult, <-chan error)
	Release() error
}

type ModelHub struct {
	mu     sync.RWMutex
	models map[string]llm
}

func NewModelManager() *ModelHub {
	return &ModelHub{
		models: map[string]llm{},
	}
}

func (m *ModelHub) Release(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	model, ok := m.models[id]
	if !ok {
		return errors.New("model not found: " + id)
	}

	_ = model.Release()
	delete(m.models, id)
	return nil
}

func (m *ModelHub) Load(id string, path string, backend string) error {
	normalized := normalizeBackend(backend)
	if normalized != "llama_cpp" {
		return errors.New("backend not supported: " + backend)
	}

	loaded, err := llama_cpp.Load(path, llama_cpp.LoadOptions{})
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if model, ok := m.models[id]; ok {
		_ = model.Release()
	}
	m.models[id] = loaded
	return nil
}

func (m *ModelHub) Generate(ctx context.Context, id string, prompt string, options llama_cpp.GenerateOptions) (<-chan llama_cpp.GenerateResult, <-chan error) {
	m.mu.RLock()
	model, ok := m.models[id]
	m.mu.RUnlock()
	if !ok {
		return failedGeneration(errors.New("model not found: " + id))
	}

	return model.Generate(ctx, prompt, options)
}

func failedGeneration(err error) (<-chan llama_cpp.GenerateResult, <-chan error) {
	resultCh := make(chan llama_cpp.GenerateResult)
	errCh := make(chan error, 1)
	close(resultCh)
	errCh <- err
	close(errCh)
	return resultCh, errCh
}

func normalizeBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "llama", "llama.cpp", "llama_cpp":
		return "llama_cpp"
	default:
		return strings.TrimSpace(backend)
	}
}
