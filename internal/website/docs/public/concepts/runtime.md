# Runtime Model

The runtime owns the durable lifecycle of a compiled graph. It records enough evidence to inspect execution and continue
safely after a pause or process restart.

## Core records

- **Run** records lifecycle status, graph identity, revision, timing, and failure details.
- **Event** records ordered execution evidence, such as node completion and checkpoint creation.
- **Checkpoint** stores resumable state at a stable execution boundary.
- **Artifact** stores larger outputs or diagnostic material associated with a Run.

## Run lifecycle

A Run starts from an exact Graph Session and initial state. The runtime advances through nodes, applies validated state
patches, publishes Events, and creates Checkpoints at configured boundaries.

A Run may complete, fail, be canceled, or pause for a breakpoint or user input. Before resuming, the runtime validates that
the checkpoint is compatible with the graph contract.

The normal lifecycle is:

```text
Session → Run created → Steps execute → Checkpoints/Events persist → completed | failed | paused | canceled
                                                   ↓
                                             resume from checkpoint
```

Each Step identifies the node and task that produced its outcome. A failed Step is retained even when a failure route
continues to a fallback node, so operators can distinguish the original error from the recovery result.

## Predictable state updates

Nodes return state changes as patches. Sequential patches apply in order. Parallel branches are merged only when their
writes are disjoint or use a registered reducer with stable merge semantics.

## Inspection

Use the Workbench or Debug Server API to inspect Run summaries, paged Events, Checkpoints, Artifacts, and selected state
snapshots. See [Workbench](/guides/workbench) for local setup.

## Recovery and side effects

Checkpoint recovery restores the recorded business state and execution position. It does not make an external side effect
transactional. For writes to an API, queue, or filesystem, use an idempotency key or an explicit effect-resolution step so
a retry cannot silently duplicate the operation. When a side-effect outcome is unknown, inspect Events before resuming.

## In-memory versus durable runners

`weaveflow.NewInMemoryRunner` is useful for tests and short examples. `weaveflow.NewLocalRunner` persists execution,
checkpoint, Event, Artifact, and transaction records below a directory and supports continuation after a process restart.
The Debug Server uses its configured runtime store for the same lifecycle model.
