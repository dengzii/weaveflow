---
name: weaveflow-graph-debug
description: "Understand, inspect, observe, diagnose, control, and resume WeaveFlow Graph Runs through the public debug server HTTP API, with optional read-only source inspection when API evidence leaves an observability gap or implementation behavior must be explained. Use for failed, paused, hanging, canceled, incomplete, historical, or side-effect-unknown Runs."
---

# WeaveFlow Graph Debug

Use the public HTTP API as the authority for persisted Run facts and this skill's references as the operating guide. Treat the exact immutable Graph Session plus persisted Run evidence as the primary debugging context. Bounded, read-only source inspection is permitted only under [Source-Assisted Diagnosis](#source-assisted-diagnosis); it may explain implementation behavior but cannot replace missing API evidence.

Read [references/debugging.md](references/debugging.md) for context reconstruction and triage, and [references/server-api.md](references/server-api.md) before making requests.

## Inputs And Outputs

- **Inputs**: server base URL, management authentication, Graph ID and Run ID when known, or criteria for selecting a Run; optional authorization for pause/cancel/resume/effect resolution.
- **Outputs**: a reconstructed definition context, execution timeline, state evolution, failed/paused node contract, model/tool and side-effect context, a four-part quality assessment (`runtime`, `business`, `evidence`, `side_effects`) plus a tool-policy gate, evidence-backed diagnosis, and any explicitly authorized recovery result. Label persisted API evidence separately from source-derived explanations.
- **Handoff**: use `weaveflow-graph-create` when the remedy is a new definition, Session, setting, or Trigger. Use `weaveflow-graph-code` for implementation fixes; `graph-debug` must not edit source. If the API lacks required evidence, report the observability/API gap even when source inspection explains a plausible cause.

## Reconstruct The Exact Context

1. Locate the Run through `GET /graphs/:graph_id/runs` if needed and record why it was selected.
2. Read `GET /graphs/:graph_id/runs/:run_id/inspection`; capture Run identity, `graph_session_id`, origin, status, timestamps, current node, errors, Steps, attempts, effect state, Checkpoints, Interrupt, and first Event page.
3. Read `GET /graphs/:graph_id/sessions/:graph_session_id`. Use that exact historical Session—not the latest Graph—to understand the definition, node configs, State Ports, bindings, topology, conditions, state modules, initial-state requirements, settings, tool permissions, and hashes. Preserve any `context_warnings` as uncertainty.
4. Read `GET /registry` and `GET /runtime/tools` only to interpret the Session's registered node/condition/tool contracts. Never substitute current registry behavior for missing historical Session data; record drift or uncertainty.
5. Build a focused context map for the affected path: external input → state binding → node/condition → Step attempts/events → Checkpoint state → Artifact/output. Include expected versus observed values and the last known valid state.
6. Page persisted Events until `next_cursor` is empty or repeats, deduplicate by Event ID, then list/read only relevant Artifacts and Checkpoints. Use SSE only for bounded live observation and reconcile it with persisted evidence.
7. For repeatable audits, run `scripts/run_quality_report.py` with the same Graph and Run IDs. It performs only public `GET` requests and emits sanitized JSON; use the raw API responses for disputed fields.

## Source-Assisted Diagnosis

- **When allowed**: inspect source only when persisted API evidence has an observability gap, implementation behavior must be explained, or source-backed diagnosis is explicitly requested. Keep the API-first path complete before opening source files.
- **Before reading**: read every applicable `AGENTS.md` for the repository path. Limit inspection to files relevant to the affected contract or execution path; do not edit source, run mutating commands, or inspect secrets and unrelated local state.
- **Establish correspondence**: verify the runtime/source version, Graph and Session identity, commit, or build metadata when possible. If correspondence cannot be established, label source conclusions as uncertain and do not present them as the running implementation's facts.
- **Evidence precedence**: persisted Run, Event, Checkpoint, Artifact, Session, and registry responses remain authoritative. Source can explain a mechanism or identify a likely fix, but cannot override API status, outputs, side-effect resolution, or historical settings.
- **Report separately**: cite the inspected paths and version/correspondence checks, mark each statement as API-persisted or source-derived, and state the confidence and remaining observability gap. Hand implementation changes to `weaveflow-graph-code`.

## Diagnose And Recover

1. Classify the failure from API evidence: definition/contract, missing initial state, model/settings, tool permission/approval, external dependency, node output, interrupt, cancellation, retention, or unresolved side effect.
2. For a paused Run, derive the minimal resume `input` from the Interrupt, exact Session contract, and Checkpoint state. Resume only when authorized and verify the resulting state and terminal status.
3. For `effect_status=unknown`, trace effect intent/outcome events and operation identity. Resolve only with explicit evidence and authorization; never guess that a non-idempotent write was not applied.
4. Use pause or cancel only when requested. Wait for safe-point transitions and re-inspect rather than repeating controls.
5. When the remedy changes the definition/settings/Triggers, report the exact API-level change and hand off to `weaveflow-graph-create`; do not modify a historical Run's Session.

## Quality Assessment

- Always report `runtime`, `business`, `evidence`, and `side_effects` independently as `pass`, `warn`, `fail`, or `unknown`, plus the tool-policy gate. Derive `overall` (`fail` if any dimension or the policy gate fails, otherwise `warn` if any warns or is unknown, otherwise `pass`).
- `runtime` comes from persisted Run status, lifecycle events, Step status, and lease state. A terminal `completed` status is not business success.
- `business` comes from the persisted business contract in the final Checkpoint (for example `shared.plan.status` and step statuses). `done` with no pending/failed steps can pass; `failed`, `pending`, or missing required business state cannot.
- `evidence` requires the final output and material claims to be connected to persisted Artifact/Event/Checkpoint evidence. Treat unsupported final claims as a finding, not as a model-quality opinion.
- `side_effects` fails or stops on `unknown`; warn on denied/failed effects or an intent without a matching outcome. Never retry a non-idempotent effect from a quality report.
- Cross-check every Session-declared tool against `/runtime/tools`, `tool_permissions`, and `tool_approvals`. A required approval missing from Session settings is a blocking policy mismatch; approval for one tool does not cover a different mutating tool such as a `process.execute` shell.

## Safety And Stop Conditions

- Investigation is read-only by default. Resume, effect resolution, pause, cancel, deletion, Session creation, and Trigger changes require explicit authorization.
- Stop when required Session detail, Event pages, Checkpoint, Artifact, registry contract, or settings are unavailable and the missing persisted fact cannot be established through the API. Name the missing API resource; source inspection may explain behavior but cannot fill that persisted-fact gap.
- Stop when evidence is unchanged, impact grows, side effects are unresolved, or a high-risk decision needs user confirmation.
- Stop a pass result when a declared tool/permission/approval mismatch can change whether the business step executes; report the mismatch and its exact API fields.
- Do not infer persisted status, output, side-effect outcome, or historical Session settings from source code.
- A successful control HTTP response is not proof of recovery. Confirm persisted status, events, state, outputs, and effect resolution.
- Respect retention: an inactive historical Session and its diagnostics may have been pruned.

## Report

Report the exact Graph/Session/Run identity, reconstructed definition and execution context, timeline, failed/paused node contract, state and side-effect evidence, diagnosis confidence, recovery action/result, missing API evidence, and endpoints consulted.
