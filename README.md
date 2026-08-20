# WeaveFlow

<p align="center">
  <a href="https://weaveflow.space/"><img src="assets/icon.svg" width="96" alt="WeaveFlow icon"></a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go" alt="Go 1.26+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <a href="https://github.com/dengzii/weaveflow/actions"><img src="https://img.shields.io/github/actions/workflow/status/dengzii/weaveflow/ci.yml?branch=master&logo=github&label=CI" alt="CI"></a>
  <a href="https://codecov.io/gh/dengzii/weaveflow"><img src="https://img.shields.io/codecov/c/github/dengzii/weaveflow?logo=codecov" alt="codecov"></a>
  <a href="https://pkg.go.dev/github.com/dengzii/weaveflow"><img src="https://pkg.go.dev/badge/github.com/dengzii/weaveflow.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/dengzii/weaveflow"><img src="https://goreportcard.com/badge/github.com/dengzii/weaveflow" alt="Go Report Card"></a>
  <a href="https://github.com/dengzii/weaveflow/releases"><img src="https://img.shields.io/github/v/release/dengzii/weaveflow?logo=github" alt="Release"></a>
</p>

<p align="center">
  <a href="https://weaveflow.space/">Website</a> .
  <a href="https://weaveflow.space/docs/">Documentation</a> .
  <a href="https://weaveflow.space/docs/zh/">Chinese</a> .
  <a href="https://playground.weaveflow.space">Playground</a> .
  <a href="https://github.com/dengzii/weaveflow">Source</a>
</p>

WeaveFlow is a Go-native graph runtime for building, executing, inspecting, and recovering LLM agent workflows.

Get started with [bun](https://bun.sh) and the [development server](https://github.com/dengzii/weaveflow#development):

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server -graph examples/graph/agent-simple.json
```

Then visit [http://127.0.0.1:8080](http://127.0.0.1:8080) to open the Workbench.

.
  [Getting started](https://weaveflow.space/docs/getting-started.html) .
  [Documentation](https://weaveflow.space/docs/) .
  [Contributing](CONTRIBUTING.md) .
  [License](LICENSE)

## Features

- **Graph-native execution** — nodes, edges, conditions, and built-in core node types for conversation, planning, and
  tool orchestration.
- **State-driven architecture** — typed `state.Path` bindings with access control patterns, custom reducers, and
  deterministic snapshot hashing.
- **Serialisable graphs** — portable `dsl.GraphDefinition` JSON with strict validation independently of the execution
  engine; every resolved graph carries a verifiable semantic hash.
- **Full lifecycle control** — pause, resume, cancel, inspect, and get checkpoints on any active run; runs are
  resumable from checkpoints after a crash or restart.
- **Runtime diagnostics** — live SSE event streaming for LLM calls, tool invocations, node progress, and state
  transitions; attach to a running session from the Workbench or the event API.
- **Extensible registry** — register custom node types, conditions, state reducers, and capabilities.
- **Web-based Workbench** — visual graph editor, run inspector, state browser, and session manager.

## Quick start

**Prerequisites:** [Go 1.26+](https://go.dev/dl/), [Bun 1.3+](https://bun.sh/).

```bash
# Build all packages and run tests
go build ./...
go test ./...
go vet ./...
```

Run the debug server with an example graph:

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server -graph examples/graph/agent-simple.json
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080) to explore the Workbench.

## Debug server flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `-addr` | — | `127.0.0.1:8080` | Listen address; non-loopback requires `WEAVEFLOW_MANAGEMENT_TOKEN` |
| `-data` | — | `.local/server` | Runtime data directory (writer-locked; exclusive access) |
| `-prefix` | `WEAVEFLOW_URL_PREFIX` | `""` | URL path prefix for reverse-proxy deployments |
| `-graph` | — | `""` | Preload a graph definition JSON file |
| `-secret-dir` | `WEAVEFLOW_SECRET_DIR` | `""` | File-backed secret references directory |
| `-cors-origins` | `WEAVEFLOW_CORS_ORIGINS` | `""` | Comma-separated CORS origins for the WebUI |
| `-log-level` | `WEAVEFLOW_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `-model` | `OPENAI_MODEL` | `gpt-4o` | Default LLM model ID |
| `-api-key` | `OPENAI_API_KEY` | `""` | OpenAI-compatible API key |
| `-base-url` | `OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible base URL |
| — | `WEAVEFLOW_MANAGEMENT_TOKEN` | `""` | Required when listening on non-loopback |
| — | `WEAVEFLOW_TOOL_WORKDIR` | `.` | Workspace root for tool execution |
| — | `WEAVEFLOW_BASH_TIMEOUT` | `2m` | Bash tool timeout (max 10m) |
| — | `WEAVEFLOW_BASH_ALLOWLIST` | `""` | Comma-separated allowed bash commands |

## Model configuration

Set API credentials via environment variables of through Graph Settings in the Workbench:

```bash
export OPENAI_API_KEY=sk-...
export OPENAI_MODEL=gpt-4o
export OPENAI_BASE_URL=https://api.openai.com/v1
```

Credentials configured through Graph Settings are managed server-side and are never stored in Graph Definitions, events,
artifacts, logs, or API responses.

## Graph definition format

Serialize graphs as JSON:

```json
{
  "version": "1.0",
  "state_modules": [{"name": "weaveflow.protocols", "version": "1"}],
  "entry_point": "start",
  "nodes": [
    {
      "id": "start",
      "type": "user_input",
      "config": {"prompt": "Enter your request"},
      "state": {
        "input": {"path": "shared.request.input"},
        "reply": {"path": "scopes.start.conversation"}
      }
    }
  ],
  "edges": [
    {"from": "start", "to": "__end__"}
  ]
}
```

Load and run:

```go
import (
    wf "github.com/dengzii/weaveflow"
    "github.com/dengzii/weaveflow/state"
)

func main() {
    g := wf.LoadGraphFromFile("graph.json")
    runner := wf.NewLocalRunner(g, ".local/server")
    result, _ := runner.Start(context.Background(), state.NewState())
    _ = result
}
```

## Debug server API

See `internal/server/routes.go` for all routes. Key endpoints:

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Health check (unauthenticated) |
| `GET` | `/registry/nodes` | Registered node types |
| `POST` | `/graphs/{graph_id}/sessions` | Create a graph session |
| `POST` | `/graphs/{graph_id}/runs` | Start a run |
| `POST` | `/graphs/{graph_id}/runs/{run_id}/pause` | Pause a run |
| `POST` | `/graphs/{graph_id}/runs/{run_id}/cancel` | Cancel a run |
| `GET` | `/graphs/{graph_id}/runs/{run_id}` | Get run details |
| `GET` | `/graphs/{graph_id}/runs/{run_id}/events` | SSE event stream |
| `POST` | `/graphs/{graph_id}/triggers` | Create a trigger |
| `POST` | `/graphs/{graph_id}/triggers/{trigger_id}/invoke` | Invoke a trigger |

## Project structure

| Path | Responsibility |
|---|---|
| `weaveflow.go` | High-level facade (`NewGraph`, `BuildGraph`, `LoadGraphFromFile`, `NewRunner`, `NewLocalRunner`) |
| `core/` | `Node` interface, `Context`, `Tool`, `ExecutionError`, `EffectClass` |
| `state/` | `State`, `Path`, `Ref[T]`, `Patch`, `Snapshot`, `Contract`, `Access` |
| `dsl/` | Graph definition types, JSON-Schema generation |
| `graph/` | Graph API (`AddNode`, `AddEdge`, `Compile`, `Run`) and scheduler |
| `runtime/` | Graph runner execution, lifecycle, stores, leases, retention |
| `registry/` | Registry of node types, conditions, capabilities |
| `builtin/` | Default registry assembly |
| `node/` | Node implementations |
| `tools/` | Bundled tools (bash, calculator, edit, web, etc.) |
| `llms/` | Model interface and adapters |
| `internal/` | Server API, runtime store, WebUI, config helpers |
| `examples/` | Runnable examples |

## Gotchas

- State bindings go in `state:` on nodes, never in `config:`.
- `internal/` and `runtime/` state sections are reserved and inaccessible to nodes.
- CI fails on unformatted Go; run `gofmt -l .` before pushing.
- Keep local runtime data under `.local/` and do not commit credentials.

Configure model credentials through Graph Settings or the server-managed credential mechanism. Credentials should not be
stored in Graph Definitions, events, artifacts, logs, or API responses.

## License

MIT
