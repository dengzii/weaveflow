# Run Debugging

Diagnose from the exact failing run and graph session. Do not change graph design until the failure evidence points
there.

## Inspection Order

1. Inspect the HTTP status, `error.code`, `error.message`, and any partial `data.run` returned by start or resume.
2. Record `run_id`, `graph_id`, `graph_hash`, `graph_snapshot_hash`, `graph_session_id`, status, current node IDs, last
   step ID, and last checkpoint ID.
3. Read `/runs/:run_id/steps?graph_id=...` and locate failed or paused attempts. Preserve the first concrete
   `error_message`.
4. Read `/runs/:run_id/events?graph_id=...` and correlate `run.failed`, `nodes.failed`, `contract.violation`,
   `tool.failed`, `warning`, and checkpoint events by `step_id` and `node_id`.
5. Read `/runs/:run_id/interrupt?graph_id=...` for a paused run.
6. Read the relevant checkpoint and compare its `business` state with the node's live registry state ports and candidate
   initial-state requirements.
7. List artifacts and open only the input, output, or diagnostic artifacts relevant to the failing step.
8. Compare the run's graph session identity with the current `GET /graph` result before attempting control or resume.

Fetch independent inspection resources in parallel when possible. Large checkpoint state and artifact content should be
summarized, not dumped.

## Common Failure Map

| Symptom                                        | Inspect first                                                                          | Likely correction                                                                          |
|------------------------------------------------|----------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|
| `service_unavailable` on graph or runs         | `GET /registry`, then graph state                                                      | Install a valid Draft; do not mistake an empty graph for a dead server.                    |
| `invalid_graph_definition`                     | Candidate-analysis message and `GET /registry`                                         | Fix schema, node type, state binding, state module, edge, or condition errors.             |
| Required state missing or `contract.violation` | Requirements, node state ports, checkpoint business state                              | Add the correct initial state or bind the port to the intended path.                       |
| `model "..." not available`                    | Runtime settings and node `config.model_id`                                            | Enable that exact ID or use an existing model such as `default`.                           |
| Tool not found or `tool.failed`                | `/runtime/tools`, node `tool_ids`, tool event payload, artifacts                       | Correct the ID or fix the tool input/environment.                                          |
| `parallel state merge requires branch patches` | Earlier failed branch steps and contract/model/tool events                             | Fix the branch failure that prevented its patch; do not begin by changing merge logic.     |
| Paused run with no obvious prompt              | Run result `interrupt`, interrupt endpoint, last checkpoint, latest `run.paused` event | Supply the expected resume state patch to the current compatible runner.                   |
| Resume returns conflict                        | Current graph hash, run hash, checkpoint stage, run status                             | Restore the compatible graph session or avoid uploading semantic changes before resume.    |
| Run appears missing after upload               | Run's `graph_id` and `/graphs`                                                         | Add `graph_id` to historical read paths.                                                   |
| Live chunks are missing                        | SSE subscription timing and consumer speed                                             | Subscribe before execution; chunks are not persisted and slow subscribers can lose events. |
| SSE and final run summary briefly disagree     | Final HTTP response and later run read                                                 | Treat SSE as best-effort; `nodes.finished` can precede the final RunRecord summary update. |

## Paused And Interrupted Runs

A paused run with a checkpoint exposes an interrupt containing the run ID, checkpoint ID, node ID, stage, message,
optional breakpoint, and runtime state.

Use run resume for the latest checkpoint:

```text
POST /runs/:run_id/resume
{"input": { ...state patch... }}
```

Use checkpoint resume only when a specific checkpoint is intentionally selected:

```text
POST /checkpoints/:checkpoint_id/resume
{"input": { ...state patch... }}
```

Do not re-upload a changed graph between pause and resume. Resume is implemented against the current runner and
validates semantic compatibility.

Pause and cancel wait for a safe-point state transition. They are not simple metadata updates. Do not retry them rapidly
if the first request is still active.

## Events And Evidence

Useful event categories include:

- Lifecycle: `run.created`, `run.started`, `run.paused`, `run.resumed`, `run.failed`, `run.finished`, `run.canceled`.
- Node: `nodes.started`, `nodes.retry`, `nodes.failed`, `nodes.finished`.
- Contract and state: `contract.violation`, `state.changed`, `checkpoint.created`, `breakpoint.hit`, `warning`.
- Model and tool: `llm.call`, `llm.content`, `llm.function_call`, `llm.usage`, `tool.called`, `tool.returned`,
  `tool.failed`.
- Live-only chunks: `llm.content_chunk`, `llm.reasoning_chunk`.

Event IDs are identities, not a global sequence. Parallel branches can interleave. Correlate by run, step, node, call
ID, and timestamp.

## Completion Criteria

Do not call a graph fixed only because Draft upload succeeds. Require:

1. Candidate analysis succeeds.
2. Draft upload returns the intended identity and no unexplained warnings.
3. A representative run reaches the expected terminal status or expected interrupt.
4. The final state contains the intended output path.
5. Failed steps and failure events are absent, or are understood and expected.
6. Relevant artifacts and persisted events agree with the run result.
7. Publish remains a separate, explicitly authorized action.

Use `docs/server-business-flow.md`, `internal/server/run_handlers.go`, `internal/server/run_support.go`, and
`runtime/runner_types.go` when deeper runtime semantics are needed.
