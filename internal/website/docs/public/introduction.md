# Why WeaveFlow?

WeaveFlow is a Go runtime for workflows that need to remain understandable after they run, not just while they are being
written. A workflow is a versioned graph: nodes do the work, edges describe control flow, and State Ports define exactly
which data each component may read or write.

## The problem it solves

An agent loop is quick to prototype, but it can become difficult to answer basic operational questions:

- Which model, tool, and prompt produced this answer?
- Which branch ran, and why did the other branch not run?
- What state changed before a failure or approval request?
- Can the run resume after a process restart without repeating a side effect?

WeaveFlow makes those decisions part of the graph and persists the evidence needed to inspect them later.

## Design principles

### Explicit control flow

Use nodes, normal edges, conditional edges, and failure routes to model sequencing, routing, retries, approval, and
fallback behavior. Serialize the graph as JSON, review it in a pull request, or edit it in the Workbench.

### Contract-bound state

Every registered node and condition exposes State Ports. A definition binds each port to a path such as
`shared.request.input` or `scopes.planner.plan`. The registry validates access mode, schema, capabilities, and parallel
merge rules before execution.

### Durable evidence

The runtime stores Runs, Steps, Events, Checkpoints, and Artifacts. This makes a workflow inspectable from its first input
through its final output, including pauses for human input and effect-resolution decisions.

### Local-first operation

The Go runtime, Debug Server, and Workbench run locally. A Graph Definition remains portable, while provider credentials
and runtime data stay outside the definition. Start in memory, use the file-backed local runner, or deploy the packaged
image with Docker Compose.

## When to use it

WeaveFlow is a good fit for:

- Model and tool workflows with a visible ReAct loop.
- Plan/execute/review or supervisor/worker orchestration.
- Human approval, pause/resume, and checkpoint recovery.
- Repeatable state transformations and fan-out/fan-in pipelines.
- Teams that need a visual debugger and auditable execution history.

It is intentionally not a hosted, multi-tenant control plane. Authentication, quotas, tenant isolation, and external
worker failover remain responsibilities of the deployment environment.

## A typical workflow

```text
input → plan → parallel workers → review → approval? → answer
             ↘ failure route ↗
```

The same topology can be loaded by Go code, uploaded to the Debug Server, or opened in the Workbench. Continue with the
[Quick Start](/getting-started), then read [Graph Definition](/concepts/graph-definition) and [Runtime Model](/concepts/runtime).
