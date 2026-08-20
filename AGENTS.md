# AGENTS.md

Guidance for working in the WeaveFlow repository: a graph-based runtime for building, executing, and inspecting LLM agents in Go. Current version: `0.1.0` (see `VERSION`). Module path: `github.com/dengzii/weaveflow`.

## Essential commands

Run everything from the repository root.

```bash
go build ./...                 # build all packages
go test ./...                  # run all Go tests (CI uses -coverprofile -covermode=atomic)
go vet ./...                   # static checks (CI runs this)
test -z "$(gofmt -l .)"        # CI-enforced formatting gate: gofmt must be clean
go run ./examples/graph        # model-based example (needs OPENAI_API_KEY/BASE_URL/MODEL)
go run ./examples/graph/conditional_routing.go   # model-free examples; run by file, see below
```

Web UI (`internal/web`, React + Bun, NOT npm):

```bash
cd internal/web
bun install --frozen-lockfile
bun test
bun run build                  # outputs to internal/web/dist (gitignored, never commit)
bun run dev                    # build.ts --watch
```

Debug server (`cmd/server`):

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server
```

Container:

```bash
docker build --build-arg VERSION=0.1.0 -t weaveflow:0.1.0 -f scripts/Dockerfile .
```

Go toolchain is pinned via `go.mod` (`go 1.26.1`); Bun is pinned to `1.3.4` in CI. There is no Makefile. CI runs on pushes/PRs against `master` (`.github/workflows/ci.yml`).

## Git and GitHub operations

- Use `origin` (`git@github.com:lazy-banana/weaveflow.git`) for all Git fetch, pull, branch, commit, and push operations.
- Use `origin`'s repository (`lazy-banana/weaveflow`) for all `gh` operations, including workflow dispatches, run inspection, issues, and pull requests; pass `--repo lazy-banana/weaveflow` when repository auto-detection could resolve `upstream`.
- Treat `upstream` (`https://github.com/dengzii/weaveflow.git`) as read-only reference material unless the user explicitly requests otherwise.

## Repository layout

| Path | Responsibility |
|---|---|
| `weaveflow.go` | High-level facade: `NewGraph`, `BuildGraph`, `LoadGraphFromFile`, `NewRunner`, `NewLocalRunner` (file-backed), options. |
| `core/` | `Node` interface, `NodeBase`/`NodeSpec`, `Context` (model/tool/env injection), `Tool`, `ExecutionError`/`ErrorClass`, `EffectClass`/`EffectStatus`, model execution, node contracts. Depends only on `state`, `llms`. |
| `state/` | `State` (4-section envelope), `Path`, `Ref[T]`, `Patch`, `Snapshot` + codec, `Contract`/`FieldAccess` validation, `Access` (node-facing read/write), reducers, parallel merge. |
| `dsl/` | Serializable `GraphDefinition`, `GraphNodeSpec`, `GraphEdgeSpec`, `GraphInstanceConfig`; JSON-Schema generation; semantic/snapshot hashes. JSON-only, strict decoding. |
| `graph/` | `Graph` API (`AddNode`, `AddEdge`, `AddConditionalEdge`, `AddFailureRoute`, `Compile`, `Validate`, `Run`), edge/condition resolution, lightweight in-memory scheduler, `NewGraphRunner`. |
| `runtime/` | The real execution engine: `GraphRunner` (Start/Resume/Pause/Cancel/Delete), `RunRecord` lifecycle, stores (`ExecutionStore`, `CheckpointStore`, `EventSink`, `ArtifactStore`, `TransactionStore`), leases, retention, run deletion, effect resolution. |
| `registry/` | `Registry` of node types, conditions, state modules, capabilities, reducers; `BuildContext`, `NodeTypeDefinition`, `ConditionDefinition`. |
| `builtin/` | `NewDefaultRegistry()` wiring: protocols state module, reducers, node types, core conditions. |
| `node/` | Production node implementations (see below) plus `node/supervisor/`, `node/plan/`, `node/stateops/`, `node/agents/` (`agent`, `claude`, `codex`). |
| `capability/` | Path-bound typed views: `conversation`, `plan`, `supervisor`, `execution`; `chat` is a channel-neutral reply protocol (not a state capability). |
| `tools/` | Bundled tools (see below). |
| `llms/` | `Model` interface, message types; `llms/openai/` OpenAI-compatible adapter. |
| `internal/` | Server API (`server/`), file runtime store (`runtimestore/file/`), Web UI (`web/`), config helpers, stateexpr (CEL), trigger, chatchannel, memory (unassembled), website. |
| `examples/` | Runnable examples; `examples/graph/*.go` carry an `//go:build ignore` tag so they are NOT compiled by `go test ./...` — run them by file with `go run`. |
| `scripts/` | Dockerfile, compose.yaml, deploy.sh, nginx/web config templates. |

`internal/web/dist` and `.local/` are generated; never commit them.

## Architecture and execution flow

1. **Define**: `dsl.GraphDefinition` (versioned `"1.0"` JSON; `entry_point`, optional `finish_point`, `nodes` with `state` bindings, `edges`, `policy`). State paths live in component `state` bindings, never in `config`. Edge `to` may be `"__end__"` (`dsl.EndNodeRef`).
2. **Resolve**: `registry.Registry` maps type IDs to `NodeTypeDefinition`s whose `Build func(*BuildContext, ResolvedNodeSpec) (core.Node, error)` resolves state-port bindings to concrete `state.Path`s. `BuildContext` carries `InstanceConfig`, `GraphResolver`, `ChildRunBuilder`, and `OnContractDiagnostic`.
3. **Validate**: build-time contract analysis checks node/condition read-write contracts against declared state fields (`graph.Validate`, `internal/graphbuild/`). Contract problems surface as `core.ContractDiagnostic` and can fail the build.
4. **Run**: `runtime.GraphRunner` (created via `weaveflow.NewRunner` or `NewLocalRunner`) executes node waves, resolves edges/conditions after each node, persists checkpoints at node/wave boundaries, and emits events. Cooperative `Pause`/`Cancel` take effect at the next checkpoint boundary. Runs are resumable from checkpoints.

The `state.State` root always has four sections: `shared` (business state), `scopes` (per-node/agent namespaces), `internal` and `runtime` (reserved for framework internals). Paths are dotted strings, e.g. `shared.request.input`, `scopes.{node_id}.conversation`. Build paths with `state.Shared(...)`, `state.Scope(scope, ...)`, `state.Internal(...)`, `state.Runtime(...)` — never string-concatenate segments containing `.` (segments must not contain `.`).

## Node implementation pattern

Nodes implement `core.Node` (`ID()`, `Name()`, `Description()`, `Execute(ctx core.Context, access *state.Access) (core.NodeResult, error)`). Convention:

- Struct embeds `Base` (`core.NodeBase`) in `node/` (aliased as `Base`/`Spec`/`Option`/`NewBase`), or `core.NodeBase` directly in subpackages.
- Constructor: `New<X>Node(options ...Option)`, builds a struct with defaults, applies options, then calls `ApplyDefaultStatePaths(target)` to fill empty `state.Path` fields.
- `Execute` delegates to a private `execute(ctx, access) error` that mutates state through the editing `*state.Access` (`state.Get/Replace/Append/Merge/Delete`, typed `Ref[T]` helpers) and returns `core.NodeResult{}`.
- Each registered node implements `Validate() error`, `GraphNodeSpec() dsl.GraphNodeSpec`, `Contract() state.Contract`, and `<X>NodeTypeDefinition() registry.NodeTypeDefinition`.
- **Gotcha**: a node that mutates the access AND returns a non-empty `NodeResult.Patch` fails with an error (`core.ExecuteNode`). Mutation via Access is the standard path; `Patch` is for non-mutating/declarative returns.

Registered node type IDs (`node/spec.go` + subpackages): `subgraph`, `user_input`, `conversation_message`, `context_reducer`, `llm_turn`, `text_generation`, `tool_execution`, `environment_context`, `explore_agent`, `chat_reply`; `state_set`, `state_copy`, `state_delete`, `state_merge`, `state_append`, `state_transform` (node/stateops); `supervisor`, `supervisor_worker`, `supervisor_synthesis`; `plan_generator`, `plan_step`, `plan_review`, `plan_synthesis`; `agent`, `claude`, `codex` (process-runner nodes with `*_unix.go`/`*_windows.go` files). `set_final_answer` and `FuncNode` are NOT registered (programmatic use only).

## Registration

Registration is **explicit** (no `init()` magic):

- `node.RegisterCoreNodeTypes(r *registry.Registry)` registers main-package nodes into groups (`"Input & Context"`, `"Model & Tools"`, `"Agents"`, `"Orchestration"`, `"Output"`, `"State"`).
- Subpackages expose their own `RegisterNodeTypes` (`supervisor`, `plan`, `agents`) and stateops exposes `stateops.NodeTypeDefinitions()`.
- `builtin.NewDefaultRegistry()` assembles everything: registry → `RegisterDefaultComponents` → protocols state module (`weaveflow.protocols` v1, fields like `shared.request.input`, `shared.final.answer`) → reducers (`sum.v1`, `max.v1`, `messages.v1` — reducer IDs must match `^[A-Za-z0-9][A-Za-z0-9_.-]*\.v[1-9][0-9]*$`) → node types → core conditions.
- Duplicate type IDs, nil `Build`, or missing `StatePorts` are hard registration errors.
- Built-in conditions: `conversation_has_tool_calls`, `conversation_has_final_answer`, `expression_conditions`, `state_expression` (CEL over `inputs.<alias>`), plus `supervisor_route_equals` and `plan_status_equals` from the node subpackages. `supervisor` reserves the member ID `__finish__`.

## State contracts and ports

- `state.Contract{Fields []state.FieldAccess, WildcardRead, WildcardWrite}` declares read/write paths per node. `FieldAccess{Path, Mode (read/write/read_write), Required, Merge, Reducer, Type, Schema}`.
- Ports on `NodeTypeDefinition.StatePorts` (`dsl.StatePortDefinition{Name, Description, DefaultPath, Required, Schema, Mode, MergeStrategy, Reducer, Capability, Contract}`) describe the node's state contract to the DSL/UI; `state_ports.go` centralizes default paths (`shared.request.input`, `shared.final.answer`, `shared.environment`, etc.). Keep `defaultPrimitivePath`/`defaultCapabilityPath` in `state_ports.go` and `defaultNodeStatePath` in `spec.go` in sync when adding node types.
- **Gotchas**:
  - Contract write grants are EXACT path matches — declaring `shared.foo` does not allow writing `shared.foo.bar`.
  - `internal`/`runtime` sections are reserved; contracts and patches cannot touch them. Wildcard reads project only `shared` + `scopes`.
  - Missing required reads on conditional edges fail routing with a contract violation error.
- `capability/` packages expose `Bind(access *state.Access, root state.Path) (*View, error)` views (e.g. `conversationcap.Bind`), capability IDs like `weaveflow.conversation.v1`, `weaveflow.plan.v1`, `weaveflow.supervisor.v1`, `weaveflow.execution.v1`.

## Error and effect model

- `core.ExecutionError` interface: `Class() core.ErrorClass`, `RetryAfter()`, `Details()`; construct with `core.NewExecutionError(class, msg, cause, details)` (returns `ClassifiedError`). Error classes: `unknown`, `invalid_input`, `timeout`, `canceled`, `rate_limited`, `unavailable`, `permission_denied`, `side_effect_failed`, `resource_exhausted`, `non_retryable`. Only `timeout`, `rate_limited`, `unavailable` are retryable by default (`IsRetryableErrorClass`).
- Retries are orchestrated by the scheduler (`graph/scheduler.go`), NOT inside node/model code. Defaults: 1 attempt, exponential backoff (base 1s, multiplier 2, max 30s, jitter 0.2). Never retry side-effectful (non-idempotent/compensatable) operations.
- Effect classes: `pure`, `read_only`, `idempotent_write`, `non_idempotent_write`, `compensatable`; effect statuses: `intent`, `succeeded`, `failed`, `unknown`, `not_applied`, `compensated`. Effect intents/outcomes are journaled as committed transactions with stable operation keys; `ResolveEffect` handles `confirm_not_applied`/`compensate` resolutions on failed runs.

## Tools

`core.Tool` is a struct: `{Function *llms.FunctionDefinition, Handler, ExecutionMode (leaf|composite), Permissions []string, Approval (never|required), Effect EffectClass}`. `core.ExecuteTool` validates args, enforces permissions (from `core.WithToolPermissions` context), and gates approval via `core.WithToolApprover`. Execution stages emit `ToolExecutionEvent`s.

Bundled tools (`tools/`, installed by `defaultTools()` in `cmd/server/main.go`): `bash` (approval required, `process.execute`), `calculator`, `current_time`, `edit`/`write` (approval required, `filesystem.write`), `read`/`grep`/`glob`/`outline`/`tree` (`filesystem.read`), `web_fetch` (`network.http`), `web_search` (`network.search`). `tools/call.go` and `tools/workspace.go` are helpers, not tools. Bash respects `WEAVEFLOW_BASH_TIMEOUT` (default 2m, max 10m), `WEAVEFLOW_BASH_ALLOWLIST`; workspace root via `WEAVEFLOW_TOOL_WORKDIR`, escape guard via `WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK`.

## LLM layer

- `llms.Model` interface: `Generate(ctx, ModelRequest) (*ModelResponse, error)`. `ModelRequest` supports chat/completion modes, tools, structured `ResponseSchema` (validated strictly; failures are non-retryable), streaming via `Stream ModelStreamHandler`.
- `llms/openai`: `openai.New(opts ...Option)`; env vars `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL`, `OPENAI_ORGANIZATION`. The env token is only honored for the default OpenAI endpoint; other base URLs require an explicit option. Providers: openai/azure/deepseek/gemini/vllm/mistral/xai/openrouter; API formats `chat_completions`/`responses`.
- Model routing inside nodes: `core.ModelByIDFromContext(ctx, id)`; empty model ID normalizes to `"default"` (see `effectiveModelID` helpers). Multiple models are injected via `core.WithModels`.

## Runtime stores and persistence

- Default in-memory store: `runtime.NewMemoryRuntimeStore()` (tests). File-backed: `internal/runtimestore/file.Open(baseDir)` — acquires an exclusive writer lock (second open fails with `ErrWriterLocked`), then creates `execution/`, `checkpoints/`, `events/`, `artifacts/`, `.transactions/` under `baseDir`. `weaveflow.NewLocalRunner(graph, baseDir)` wraps it (storage cannot be overridden there).
- `ExecutionStore` updates are optimistic CAS on `RunRecord.Revision` (retry limit 8, `ErrRunRevisionConflict`). Commits to `TransactionStore` are idempotent and fingerprint-verified; every commit for an active lease must carry an `ExecutionLeaseGuard` or it fails (`ErrExecutionLeaseLost`). Lease TTL 30s, heartbeat 10s (`WithExecutionLeasePolicy`).
- `MemoryEventSink` (and `LoggerEventSink`) drop streaming events (`EventLLMContentChunk`, `EventLLMReasoningChunk`) — they are in-process only.
- Run statuses: `pending`, `running`, `paused`, `failed`, `completed`, `canceled`. Deletion requires the run stopped plus fencer support on both transaction and artifact stores; manifests are replayed by `ReconcileRunDeletions`. Retention only runs when `WithRunRetention` is set (requires a `RunDeleter` AND a `RetentionAuditSink`).
- Snapshot codec version is `"state-v2"` and strict: Encode/Decode reject version mismatches. JSON decoding always uses `json.Number` (avoids float drift); JSON shapes are preserved, not arbitrary Go types — capability views convert explicitly.

## Debug server and web UI

`cmd/server` flags: `-addr` (default `127.0.0.1:8080`), `-data` (default `.local/server`), `-prefix`, `-graph` (preload a graph JSON), `-log-level`. **Non-loopback listen address requires `WEAVEFLOW_MANAGEMENT_TOKEN` env** (fatal otherwise). The server mounts gin routes from `internal/server/routes.go` (registry, graphs, sessions, runs, triggers, chat-channels, SSE events; `GET /healthz` is unauthenticated). Claude/Codex process runners are configured from environment via `RunnerConfigFromEnvironment()`.

Before doing HTTP/API work with the debug server (registry discovery, graph upload, sessions, runs, SSE, debugging failed runs), read the project skill at `.agents/skills/weaveflow-graph/SKILL.md` and its `references/` (server-api.md, graph-definition.md, debugging.md). Notable server behaviors: session creation is the settings-validation boundary and immediately makes the session trigger-visible; the server keeps the latest five complete sessions per graph; session settings persist locally with credentials redacted; never start/stop the server just for validation.

Web UI: React 19 + Tailwind 4 + `@xyflow/react`, built with Bun (`bun run build.ts`); the built `dist` is embedded in the container.

## Testing conventions

- Table-driven tests co-located with sources (`*_test.go`), heavy use of `MemoryRuntimeStore` and `NewLocalRunner` with temp dirs; runtime behavior tests cover leases, revision conflicts, pause/cancel races, deletion reconciliation, retention.
- `weaveflow_test.go` and `example_test.go` use Go `Example` functions whose `// Output:` comments are asserted by `go test`.
- `examples/graph/*.go` use `//go:build ignore` so independent `main`s don't collide and stay out of `go test ./...`.
- CI additionally runs cross-compile builds (`CGO_ENABLED=0` for linux/darwin/windows × amd64/arm64), so keep files buildable per-OS (see `process_unix.go`/`process_windows.go`, `durability_unix.go`/`durability_windows.go` splits).

## Gotchas summary

- CI fails on any unformatted Go file — run `gofmt` before pushing.
- State bindings go in `state:` on nodes/conditions, never in `config:` (config holds static node settings; runtime-bound values belong in `GraphInstanceConfig`).
- `internal/` and `runtime/` state sections are reserved and inaccessible to nodes.
- Node IDs default to type/name via `ensureNodeID`; scoped-path owners replace `.` with `_` in node IDs.
- `stateops` nodes validate config keys strictly (unknown keys rejected); `state_transform` uses a restricted CEL (`stateexpr`).
- Edge cannot declare both `condition` and `failure`; failure routes stage by `node`/`condition`.
- When you add a node type, wire it through: constructor + `ApplyDefaultStatePaths` + `NodeTypeDefinition` + `RegisterNodeTypes` + defaults in `state_ports.go`/`spec.go` — then register it in `builtin` if it should ship in the default registry.
