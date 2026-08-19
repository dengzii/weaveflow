# Package Map

| Package | Responsibility |
| --- | --- |
| `core/` | Shared execution contracts, model and tool abstractions, and common runtime types. |
| `dsl/` | Serializable Graph Definitions, component schemas, and State Port contracts. |
| `graph/` | Topology validation, edge resolution, graph compilation, and lightweight execution. |
| `runtime/` | Run lifecycle, checkpoints, events, artifacts, recovery, and ownership policy. |
| `state/` | State paths, snapshots, patches, projection, validation, and merge behavior. |
| `capability/` | Typed, path-bound views for conversation, plan, supervisor, and execution protocols. |
| `registry/` | Node, condition, State Module, capability metadata, and build wiring. |
| `node/` | Built-in production-oriented nodes. |
| `builtin/` | Built-in conditions, helpers, and default registry assembly. |
| `tools/` | Bundled tool implementations. |
| `llms/` | Model integrations, including OpenAI-compatible endpoints. |
| `cmd/server/` | Debug server entry point. |
| `internal/server/` | Debug server API implementation. |
| `internal/web/` | Workbench Graph editor and Run debugger. |

For exported Go identifiers and signatures, use [pkg.go.dev](https://pkg.go.dev/github.com/dengzii/weaveflow) alongside
these conceptual guides.
