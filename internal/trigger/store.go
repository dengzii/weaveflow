package trigger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNotFound = errors.New("trigger not found")
	ErrExists   = errors.New("trigger already exists")
)

type DefinitionStore interface {
	Create(context.Context, Trigger) error
	Update(context.Context, Trigger) error
	Get(context.Context, string) (Trigger, error)
	List(context.Context) ([]Trigger, error)
	Delete(context.Context, string) error
	ReplaceGraph(context.Context, string, []Trigger) error
}

func (s *FileStore) ReplaceGraph(ctx context.Context, graphID string, items []Trigger) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	graphID = strings.TrimSpace(graphID)
	if graphID == "" {
		return fmt.Errorf("graph_id is required")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	existingGraphPaths := make(map[string][]byte)
	otherIDs := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		item, err := s.getLocked(id)
		if err != nil {
			return err
		}
		path := filepath.Join(s.dir, entry.Name())
		if item.Target.GraphID == graphID {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			existingGraphPaths[path] = data
		} else {
			otherIDs[item.ID] = struct{}{}
		}
	}

	nextPaths := make(map[string]Trigger, len(items))
	for _, item := range items {
		if item.Target.GraphID != graphID {
			return fmt.Errorf("trigger %q targets graph %q instead of %q", item.ID, item.Target.GraphID, graphID)
		}
		id, err := storeID(item.ID)
		if err != nil {
			return err
		}
		if _, exists := otherIDs[id]; exists {
			return ErrExists
		}
		path := filepath.Join(s.dir, id+".json")
		if _, exists := nextPaths[path]; exists {
			return ErrExists
		}
		nextPaths[path] = item
	}

	rollback := func() {
		for path := range nextPaths {
			_ = os.Remove(path)
		}
		for path, data := range existingGraphPaths {
			_ = os.WriteFile(path, data, 0o600)
		}
	}
	for path, item := range nextPaths {
		if err := s.writeLocked(ctx, path, item); err != nil {
			rollback()
			return err
		}
	}
	for path := range existingGraphPaths {
		if _, keep := nextPaths[path]; keep {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			rollback()
			return err
		}
	}
	return nil
}

type InvocationStore interface {
	CreateRecord(context.Context, Record) error
	UpdateRecord(context.Context, Record) error
	ListRecords(context.Context, string, int) ([]Record, error)
}

type ChatHistoryStore interface {
	CreateChatHistory(context.Context, ChatHistory) (ChatHistory, error)
	UpdateChatHistory(context.Context, ChatHistory) error
	ListChatHistory(context.Context, ChatHistoryFilter) ([]ChatHistory, error)
}

type ChatConversationStore interface {
	CreateChatConversation(context.Context, ChatConversation) (ChatConversation, error)
	CurrentChatConversation(context.Context, ChatConversationIdentity) (ChatConversation, error)
}

type Store interface {
	DefinitionStore
	InvocationStore
	ChatHistoryStore
	ChatConversationStore
}

type FileStore struct {
	mu  sync.RWMutex
	dir string
}

func NewFileStore(dir string) (*FileStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("trigger store directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	recordsDir := filepath.Join(dir, "records")
	if err := os.MkdirAll(recordsDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(recordsDir, 0o700); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) Create(ctx context.Context, definition Trigger) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	id, err := storeID(definition.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return err
	}
	path := filepath.Join(s.dir, id+".json")
	if _, err := os.Stat(path); err == nil {
		return ErrExists
	} else if !os.IsNotExist(err) {
		return err
	}
	return s.writeLocked(ctx, path, definition)
}

func (s *FileStore) Update(ctx context.Context, definition Trigger) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	id, err := storeID(definition.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return err
	}
	path := filepath.Join(s.dir, id+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return s.writeLocked(ctx, path, definition)
}

func (s *FileStore) Get(ctx context.Context, id string) (Trigger, error) {
	if s == nil {
		return Trigger{}, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return Trigger{}, err
	}
	id, err := storeID(id)
	if err != nil {
		return Trigger{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := storeContextError(ctx); err != nil {
		return Trigger{}, err
	}
	return s.getLocked(id)
}

func (s *FileStore) getLocked(id string) (Trigger, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if os.IsNotExist(err) {
		return Trigger{}, ErrNotFound
	}
	if err != nil {
		return Trigger{}, err
	}
	var stored Trigger
	if err := decodeStoredJSON(data, &stored); err != nil {
		return Trigger{}, fmt.Errorf("decode trigger %q: %w", id, err)
	}
	if strings.TrimSpace(stored.ID) != id {
		return Trigger{}, fmt.Errorf("decode trigger %q: stored id %q does not match file name", id, stored.ID)
	}
	if err := validateStoredTrigger(stored); err != nil {
		return Trigger{}, fmt.Errorf("decode trigger %q: %w", id, err)
	}
	return stored, nil
}

func (s *FileStore) List(ctx context.Context) ([]Trigger, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	items := make([]Trigger, 0, len(entries))
	for _, entry := range entries {
		if err := storeContextError(ctx); err != nil {
			return nil, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		item, err := s.getLocked(id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *FileStore) Delete(ctx context.Context, id string) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	id, err := storeID(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return err
	}
	err = os.Remove(filepath.Join(s.dir, id+".json"))
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}

func (s *FileStore) CreateRecord(ctx context.Context, record Record) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	if err := validateRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return err
	}
	path := s.recordPath(record.ID)
	if _, err := os.Stat(path); err == nil {
		return ErrExists
	} else if !os.IsNotExist(err) {
		return err
	}
	return s.writeRecordLocked(ctx, path, record)
}

func (s *FileStore) UpdateRecord(ctx context.Context, record Record) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	if err := validateRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return err
	}
	path := s.recordPath(record.ID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return s.writeRecordLocked(ctx, path, record)
}

func (s *FileStore) ListRecords(ctx context.Context, triggerID string, limit int) ([]Record, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return nil, err
	}
	triggerID = strings.TrimSpace(triggerID)
	if triggerID != "" {
		if _, err := storeID(triggerID); err != nil {
			return nil, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.recordsDir())
	if err != nil {
		return nil, err
	}
	items := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if err := storeContextError(ctx); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.recordsDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		var record Record
		if err := decodeStoredJSON(data, &record); err != nil {
			return nil, fmt.Errorf("decode trigger record %q: %w", entry.Name(), err)
		}
		fileID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if record.ID != fileID {
			return nil, fmt.Errorf("decode trigger record %q: stored id %q does not match file name", fileID, record.ID)
		}
		if err := validateRecord(record); err != nil {
			return nil, fmt.Errorf("decode trigger record %q: %w", fileID, err)
		}
		if triggerID == "" || record.TriggerID == triggerID {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TriggeredAt.Equal(items[j].TriggeredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].TriggeredAt.After(items[j].TriggeredAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) recordsDir() string {
	return filepath.Join(s.dir, "records")
}

func (s *FileStore) recordPath(id string) string {
	return filepath.Join(s.recordsDir(), id+".json")
}

func (s *FileStore) writeLocked(ctx context.Context, path string, definition Trigger) error {
	if err := validateStoredTrigger(definition); err != nil {
		return err
	}
	data, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.dir, ".trigger-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (s *FileStore) writeRecordLocked(ctx context.Context, path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.recordsDir(), ".record-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func validateStoredTrigger(definition Trigger) error {
	if err := definition.Validate(); err != nil {
		return err
	}
	if definition.CreatedAt.IsZero() || definition.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid trigger: timestamps are required")
	}
	return nil
}

func decodeStoredJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("stored JSON contains multiple values")
		}
		return err
	}
	return nil
}

func validateRecord(record Record) error {
	if _, err := storeID(record.ID); err != nil {
		return fmt.Errorf("invalid trigger record: %w", err)
	}
	if _, err := storeID(record.TriggerID); err != nil {
		return fmt.Errorf("invalid trigger record: %w", err)
	}
	if record.TriggerType != TypeWebhook && record.TriggerType != TypeSchedule && record.TriggerType != TypeChat {
		return fmt.Errorf("invalid trigger record: trigger type %q is invalid", record.TriggerType)
	}
	if strings.TrimSpace(record.Target.GraphID) == "" {
		return fmt.Errorf("invalid trigger record: graph_id is required")
	}
	if record.Status == "" {
		return fmt.Errorf("invalid trigger record: status is required")
	}
	if record.TriggeredAt.IsZero() || record.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid trigger record: timestamps are required")
	}
	return nil
}

func storeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if err := validateTriggerID(id); err != nil {
		return "", err
	}
	return id, nil
}

func storeContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
