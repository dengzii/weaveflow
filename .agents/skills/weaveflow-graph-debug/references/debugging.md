# Run Debugging

Diagnose from the exact Run and immutable Graph Session. Reconstruct all context through public API responses; never inspect implementation source to fill gaps.

## Evidence Order

1. Capture the start/resume response, structured error, partial data, Run status, Graph ID, Session ID, and timestamps.
2. Read `GET /graphs/:graph_id/runs/:run_id/inspection`.
3. Identify failed or active Step, `step_id`, `node_id`, attempt, operation key, effect class/status, current stage, interrupt, checkpoint ID, and Artifact references.
4. Read persisted Event pages around the transition. Lifecycle events include `run.created`, `run.started`, `run.paused`, `run.resumed`, `run.failed`, `run.finished`, and `run.canceled`; effect events include intent, outcome, and resolution transitions.
5. List Artifacts and read only relevant Artifact bodies. Read the referenced Checkpoint state when it explains a pause, missing input, or side-effect boundary.
6. Read `GET /graphs/:graph_id/sessions/:graph_session_id` and compare the Run's exact definition, semantic graph hash, snapshot hash, settings, and required state. Read `GET /graphs/:graph_id` separately only to detect whether a newer Session exists.

## Context Map

For the failing path, record:

- Graph purpose, exact Session ID/version/hashes, entry/finish points, and Run origin.
- Relevant node/condition type, config, State Port bindings, model/tool dependencies, and expected output.
- Initial state and Trigger-provided values relevant to the path.
- Step attempts, errors, effect class/status, operation identity, and event order.
- Checkpoint business/runtime state before and after the affected node.
- Artifact references and final observed output.

Use this map to distinguish a contract error from a runtime dependency failure. If any required field is absent from the API, preserve it as an explicit unknown.

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
| Historical Run becomes `404` | Session inventory and upload history | Check five-Session retention; the inactive Session may have been pruned. |

## Resume And Effect Resolution

Resume is `POST /graphs/:graph_id/runs/:run_id/resume` with `{ "input": { ... } }`. There is no independent checkpoint-resume route. Resume must use the Run's stored Session and compatible graph hash.

Effect resolution is `POST /graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution`. Required fields include `resolution_id`, `action`, `actor`, `reason`, and `continue`; supported actions are server-defined and currently include `confirm_not_applied` and `compensate`. Use a stable unique resolution ID for retries of the same human decision, and stop if compensation returns an unknown outcome.

## Completion Criteria

Do not call a Run fixed merely because resume or effect resolution returned HTTP success. Confirm the resulting Run status, persisted lifecycle events, Step effect status, output/failure evidence, and any required Checkpoint or Artifact result.
