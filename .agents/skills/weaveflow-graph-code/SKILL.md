---
name: weaveflow-graph-code
description: Implement, review, and validate WeaveFlow repository changes across Go DSL, graph compilation, State Ports, node behavior, runtime scheduling, persistence, server handlers, WebUI Graph contracts, examples, tests, and durable design docs. Use when the requested solution changes source files or repository-local documentation. Do not use for API-level Graph Session/Trigger operations or diagnosing a live Run.
---

# WeaveFlow Graph Code

Use this skill for repository implementation and code-level diagnosis. It changes local source or documentation only; it does not call the live Graph API. Read [references/repository-map.md](references/repository-map.md) when the affected layer or contract is unclear.

## Inputs And Outputs

- **Inputs**: requested behavior, affected package or symptom, applicable repository instructions, and any permitted test or documentation scope.
- **Outputs**: focused source/doc diff, targeted validation results, explicit compatibility impact, and unresolved risks or unrelated failures.
- **Handoff**: use `weaveflow-graph-create` for an API-level Session/Trigger check; use `weaveflow-graph-debug` for live Run evidence after implementation.

## Workflow

1. Read every applicable `AGENTS.md`. If the user requests a TODO-first workflow, write the executable repository-local TODO before implementation. Treat `只查看`/inspection requests as read-only.
2. Inspect `git status`, recent history, and the real affected path before editing. Preserve unrelated tracked and untracked changes. Do not commit unless explicitly requested; never stage or remove unrelated files.
3. Locate the contract and all consumers before changing an interface. For Graph v2, inspect DSL schema, registry registration, State Ports, `GraphNodeSpec` bindings, graph compilation, runtime scheduler/checkpoints, server handlers, WebUI consumers, and adjacent tests as applicable.
4. Fix the root cause with the smallest focused change. Keep state paths in component `state` bindings, not `config`; simplify superseded compatibility code instead of adding another compatibility layer. Ask before changing package layout or introducing a new package.
5. Add a regression test only for complex or failure-prone behavior, following repository test naming and placement. Do not add tests for trivial logic or unrelated failures.
6. Format and validate narrowly: `gofmt` changed Go files, focused `go test` for changed packages/cases, `go vet` for affected Go packages, named Bun tests and `bun run build` for relevant WebUI changes. Do not start servers, watchers, examples, or other long-running processes for validation; do not run complete suites unless explicitly requested.
7. Review the final diff, `git diff --check`, and status. Check documentation and schema inventories for drift. Report what was actually tested, what was not, and any known unrelated failure.

## Contract And Safety Boundaries

- This skill does not create Sessions, replace Triggers, start Runs, or mutate live runtime data.
- Dynamic/custom node reads and writes require registered types, matching State Ports, and matching `GraphNodeSpec` bindings. Keep strict initial-state validation unless the requested behavior changes that contract.
- Parallel branches need disjoint output paths or declared reducers before merge. Effect-aware retries require stable idempotency keys; non-idempotent or compensatable writes must not retry automatically.
- Treat credentials, `.local/` runtime data, generated frontend output, and deployment targets as local or sensitive unless explicitly included in scope.
- Do not claim live-server, browser, Docker, or full-suite validation unless it actually ran.

## Report

Report the changed files, user-visible/runtime/API/DSL compatibility impact, focused checks, skipped checks, retained unrelated changes, and next handoff if live verification is needed.
