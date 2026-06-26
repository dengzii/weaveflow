# WeaveFlow

[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

WeaveFlow is a graph-based runtime for building, executing, and inspecting LLM agents in Go.

It combines a declarative graph DSL, a deterministic execution engine, checkpointed state, and a reusable node library
for common agent behaviors such as model calls, tool use, planning, memory, routing, and human approval. The project is
designed for local-first development: runs are persisted, resumable, and replayable.

## Why WeaveFlow

Most agent frameworks make it easy to get a demo running and hard to understand what actually happened at runtime.
WeaveFlow takes the opposite approach:

- Graphs are explicit and serializable.
- Node state access is constrained through contracts.
- Execution emits structured events and checkpoints.
- Runs can be paused, resumed, replayed, and inspected after the fact.
- The framework ships with practical building blocks instead of only low-level primitives.

This makes WeaveFlow suitable for agents that need stronger runtime control than prompt-chaining alone can provide.

## Core Capabilities

- Declarative graph DSL with JSON-serializable definitions.
- Deterministic runtime with execution stores, checkpoint stores, and event sinks.
- State contracts that validate node read/write behavior at build time.
- Built-in nodes for LLM calls, tool execution, planning, replanning, verification, routing, memory, iteration, and
  approval gates.
- Artifact persistence for debugging and replay.
- OpenAI-compatible model adapter and local `llama.cpp` integration.
- Reference server (`cmd/neo`) with chat, history, live event streaming, and replay views.

## Repository Layout

| Package         | Responsibility                                                                            |
|-----------------|-------------------------------------------------------------------------------------------|
| `core/`         | Shared node contracts, execution primitives, tool abstractions, and state-adjacent types. |
| `dsl/`          | Serializable graph definitions, node specs, and contract schemas.                         |
| `graph/`        | Topology, edge resolution, langgraph compilation, and lightweight `Graph.Run`.            |
| `runtime/`      | Run lifecycle, checkpoints, resume, events, artifacts, and runtime contract policy.       |
| `state/`        | Scoped state, snapshots, validation, merge behavior, and conversation helpers.            |
| `registry/`     | Node/condition registration, build context, and graph instance configuration.             |
| `node/`         | Production-oriented node implementations.                                                 |
| `builtin/`      | Built-in conditions, helpers, and default registry wiring for advanced use.               |
| `tools/`        | Bundled tool implementations.                                                             |
| `llms/openai/`  | OpenAI-compatible LLM adapter.                                                            |
| `memory/`       | Memory manager, repositories, and retrieval helpers.                                      |
| `cmd/neo/`      | Reference server entrypoint.                                                              |
| `internal/neo/` | Neo server implementation and replay support.                                             |

## Getting Started

### Requirements

- Go `1.26` or newer
- An OpenAI-compatible endpoint for the default examples

### Build from source

```bash
git clone https://github.com/dengzii/weaveflow.git
cd weaveflow
go build ./...
```

Install it as a Go module dependency:

```bash
go get github.com/dengzii/weaveflow
```

### Run the graph example

Set the model credentials used by `llms/openai`:

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>
```

Then run:

```bash
go run ./examples/graph
```

The example:

- builds a ReAct-style graph,
- persists the graph definition to `.local/instance/graph.json`,
- writes execution data, checkpoints, events, and artifacts under `.local/instance/`,
- demonstrates resuming a paused run with additional human input.

## Minimal Example

```go
g := weaveflow.NewGraph()

human := node.NewHumanMessageNode("")
llm := node.NewLLMNode()
tool := node.NewToolsNode()

_ = g.AddNode(human)
_ = g.AddNode(llm)
_ = g.AddNode(tool)

_ = g.AddEdge(human.ID(), llm.ID())
_ = g.AddConditionalEdge(llm.ID(), tool.ID(), weaveflow.LastMessageHasToolCalls())
_ = g.AddEdge(tool.ID(), llm.ID())
_ = g.AddEdge(llm.ID(), weaveflow.EndNodeRef)

_ = g.SetEntryPoint(human.ID())

runner, err := weaveflow.NewRunner(g)
if err != nil {
return err
}
_, finalState, err := runner.Start(context.Background(), weaveflow.NewState())
```

Load a graph definition from disk:

```go
graph, err := weaveflow.LoadGraphFromFile("graph.json")
```

Use explicit build settings when the DSL references custom node types,
conditions, graph resolvers, or instance-bound config:

```go
registry := weaveflow.NewDefaultRegistry()
graph, err := weaveflow.LoadGraphFromFile(
"graph.json",
weaveflow.WithRegistry(registry),
weaveflow.WithBuildContext(&weaveflow.BuildContext{}),
)
```

For persisted local runs, construct the runner in one call:

```go
runner, err := weaveflow.NewLocalRunner(
graph,
".local/instance",
weaveflow.WithGraphID("agent"),
weaveflow.WithGraphVersion("v1"),
)
```

## State Persistence

Checkpoint state is persisted as JSON. After decode, the runtime guarantees JSON-compatible value shapes
(`map[string]any`, `[]any`, strings, numbers, booleans, and null), plus explicit conversions owned by core accessors
such as conversation messages and record collections. `state.Read[T]` intentionally uses Go type assertions; custom
typed accessors should convert restored JSON shapes at the accessor boundary instead of relying on the snapshot codec
to reconstruct arbitrary Go structs or typed slices.

## Neo Reference Server

`cmd/neo` is the reference application shipped with the repository. It exposes a chat-oriented agent server with
persistent history, live execution events, and replay endpoints for run inspection.

Start it with:

```bash
go run ./cmd/neo --addr :9090 --data .local/neo
```

Then open `http://127.0.0.1:9090/neo/`.

Route groups:

- `/neo` for chat, history, config, memory, and registry endpoints
- `/api` for replay and live debugging endpoints

## Examples

| Path                  | Description                                              |
|-----------------------|----------------------------------------------------------|
| `examples/graph/`     | End-to-end ReAct-style agent with checkpoint and resume. |
| `examples/dsl/`       | Exports the default registry and graph JSON schema.      |
| `examples/node/`      | Focused runnable examples for individual node types.     |
| `examples/llama_cpp/` | Runs against a local `llama.cpp` model.                  |

## Development

Run the test suite:

```bash
go test ./...
```

The codebase already includes coverage around state merging, contract validation, runtime stores, Neo server behavior,
and major node implementations. Some surfaces, especially the reference server and advanced orchestration features, are
still evolving.

Package boundary notes:

- `graph.SetLogger` is a compatibility facade for `runtime.SetLogger`; runtime owns runner logging.
- DSL-built mapped subgraphs invoke a graph function, typically `Graph.Run`, through
  `MappedSubgraphNode.InvokeSubgraph`.
  They do not create an independent `GraphRunner` run, checkpoint, or artifact lifecycle unless an application supplies
  that behavior explicitly.
- Nested `GraphRunner` lifecycle support is not part of the current graph/runtime boundary. If it is needed later,
  parent/child run linkage should be designed as a separate feature.

## Project Status

WeaveFlow is under active development. The execution kernel, state model, graph builder, and node abstractions are far
enough along for non-trivial agent workflows. Public APIs and higher-level application surfaces should still be treated
as moving parts.

## License

MIT. See [LICENSE](LICENSE).
