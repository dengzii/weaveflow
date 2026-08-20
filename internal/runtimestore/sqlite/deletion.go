package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

func (store *Store) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return err
	}
	if err := fruntime.ValidateStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	if err := fruntime.RequireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	var existing string
	err := store.db.QueryRowContext(ctx, `SELECT deletion_id FROM run_deletion_fences WHERE run_id=?`, runID).Scan(&existing)
	if err == nil {
		if existing != deletionID {
			return fmt.Errorf("run %q is fenced by deletion %q, not %q", runID, existing, deletionID)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO run_deletion_fences(run_id,deletion_id) VALUES(?,?)`, runID, deletionID)
	return err
}

func (store *Store) DeleteRun(ctx context.Context, runID string) error {
	return store.deleteRunComponent(ctx, runID, "execution")
}

func (store *Store) deleteRunComponent(ctx context.Context, runID, component string) error {
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return err
	}
	var deletionID string
	if err := store.db.QueryRowContext(ctx, `SELECT deletion_id FROM run_deletion_fences WHERE run_id=?`, runID).Scan(&deletionID); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: run %q has no deletion fence", fruntime.ErrRunControlNotAllowed, runID)
	} else if err != nil {
		return err
	}
	if err := fruntime.RequireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	var statement string
	switch component {
	case "checkpoints":
		statement = `DELETE FROM checkpoints WHERE run_id=?`
	case "events":
		statement = `DELETE FROM events WHERE run_id=?`
	case "artifacts":
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `DELETE FROM artifact_stages WHERE run_id=?`, runID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM artifacts WHERE run_id=?`, runID); err != nil {
			return err
		}
		return tx.Commit()
	case "execution":
		statement = `DELETE FROM runs WHERE run_id=?`
	default:
		return fmt.Errorf("unsupported sqlite runtime deletion component %q", component)
	}
	_, err := store.db.ExecContext(ctx, statement, runID)
	return err
}

func (store *Store) LoadRunDeletionManifest(ctx context.Context, deletionID string) (fruntime.RunDeletionManifest, error) {
	if err := fruntime.ValidateStorageID("deletion ID", deletionID); err != nil {
		return fruntime.RunDeletionManifest{}, err
	}
	var data []byte
	if err := store.db.QueryRowContext(ctx, `SELECT data FROM run_deletion_manifests WHERE deletion_id=?`, deletionID).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return fruntime.RunDeletionManifest{}, fruntime.ErrRunnerRecordNotFound
	} else if err != nil {
		return fruntime.RunDeletionManifest{}, err
	}
	var manifest fruntime.RunDeletionManifest
	if err := unmarshal(data, &manifest); err != nil {
		return fruntime.RunDeletionManifest{}, err
	}
	return manifest, nil
}

func (store *Store) ListRunDeletionManifests(ctx context.Context) ([]fruntime.RunDeletionManifest, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT data FROM run_deletion_manifests ORDER BY updated_at_ns,deletion_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var manifests []fruntime.RunDeletionManifest
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var manifest fruntime.RunDeletionManifest
		if err := unmarshal(data, &manifest); err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, rows.Err()
}

func (store *Store) SaveRunDeletionManifest(ctx context.Context, manifest fruntime.RunDeletionManifest) error {
	if err := fruntime.ValidateRunDeletionManifest(manifest); err != nil {
		return err
	}
	data, err := marshal(manifest)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO run_deletion_manifests(deletion_id,phase,data,updated_at_ns) VALUES(?,?,?,?) ON CONFLICT(deletion_id) DO UPDATE SET phase=excluded.phase,data=excluded.data,updated_at_ns=excluded.updated_at_ns`, manifest.ID, manifest.Phase, data, unixNano(manifest.UpdatedAt))
	return err
}

func (store *Store) ValidateRunDeletionFences(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `SELECT run_id,deletion_id FROM run_deletion_fences ORDER BY run_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, deletionID string
		if err := rows.Scan(&runID, &deletionID); err != nil {
			return err
		}
		if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
			return err
		}
		if err := fruntime.ValidateStorageID("deletion ID", deletionID); err != nil {
			return err
		}
		var count int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_deletion_manifests WHERE deletion_id=?`, deletionID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := store.GetRun(ctx, runID); err != nil {
				return fmt.Errorf("orphan deletion fence for run %q and deletion %q", runID, deletionID)
			}
		}
	}
	return rows.Err()
}
