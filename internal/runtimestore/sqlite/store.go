package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Store struct {
	db   *sql.DB
	path string
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite runtime store path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "_txlock=immediate"}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(8)
	database.SetMaxIdleConns(8)
	store := &Store{db: database, path: absolute}
	if err := store.initialize(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) initialize(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite runtime store: %w", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("create sqlite runtime schema: %w", err)
	}
	var version int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("sqlite runtime schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version < schemaVersion {
		if _, err := store.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *Store) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

func (store *Store) ExecutionStore() fruntime.ExecutionStore                    { return store }
func (store *Store) CheckpointStore() fruntime.CheckpointStore                  { return store }
func (store *Store) EventSink() fruntime.EventSink                              { return store }
func (store *Store) ArtifactStore() fruntime.ArtifactStore                      { return artifactStore{store: store} }
func (store *Store) TransactionStore() fruntime.TransactionStore                { return store }
func (store *Store) ExecutionDeletionStore() fruntime.RunDeletionExecutionStore { return store }
func (store *Store) CheckpointDeleter() fruntime.RunDeleter {
	return runComponentDeleter{store: store, component: "checkpoints"}
}
func (store *Store) EventDeleter() fruntime.RunDeleter {
	return runComponentDeleter{store: store, component: "events"}
}
func (store *Store) ArtifactDeleter() fruntime.RunDeleter { return artifactStore{store: store} }
func (store *Store) TaskQueue() fruntime.TaskQueue        { return store }

type runComponentDeleter struct {
	store     *Store
	component string
}

func (deleter runComponentDeleter) DeleteRun(ctx context.Context, runID string) error {
	return deleter.store.deleteRunComponent(ctx, runID, deleter.component)
}

func (deleter runComponentDeleter) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	return deleter.store.FenceRunDeletion(ctx, runID, deletionID)
}

func marshal(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode sqlite runtime record: %w", err)
	}
	return data, nil
}

func unmarshal(data []byte, target any) error {
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode sqlite runtime record: %w", err)
	}
	return nil
}

func unixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
