# Run Debugging

Diagnose from the exact Run and immutable Graph Session. Reconstruct persisted context through public API responses first; the API is authoritative. Use bounded, read-only source inspection only under [Source-Assisted Diagnosis](#source-assisted-diagnosis) to explain implementation behavior, never to replace persisted evidence.

## Evidence Order

1. Capture the start/resume response, structured error, partial data, Run status, Graph ID, Session ID, and timestamps.
2. Read `GET /graphs/:graph_id/runs/:run_id/inspection`.
3. Identify root/parent/child/fork provenance, then the failed or active Step, `step_id`, `node_id`, attempt, operation key, effect class/status, current stage, interrupt, checkpoint ID, and Artifact references.
4. Read persisted Event pages around the transition. Lifecycle events include `run.created`, `run.started`, `run.paused`, `run.resumed`, `run.failed`, `run.finished`, and `run.canceled`; effect events include intent, outcome, and resolution transitions.
5. List Artifacts and read only relevant Artifact bodies. Read the referenced Checkpoint state when it explains a pause, missing input, or side-effect boundary.
6. Read `GET /graphs/:graph_id/sessions/:graph_session_id` and compare the Run's exact definition, semantic graph hash, snapshot hash, settings, and required state. Read `GET /graphs/:graph_id` separately only to detect whether a newer Session exists.

## Source-Assisted Diagnosis

Source inspection is optional and read-only. Use it only after the API-first reconstruction when one of these conditions applies: an API observability gap prevents explaining the behavior, implementation behavior must be explained, or a source-backed diagnosis was explicitly requested.

Before reading source:

1. Read every applicable `AGENTS.md` for the repository path and follow its safety, testing, and editing rules.
2. Restrict inspection to the implementation, tests, or documentation that directly covers the affected node, scheduler transition, persistence boundary, or API contract. Do not edit files, run mutating commands, or inspect secrets and unrelated local state.
3. Verify runtime/source correspondence using available version/build metadata, Graph and Session identity, semantic or snapshot hashes, commit, or deployment information. Record what was checked. If correspondence cannot be established, mark all source conclusions as uncertain.

Keep evidence boundaries explicit:

- Persisted Run, Event, Step, Checkpoint, Artifact, Session, registry, and settings responses determine status, outputs, side effects, and historical configuration.
- Source findings are secondary explanations of possible mechanisms or likely fixes. They must not override API evidence or be presented as proof that a branch executed in the inspected Run.
- If source explains a plausible cause while the API fact remains unavailable, report both the source-derived explanation and the unresolved API observability gap. Hand implementation changes to `weaveflow-graph-code`; do not modify source from this skill.

## Quality Assessment

Report these dimensions separately; never collapse them into the Run's terminal status:

| Dimension | Pass condition | Failure or uncertainty |
| --- | --- | --- |
| `runtime` | Persisted Run is terminally successful, lifecycle is coherent, all executed Steps succeed, and the lease is released | `run.failed`, failed Step, inconsistent identity, active lease after a supposed finish, or missing inspection fields |
| `business` | The final Checkpoint's business contract is complete (`done` or its API-defined equivalent) with no pending/failed required steps | Business status is `failed`/`pending`, required state is absent, or the graph has no observable completion contract |
| `evidence` | Final output claims have persisted Artifact/Event/Checkpoint references, and required outputs were actually observed | Missing references, unsupported claims, or only an LLM narrative without tool/output evidence |
| `side_effects` | Every effect intent has a matching persisted outcome and no outcome is `unknown` | `unknown` status, denied/failed effect, or an intent without an outcome; stop before retrying non-idempotent work |

Derive `overall=fail` if any dimension or the tool-policy gate is `fail`; otherwise `overall=warn` if any dimension is `warn` or `unknown`; otherwise `overall=pass`. Preserve the individual values in the report.

For every diagnosis, label claims as `API-persisted` or `source-derived`, include source paths and correspondence checks when source was inspected, and state confidence plus any remaining observability gap. Source-derived claims cannot turn an `unknown` API dimension into `pass`.

### Tool Policy Preflight

Before calling a Run healthy, compare the exact Session settings with `/runtime/tools` and observed tool events:

1. Collect tool IDs from every Session node and every `tool.*` Event.
2. For each tool, verify it is registered, every declared permission is present in Session `tool_permissions`, and `approval: required` has an explicit true entry in Session `tool_approvals`.
3. Treat tools with `process.execute` as potentially mutating even when their name is not `write` or `edit`; an approval entry for another tool does not satisfy this check.
4. Report denied/failed tool calls separately from successful later work. A later successful shell command does not erase an earlier failed write attempt or change the persisted business status.

### Event Pagination

Follow `next_cursor` using the API's exact cursor value until it is empty. Deduplicate by Event ID, record page and item counts, and report a cursor loop or missing page as an observability gap. Do not treat the first page as the complete timeline.

## Context Map

For the failing path, record:

- Graph purpose, exact Session ID/version/hashes, entry/finish points, and Run origin.
- Relevant node/condition type, config, State Port bindings, model/tool dependencies, and expected output.
- Initial state and Trigger-provided values relevant to the path.
- Step attempts, errors, effect class/status, operation identity, and event order.
- Checkpoint business/runtime state before and after the affected node.
- Artifact references and final observed output.

Use this map to distinguish a contract error from a runtime dependency failure. If any required field is absent from the API, preserve it as an explicit unknown.

## Run Lineage, Fork, And Compare

- Use `GET /graphs/:graph_id/runs` with `parent_run_id`, `parent_task_id`, `root_run_id`, or `namespace` filters to reconstruct relevant Run lineage. A child Run or Agent invocation that affects the result belongs in the timeline rather than a footnote.
- `GET /graphs/:graph_id/runs/:run_id/compare/:other_run_id` is read-only and requires both Runs to use the same Graph hash and snapshot hash. It reports Run, Step, Event, Artifact, and last-Checkpoint state differences; it does not replace complete Event pagination or independent inspection of each Run.
- `POST /graphs/:graph_id/runs/:run_id/forks` is state-changing. Require explicit authorization, resolved source effects, a non-final independently resumable Checkpoint, a stable `request_key`, and an optional external `input` patch. The source Run remains immutable.
- A `202` Fork response is admission only. Inspect the forked Run to terminal or requested stopping state, confirm its Session/snapshot provenance, and Compare it with the source before calling the recovery successful.

## Failure Map

| Symptom | Evidence to compare | Safe next action |
| --- | --- | --- |
| Graph list is empty or detail is `404` | Base URL, management auth, `/registry`, `/graphs` | Check API scope and persistence; do not assume the server is dead. |
| `invalid_graph_definition` | Analysis error, live registry, State Ports, bindings | Prepare an API-level corrected definition through `weaveflow-graph-create`; do not mutate the Run. |
| Analysis succeeds but Session creation fails | Session error and intended settings | Check provider, API format, credentials, model enablement, and runtime setup. |
| Run fails in a node | Step attempt, node config, events, checkpoint, Artifact | Determine deterministic contract failure versus external dependency failure. |
| Run pauses without an obvious prompt | Interrupt, last Checkpoint, latest `run.paused` event | Build the exact required `input` patch and resume the same Run only when authorized. |
| Resume conflict | Run Session/hash, checkpoint stage, Session inventory | Verify stored Session availability; do not create a replacement first. |
| Run has `effect_status=unknown` | Effect intent/outcome events, operation key, side-effect class | Do not retry blindly; resolve as not-applied or compensate only with evidence and authorization. |
| Fork is rejected | Source effect state, Checkpoint stage/independence, request key, Session/hash | Resolve the exact safety or identity conflict; do not fall back to replaying the original effect. |
| Historical Run becomes `404` | Session inventory, upload history, and `/graphs/:graph_id/retention-audit` | Determine whether retention selected the Run for deletion; an audit intent alone does not prove deletion completed. Do not reconstruct missing facts from source. |

## Resume And Effect Resolution

Resume is `POST /graphs/:graph_id/runs/:run_id/resume` with `{ "input": { ... } }`. There is no independent checkpoint-resume route. Resume must use the Run's stored Session and compatible graph hash.

Effect resolution is `POST /graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution`. Required fields include `resolution_id`, `action`, `actor`, `reason`, and `continue`; supported actions are server-defined and currently include `confirm_not_applied` and `compensate`. Use a stable unique resolution ID for retries of the same human decision, and stop if compensation returns an unknown outcome.

## Completion Criteria

Do not call a Run fixed merely because resume, Fork, or effect resolution returned HTTP success. Confirm the resulting Run status, persisted lifecycle events, Step effect status, output/failure evidence, and any required Checkpoint or Artifact result. For a Fork, preserve the source verdict and report the forked verdict separately.
