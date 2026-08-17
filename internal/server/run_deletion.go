package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
	"github.com/dengzii/weaveflow/runtime"
)

func (s *Server) reconcileCachedRunDeletions(ctx context.Context) error {
	if s == nil || s.baseDir == "" {
		return nil
	}
	graphsDir := filepath.Join(s.baseDir, "graphs")
	entries, err := os.ReadDir(graphsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		historyDir := filepath.Join(graphsDir, entry.Name(), "history")
		if _, err := os.Stat(historyDir); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		store, err := filestore.Open(historyDir)
		if errors.Is(err, filestore.ErrWriterLocked) {
			continue
		}
		if err != nil {
			return err
		}
		coordinator := runtime.NewRunDeletionCoordinator(
			store.ExecutionDeletionStore(), store.CheckpointDeleter(), store.EventDeleter(), store.ArtifactDeleter(),
		)
		reconcileErr := coordinator.ReconcileRunDeletions(ctx)
		closeErr := store.Close()
		if reconcileErr != nil {
			return reconcileErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
