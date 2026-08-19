# Runtime Model

The runtime owns the durable lifecycle around a compiled graph. It records enough evidence to inspect execution and
continue safely after a pause or process restart.

## Core records

- **Run** records lifecycle status, graph identity, revision, timing, and failure details.
- **Event** records ordered execution evidence such as node completion and checkpoint creation.
- **Checkpoint** stores resumable state at a stable execution boundary.
- **Artifact** stores larger outputs or diagnostic material associated with a Run.

## Run lifecycle

A Run starts from an exact Graph Session and initial state. The runtime advances through nodes, applies validated state
patches, publishes events, and creates checkpoints at configured boundaries.

A Run may complete, fail, be canceled, or pause for a breakpoint or user input. Resume validates that the checkpoint is
compatible with the graph contract before continuing.

## Deterministic state updates

Nodes return state changes as patches. Sequential patches apply in order. Parallel branches are merged only when their
writes are disjoint or use a registered deterministic reducer.

## Inspection

Use the Workbench or debug server API to inspect Run summaries, paged Events, Checkpoints, Artifacts, and selected state
snapshots. See [Workbench](/guides/workbench) for local setup.
