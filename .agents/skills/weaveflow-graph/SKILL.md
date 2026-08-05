---
name: weaveflow-graph
description: Create, configure, validate, upload, run, inspect, and resume WeaveFlow Graph Definition v2 agent graphs through the project debug server HTTP API. Use when an agent must discover the live registry, compose node config and state bindings, upload graph-scoped runtime settings with a definition, execute a run, consume runtime events, diagnose failed or paused runs, or inspect checkpoints and artifacts.
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
3. Probe `GET /registry`, then read `GET /runtime/tools` and `GET /runtime/settings`. Do not treat `GET /graph` as a health check because it legitimately returns `503` before a graph is configured.
4. Build the definition from the returned `graph_schema`, `node_types`, `conditions`, `state_modules`, and `capabilities`. Put state paths in node or condition `state` bindings, never in `config`.
5. Call `POST /graph/initial-state-requirements` with the intended upload envelope. Fix every schema, build, and required-state problem before changing the current graph.
6. Build one `PUT /graph` request containing the definition, graph identity, and the required `settings` object. Keep node config, environment, models, and memory settings aligned in that request; never print secrets outside the server's intended request.
7. Upload with `PUT /graph` only when the requested workflow calls for an upload or run. Confirm the returned graph ID, version, semantic hash, snapshot hash, session ID, settings, and warnings. A successful upload is immediately the latest trigger-visible session for its `graph_id`.
8. Subscribe to `GET /runtime/events/stream` before starting the run when token chunks or exact live ordering matter. Close the subscription after the observation window.
9. Start the uploaded graph with `POST /runs`. Remember that this request blocks until the run completes, pauses, fails, or is canceled.
10. Inspect the run record, failed steps, persisted events, checkpoints, interrupt, and artifacts. Use the run's `graph_id` for historical reads.
11. Resume without uploading a semantically different graph. Supply only the state patch required by the interrupt or missing contract.

## Apply Safety Boundaries

- Do not start, stop, restart, or reconfigure the server process unless the user explicitly authorizes that runtime action. Repository guidance forbids starting `cmd/server` merely for validation.
- Treat `PUT /graph`, run controls, and deletion as state-changing operations. Keep them within the user's requested scope.
- Treat every successful `PUT /graph` as trigger-visible. Do not upload exploratory edits when the user has not authorized changing the server graph.
- Do not delete runs or old sessions during diagnosis unless deletion is explicitly requested.
- Treat SSE as best-effort live observation, not durable evidence. Reconcile it with the HTTP response and persisted events.
- On an API mismatch, inspect `internal/server/routes.go`, the relevant handler, and `dsl/registry_schema.go`; current code and the live registry override this skill's examples.

## Report The Result

Report the base URL, graph identity and session used, whether a graph upload occurred, run ID and terminal status, relevant warnings or interrupt, and the exact endpoints consulted. Redact credentials and avoid dumping large checkpoints or artifact bodies unless requested.
