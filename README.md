<p align="center">
  <a href="https://weaveflow.space/"><img src="assets/icon.svg" width="72" alt="WeaveFlow"></a>
</p>

<h2 align="center">WeaveFlow</h2>

<p align="center">
  A Go-native graph runtime for building, running, inspecting, and recovering LLM agent workflows.
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26.6%2B-00ADD8?logo=go" alt="Go 1.26.6+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <a href="https://github.com/dengzii/weaveflow/actions"><img src="https://img.shields.io/github/actions/workflow/status/dengzii/weaveflow/ci.yml?branch=master&amp;logo=github&amp;label=CI" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/dengzii/weaveflow"><img src="https://pkg.go.dev/badge/github.com/dengzii/weaveflow.svg" alt="Go Reference"></a>
  <a href="https://github.com/dengzii/weaveflow/releases"><img src="https://img.shields.io/github/v/release/dengzii/weaveflow?logo=github" alt="Release"></a>
  <a href="https://codecov.io/gh/dengzii/weaveflow"><img src="https://codecov.io/gh/dengzii/weaveflow/branch/master/graph/badge.svg" alt="Codecov"></a>
  <a href="https://hub.docker.com/r/dengzixx/weaveflow/tags?name=0.1.0"><img src="https://img.shields.io/docker/v/dengzixx/weaveflow/0.1.0?logo=docker&amp;label=Docker" alt="Docker Image 0.1.0"></a>
</p>

<p align="center">
  <a href="https://weaveflow.space/">Website</a> ·
  <a href="https://weaveflow.space/docs/">Documentation</a> ·
  <a href="https://weaveflow.space/docs/zh/">中文文档</a> ·
  <a href="https://playground.weaveflow.space">Playground</a> ·
  <a href="https://github.com/dengzii/weaveflow">Source</a>
</p>

WeaveFlow makes workflow structure explicit: topology, state access, control flow, and runtime policies remain
serializable and reviewable instead of disappearing inside an orchestration loop. Runs preserve Steps, Events,
Checkpoints, and Artifacts so execution can be understood after it finishes.

## Quick Start

<a href="https://codespaces.new/dengzii/weaveflow?quickstart=1&amp;devcontainer_path=.devcontainer%2Fdemo%2Fdevcontainer.json"><img src="https://github.com/codespaces/badge.svg" alt="Open in GitHub Codespaces"></a>

The Codespaces button above opens a credential-free demo with a ready-to-run Graph Session. Select `codespaces_demo`,
click **Run**, and inspect its State, Steps, Events, and Checkpoints. Keep forwarded port `8080` private unless
`WEAVEFLOW_MANAGEMENT_TOKEN` is configured.

To run the same demo locally, install [Go 1.26.6+](https://go.dev/dl/) and start the bundled server and Workbench:

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server -graph examples/codespaces_demo/graph.json
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). Bun is only required when developing the WebUI.

For model-free command-line examples:

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/dynamic_map_reduce.go
go run ./examples/graph/human_approval.go
```

See [`examples/README.md`](examples/README.md) for failure handling, fan-out/fan-in, planning, supervisor routing,
agent handoff, and model-driven examples.

## Key Features

- **Declarative Graphs** — Store, review, and reuse workflows as serializable definitions with registry-backed types
  and build-time validation.
- **Explicit State Contracts** — Declare reads and writes through State Ports, schemas, bindings, capabilities, and
  reducers; detect missing producers and unsafe parallel writes before execution.
- **Powerful Control Flow** — Combine conditions, bounded parallel waves, fan-out/fan-in, and explicit `Goto`, `Send`,
  `Suspend`, and `Return` commands without hiding orchestration in an opaque loop.
- **Checkpointed Recovery** — Persist meaningful execution boundaries for pause, resume, human input, failure routing,
  and recovery after interruption.
- **First-Class Observability** — Inspect Runs, Steps, Events, State, Checkpoints, and Artifacts during execution or
  after a Run finishes.
- **Local-First Workbench** — Edit Graphs, create immutable Sessions, control Runs, and configure extensible model and
  tool registries from one debugging environment.

These capabilities support non-trivial local workflows. WeaveFlow is still evolving and does not yet claim to be a
complete multi-tenant control plane or a cross-worker durable execution service.

## Core Model

```text
Graph Definition  ->  Graph Session  ->  Run
      |                    |              |-- Steps
      |                    |              |-- Events
      |                    |              |-- Checkpoints
      |                    |              `-- Artifacts
      `-- Nodes, edges, State bindings, and runtime policies
```

- **Graph Definition** is the serializable workflow: nodes, edges, conditions, State Modules, bindings, and policies.
- **Graph Session** is an immutable, build-validated version of a Graph used to start Runs.
- **Run** is one execution with persisted lifecycle state and diagnostic records.
- **Checkpoint** captures state and control position for inspection, recovery, or resume.

## Graph Definitions

Graph Definitions are versioned JSON documents that can be stored, reviewed, loaded by the Go runtime, and edited in
the Workbench. Their most important rule is:

> State paths belong in component `state` bindings, not in component `config`.

Node and Condition State Ports declare required reads, writes, schemas, capabilities, and merge behavior. Graph building
rejects missing bindings, reserved paths, incompatible schemas, missing producers, and unsafe parallel writes before a
Run starts.

Browse [`examples/dsl/`](examples/dsl/), [`examples/state_operations/`](examples/state_operations/), and
[`examples/README.md`](examples/README.md) for complete definitions and runnable scenarios.

## Debug Server and Workbench

`cmd/server` exposes the local debugging API and serves the Workbench. Together they support Graph upload, immutable
Session creation, Run control, live Events, State inspection, Checkpoints, Artifacts, and Trigger-backed execution.

Useful server options:

- `-graph <path>` preloads a Graph Definition.
- `-prefix /debug` mounts API routes below a path prefix.
- `-secret-dir <path>` configures file-backed secret references.
- `-cors-origins <origins>` allows a separately hosted WebUI.
- A non-loopback `-addr` requires `WEAVEFLOW_MANAGEMENT_TOKEN`.

For WebUI development and deployment details, see [`internal/web/README.md`](internal/web/README.md). Keep generated
runtime data under `.local/`, and never store credentials in Graph Definitions, Events, Artifacts, logs, or API
responses.

## Agent Skills

Repository-local skills under [`.agents/skills/`](.agents/skills/) keep live API work separate from source changes:

- [`weaveflow-graph-create`](.agents/skills/weaveflow-graph-create/SKILL.md) authors, validates, installs, and
  configures
  Graph Definitions and Sessions through the public Debug Server API.
- [`weaveflow-graph-debug`](.agents/skills/weaveflow-graph-debug/SKILL.md) reconstructs Run context and diagnoses
  persisted
  evidence through the public API; investigation is read-only by default.
- [`weaveflow-graph-code`](.agents/skills/weaveflow-graph-code/SKILL.md) implements and validates repository source or
  documentation changes without mutating live runtime data.

The usual handoff is `create -> debug -> code`: create the Session, diagnose the Run, then change the repository only
when the remedy requires it.

## More

- [Getting started](https://weaveflow.space/docs/getting-started.html)
- [Container and deployment scripts](scripts/README.md)
- [Contributing](CONTRIBUTING.md)
- [Capability gap analysis](docs/weaveflow-capability-gap-analysis.md)
- [License](LICENSE)

WeaveFlow is released under the MIT License.
