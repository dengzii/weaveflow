---
name: weaveflow-graph
description: Create, configure, validate, run, inspect, resume, and publish WeaveFlow Graph Definition v2 agent graphs through the project debug server HTTP API. Use when an agent must discover the live registry, compose node config and state bindings, update runtime model or memory settings, upload a draft graph, execute a run, consume runtime events, diagnose failed or paused runs, inspect checkpoints and artifacts, or explicitly publish an official graph for triggers.
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
5. Call `POST /graph/initial-state-requirements` with the complete upload envelope. Fix every schema, build, and required-state problem before changing the current graph.
6. Update `PUT /runtime/settings` only when the requested graph needs different models, environment, or memory. Preserve unrelated settings and never print or persist secrets outside the server's intended request.
7. Upload a debug draft with `PUT /graph`. Confirm the returned graph ID, version, semantic hash, snapshot hash, session ID, and warnings.
8. Subscribe to `GET /runtime/events/stream` before starting the run when token chunks or exact live ordering matter. Close the subscription after the observation window.
9. Start the draft with `POST /runs`. Remember that this request blocks until the run completes, pauses, fails, or is canceled.
10. Inspect the run record, failed steps, persisted events, checkpoints, interrupt, and artifacts. Use the run's `graph_id` for historical reads.
11. Resume without uploading a semantically different graph. Supply only the state patch required by the interrupt or missing contract.
12. Publish with `POST /graph/publish` only when the user explicitly requests deployment or trigger-visible publication. Publishing is not a validation step.

## Apply Safety Boundaries

- Do not start, stop, restart, or reconfigure the server process unless the user explicitly authorizes that runtime action. Repository guidance forbids starting `cmd/server` merely for validation.
- Treat `PUT /graph`, `PUT /runtime/settings`, run controls, deletion, and publish as state-changing operations. Keep them within the user's requested scope.
- Prefer a draft upload for debugging. Do not publish as a shortcut to make a run possible.
- Do not delete runs or old sessions during diagnosis unless deletion is explicitly requested.
- Treat SSE as best-effort live observation, not durable evidence. Reconcile it with the HTTP response and persisted events.
- On an API mismatch, inspect `internal/server/routes.go`, the relevant handler, and `dsl/registry_schema.go`; current code and the live registry override this skill's examples.

## Report The Result

Report the base URL and graph identity used, draft versus published status, run ID and terminal status, relevant warnings or interrupt, and the exact endpoints consulted. Redact credentials and avoid dumping large checkpoints or artifact bodies unless requested.
