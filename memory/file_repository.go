package memory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const fileRepositoryDateLayout = "2006-01-02"
const fileRepositoryStorageDir = "memory"

type fileRepository struct {
	dir       string
	retriever Retriever
}

type fileMemoryRecord struct {
	Sequence  int            `json:"sequence"`
	ID        string         `json:"id"`
	Text      string         `json:"text"`
	Role      string         `json:"role"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	Type      EntryType      `json:"type"`
	Tags      []string       `json:"tags,omitempty"`
}

type sortableFileEntry struct {
	sequence int
	entry    Entry
}

func NewFileMemoryRepository(path string) Repository {
	return &fileRepository{
		dir: strings.TrimSpace(path),
	}
}

func (f *fileRepository) Store(memory []Entry) (err error) {
	if f == nil || strings.TrimSpace(f.dir) == "" {
		return nil
	}

	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp(f.dir, fileRepositoryStorageDir+"-tmp-")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	grouped := make(map[string][]fileMemoryRecord)
	for index, item := range memory {
		entry := normalizeEntry(item)
		fileName := memoryTime(entry).Format(fileRepositoryDateLayout) + ".json"
		grouped[fileName] = append(grouped[fileName], fileMemoryRecord{
			Sequence:  index,
			ID:        entry.ID,
			Text:      entry.Text,
			Role:      entry.Role,
			Payload:   entry.Payload,
			CreatedAt: entry.CreatedAt,
			Type:      entry.Type,
			Tags:      cloneStringSlice(entry.Tags),
		})
	}

	for fileName, records := range grouped {
		targetPath := filepath.Join(tempDir, fileName)
		if err := writeFileMemoryRecords(targetPath, records); err != nil {
			return err
		}
	}

	targetDir := f.storageDir()
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if len(memory) == 0 {
		return nil
	}

	if err := os.Rename(tempDir, targetDir); err != nil {
		return err
	}

	return nil
}

func (f *fileRepository) Load(options *LoadOptions) ([]Entry, error) {
	if f == nil || strings.TrimSpace(f.dir) == "" {
		return []Entry{}, nil
	}

	memoryDir := f.storageDir()
	files, err := os.ReadDir(memoryDir)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	items := make([]sortableFileEntry, 0)
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}

		records, err := readFileMemoryRecords(filepath.Join(memoryDir, file.Name()))
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			items = append(items, sortableFileEntry{
				sequence: record.Sequence,
				entry: Entry{
					ID:        record.ID,
					Text:      record.Text,
					Role:      strings.TrimSpace(record.Role),
					Payload:   record.Payload,
					CreatedAt: record.CreatedAt,
					Type:      record.Type,
					Tags:      cloneStringSlice(record.Tags),
				},
			})
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		leftTime := memoryTime(items[i].entry)
		rightTime := memoryTime(items[j].entry)
		if !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		return items[i].sequence < items[j].sequence
	})

	result := make([]Entry, 0, len(items))
	for _, item := range items {
		result = append(result, item.entry)
	}

	return filterEntries(result, options), nil
}

func (f *fileRepository) Delete() error {
	if f == nil || strings.TrimSpace(f.dir) == "" {
		return nil
	}

	if err := os.RemoveAll(f.storageDir()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (f *fileRepository) storageDir() string {
	return filepath.Join(f.dir, fileRepositoryStorageDir)
}

func writeFileMemoryRecords(path string, records []fileMemoryRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func readFileMemoryRecords(path string) ([]fileMemoryRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return []fileMemoryRecord{}, nil
	}

	var records []fileMemoryRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func cloneStringSlice(items []string) []string {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]string, len(items))
	copy(cloned, items)
	return cloned
}
