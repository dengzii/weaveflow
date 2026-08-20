# Debug API

Use Graph-scoped routes with the management Bearer token. Successful data is wrapped in `data`; failures include `error.code` and `error.message`. Never log Authorization headers or large state bodies.

## Run Discovery And Inspection

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/graphs/:graph_id/runs` | List Runs; use the returned cursor when paging. |
| `GET` | `/graphs/:graph_id/runs/:run_id/inspection` | Read the primary Run, Step, Checkpoint, Event, and Interrupt view. |
| `GET` | `/graphs/:graph_id/runs/:run_id/events` | Read persisted Event pages with cursor and limit. |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts` | List Run Artifacts. |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts/:artifact_id` | Read one referenced Artifact. |
| `GET` | `/graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id` | Read one referenced Checkpoint. |
| `GET` | `/graphs/:graph_id/sessions/:session_id` | Read the Run's exact immutable definition, settings, required state, hashes, and registry-drift warnings. |
| `GET` | `/graphs/:graph_id` | Read the latest Session only, for drift comparison. |

## Run Control

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/graphs/:graph_id/runs/:run_id/resume` | Resume the stored Session with an `input` state patch. |
| `POST` | `/graphs/:graph_id/runs/:run_id/pause` | Request a safe-point pause. |
| `POST` | `/graphs/:graph_id/runs/:run_id/cancel` | Request cancellation. |
| `POST` | `/graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution` | Resolve an unresolved side effect. |
| `DELETE` | `/graphs/:graph_id/runs/:run_id` | Delete Run diagnostics; use only when explicitly requested. |

Run start is asynchronous: `POST /graphs/:graph_id/sessions/:session_id/runs` returns `202` with a `RunRecord` and accepts `initial_state`. Trigger-started Runs include an origin; direct starts do not.

If exact Session detail returns `context_warnings`, keep the persisted definition, settings, Session ID, and manifest hashes as historical context. Do not reinterpret missing derived requirements through the current registry or guess how the old Session behaved.

## Live Events

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/graphs/:graph_id/events/stream` | Observe bounded Graph events with optional `session_id`, `run_id`, `node_id`, `type`, `cursor`, or `Last-Event-ID` filters. |

SSE is an observation aid, not durable evidence. A stream gap means recoverable events must be read from persistent Event pages. Close the stream after the observation window.
