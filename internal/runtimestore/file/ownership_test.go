package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRejectsSecondWriterUntilClose(t *testing.T) {
	directory := t.TempDir()
	first, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	if second, err := Open(filepath.Join(directory, ".")); !errors.Is(err, ErrWriterLocked) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("Open(second) error = %v, want ErrWriterLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(after close) error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close(reopened) error = %v", err)
	}
}

func TestClosedWriterCannotWriteAfterOwnershipTransfer(t *testing.T) {
	directory := t.TempDir()
	first, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	second, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	defer func() { _ = second.Close() }()

	if err := first.CreateRun(context.Background(), RunRecord{RunID: "stale-writer"}); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("closed writer CreateRun() error = %v, want ErrWriterClosed", err)
	}
	if err := second.CreateRun(context.Background(), RunRecord{RunID: "active-writer"}); err != nil {
		t.Fatalf("active writer CreateRun() error = %v", err)
	}
}

func TestReaderCanOpenAlongsideWriter(t *testing.T) {
	directory := t.TempDir()
	writer, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(writer) error = %v", err)
	}
	defer func() { _ = writer.Close() }()

	reader, err := OpenReader(directory)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	if reader.ExecutionReader() == nil || reader.CheckpointReader() == nil || reader.EventReader() == nil || reader.ArtifactReader() == nil {
		t.Fatal("OpenReader() returned incomplete readers")
	}
}

func TestOpenReaderDoesNotCreateMissingDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	if _, err := OpenReader(directory); err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("OpenReader() created directory: %v", err)
	}
}
