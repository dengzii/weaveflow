package memory

import (
	"sync"
)

type inMemoryRepository struct {
	mu    sync.RWMutex
	items []Entry
}

func NewInMemoryRepository() Repository {
	return &inMemoryRepository{}
}

func (r *inMemoryRepository) Store(memory []Entry) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.items = cloneEntries(memory)
	return nil
}

func (r *inMemoryRepository) Load(options *LoadOptions) ([]Entry, error) {
	if r == nil {
		return []Entry{}, nil
	}

	r.mu.RLock()
	items := cloneEntries(r.items)
	r.mu.RUnlock()

	return filterEntries(items, options), nil
}

func (r *inMemoryRepository) Delete() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.items = nil
	return nil
}
