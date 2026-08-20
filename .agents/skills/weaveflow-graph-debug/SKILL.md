---
name: weaveflow-graph-debug
description: "Understand, inspect, observe, diagnose, control, and resume WeaveFlow Graph Runs through the public debug server HTTP API. Use for failed, paused, hanging, canceled, incomplete, historical, or side-effect-unknown Runs. This skill is source-independent: reconstruct definition and execution context from Graph Session, Run, Step, Event, Checkpoint, Artifact, registry, and settings responses; do not inspect a code repository or implementation files."
---

# WeaveFlow Graph Debug

Operate only through the public HTTP API and this skill's references. Treat the exact immutable Graph Session plus persisted Run evidence as the complete debugging context. Do not read source code, local project files, git history, logs outside exposed API data, or repository-specific instructions.

Read [references/debugging.md](references/debugging.md) for context reconstruction and triage, and [references/server-api.md](references/server-api.md) before making requests.

## Inputs And Outputs

- **Inputs**: server base URL, management authentication, Graph ID and Run ID when known, or criteria for selecting a Run; optional authorization for pause/cancel/resume/effect resolution.
- **Outputs**: a reconstructed definition context, execution timeline, state evolution, failed/paused node contract, model/tool and side-effect context, evidence-backed diagnosis, and any explicitly authorized recovery result.
- **Handoff**: use `weaveflow-graph-create` when the remedy is a new definition, Session, setting, or Trigger. If the API lacks required evidence, report an observability/API gap without inspecting implementation code.

## Reconstruct The Exact Context

1. Locate the Run through `GET /graphs/:graph_id/runs` if needed and record why it was selected.
2. Read `GET /graphs/:graph_id/runs/:run_id/inspection`; capture Run identity, `graph_session_id`, origin, status, timestamps, current node, errors, Steps, attempts, effect state, Checkpoints, Interrupt, and first Event page.
3. Read `GET /graphs/:graph_id/sessions/:graph_session_id`. Use that exact historical Session—not the latest Graph—to understand the definition, node configs, State Ports, bindings, topology, conditions, state modules, initial-state requirements, settings, tool permissions, and hashes. Preserve any `context_warnings` as uncertainty.
4. Read `GET /registry` and `GET /runtime/tools` only to interpret the Session's registered node/condition/tool contracts. Never substitute current registry behavior for missing historical Session data; record drift or uncertainty.
5. Build a focused context map for the affected path: external input → state binding → node/condition → Step attempts/events → Checkpoint state → Artifact/output. Include expected versus observed values and the last known valid state.
6. Page persisted Events as needed, then list/read only relevant Artifacts and Checkpoints. Use SSE only for bounded live observation and reconcile it with persisted evidence.

## Diagnose And Recover

1. Classify the failure from API evidence: definition/contract, missing initial state, model/settings, tool permission/approval, external dependency, node output, interrupt, cancellation, retention, or unresolved side effect.
2. For a paused Run, derive the minimal resume `input` from the Interrupt, exact Session contract, and Checkpoint state. Resume only when authorized and verify the resulting state and terminal status.
3. For `effect_status=unknown`, trace effect intent/outcome events and operation identity. Resolve only with explicit evidence and authorization; never guess that a non-idempotent write was not applied.
4. Use pause or cancel only when requested. Wait for safe-point transitions and re-inspect rather than repeating controls.
5. When the remedy changes the definition/settings/Triggers, report the exact API-level change and hand off to `weaveflow-graph-create`; do not modify a historical Run's Session.

## Safety And Stop Conditions

- Investigation is read-only by default. Resume, effect resolution, pause, cancel, deletion, Session creation, and Trigger changes require explicit authorization.
- Stop when required Session detail, Event pages, Checkpoint, Artifact, registry contract, or settings are unavailable. Name the missing API resource rather than reading local files.
- Stop when evidence is unchanged, impact grows, side effects are unresolved, or a high-risk decision needs user confirmation.
- A successful control HTTP response is not proof of recovery. Confirm persisted status, events, state, outputs, and effect resolution.
- Respect retention: an inactive historical Session and its diagnostics may have been pruned.

## Report

Report the exact Graph/Session/Run identity, reconstructed definition and execution context, timeline, failed/paused node contract, state and side-effect evidence, diagnosis confidence, recovery action/result, missing API evidence, and endpoints consulted.
