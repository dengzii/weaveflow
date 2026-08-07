---
name: weaveflow-graph
description: Create, configure, validate, upload, run, inspect, and resume WeaveFlow Graph Definition v2 agent graphs through the project debug server HTTP API. Use when an agent must discover the live registry, compose node config and state bindings, create an immutable graph session with runtime settings, execute an exact session, consume runtime events, diagnose failed or paused runs, or inspect checkpoints and artifacts.
---

# WeaveFlow Graph

Use the server as the validation and debugging boundary for Graph Definition v2. Discover the running server's registry instead of guessing node schemas.

## Read The Relevant Reference

- Read [references/server-api.md](references/server-api.md) before making HTTP calls or changing runtime settings.
- Read [references/graph-definition.md](references/graph-definition.md) before creating or modifying graph JSON.
- Read [references/debugging.md](references/debugging.md) when a run fails, pauses, produces incomplete output, or belongs to an older graph session.

## Follow The Workflow

1. Read the repository `AGENTS.md` and honor its runtime and validation restrictions.
2. Resolve the exact base URL, including any `-prefix`. Default to `http://127.0.0.1:8080` only when no configured URL is available.
3. Probe `GET /registry` and `GET /runtime/tools`, then list `GET /graphs`. Fetch `GET /graphs/:graph_id` only for the graph that must be inspected or changed.
4. Build the definition from the returned `graph_schema`, `node_types`, `conditions`, `state_modules`, and `capabilities`. Put state paths in node or condition `state` bindings, never in `config`.
5. Call `POST /graphs/:graph_id/analysis/initial-state-requirements` with the intended session envelope. Fix every schema, build, and required-state problem before creating a session.
6. Build one `POST /graphs/:graph_id/sessions` request containing the definition and required `settings` object. Keep node config, environment, models, and memory settings aligned in that request; never print secrets outside the server's intended request.
7. Create a session only when the requested workflow calls for an upload or run. Confirm the returned graph ID, version, semantic hash, snapshot hash, session ID, settings, and warnings. A successful session immediately becomes the latest trigger-visible session for its `graph_id`.
8. Subscribe to `GET /graphs/:graph_id/events/stream` before starting the run when token chunks or exact live ordering matter. Retain the last event ID for bounded reconnect replay and close the subscription after the observation window.
9. Start the exact uploaded session with `POST /graphs/:graph_id/sessions/:session_id/runs`. The server returns `202 Accepted` with a pending or running `RunRecord`; poll the Graph-scoped run list or inspection endpoint for progress.
10. Inspect `GET /graphs/:graph_id/runs/:run_id/inspection` first, then load only the needed event pages, checkpoint state, or artifacts.
11. Resume without creating a semantically different session. Supply only the state patch required by the interrupt or missing contract.

## Apply Safety Boundaries

- Do not start, stop, restart, or reconfigure the server process unless the user explicitly authorizes that runtime action. Repository guidance forbids starting `cmd/server` merely for validation.
- Treat session creation, Trigger replacement, run controls, and deletion as state-changing operations. Keep them within the user's requested scope.
- Treat every successful Graph Session creation as trigger-visible. Do not upload exploratory edits when the user has not authorized changing the server graph.
- Do not delete runs or old sessions during diagnosis unless deletion is explicitly requested.
- Treat SSE as bounded in-process observation, not durable evidence. Reconcile it with Run inspection and persisted events.
- On an API mismatch, inspect `internal/server/routes.go`, the relevant handler, and `dsl/registry_schema.go`; current code and the live registry override this skill's examples.

## Report The Result

Report the base URL, graph identity and exact session used, whether a session was created, run ID and latest status, relevant warnings or interrupt, and the exact endpoints consulted. Redact credentials and avoid dumping large checkpoints or artifact bodies unless requested.
