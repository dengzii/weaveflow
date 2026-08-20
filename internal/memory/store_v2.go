package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const AnyVersion = "*"

var (
	ErrRecordNotFound    = errors.New("memory record not found")
	ErrVersionConflict   = errors.New("memory record version conflict")
	ErrInvalidRecord     = errors.New("memory record is invalid")
	ErrInvalidMemoryPage = errors.New("memory page cursor is invalid")
)

type Namespace string

type MemoryRecord struct {
	Namespace          Namespace         `json:"namespace"`
	Key                string            `json:"key"`
	Version            string            `json:"version"`
	Value              any               `json:"value,omitempty"`
	Content            string            `json:"content,omitempty"`
	Text               string            `json:"text,omitempty"`
	ContentMetadata    map[string]string `json:"content_metadata,omitempty"`
	SourceRunID        string            `json:"source_run_id,omitempty"`
	SourceGraphSession string            `json:"source_graph_session_id,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	RetainUntil        *time.Time        `json:"retain_until,omitempty"`
	SourceRetention    bool              `json:"source_retention,omitempty"`
	DeletedAt          *time.Time        `json:"deleted_at,omitempty"`
}

type MemoryQuery struct {
	Text           string
	Limit          int
	Cursor         string
	IncludeExpired bool
}

type MemoryListOptions struct {
	Limit          int
	Cursor         string
	IncludeExpired bool
}

type MemoryPage struct {
	Records    []MemoryRecord `json:"records"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type Store interface {
	Get(context.Context, Namespace, string) (MemoryRecord, error)
	Put(context.Context, Namespace, MemoryRecord, string) (MemoryRecord, error)
	Delete(context.Context, Namespace, string, string) error
	Search(context.Context, Namespace, MemoryQuery) (MemoryPage, error)
	List(context.Context, Namespace, MemoryListOptions) (MemoryPage, error)
	PurgeExpired(context.Context, time.Time, int) (int, error)
}

type VersionedStore struct {
	mu       sync.RWMutex
	records  map[string]MemoryRecord
	filePath string
	now      func() time.Time
}

func NewInMemoryStore() Store {
	return &VersionedStore{records: make(map[string]MemoryRecord), now: time.Now}
}

func NewFileStore(path string) (Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("memory store path is required")
	}
	store := &VersionedStore{records: make(map[string]MemoryRecord), filePath: path, now: time.Now}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *VersionedStore) Get(ctx context.Context, namespace Namespace, key string) (MemoryRecord, error) {
	if err := checkContext(ctx); err != nil {
		return MemoryRecord{}, err
	}
	namespace, key, err := normalizeIdentity(namespace, key)
	if err != nil {
		return MemoryRecord{}, err
	}
	store.mu.RLock()
	record, ok := store.records[recordKey(namespace, key)]
	store.mu.RUnlock()
	if !ok || record.DeletedAt != nil {
		return MemoryRecord{}, ErrRecordNotFound
	}
	return cloneRecord(record), nil
}

func (store *VersionedStore) Put(ctx context.Context, namespace Namespace, incoming MemoryRecord, expectedVersion string) (MemoryRecord, error) {
	if err := checkContext(ctx); err != nil {
		return MemoryRecord{}, err
	}
	namespace, key, err := normalizeIdentity(namespace, incoming.Key)
	if err != nil {
		return MemoryRecord{}, err
	}
	if strings.TrimSpace(incoming.Content) == "" {
		incoming.Content = strings.TrimSpace(incoming.Text)
	}
	now := store.currentTime()
	store.mu.Lock()
	defer store.mu.Unlock()
	identity := recordKey(namespace, key)
	current, exists := store.records[identity]
	if exists && !versionMatches(current, expectedVersion) {
		return MemoryRecord{}, ErrVersionConflict
	}
	if !exists && expectedVersion != "" && expectedVersion != AnyVersion {
		return MemoryRecord{}, ErrVersionConflict
	}
	if exists && current.DeletedAt == nil && expectedVersion == "" {
		return MemoryRecord{}, ErrVersionConflict
	}
	version := nextVersion(current, exists)
	createdAt := now
	if exists && !current.CreatedAt.IsZero() {
		createdAt = current.CreatedAt
	}
	record := normalizeRecord(incoming)
	record.Namespace = namespace
	record.Key = key
	record.Version = version
	record.CreatedAt = createdAt
	record.UpdatedAt = now
	record.DeletedAt = nil
	store.records[identity] = record
	if err := store.persistLocked(); err != nil {
		if exists {
			store.records[identity] = current
		} else {
			delete(store.records, identity)
		}
		return MemoryRecord{}, err
	}
	return cloneRecord(record), nil
}

func (store *VersionedStore) Delete(ctx context.Context, namespace Namespace, key, expectedVersion string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	namespace, key, err := normalizeIdentity(namespace, key)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	identity := recordKey(namespace, key)
	current, ok := store.records[identity]
	if !ok || current.DeletedAt != nil {
		return ErrRecordNotFound
	}
	if !versionMatches(current, expectedVersion) {
		return ErrVersionConflict
	}
	previous := current
	now := store.currentTime()
	current.Version = nextVersion(current, true)
	current.UpdatedAt = now
	current.DeletedAt = &now
	store.records[identity] = current
	if err := store.persistLocked(); err != nil {
		store.records[identity] = previous
		return err
	}
	return nil
}

func (store *VersionedStore) Search(ctx context.Context, namespace Namespace, query MemoryQuery) (MemoryPage, error) {
	if err := checkContext(ctx); err != nil {
		return MemoryPage{}, err
	}
	namespace = Namespace(strings.TrimSpace(string(namespace)))
	if namespace == "" {
		return MemoryPage{}, fmt.Errorf("%w: namespace is required", ErrInvalidRecord)
	}
	store.mu.RLock()
	items := store.visibleLocked(namespace, query.IncludeExpired)
	store.mu.RUnlock()
	terms := tokenizeMemoryText(query.Text)
	type scoredRecord struct {
		record MemoryRecord
		score  int
	}
	scored := make([]scoredRecord, 0, len(items))
	for _, record := range items {
		score := scoreMemoryRecord(record, terms)
		if len(terms) > 0 && score == 0 {
			continue
		}
		scored = append(scored, scoredRecord{record: record, score: score})
	}
	sort.SliceStable(scored, func(left, right int) bool {
		if scored[left].score != scored[right].score {
			return scored[left].score > scored[right].score
		}
		if !scored[left].record.UpdatedAt.Equal(scored[right].record.UpdatedAt) {
			return scored[left].record.UpdatedAt.After(scored[right].record.UpdatedAt)
		}
		return scored[left].record.Key < scored[right].record.Key
	})
	records := make([]MemoryRecord, len(scored))
	for index, item := range scored {
		records[index] = item.record
	}
	return pageRecords(records, query.Limit, query.Cursor)
}

func (store *VersionedStore) List(ctx context.Context, namespace Namespace, options MemoryListOptions) (MemoryPage, error) {
	if err := checkContext(ctx); err != nil {
		return MemoryPage{}, err
	}
	namespace = Namespace(strings.TrimSpace(string(namespace)))
	if namespace == "" {
		return MemoryPage{}, fmt.Errorf("%w: namespace is required", ErrInvalidRecord)
	}
	store.mu.RLock()
	records := store.visibleLocked(namespace, options.IncludeExpired)
	store.mu.RUnlock()
	sort.SliceStable(records, func(left, right int) bool {
		if !records[left].UpdatedAt.Equal(records[right].UpdatedAt) {
			return records[left].UpdatedAt.Before(records[right].UpdatedAt)
		}
		return records[left].Key < records[right].Key
	})
	return pageRecords(records, options.Limit, options.Cursor)
}

func (store *VersionedStore) PurgeExpired(ctx context.Context, now time.Time, limit int) (int, error) {
	if err := checkContext(ctx); err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = store.currentTime()
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	keys := make([]string, 0)
	for identity, record := range store.records {
		if record.DeletedAt != nil || record.RetainUntil == nil || record.RetainUntil.After(now) {
			continue
		}
		keys = append(keys, identity)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	previous := make(map[string]MemoryRecord, len(keys))
	for _, identity := range keys {
		record := store.records[identity]
		previous[identity] = record
		deletedAt := now
		record.Version = nextVersion(record, true)
		record.UpdatedAt = now
		record.DeletedAt = &deletedAt
		store.records[identity] = record
	}
	if len(keys) > 0 {
		if err := store.persistLocked(); err != nil {
			for identity, record := range previous {
				store.records[identity] = record
			}
			return 0, err
		}
	}
	return len(keys), nil
}

func WithStore(ctx context.Context, store Store) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, storeContextKey{}, store)
}

func StoreFromContext(ctx context.Context) (Store, bool) {
	if ctx == nil {
		return nil, false
	}
	store, ok := ctx.Value(storeContextKey{}).(Store)
	return store, ok && store != nil
}

type storeContextKey struct{}

func (store *VersionedStore) visibleLocked(namespace Namespace, includeExpired bool) []MemoryRecord {
	now := store.currentTime()
	items := make([]MemoryRecord, 0)
	for _, record := range store.records {
		if record.Namespace != namespace || record.DeletedAt != nil {
			continue
		}
		if !includeExpired && record.RetainUntil != nil && !record.RetainUntil.After(now) {
			continue
		}
		items = append(items, cloneRecord(record))
	}
	return items
}

func (store *VersionedStore) currentTime() time.Time {
	if store == nil || store.now == nil {
		return time.Now().UTC()
	}
	return store.now().UTC()
}

func (store *VersionedStore) load() error {
	data, err := os.ReadFile(store.filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var records []MemoryRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("decode memory store: %w", err)
	}
	for _, record := range records {
		normalized := normalizeRecord(record)
		if _, _, err := normalizeIdentity(normalized.Namespace, normalized.Key); err != nil {
			return err
		}
		if normalized.Version == "" {
			return fmt.Errorf("%w: record %q has no version", ErrInvalidRecord, normalized.Key)
		}
		store.records[recordKey(normalized.Namespace, normalized.Key)] = normalized
	}
	return nil
}

func (store *VersionedStore) persistLocked() error {
	if strings.TrimSpace(store.filePath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(store.filePath), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(store.records))
	for key := range store.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]MemoryRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, cloneRecord(store.records[key]))
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(store.filePath), filepath.Base(store.filePath)+".tmp-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, store.filePath); err != nil {
		if removeErr := os.Remove(store.filePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(temporaryName, store.filePath); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func normalizeRecord(record MemoryRecord) MemoryRecord {
	record.Namespace = Namespace(strings.TrimSpace(string(record.Namespace)))
	record.Key = strings.TrimSpace(record.Key)
	record.Content = strings.TrimSpace(record.Content)
	record.Text = strings.TrimSpace(record.Text)
	record.ContentMetadata = cloneStringMapV2(record.ContentMetadata)
	if record.CreatedAt.IsZero() {
		record.CreatedAt = record.UpdatedAt
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	return record
}

func normalizeIdentity(namespace Namespace, key string) (Namespace, string, error) {
	namespace = Namespace(strings.TrimSpace(string(namespace)))
	key = strings.TrimSpace(key)
	if namespace == "" || key == "" {
		return "", "", fmt.Errorf("%w: namespace and key are required", ErrInvalidRecord)
	}
	if strings.Contains(namespace.String(), "\\") || strings.Contains(key, "\\") {
		return "", "", fmt.Errorf("%w: namespace and key cannot contain path separators", ErrInvalidRecord)
	}
	return namespace, key, nil
}

func (namespace Namespace) String() string {
	return strings.TrimSpace(string(namespace))
}

func recordKey(namespace Namespace, key string) string {
	return namespace.String() + "\x00" + key
}

func versionMatches(record MemoryRecord, expected string) bool {
	return expected == AnyVersion || strings.TrimSpace(expected) == record.Version
}

func nextVersion(record MemoryRecord, exists bool) string {
	if !exists {
		return "1"
	}
	version, err := strconv.ParseUint(record.Version, 10, 64)
	if err != nil {
		return record.Version + ".next"
	}
	return strconv.FormatUint(version+1, 10)
}

func pageRecords(records []MemoryRecord, limit int, cursor string) (MemoryPage, error) {
	start := 0
	if strings.TrimSpace(cursor) != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 || parsed > len(records) {
			return MemoryPage{}, ErrInvalidMemoryPage
		}
		start = parsed
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	end := start + limit
	if end > len(records) {
		end = len(records)
	}
	page := MemoryPage{Records: make([]MemoryRecord, end-start)}
	for index := start; index < end; index++ {
		page.Records[index-start] = cloneRecord(records[index])
	}
	if end < len(records) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func scoreMemoryRecord(record MemoryRecord, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	content := strings.ToLower(record.Content + " " + record.Text)
	score := 0
	for _, term := range terms {
		if strings.Contains(content, term) {
			score++
		}
	}
	return score
}

func tokenizeMemoryText(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	var tokens []string
	var buffer []rune
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		tokens = append(tokens, string(buffer))
		buffer = buffer[:0]
	}
	for _, character := range text {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			buffer = append(buffer, character)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func cloneRecord(record MemoryRecord) MemoryRecord {
	cloned := record
	if record.Value != nil {
		if data, err := json.Marshal(record.Value); err == nil {
			_ = json.Unmarshal(data, &cloned.Value)
		}
	}
	cloned.ContentMetadata = cloneStringMapV2(record.ContentMetadata)
	if record.RetainUntil != nil {
		value := *record.RetainUntil
		cloned.RetainUntil = &value
	}
	if record.DeletedAt != nil {
		value := *record.DeletedAt
		cloned.DeletedAt = &value
	}
	return cloned
}

func cloneStringMapV2(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
