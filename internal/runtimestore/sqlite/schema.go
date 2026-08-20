package sqlite

const schemaSQL = `
CREATE TABLE IF NOT EXISTS runs (
    run_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL,
    status TEXT NOT NULL,
    parent_run_id TEXT NOT NULL DEFAULT '',
    parent_task_id TEXT NOT NULL DEFAULT '',
    root_run_id TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    started_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    data BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_status_started ON runs(status, started_at_ns, run_id);
CREATE INDEX IF NOT EXISTS runs_parent ON runs(parent_run_id, parent_task_id);
CREATE INDEX IF NOT EXISTS runs_root ON runs(root_run_id, namespace);

CREATE TABLE IF NOT EXISTS steps (
    step_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    task_id TEXT NOT NULL DEFAULT '',
    started_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    data BLOB NOT NULL,
    FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS steps_run_started ON steps(run_id, started_at_ns, step_id);

CREATE TABLE IF NOT EXISTS checkpoints (
    checkpoint_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    created_at_ns INTEGER NOT NULL,
    metadata BLOB NOT NULL,
    payload BLOB NOT NULL,
    FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS checkpoints_run_created ON checkpoints(run_id, created_at_ns, checkpoint_id);

CREATE TABLE IF NOT EXISTS events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    run_id TEXT NOT NULL,
    timestamp_ns INTEGER NOT NULL,
    data BLOB NOT NULL,
    FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS events_run_sequence ON events(run_id, sequence);

CREATE TABLE IF NOT EXISTS artifact_stages (
    transaction_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    created_at_ns INTEGER NOT NULL,
    metadata BLOB NOT NULL,
    payload BLOB NOT NULL,
    PRIMARY KEY(transaction_id, artifact_id),
    FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS artifacts (
    run_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    transaction_id TEXT NOT NULL,
    created_at_ns INTEGER NOT NULL,
    metadata BLOB NOT NULL,
    payload BLOB NOT NULL,
    PRIMARY KEY(run_id, artifact_id),
    FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS artifacts_run_created ON artifacts(run_id, created_at_ns, artifact_id);

CREATE TABLE IF NOT EXISTS runtime_transactions (
    transaction_id TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    result BLOB NOT NULL,
    committed_at_ns INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS run_deletion_fences (
    run_id TEXT PRIMARY KEY,
    deletion_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS run_deletion_manifests (
    deletion_id TEXT PRIMARY KEY,
    phase TEXT NOT NULL,
    data BLOB NOT NULL,
    updated_at_ns INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    task_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    version INTEGER NOT NULL,
    available_at_ns INTEGER NOT NULL,
    lease_expires_at_ns INTEGER NOT NULL DEFAULT 0,
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    data BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_claim ON tasks(status, available_at_ns, lease_expires_at_ns, kind, created_at_ns);
CREATE INDEX IF NOT EXISTS tasks_run ON tasks(run_id, created_at_ns);

CREATE TABLE IF NOT EXISTS attempts (
    attempt_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    number INTEGER NOT NULL,
    status TEXT NOT NULL,
    started_at_ns INTEGER NOT NULL,
    data BLOB NOT NULL,
    UNIQUE(task_id, number),
    FOREIGN KEY(task_id) REFERENCES tasks(task_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS attempts_task_number ON attempts(task_id, number);

CREATE TABLE IF NOT EXISTS workers (
    worker_id TEXT PRIMARY KEY,
    heartbeat_at_ns INTEGER NOT NULL,
    data BLOB NOT NULL
);
`
