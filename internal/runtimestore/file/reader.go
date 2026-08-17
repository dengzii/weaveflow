package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Reader struct {
	execution   *executionStore
	checkpoints *checkpointStore
	events      *eventSink
	artifacts   *artifactStore
}

func OpenReader(baseDir string) (*Reader, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("runtime store base directory is required")
	}
	absolute, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !info.IsDir() {
		return nil, fmt.Errorf("runtime store base path is not a directory: %s", absolute)
	}
	shared := &sync.Mutex{}
	return &Reader{
		execution:   newExecutionStore(filepath.Join(absolute, "execution"), shared),
		checkpoints: newCheckpointStore(filepath.Join(absolute, "checkpoints"), shared),
		events:      newEventSink(filepath.Join(absolute, "events"), shared),
		artifacts:   newArtifactStore(filepath.Join(absolute, "artifacts"), shared),
	}, nil
}

func (reader *Reader) ExecutionReader() ExecutionReader {
	return reader.execution
}

func (reader *Reader) CheckpointReader() CheckpointReader {
	return reader.checkpoints
}

func (reader *Reader) EventReader() EventReader {
	return reader.events
}

func (reader *Reader) EventPageReader() EventPageReader {
	return reader.events
}

func (reader *Reader) ArtifactReader() ArtifactReader {
	return reader.artifacts
}
