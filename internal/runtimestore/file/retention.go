package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type RetentionAuditSink struct {
	path string
	mu   storeMutex
}

const maxRetentionAuditBytes = 64 << 20

func NewRetentionAuditSink(path string) *RetentionAuditSink {
	path = filepath.Clean(strings.TrimSpace(path))
	return &RetentionAuditSink{path: path, mu: storeMutex{shared: &sync.Mutex{}}}
}

func (sink *RetentionAuditSink) RecordRetention(ctx context.Context, record RetentionAuditRecord) error {
	if sink == nil || strings.TrimSpace(sink.path) == "" || sink.path == "." {
		return fmt.Errorf("retention audit path is required")
	}
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	existing, err := os.ReadFile(sink.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(existing) > maxRetentionAuditBytes || len(data) > maxRetentionAuditBytes-len(existing)-1 {
		return fmt.Errorf("retention audit log is too large")
	}
	combined := make([]byte, 0)
	combined = append(combined, existing...)
	combined = append(combined, data...)
	combined = append(combined, '\n')
	return writeRunnerBinaryFile(sink.path, combined)
}

func (sink *RetentionAuditSink) List() ([]RetentionAuditRecord, error) {
	if sink == nil || strings.TrimSpace(sink.path) == "" || sink.path == "." {
		return nil, nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	data, err := os.ReadFile(sink.path)
	if os.IsNotExist(err) {
		return []RetentionAuditRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]RetentionAuditRecord, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record RetentionAuditRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
