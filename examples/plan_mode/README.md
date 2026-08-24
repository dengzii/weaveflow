# Profile-based Plan Mode

This example builds one reusable Plan graph and configures its prompts, tools, permissions, budgets, approval policy, deterministic verifier, and optional source-grounded Critic through a `TaskProfile`. Profiles and verifiers are registered through `ProfileRegistry` and `VerifierRegistry`, so a new task family can extend the example without adding another graph topology. A step advances only after `plan_verifier` records a `passed` decision backed by tool evidence; a model's completion claim is not sufficient.

The graph routes through generator, step executor, tool executor, finalizer, verifier, reviewer, and synthesis nodes. Each profile selects which tools, verifier, permissions, and limits apply at every stage.

## Run

From the repository root, place the OpenAI-compatible provider settings in `.env`:

```dotenv
OPENAI_API_KEY=...
OPENAI_BASE_URL=https://provider.example/v1
OPENAI_MODEL=...
```

The example reads only the root `.env`, never writes credentials to Plan state or evidence, and redacts common token and Authorization forms from persisted evidence.

```bash
go run ./examples/plan_mode
go run ./examples/plan_mode -profile analysis
go run ./examples/plan_mode -profile multi-step
go run ./examples/plan_mode -profile documentation -objective "Document the selected package."
go run ./examples/plan_mode -profile coding-go -data .local/my-plan -timeout 10m
go run ./examples/plan_mode "A positional objective remains supported."
```

CLI flags:

- `-profile`: `tiny-script` (default), `coding-go`, `documentation`, `analysis`, or `multi-step`.
- `-objective`: overrides the profile's default objective; positional words provide the same override.
- `-data`: persistent runtime directory, default `.local/plan_mode`.
- `-timeout`: total run deadline; the profile default applies when omitted.

The runner assigns the stable Graph ID `examples.plan_mode.<profile>`, Graph Version `1.0`, and a unique Run ID. An interrupted or otherwise continuable run is resumed from its latest checkpoint. Verified steps and their evidence are restored instead of executed again.

## Extension

The CLI builds its built-in definitions through `defaultProfileRegistry()`. Add a task family by registering a validated `TaskProfile`; add a deterministic task verifier with `VerifierRegistry.Register`. A profile can then select the registered verifier ID and its own `AllowedPaths` without changing the Plan graph topology. Keep custom verifiers deterministic and fixed-configured: model tool calls must not be able to supply commands, packages, or paths at runtime.

## Profiles

| Profile | Mutation | Tools | Deterministic verifier | Grounded Critic |
|---|---:|---|---|---:|
| `tiny-script` | yes | repository read/write/edit plus fixed verify | `gofmt -l` and `go test ./examples/plan_mode/tiny_script/...` | no |
| `coding-go` | yes | repository read/write/edit plus fixed verify | allowlisted `go test` package pattern | no |
| `documentation` | yes | repository read/write/edit plus fixed verify | required and forbidden content patterns | yes |
| `analysis` | no | outline, bounded read, grep, glob | explicit analysis-only `no-op` with required read evidence | yes |
| `multi-step` | no | outline, bounded read, grep, glob | explicit analysis-only `no-op` with required read evidence | yes |

## Node Controls

The registered plan nodes are intentionally composable; state ports can be rebound without changing node code. Their configuration controls are:

- `plan_generator`: `model_id`, `tool_ids`, `system_prompt`, `max_steps`, `max_replans`, `verification_strategy`, `max_tokens`, `temperature`, and `thinking`.
- `plan_step`: `system_prompt`, `max_iterations`, and `prompt_max_chars`.
- `plan_verifier`: `verifier_id`, `config`, `max_attempts`, `minimum_evidence`, `max_evidence`, `allow_no_op`, `require_test_evidence`, and optional grounded Critic controls.
- `plan_review`: `max_attempts`, `retry_exhausted_action` (`replan` or `finalize`), and `failure_action` (`replan` or `finalize`).
- `plan_synthesis`: `model_id`, `system_prompt`, `require_evidence_refs`, `fail_on_incomplete`, `max_tokens`, `temperature`, and `thinking`.
- `TaskProfile.AllowedPaths`: relative workspace paths that every read, search, write, and edit tool call must remain within. An empty allowlist means the workspace root for read-only profiles; writable profiles must configure it explicitly.

Use `verification_strategy` to select a registered deterministic verifier per step. Leave it empty to let the generated step choose `evidence`, `no-op`, or another handler-supported strategy. `plan_review` only advances a step after `passed`; all other outcomes remain observable and follow the configured failure action.

`file-exists` is also available to task profiles for workspace-contained deliverables. Verifier configuration is static: models cannot add a command, package, file, or shell fragment at call time.

The Critic runs only after deterministic verification passes. It receives the objective, current step result, and enumerated evidence (`E1`, `E2`, ...), then must return strict structured claims with valid evidence refs. Unsupported claims, missing refs, or unknown refs produce `retry_step`. A Critic can therefore reject a semantically wrong conclusion, but it can never override a failed deterministic check.

The planner persists a canonical summary derived from the normalized steps instead of trusting a model-supplied step count. Every step can use the configured default verification strategy, and the final normalized step is always required to cover the objective, name an observable deliverable, and include the configured verifier in its acceptance criteria. Mutation objectives receive an available `edit` or `write` tool on that final step.

Final synthesis numbers successful evidence as `[S1:E1]`, `[S1:E2]`, and so on. Material factual claims are prompted to cite those refs; failed evidence is never treated as a valid final reference. If the answer omits a successful ref, synthesis appends an explicit evidence-reference footer, and synthesis fails when no successful evidence exists.

The `analysis` profile exposes `outline` for structural inspection and limits each `read` call to 240 lines and 12 KiB of returned text. The `documentation` profile limits each `read` call to 320 lines and 16 KiB. A missing line limit gets the profile default, and an excessive requested limit is rejected.

## Safety

- No arbitrary shell tool is exposed. Go verification uses a fixed executable and argument array.
- Tool IDs and permissions are filtered per profile. Write and process tools also require explicit profile approval.
- File access is confined to the workspace after path and symlink resolution; traversal and workspace-external verification are rejected.
- Profile allowlists further constrain tool paths to task-specific directories; omitted search roots are rejected when they would scan outside the allowlist.
- Evidence is size-limited and redacted before checkpoint persistence.
- Source-grounded Critic input is limited to the latest 12 evidence items with a shared 24 KiB evidence budget.
- Steps, replans, worker iterations, verification attempts, model retries, model-call timeout, and total runtime all have explicit limits.
- `no-op` is valid only for read-only analysis and rejects coding or mutation objectives.
- The `verify` tool accepts no arguments, preventing model-driven command injection at verification time.
- Workspace path confinement resolves symlinks, rejects `..` traversal, and validates existing targets or the existing parent of a new file.
- Tool approval is enforced per-profile: the `analysis` profile denies all writes, while mutation profiles require explicit approval for `write`, `edit`, and `verify` tools.
- Retry logic only retries transient errors (timeout, unavailable, rate-limited); permanent errors are surfaced immediately.

## Tests

Deterministic regression suite:

```bash
go test ./examples/plan_mode ./capability/plan ./node/plan
```

For a real-provider matrix, run at least three profiles twice with separate data directories, then require both a profile verifier `passed` decision and `plan.status = done` in each printed Plan state. Preserve the Run ID and `.local` checkpoint data while diagnosing failures; `.local/` is gitignored and must not be committed. No live-provider result is bundled with this repository; local tests do not replace that matrix.
