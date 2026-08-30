# Debug API

Use Graph-scoped routes with the management Bearer token. Successful data is wrapped in `data`; failures include `error.code` and `error.message`. Never log Authorization headers or large state bodies.

## Run Discovery And Inspection

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/graphs/:graph_id/runs` | List Runs; page the cursor and optionally filter by `status`, `parent_run_id`, `parent_task_id`, `root_run_id`, or `namespace`. |
| `GET` | `/graphs/:graph_id/runs/:run_id/inspection` | Read the primary Run, Step, Checkpoint, Event, and Interrupt view. |
| `GET` | `/graphs/:graph_id/runs/:run_id/events` | Read persisted Event pages with cursor and limit. |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts` | List Run Artifacts. |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts/:artifact_id` | Read one referenced Artifact. |
| `GET` | `/graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id` | Read one referenced Checkpoint. |
| `GET` | `/graphs/:graph_id/runs/:run_id/compare/:other_run_id` | Compare two existing Runs on the same Graph snapshot, including Steps, Events, Artifacts, and last-Checkpoint state changes. |
| `GET` | `/graphs/:graph_id/retention-audit` | Read persisted Run-retention deletion intents for this Graph; an intent alone does not prove deletion completed. |
| `GET` | `/graphs/:graph_id/sessions/:session_id` | Read the Run's exact immutable definition, settings, required state, hashes, and registry-drift warnings. |
| `GET` | `/graphs/:graph_id` | Read the latest Session only, for drift comparison. |

## Run Control

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/graphs/:graph_id/runs/:run_id/resume` | Resume the stored Session with an `input` state patch. |
| `POST` | `/graphs/:graph_id/runs/:run_id/forks` | Create and asynchronously execute an independent Run from a safe non-final Checkpoint. |
| `POST` | `/graphs/:graph_id/runs/:run_id/pause` | Request a safe-point pause. |
| `POST` | `/graphs/:graph_id/runs/:run_id/cancel` | Request cancellation. |
| `POST` | `/graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution` | Resolve an unresolved side effect. |
| `DELETE` | `/graphs/:graph_id/runs/:run_id` | Delete Run diagnostics; use only when explicitly requested. |

Run start is asynchronous: `POST /graphs/:graph_id/sessions/:session_id/runs` returns `202` with a `RunRecord` and accepts `initial_state`. Trigger-started Runs include an origin; direct starts do not.

Fork accepts `checkpoint_id`, stable `request_key`, and optional external `input`. It returns `202`; repeating the same source/checkpoint/request key is idempotent, while a conflicting identity is rejected. Fork is not allowed from a final or non-independent Checkpoint or while source effects are unresolved. Compare is read-only but rejects Runs from different Graph snapshots.

If exact Session detail returns `context_warnings`, keep the persisted definition, settings, Session ID, and manifest hashes as historical context. Do not reinterpret missing derived requirements through the current registry or guess how the old Session behaved.

## Live Events

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/graphs/:graph_id/events/stream` | Observe bounded Graph events with optional `session_id`, `run_id`, `node_id`, `type`, `cursor`, or `Last-Event-ID` filters. |

SSE is an observation aid, not durable evidence. A stream gap means recoverable events must be read from persistent Event pages. Close the stream after the observation window.

## Repeatable Quality Report

For a bounded, read-only summary of one Run, invoke the bundled helper after resolving the exact Graph and Run IDs:

```text
python scripts/run_quality_report.py --base-url http://127.0.0.1:8080 --graph-id <graph_id> --run-id <run_id>
```

Set `--token-env` to the name of the configured management-token environment variable when authentication is required. The helper never prints the token, sends only `GET` requests, follows persisted Event cursors, and reports runtime/business/evidence/side-effect quality plus tool-policy mismatches. It is a reporting aid, not a substitute for reading disputed Artifact or Checkpoint bodies.
