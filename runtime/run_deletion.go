package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type RunDeleter interface {
	DeleteRun(context.Context, string) error
}

type RunDeletionCoordinator struct {
	executionStore  RunDeleter
	checkpointStore RunDeleter
	eventStore      RunDeleter
	artifactStore   RunDeleter
}

func NewRunDeletionCoordinator(
	executionStore RunDeleter,
	checkpointStore RunDeleter,
	eventStore RunDeleter,
	artifactStore RunDeleter,
) *RunDeletionCoordinator {
	return &RunDeletionCoordinator{
		executionStore:  executionStore,
		checkpointStore: checkpointStore,
		eventStore:      eventStore,
		artifactStore:   artifactStore,
	}
}

func (coordinator *RunDeletionCoordinator) DeleteRun(ctx context.Context, runID string) error {
	if coordinator == nil || coordinator.executionStore == nil {
		return fmt.Errorf("run deletion execution store is required")
	}
	ctx = normalizeRunnerContext(ctx)
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ErrRunnerRecordNotFound
	}
	stores := []struct {
		name  string
		store RunDeleter
	}{
		{name: "checkpoints", store: coordinator.checkpointStore},
		{name: "artifacts", store: coordinator.artifactStore},
		{name: "events", store: coordinator.eventStore},
		{name: "execution", store: coordinator.executionStore},
	}
	for _, target := range stores {
		if target.store == nil {
			continue
		}
		if err := target.store.DeleteRun(ctx, runID); err != nil && !errors.Is(err, ErrRunnerRecordNotFound) {
			return fmt.Errorf("delete run %q from %s store: %w", runID, target.name, err)
		}
	}
	return nil
}
