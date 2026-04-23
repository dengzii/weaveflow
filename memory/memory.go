package memory

import (
	"strings"
	"time"
)

type EntryType string

const (
	EntryTypeMessage EntryType = "message"
	EntryTypeSummary EntryType = "summary"
	EntryTypeFact    EntryType = "fact"
	EntryTypeTool    EntryType = "tool"
)

type Entry struct {
	ID        string         `json:"id"`
	Text      string         `json:"text"`
	Role      string         `json:"role"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
	Type      EntryType      `json:"type"`
	Tags      []string       `json:"tags"`
}

type Query struct {
	Text  string
	Roles []string
	Tags  []string
	Types []EntryType
	Since time.Time
	Until time.Time
	Limit int
}

type LoadOptions struct {
	Limit int
	Roles []string
	Tags  []string
	Types []EntryType
	Since time.Time
	Until time.Time
}

type Repository interface {
	Store(memory []Entry) error
	Load(options *LoadOptions) ([]Entry, error)
	Delete() error
}

type Retriever interface {
	Recall(query *Query) ([]Entry, error)
}

type Manager interface {
	Store(memory []Entry) error
	Append(memory ...Entry) error
	Load(options *LoadOptions) ([]Entry, error)
	Recall(query *Query) ([]Entry, error)
	Delete() error
}

type Options struct {
	Repository Repository
	Retriever  Retriever
}

func New(options *Options) Manager {
	normalized := normalizeOptions(options)
	return &memoryManager{
		options: normalized,
		repo:    normalized.Repository,
	}
}

func normalizeOptions(options *Options) *Options {
	if options == nil {
		return &Options{
			Repository: NewInMemoryRepository(),
		}
	}

	normalized := *options
	if normalized.Repository == nil {
		normalized.Repository = NewInMemoryRepository()
	}

	return &normalized
}

func normalizeEntry(entry Entry) Entry {
	normalized := entry
	normalized.Role = strings.TrimSpace(normalized.Role)
	if normalized.Role == "" {
		normalized.Role = "user"
	}
	normalized.Text = strings.TrimSpace(normalized.Text)
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = time.Now()
	}
	if normalized.Type == "" {
		normalized.Type = EntryTypeMessage
	}
	normalized.Tags = cloneStringSlice(normalized.Tags)
	normalized.Payload = cloneStringMap(normalized.Payload)
	return normalized
}

func cloneStringMap(items map[string]any) map[string]any {
	if len(items) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(items))
	for key, value := range items {
		cloned[key] = value
	}
	return cloned
}
