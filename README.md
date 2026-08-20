# WeaveFlow

<p align="center">
  <a href="https://weaveflow.space/"><img src="assets/icon.svg" width="96" alt="WeaveFlow icon"></a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go" alt="Go 1.26+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <a href="https://github.com/dengzii/weaveflow/actions"><img src="https://img.shields.io/github/actions/workflow/status/dengzii/weaveflow/ci.yml?branch=master&logo=github&label=CI" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/dengzii/weaveflow"><img src="https://pkg.go.dev/badge/github.com/dengzii/weaveflow.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/dengzii/weaveflow"><img src="https://goreportcard.com/badge/github.com/dengzii/weaveflow" alt="Go Report Card"></a>
  <a href="https://github.com/dengzii/weaveflow/releases"><img src="https://img.shields.io/github/v/release/dengzii/weaveflow?logo=github" alt="Release"></a>
  <a href="https://codecov.io/gh/dengzii/weaveflow"><img src="https://codecov.io/gh/dengzii/weaveflow/branch/master/graph/badge.svg" alt="Codecov"></a>
  <a href="https://golangci-lint.run/"><img src="https://img.shields.io/badge/lint-golangci--lint-00ADD8" alt="golangci-lint"></a>
  <a href="https://hub.docker.com/r/dengzii/weaveflow"><img src="https://img.shields.io/badge/Docker-dengzii%2Fweaveflow-2496ED?logo=docker&logoColor=white" alt="Docker Image"></a>
</p>

<p align="center">
  <a href="https://weaveflow.space/">Website</a> ·
  <a href="https://weaveflow.space/docs/">Documentation</a> ·
  <a href="https://weaveflow.space/docs/zh/">中文文档</a> ·
  <a href="https://playground.weaveflow.space">Playground</a> ·
  <a href="https://github.com/dengzii/weaveflow">Source</a>
</p>
WeaveFlow is a Go-native graph runtime for building, executing, inspecting, and recovering LLM agent workflows.

Build workflows with explicit topology, explicit state contracts, checkpointed execution, and runtime records that remain
available for inspection after a run finishes.

- [Quick Start](#quick-start)
- [Core Model](#core-model)
- [Key Features](#key-features)
- [Graph Definition](#graph-definition)
- [Debug Server and Workbench](#debug-server-and-workbench)
- [Container Deployment](#container-deployment)
- [Examples](#examples)
- [Agent Skills](#agent-skills)

Agent workflows become difficult to understand when topology, state access, and control flow are hidden inside an
implicit loop. WeaveFlow keeps those boundaries explicit:

- **Explicit:** Graph topology, conditions, state paths, and runtime policies are serializable and reviewable.
- **Contract-driven:** Nodes and conditions declare their state access through State Ports; missing or conflicting
  bindings can fail during graph construction.
- **Recoverable:** Runs persist state and checkpoints and can support pause, resume, human input, and failure paths.
- **Inspectable:** Steps, events, checkpoints, and artifacts provide evidence of what happened during execution.

WeaveFlow is intended for workflows where the execution path matters as much as the final answer.

## Core Model

```text
Graph Definition
  ├─ Nodes + Conditions
  ├─ State Modules + State Bindings
  └─ Edges + Runtime Policies
          |
          v
      Graph Session
          |
          v
          Run
  ├─ Steps
  ├─ Checkpoints
  ├─ Events
  └─ Artifacts
```

- **Graph:** Describes nodes, edges, conditions, state modules, and execution boundaries.
- **Node:** Performs one explicit action, such as a model call, tool execution, state transformation, or approval gate.
- **State:** Structured data passed between nodes; access paths are declared through bindings.
- **Graph Session:** An immutable, build-validated version of a graph used to start runs.
- **Run:** One actual execution with lifecycle state and runtime records.
- **Checkpoint:** A persisted state and control boundary that can be used for recovery or resume.
- **Event:** A structured record emitted during execution for debugging and external consumers.
- **Artifact:** A persisted output or debugging item produced by a node or run.

## What You Can Build

| Scenario | Runtime capability | Example |
|---|---|---|
| Agent loop | LLM calls, tool execution, conditions, and conversation state | `examples/graph/` |
| Structured workflow | Serial nodes, conditional routing, and fallback branches | `conditional_routing.go` |
| Parallel workflow | Fan-out, fan-in, reducers, and wave checkpoints | `fan_in_fan_out.go`, `dynamic_map_reduce.go` |
| Failure handling | Classified errors, failure routes, and fallback results | `failure_fallback.go` |
| Human intervention | Suspend, checkpoint-backed resume, and dynamic control | `human_approval.go` |
| Multi-agent workflow | Supervisor routing, handoff, and isolated conversation roots | `supervisor_mode/`, `two_agent_handoff/` |
These examples demonstrate the current runtime and are not a claim that WeaveFlow is already a complete distributed
production control plane.

## Key Features

### Declarative and serializable graphs

Define workflows as nodes, edges, conditions, state modules, and runtime policies instead of hiding control flow inside
application callbacks. Graph Definitions can be stored as JSON, reviewed independently from execution, loaded by the Go
runtime, and edited through the Workbench.

### Explicit state contracts

State Ports describe what a node or condition reads and writes. Bindings keep state paths separate from component
configuration, while capability roots provide reusable views for conversations, plans, execution state, and supervisor
protocols. Required paths, reserved sections, schema mismatches, missing producers, and parallel write conflicts can be
detected before a run starts.

### Deterministic execution and control flow

The runtime supports ordinary edges, conditional routing, structured route decisions, bounded parallel waves, fan-out and
fan-in, reducers, and dynamic control commands. Nodes can request `Goto`, `Send`, `Suspend`, or `Return`, allowing a graph
to express retries, approvals, dynamic task lists, and explicit early results without introducing an opaque orchestration
loop.

### Checkpointed pause and recovery

Runs record state at meaningful execution boundaries, including node, wave, and final checkpoints. A paused workflow can
resume with new user input or a restored checkpoint, and the runtime validates graph identity and state contracts before
continuing. This makes human approval and failure recovery part of the workflow model rather than application-specific
glue code.

### Runtime observability

Run, Step, Event, Checkpoint, and Artifact records are first-class runtime data. Events cover node execution, model calls,
tool calls, state changes, routing, checkpoints, warnings, failures, and nested execution. Applications can consume these
records through the runtime APIs, while the Debug Server and Workbench provide an interactive inspection path.

### Model and tool integration

Built-in nodes cover model turns, conversation messages, tool execution, planning, replanning, verification, routing, and
user input. The runtime includes an OpenAI-compatible adapter, bundled local tools, structured model output support, and
explicit model and tool configuration through the registry and Graph Settings.

### Failure routes and bounded execution

Execution failures can be classified and routed to dedicated fallback nodes. Runtime policies provide limits for execution
time, state size, concurrency, and related resource usage, so failure behavior and operational boundaries remain visible
in the graph and in runtime records.

### Local-first debugging workflow

The Debug Server provides graph upload, immutable session creation, run control, event streaming, state and checkpoint
inspection, artifact access, and trigger-backed execution. The Workbench combines graph editing with run diagnostics so a
developer can move from a definition change to an observed execution without building a separate control plane.

## Quick Start

### Requirements

- Go `1.26` or newer.
- Bun for the WebUI development workflow.
- An OpenAI-compatible endpoint only for model-driven examples.

### Build from source

```bash
git clone https://github.com/dengzii/weaveflow.git
cd weaveflow
go build ./...
```

Use WeaveFlow as a Go module dependency:

```bash
go get github.com/dengzii/weaveflow
```

For more information, see [Getting started](https://weaveflow.space/docs/getting-started.html),
[Documentation](https://weaveflow.space/docs/), [Contributing](CONTRIBUTING.md), and [License](LICENSE).

### Run a model-free example

Start with these examples to understand graph execution without credentials or external services:

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/dynamic_map_reduce.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
go run ./examples/graph/fan_in_fan_out.go
```

They cover conditional routing, dynamic fan-out and reduction, classified failure handling, human approval and resume,
and static fan-in/fan-out.

### Run a model-driven example

Configure the OpenAI-compatible endpoint used by the examples:

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>
```

Then run the basic agent workflow:

```bash
go run ./examples/graph
```

Other workflows cover planning, supervisor routing, agent handoff, multiple LLM turns, and shared tool loops. See
[`examples/README.md`](examples/README.md) for the complete catalog.

The persisted example data is written under `.local/instance/`. Keep generated local data out of commits.

### Embed a graph in Go

The embedding path is intentionally small:

```go
workflow, err := weaveflow.LoadGraphFromFile("graph.json")
runner, err := weaveflow.NewLocalRunner(workflow, ".local/instance")
_, _, err = runner.Start(ctx, initialState)
```

The complete error handling, registry setup, model context, and initial state are shown in the runnable examples under
[`examples/graph/`](examples/graph/) and [`examples/node/`](examples/node/).

## Graph Definition

Graph Definition files are serializable JSON documents. The smallest useful shape is:

```json
{
  "version": "1.0",
  "name": "two_step",
  "state_modules": [{"name": "weaveflow.protocols", "version": "1"}],
  "entry_point": "input",
  "nodes": [{
    "id": "llm",
    "type": "llm_turn",
    "config": {"model_id": "default"},
    "state": {"conversation": {"path": "scopes.first.conversation"}}
  }],
  "edges": [{"from": "input", "to": "llm"}]
}
```

The most important contract is:

> State paths belong in component `state` bindings, not in component `config`.

State binding rules:

- Every Graph Definition declares its State Modules.
- Node and Condition State Ports describe required reads, writes, schemas, capabilities, and merge behavior.
- Capability ports bind a capability root and expand into a concrete state contract.
- Required bindings, reserved paths, schema conflicts, missing producers, and parallel write conflicts are validated
  while building the graph.
- Registry metadata determines which node types, conditions, tools, models, and schemas are available in the current
  environment.

For the complete scenario catalog and definition examples, see [`examples/README.md`](examples/README.md),
[`examples/dsl/`](examples/dsl/), and [`examples/state_operations/`](examples/state_operations/).

## Debug Server and Workbench

`cmd/server` exposes the graph debugging API used by the local WebUI. It supports graph upload and session management,
run control, live events, checkpoints, state inspection, and artifacts.

Start the API server:

```bash
go run ./cmd/server -data .local/server
```

Preload a Graph Definition:

```bash
go run ./cmd/server \
  -data .local/server \
  -graph examples/supervisor_mode/graph.json
```

The server listens on `127.0.0.1:8080` by default. Start the WebUI in another terminal:

```bash
cd internal/web
bun install
bun run dev
```

Open the printed development URL. The app redirects to `/app/graph`; its default backend is
`http://localhost:8080`. Change the Backend base URL under `/app/settings` when using another server or route prefix.

The Workbench provides:

- Graph editing, node and edge inspection, and registry metadata.
- Immutable Graph Session creation before execution.
- Run list, selected-run overview, events, state, checkpoints, and artifacts.
- Pause, resume, cancel, and human-input workflows.
- Graph-scoped event replay and runtime diagnostics.

For WebUI-specific development and deployment notes, see [`internal/web/README.md`](internal/web/README.md).

### Server configuration notes

- Use `-prefix /debug` to mount API routes below a path prefix.
- Use `-graph <path>` to preload a definition.
- Use `-secret-dir <path>` for file-backed secret references.
- Use `-cors-origins <origins>` when the WebUI is hosted on another origin.
- A non-loopback `-addr` requires `WEAVEFLOW_MANAGEMENT_TOKEN`.
- Keep local runtime data under `.local/` and do not commit credentials or generated records.

Configure model credentials through Graph Settings or the server-managed credential mechanism. Credentials should not be
stored in Graph Definitions, events, artifacts, logs, or API responses.

## Container Deployment

The packaged container runs the compiled WebUI and API behind one non-root Nginx process. It includes a health check,
supports a read-only root filesystem, and binds to `127.0.0.1:8080` by default. Configure
`WEAVEFLOW_MANAGEMENT_TOKEN` before exposing it beyond the local machine.

See [`scripts/README.md`](scripts/README.md) for Docker, Compose, and deployment-helper instructions. The local image
version is recorded in [`VERSION`](VERSION).

## Examples

Start from the goal that matches your task:

- **Understand runtime control:** [`examples/README.md`](examples/README.md), Runtime control.
- **Run agent workflows:** [`examples/README.md`](examples/README.md), Agent workflows.
- **Learn Graph JSON and state operations:** [`examples/dsl/`](examples/dsl/) and
  [`examples/state_operations/`](examples/state_operations/).
- **Use the Workbench:** [`internal/web/README.md`](internal/web/README.md).

## Agent Skills

The repository includes focused Agent Skills under [`.agents/skills/`](.agents/skills/) for working with WeaveFlow.
Choose the skill that matches the task so API operations and repository changes remain clearly separated:

| Skill                                                                      | Use it for                                                                                                                              | Boundary                                                                                                                 |
|----------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------|
| [`weaveflow-graph-create`](.agents/skills/weaveflow-graph-create/SKILL.md) | Understanding, authoring, validating, installing, and configuring Graph Definitions, Sessions, settings, Chat, and Triggers             | Uses only the public Debug Server HTTP API and bundled references; it does not inspect source code                       |
| [`weaveflow-graph-debug`](.agents/skills/weaveflow-graph-debug/SKILL.md)   | Reconstructing Run context, inspecting persisted evidence, diagnosing failures or pauses, and performing explicitly authorized recovery | Uses only the public Debug Server HTTP API and the exact historical Graph Session; investigation is read-only by default |
| [`weaveflow-graph-code`](.agents/skills/weaveflow-graph-code/SKILL.md)     | Implementing, reviewing, and validating repository source or documentation changes                                                      | Works in the repository and does not create Sessions, start Runs, or mutate live runtime data                            |

Use `weaveflow-graph-create` before a Run, hand the resulting Graph ID, Session ID, and Run ID to
`weaveflow-graph-debug` for runtime diagnosis, and use `weaveflow-graph-code` only when the remedy requires a repository
change. Agents that support repository-local skills can load these definitions directly from the checkout.

## Development

Prefer focused checks for the packages and files changed:

```bash
gofmt -d <changed.go files>
go vet ./path/to/changed/package
go test ./path/to/changed/package -run TestName -count=1
```

For WebUI changes:

```bash
cd internal/web
bun test path/to/changed.test.ts
bun run build
```

The project is actively evolving. Keep package boundaries focused, preserve Graph Definition state-binding contracts,
and update the relevant example or documentation when a public workflow changes.

## Project Status

WeaveFlow is under active development. The graph model, state contracts, execution runtime, local persistence, run
inspection, and example workflows are usable for experimentation and non-trivial local applications. Public APIs,
server surfaces, and production-hardening boundaries may continue to evolve.

The current project should not be read as a claim that it already provides:

- A complete multi-tenant authorization, quota, audit, and production control plane.
- Cross-worker durable execution and automatic failure takeover.
- A hosted cloud service, online Playground, or commercial SLA.

## License

MIT. See [`LICENSE`](LICENSE).
