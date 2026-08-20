# Repository Map

Use current source, tests, and applicable `AGENTS.md` files as the authority. This map is navigation guidance, not a substitute for reading the affected code.

## Main Layers

- `core/`: shared runtime contracts, errors, effects, and environment context.
- `dsl/`: serializable Graph Definition v2, registry schema, node specs, State Port declarations, and graph identity.
- `graph/`: topology compilation, routing, scheduling, waves, and execution coordination.
- `runtime/`: Runs, Steps, checkpoints, events, artifacts, persistence, leases, effect journals, and recovery.
- `state/`: state registry, accessors, ports, reducers, scopes, initial-state validation, and binding merge rules.
- `capability/`, `registry/`, `node/`, `builtin/`: capabilities, registration, node implementations, and built-ins.
- `internal/server/`: authenticated management routes, Graph Sessions, settings, Triggers, Runs, events, checkpoints, and Artifacts.
- `internal/trigger/`: Trigger validation, invocation, scheduling, concurrency, and origin propagation.
- `internal/web/`: Bun/React Workbench, Graph canvas, Inspector, Run panel, and API clients.
- `examples/`: runnable examples; `docs/`: durable design notes; `todo/`: implementation plans and acceptance criteria.

## Contract Checks

- Dynamic or custom node reads and writes need registered types, matching State Ports, and matching `GraphNodeSpec` bindings.
- State paths belong in component `state` bindings, not `config`. Keep strict initial-state validation enabled unless intentionally changing the contract.
- Parallel branches should write disjoint paths before a merge node or use the repository's declared reducer contract; deterministic patch merging matters.
- Persisted Run/Session identity includes Graph ID, Graph Session ID, graph version, semantic hash, and snapshot hash. Do not replace an immutable Session to resume an existing Run.
- Effect-aware retries require stable idempotency keys. Non-idempotent or compensatable writes must not retry automatically; unresolved effects require evidence and explicit resolution.
- A File Store is process-local persistence, not a distributed queue or multi-worker control plane. Do not claim Durable Worker behavior from single-process tests.

## Focused Validation

Use the narrowest applicable command:

- `gofmt -d <changed.go files>` for formatting inspection.
- `go test ./<changed/package> -run TestBehavior -count=1` for a named Go regression.
- `go vet ./<changed/package>` for affected Go code.
- `cd internal/web && bun test <changed.test.ts>` for a focused frontend test.
- `cd internal/web && bun run build` for a frontend bundle check.
- `git diff --check` for whitespace and `git status --short` for scope review.

Do not report server startup, live API execution, Docker builds, browser screenshots, or full-suite success unless actually performed. A focused passing gate does not make a broader failed command green.
