# WeaveFlow

[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**WeaveFlow** is a graph-based runtime for building, running, and debugging LLM agents in Go.

It provides a declarative graph DSL, a deterministic execution engine with checkpoint and resume, and a curated set of nodes for LLM calls, tool execution, planning, memory, and human-in-the-loop control. The runtime is local-first: every step is persisted, replayable, and inspectable.

---

## Highlights

- **Graph-based orchestration**: describe an agent as a directed graph of nodes and conditional edges, serializable to JSON.
- **Deterministic runtime**: every node execution emits events and writes checkpoints; runs can be paused, resumed, and replayed.
- **State contracts**: nodes declare which state fields they read and write; contracts are validated at build time.
- **Rich node library**: LLM call, tool call, planner/replanner, plan-step executor, verifier, intent analyzer, orchestration router, memory recall/write, iterator, mapped subgraph, approval gate, cost budget guard, and more.
- **Built-in agent server**: `cmd/neo` ships a Gin-based HTTP server with a web UI, live event stream, and run replay.
- **Safety & observability**: checkpointed execution, replay, and structured runtime artifacts for inspection.

---

## Installation

Requires Go **1.26** or later.

```bash
go get weaveflow
```

Or clone and build from source:

```bash
git clone <repo-url> weaveflow
cd weaveflow
go build ./...
```

---

## Quick Start

Set credentials for an OpenAI-compatible endpoint and run the ReAct example:

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>

go run ./examples/graph
```

The example builds a ReAct-style agent (human input -> LLM -> tool calls -> final answer), persists the graph to `.local/instance/graph.json`, writes checkpoints to `.local/instance/checkpoints/`, and then demonstrates resuming from a checkpoint with new human input.

### Building a graph in code

```go
g := weaveflow.NewGraph()

human := nodes.NewHumanMessageNode()
llm   := nodes.NewLLMNode()
tool  := nodes.NewToolCallNode()

_ = g.AddNode(human)
_ = g.AddNode(llm)
_ = g.AddNode(tool)

_ = g.AddEdge(human.ID(), llm.ID())
_ = g.AddConditionalEdge(llm.ID(), tool.ID(), builtin.LastMessageHasToolCalls(llm.StateScope))
_ = g.AddEdge(tool.ID(), llm.ID())
_ = g.AddConditionalEdge(llm.ID(), weaveflow.EndNodeRef, builtin.HasFinalAnswer(llm.StateScope))

_ = g.SetEntryPoint(human.ID())
```

### Loading a graph from JSON

```go
graph, err := weaveflow.LoadGraphFromFile(&builder.BuildContext{}, "graph.json")
```

---

## Architecture

```text
+------------------------------------------------------------------+
|                           DSL (dsl/)                             |
|        Graph definitions, node specs, state contracts            |
+------------------------------------------------------------------+
                                |
+------------------------------------------------------------------+
|                        Builder (builder/)                         |
|      Resolves registry refs, validates contracts, builds Graph    |
+------------------------------------------------------------------+
                                |
+------------------------------------------------------------------+
|                         Graph (graph/)                            |
|           Nodes, edges, conditional routing, topology            |
+------------------------------------------------------------------+
                                |
+------------------------------------------------------------------+
|                        Runtime (runtime/)                         |
|   GraphRunner -> ExecutionStore -> CheckpointStore -> EventSink   |
|            ArtifactStore -> LLM wrapping -> Contract policy       |
+------------------------------------------------------------------+
                                |
+--------------+---------------+---------------+-------------------+
|   nodes/     |   builtin/    |    tools/     | llms/ -> memory/  |
| LLM, Tool,   | Conditions,   | AskQuestion,  | OpenAI client,    |
| Planner,     | Conversation  | file, web,    | BM25 retriever,   |
| Verifier,    | helpers,      | bash, ...     | file/in-memory    |
| Iterator,    | Memory,       |               | repositories      |
| Router, ...  | Safety, ...   |               |                   |
+--------------+---------------+---------------+-------------------+
```

### Core packages

| Package | Responsibility |
| --- | --- |
| `core/` | Core interfaces: `Node`, `Services`, contracts, state primitives. |
| `dsl/` | Serializable graph definition, node specs, state contracts. |
| `builder/` | Builds runnable `Graph` from a DSL definition plus registry. |
| `graph/` | Graph topology, conditional edges, contract analysis. |
| `runtime/` | Execution engine: checkpoints, events, artifacts, contract policy. |
| `state/` | Scoped state with typed paths, merge strategies, conversation helpers, snapshots. |
| `registry/` | Node and condition registration, instance configuration. |
| `nodes/` | Production-ready node implementations. |
| `builtin/` | Built-in conditions, conversation/memory helpers, safety primitives. |
| `tools/` | Tool interface and out-of-the-box tools. |
| `llms/openai/` | OpenAI-compatible client adapter. |
| `memory/` | Memory manager, repositories, retrievers. |
| `internal/neo/` | Agent server: chat, history, replay, live events. |
| `cmd/neo/` | Standalone server binary. |

---

## The Neo Agent Server

`cmd/neo` is a reference application: a chat agent with persistent history, a live event stream, and a replay viewer.

```bash
go run ./cmd/neo --addr :9090 --data .local/neo
```

Then open `http://127.0.0.1:9090/neo/`.

Endpoints are registered under `/neo` (chat, history, registry, live hub) and `/api` (replay).

---

## Examples

| Path | What it shows |
| --- | --- |
| `examples/graph/` | End-to-end ReAct agent with checkpoint and resume. |
| `examples/dsl/` | Building a graph through the DSL. |
| `examples/node/` | Focused, runnable demos for each major node type. |
| `examples/llama_cpp/` | Running graphs against a local `llama.cpp` model. |

---

## Testing

```bash
go test ./...
```

Unit tests cover state merging, contract validation, the runner store, and most node implementations.

---

## Status

WeaveFlow is under active development. The kernel - DSL, builder, graph, runtime, state - is stable enough to build non-trivial agents on top of. The HTTP surface in `internal/neo/` and some advanced node capabilities are still evolving.

---

## License

MIT - see [LICENSE](LICENSE).
