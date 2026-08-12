# Run Debugging

Diagnose from the exact failing Run and immutable Graph Session. Do not change graph design until the failure evidence
points there.

## Contents

- [Inspection Order](#inspection-order)
- [Common Failure Map](#common-failure-map)
- [Paused And Interrupted Runs](#paused-and-interrupted-runs)
- [Events And Evidence](#events-and-evidence)
- [Completion Criteria](#completion-criteria)

## Inspection Order

1. Record the start or resume HTTP status, `error.code`, `error.message`, and any partial `data`.
2. Record `run_id`, `graph_id`, `graph_hash`, `graph_snapshot_hash`, `graph_session_id`, `origin`, status, current node
   IDs, last step ID, and last checkpoint ID.
3. Read `GET /graphs/:graph_id/runs/:run_id/inspection`. Locate the first failed or paused step and preserve its concrete
   `error_message`.
4. Correlate the included persisted events such as `run.failed`, `nodes.failed`, `contract.violation`, `tool.failed`,
   `warning`, and checkpoint events by `step_id` and `node_id`.
5. Follow `inspection.events.next_cursor` through the Graph-scoped Run event endpoint only when older evidence is needed.
6. For a paused Run, inspect the aggregate `interrupt` and load its exact checkpoint through the Run-scoped checkpoint
   path.
7. List artifacts and open only the input, output, or diagnostic artifacts relevant to the failing step.
8. Read `GET /graphs/:graph_id` and compare the latest Session with the Run's `graph_session_id` before control or
   resume. A newer Session does not change the Run's execution identity.

The inspection endpoint already aggregates Run, steps, checkpoints, the newest event page, and interrupt. Do not issue
separate requests for each of those resources. Large checkpoint state and artifact content should be summarized, not
dumped.

The Server retains the latest five complete Sessions per Graph and temporarily protects older active Sessions. If an
inactive Session has been pruned, its persisted Run diagnostics and resume source are no longer available. Do not create
replacement Sessions while collecting historical evidence.

## Common Failure Map

| Symptom | Inspect first | Likely correction |
|---------|---------------|-------------------|
| Graph list is empty or detail is `404` | `GET /registry`, then `GET /graphs` | Create an authorized valid Session; do not mistake an empty Graph collection for a dead Server. |
| `invalid_graph_definition` | Candidate-analysis message and `GET /registry` | Fix schema, node type, state binding, state module, edge, or condition errors. |
| Analysis succeeds but Session creation fails | Session error plus intended `settings` | Fix model provider, API format, credentials, or runtime setup; analysis does not validate them. |
| Required state missing or `contract.violation` | Requirements, node state ports, checkpoint business state | Add the correct initial state or bind the port to the intended path. |
| `model "..." not available` | Graph detail settings and node `config.model_id` | Enable that exact ID in a new Session or use an existing model such as `default`. |
| Codex rejects provider or API format | Selected Graph model and Codex error | Configure that model with provider `openai` and `api_format: responses`. |
| Tool not found or `tool.failed` | `/runtime/tools`, node `tool_ids`, tool event payload, artifacts | Correct the ID or fix the tool input/environment. |
| `parallel state merge requires branch patches` | Earlier failed branch steps and contract/model/tool events | Fix the branch failure that prevented its patch; do not begin by changing merge logic. |
| Paused Run with no obvious prompt | Aggregate interrupt, last checkpoint, latest `run.paused` event | Supply the expected resume state patch to the compatible Run Session. |
| Resume returns conflict | Run Session/hash, checkpoint stage, Run status | Do not create a semantic replacement before resuming; verify the stored Session is loadable. |
| Run appears missing | Run `graph_id` and the path Graph ID | Use the Run's Graph-scoped resource path. |
| Historical Run becomes `404` after repeated uploads | Graph Session inventory and upload history | The inactive Session may have fallen outside the five-Session retention window; recover from external evidence if available. |
| Run stays pending/running after process loss | Inspection status and execution-lost error/event | Treat it as lost execution; a restarted process cannot reattach to the old goroutine. |
| Live chunks are missing | SSE subscription timing, Graph/session filters, reconnect cursor | Subscribe before execution; chunks are not persisted and an overflowing subscriber is disconnected. |
| SSE and inspection briefly disagree | Later inspection and persisted events | The stream is bounded in-process observation; inspection is the durable source of truth. |

## Paused And Interrupted Runs

A paused Run with a checkpoint exposes an interrupt containing Run ID, checkpoint ID, node ID, stage, message,
optional breakpoint, and runtime state.

Resume from the Run's latest checkpoint:

```text
POST /graphs/:graph_id/runs/:run_id/resume
{"input": { ...state patch... }}
```

There is no independent checkpoint-resume route. A checkpoint can be read at:

```text
GET /graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id
```

Do not create a changed Graph Session between pause and resume. Resume resolves the Runner for the Run's recorded
Session and validates control state. Pause and cancel wait for a safe-point transition; do not retry them rapidly while
the first request remains active.

## Events And Evidence

Useful event categories include:

- Lifecycle: `run.created`, `run.started`, `run.paused`, `run.resumed`, `run.failed`, `run.finished`, `run.canceled`.
- Node: `nodes.started`, `nodes.retry`, `nodes.failed`, `nodes.finished`.
- Contract and state: `contract.violation`, `state.changed`, `checkpoint.created`, `breakpoint.hit`, `warning`.
- Model and tool: `llm.call`, `llm.content`, `llm.function_call`, `llm.usage`, `tool.called`, `tool.returned`,
  `tool.failed`.
- Live-only chunks: `llm.content_chunk`, `llm.reasoning_chunk`.

Event IDs are SSE cursor identities, not a global business sequence. Parallel branches can interleave. Correlate by
Graph, Session, Run, step, node, call ID, and timestamp. Persisted event pagination uses a separate opaque cursor and
returns newest events first.

## Completion Criteria

Do not call a graph fixed only because Session creation succeeds. Require:

1. Candidate analysis succeeds.
2. Session creation returns the intended identity and settings with no unexplained warnings.
3. A representative asynchronous Run reaches the expected terminal status or expected interrupt.
4. The final checkpoint or relevant output artifact contains the intended output when final state is not returned by
   asynchronous start.
5. Failed steps and failure events are absent, or are understood and expected.
6. Relevant artifacts and persisted events agree with the Run inspection.
7. Account for the fact that the latest complete Session is immediately available to Triggers for its Graph ID.

Use `docs/server-business-flow.md`, `internal/server/run_handlers.go`, `internal/server/run_support.go`, and
`runtime/runner_types.go` when deeper runtime semantics are needed.
