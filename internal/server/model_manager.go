package server

import (
	"context"
	"errors"
	"sync"

	"falcon/llama_cpp"

	"github.com/tmc/langchaingo/llms"
)

type llm interface {
	llms.Model
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

func (m *ModelHub) ResolveModel(id string) (llms.Model, error) {
	m.mu.RLock()
	model, ok := m.models[id]
	m.mu.RUnlock()
	if !ok {
		return nil, errors.New("model not found: " + id)
	}
	return model, nil
}

func (m *ModelHub) ResolveDefaultModel() (string, llms.Model, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.models) == 0 {
		return "", nil, errors.New("no model loaded")
	}
	if len(m.models) > 1 {
		return "", nil, errors.New("multiple models loaded, model_id is required")
	}
	for id, model := range m.models {
		return id, model, nil
	}
	return "", nil, errors.New("no model loaded")
}

func failedGeneration(err error) (<-chan llama_cpp.GenerateResult, <-chan error) {
	resultCh := make(chan llama_cpp.GenerateResult)
	errCh := make(chan error, 1)
	close(resultCh)
	errCh <- err
	close(errCh)
	return resultCh, errCh
}
