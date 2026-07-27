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
- Node and condition state access is resolved from explicit path bindings and constrained through contracts.
- Execution emits structured events and checkpoints.
- Runs can be paused, resumed, replayed, and inspected after the fact.
- The framework ships with practical building blocks instead of only low-level primitives.

This makes WeaveFlow suitable for agents that need stronger runtime control than prompt-chaining alone can provide.

## Core Capabilities

- Declarative Graph Definition v2 with explicit State Module dependencies and path bindings.
- Deterministic runtime with execution stores, checkpoint stores, and event sinks.
- State Ports and capability contracts that validate node/condition read-write behavior at build time.
- Built-in nodes for LLM calls, tool execution, planning, replanning, verification, routing, memory, iteration, and
  approval gates.
- Artifact persistence for debugging and replay.
- OpenAI-compatible model adapter and local `llama.cpp` integration.
- Debug server (`cmd/server`) and web UI for graph upload, runs, live events, checkpoints, and artifacts.

## Repository Layout

| Package            | Responsibility                                                                            |
|--------------------|-------------------------------------------------------------------------------------------|
| `core/`            | Shared node contracts, execution primitives, tool abstractions, and state-adjacent types. |
| `dsl/`             | Serializable graph definitions, node specs, and contract schemas.                         |
| `graph/`           | Topology, edge resolution, langgraph compilation, and lightweight `Graph.Run`.            |
| `runtime/`         | Run lifecycle, checkpoints, resume, events, artifacts, and runtime contract policy.       |
| `state/`           | Paths, typed refs, snapshots, validation, projection, patches, and merge behavior.        |
| `capability/`      | Path-bound typed views for conversation, plan, supervisor, and execution protocols.       |
| `registry/`        | Node, condition, State Module, capability metadata, and build wiring.                     |
| `node/`            | Production-oriented node implementations.                                                 |
| `builtin/`         | Built-in conditions, helpers, and default registry wiring for advanced use.               |
| `tools/`           | Bundled tool implementations.                                                             |
| `llms/openai/`     | OpenAI-compatible LLM adapter.                                                            |
| `memory/`          | Memory manager, repositories, and retrieval helpers.                                      |
| `cmd/server/`      | Graph debugging server entrypoint.                                                        |
| `internal/server/` | Server API implementation for graph upload, runs, events, checkpoints, and artifacts.     |
| `internal/web/`    | Debug web UI for editing graphs and inspecting runs.                                      |

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

Run the plan-mode example with an optional objective:

```bash
go run ./examples/plan_mode "Calculate 125 * 48 and verify the result."
```

Run the supervisor-mode example to route work between qualitative and quantitative specialists:

```bash
go run ./examples/supervisor_mode "Compare saving 250 per month for 3 years with 400 per month for 2 years."
```

Run the explicit state-binding examples:

```bash
go run ./examples/two_agent_handoff "Research and summarize explicit state bindings."
go run ./examples/multi_llm "Draft and review an explanation of capability roots."
go run ./examples/shared_tool_loop "Use the calculator to evaluate 125 * 48."
```

The example:

- builds a ReAct-style graph,
- persists the graph definition to `.local/instance/graph.json`,
- writes execution data, checkpoints, events, and artifacts under `.local/instance/`,
- demonstrates resuming a paused run with additional human input.

## Minimal Example

```go
model, err := openai.New()
if err != nil {
return err
}
g := weaveflow.NewGraph()

conversationPath := state.Scope("agent", "conversation")

input := node.NewConversationInputNode(node.WithID("input"))
input.Content = "What is 125 * 48?"
input.ConversationPath = conversationPath

llm := node.NewLLMNode(node.WithID("llm"))
llm.ToolIDs = []string{"calculator"}
llm.ConversationPath = conversationPath
llm.OutputPath = state.Shared("final", "answer")

tool := node.NewToolsNode(node.WithID("tools"))
tool.ToolIDs = []string{"calculator"}
tool.ConversationPath = conversationPath

_ = g.AddNode(input)
_ = g.AddNode(llm)
_ = g.AddNode(tool)

_ = g.AddEdge(input.ID(), llm.ID())
_ = g.AddConditionalEdge(llm.ID(), tool.ID(), weaveflow.ConversationHasToolCalls(conversationPath))
_ = g.AddEdge(tool.ID(), llm.ID())
_ = g.AddEdge(llm.ID(), weaveflow.EndNodeRef)

_ = g.SetEntryPoint(input.ID())

runner, err := weaveflow.NewRunner(g)
if err != nil {
return err
}
ctx := core.WithModel(context.Background(), model)
ctx = core.WithTools(ctx, map[string]core.Tool{"calculator": tools.NewCalculator()})
_, finalState, err := runner.Start(ctx, weaveflow.NewState())

```

## Graph Definition v2 State Bindings

Every Graph v2 definition declares its State Modules. Every Node and Condition binds its declared State Ports at the
top-level `state` field; state paths never belong in component `config`.

Built-in state ports also declare a `default_path`. New nodes materialize those paths in their `state` bindings, and the
graph resolver applies the same defaults when a binding is omitted. A `{node_id}` token is replaced with the node ID
(with dots normalized to underscores), so conversation roots remain isolated. An explicit binding path always overrides
the default. The built-in conventions are `shared.request.input` for input/task/objective ports,
`shared.final.answer` for output/result ports, `shared.environment` for environment, shared protocol roots for plan,
execution, and supervisor capabilities, and `scopes.<node_id>.<port>` for conversation capabilities.

```json
{
  "version": "2.0",
  "name": "two_step",
  "state_modules": [
    {
      "name": "weaveflow.protocols",
      "version": "1"
    }
  ],
  "entry_point": "input",
  "finish_point": "llm",
  "nodes": [
    {
      "id": "input",
      "type": "conversation_input",
      "config": {
        "content": "hello"
      },
      "state": {
        "conversation": {
          "path": "scopes.first.conversation"
        }
      }
    },
    {
      "id": "llm",
      "type": "llm",
      "config": {
        "model_id": "default"
      },
      "state": {
        "conversation": {
          "path": "scopes.first.conversation"
        },
        "output": {
          "path": "shared.final.answer"
        }
      }
    }
  ],
  "edges": [
    {
      "from": "input",
      "to": "llm"
    }
  ]
}
```

Primitive ports bind an exact path and carry a JSON schema plus access mode. Capability ports bind a root path and
expand their declared relative fields into the concrete state contract. Two components share protocol state only when
they bind the same capability root; isolated roots remain independent. Required bindings, reserved sections,
schema/capability conflicts, producer requirements, and deterministic parallel write conflicts fail during graph build.
Checkpoint resume validation uses a semantic hash that includes module versions, binding paths, capability IDs, and the
expanded state contracts resolved by the registry. Built-in nodes added through the direct Go graph API resolve the
same contracts, and each bound edge condition evaluates against its own projected contract view.

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
(`map[string]any`, `[]any`, strings, numbers, booleans, and null). Path-bound capability views explicitly decode the
protocol shapes they own, such as conversation messages. `state.Read[T]` intentionally uses Go type assertions;
applications should convert custom business values from their persisted JSON shape instead of expecting the snapshot
codec to reconstruct arbitrary Go structs or typed slices.

## Debug Server

`cmd/server` exposes the graph debugging API used by the local web UI. It can preload a graph definition, start and
resume runs, stream live events, and inspect persisted checkpoints, events, and artifacts.

Start the API server with:

```bash
go run ./cmd/server -addr :8080 -data .local/server
```

To preload a graph definition, including the ready-to-edit supervisor example:

```bash
go run ./cmd/server -addr :8080 -data .local/server -graph examples/supervisor_mode/graph.json
```

If `OPENAI_API_KEY` is set, model-backed nodes are enabled. The server also wires local memory and the bundled `read`,
`write`, `edit`, `glob`, `grep`, `calculator`, `current_time`, and `web_fetch` tools into the runtime context.

The API routes are mounted at the root by default. Use `-prefix /debug` to mount them under a path prefix.

Run the web UI during development:

```bash
cd internal/web
bun install
bun run dev
```

Open the printed dev-server URL; the app redirects to `/app/graph`. The WebUI connects directly to
`http://localhost:8080` by default. Change the Backend base URL under `/app/settings` to use another server; the value
may include the server route prefix, for example `http://localhost:8080/debug`.

`bun run build` creates a standalone static site in `internal/web/dist`. For deployment-wide configuration, edit the
unbundled `dist/config.js`:

```js
window.__WEAVEFLOW_CONFIG__ = {
  backendBaseUrl: "https://api.example.com/debug",
};
```

The browser setting overrides `config.js`. A separately hosted WebUI also requires its origin to be allowed by the API
server. Local WebUI origins on port `3031` are allowed by default; configure other origins explicitly:

```bash
go run ./cmd/server -addr :8080 -prefix /debug -cors-origins https://web.example.com
```

## Examples

| Path                          | Description                                                                        |
|-------------------------------|------------------------------------------------------------------------------------|
| `examples/graph/`             | End-to-end ReAct-style agent with checkpoint and resume.                           |
| `examples/plan_mode/`         | Structured planning, tool execution, replanning, and final synthesis.              |
| `examples/supervisor_mode/`   | Supervisor routing, specialist agent delegation, and final synthesis.              |
| `examples/two_agent_handoff/` | Agent result-to-task handoff with isolated conversation roots.                     |
| `examples/multi_llm/`         | Two model IDs with separate conversations and an explicit output-to-input handoff. |
| `examples/shared_tool_loop/`  | LLM, Tools, and edge Condition sharing one conversation capability root.           |
| `examples/dsl/`               | Exports the default registry and graph JSON schema.                                |
| `examples/node/`              | Focused runnable examples for individual node types.                               |

## Development

Run the test suite:

```bash
go test ./...
```

The codebase already includes coverage around state merging, contract validation, runtime stores, debug server behavior,
and major node implementations. Some surfaces, especially the debug server and advanced orchestration features, are
still evolving.

Package boundary notes:

- `runtime` owns runner logging and persisted run lifecycle.
- DSL-built subgraphs use `SubgraphNode` object-valued `input` and `output` State Ports. A child graph receives only the
  explicitly bound input snapshot; it does not inherit a parent node scope or any implicit state namespace.
- Nested `GraphRunner` lifecycle support is not part of the current graph/runtime boundary. If it is needed later,
  parent/child run linkage should be designed as a separate feature.

## Project Status

WeaveFlow is under active development. The execution kernel, state model, graph builder, and node abstractions are far
enough along for non-trivial agent workflows. Public APIs and higher-level application surfaces should still be treated
as moving parts.

## License

MIT. See [LICENSE](LICENSE).
