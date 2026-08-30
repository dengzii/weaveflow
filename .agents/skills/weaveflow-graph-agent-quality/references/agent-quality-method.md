# Agent Quality From a Graph Run Path

This method evaluates the design of an Agent/Graph as observed through one persisted Run. It is deliberately separate from infrastructure health. A transient model outage can explain where a path stopped, but it cannot erase design defects already visible before that outage.

## 1. Build the Evidence Map

Use the public debug API in this order:

1. `GET /graphs/:graph_id/runs/:run_id/inspection`
2. `GET /graphs/:graph_id/sessions/:graph_session_id`
3. The relevant parent/child Runs from inspection provenance or `GET /graphs/:graph_id/runs` filters
4. `GET /registry` and `GET /runtime/tools` for contract interpretation only
5. `GET /graphs/:graph_id/runs/:run_id/events` with complete cursor pagination for every in-scope Run
6. `GET /graphs/:graph_id/runs/:run_id/artifacts`, then `GET /graphs/:graph_id/runs/:run_id/artifacts/:artifact_id` for relevant bodies
7. `GET /graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id` for the last valid Checkpoint and Checkpoints around retries, branch changes, replans, Agent invocations, and terminal failure
8. Optionally, `GET /graphs/:graph_id/runs/:run_id/compare/:other_run_id` for two existing Runs on the same Graph snapshot

Record:

- Graph, Run, historical Session, version, graph hash, snapshot hash, entry/finish nodes, and root/parent/child/fork provenance
- External input and Trigger/run input sources
- Node configs, prompts, acceptance criteria, State Port paths, edge conditions, and budgets
- Step IDs, node IDs, attempts, statuses, operation keys, timestamps, and checkpoint links
- Agent invocation ID, kind, iteration, operation ID, tool-call ID, synthetic Step, checkpoint, and prompt/response/error Artifact references
- Condition decisions and their reasons
- Plan/business state before and after each material transition
- Tool inputs and outputs, source URLs/statuses, LLM responses, usage/cost, verifier/reviewer decisions, and final output bindings

The latest Graph is only a drift comparison. The historical Session bound to the Run is authoritative for intended design.

Agent invocation lifecycle is persisted as `nodes.custom` Events whose payload `event` is `agent.invocation.started`, `agent.invocation.checkpoint`, or `agent.invocation.finished`. Correlate the payload identity with synthetic Steps, `agent_invocation` Checkpoints, `agent.llm.prompt`/`agent.llm.response`/`agent.llm.error` Artifacts, tool Events, and `llm.usage`; do not infer an invocation solely from a prompt Artifact.

## 2. Reconstruct the Actual Path

Write a compact path trace before judging quality. Group repeated cycles, but preserve attempt boundaries:

```text
input
→ plan generation
→ prepare(step N)
→ model/tool invocation loop × K
→ verifier decision
→ reviewer decision
→ next step, retry, replan, synthesis, or terminal boundary
```

For every repeated cycle answer:

- Which plan step was active?
- Which Agent invocation, child Run, Step attempt, and operation identity performed the work?
- Did the model produce a result, tool calls, or neither?
- Which tools actually returned usable evidence versus an HTTP/error payload?
- Did the verifier evaluate the acceptance criteria or only count generic evidence?
- Did the reviewer preserve completed work, retry the same work, or rewrite the plan?
- What state was persisted at the checkpoint?
- Was the next branch selected by an explicit condition or an unconditional fallback?

Use event order, not node counts alone. A successful Step can still be a design failure if it accepted weak evidence, wrote the wrong state, or advanced the wrong plan step.

## 3. Design Rubric

Assess each dimension independently as `pass`, `warn`, `fail`, or `unknown`.

| Dimension | Pass signal | Fail or warning signal |
| --- | --- | --- |
| Problem framing and decomposition | Plan scope, depth, and number of steps match the input; simple work can use a fast path | Fixed multi-step planning for trivial inputs; steps duplicate one another or lack measurable acceptance criteria |
| State and contract design | Inputs, outputs, current step, evidence, and final result have explicit typed bindings with clear ownership | Important facts live only in free-form conversation; shared state has ambiguous writers; intermediate and final outputs share a field |
| Tool and source strategy | Tools are selected by step, source quality and access status are tracked, and blocked sources have a deterministic fallback | Repeated blocked URLs, irrelevant searches, no source ranking, or tool results treated as evidence without inspecting their content/status |
| Evidence and provenance | Verifier requires the right evidence types, independent sources, provenance, and claim-to-source links | Gate passes on a raw count; non-substantive outputs such as time/status calls satisfy the threshold; search snippets become final evidence |
| Loop and termination | Tool loop has a bounded budget and exits only after a valid result or an explicit failure state | A no-tool turn is enough to advance; max-iteration exhaustion becomes an opaque “no result”; repeated work has no convergence signal |
| Retry and replanning | Retry reason is structured; completed steps and evidence are preserved; replan changes only the affected work | Replan resets done steps, renames or discards useful evidence, repeats the same failing strategy, or has no repair objective |
| Completion and finalization | Synthesis is reachable only with complete business state; final output is written once with durable references | Partial step output is exposed as final; final node is unexecuted; completion depends on an unconditional edge or an unverified implicit reducer |
| Context isolation | Per-step conversation/evidence is bounded or intentionally shared with explicit provenance | One growing conversation mixes steps, attempts, and stale instructions without a typed step boundary |
| Efficiency and resource governance | Model/tool turns, elapsed time, retries, token/cost budgets, and child work are proportional to the objective; repeated work has a measured benefit | Wall time or event volume grows without progress; retry delay is counted as execution; trivial work takes a mandatory long path; budgets exist only in prompts |
| Resilience as Agent behavior | External dependency failure is classified, bounded, and routed to a safe fallback or explicit resumable boundary | One model/tool failure aborts the whole research objective with no node-level fallback; distinguish this from the external incident itself |

Do not mark a dimension `fail` merely because a node was not reached. Mark it `unknown` or `untested`, then identify the missing path evidence.

## 4. Common Path Findings

These are checks, not universal rules:

- **Count-based verifier**: compare evidence count with evidence meaning. A successful `current_time`, health, or metadata call is not source corroboration.
- **Prompt-contract drift**: if a prompt says search snippets are leads only, inspect whether the persisted result uses snippets to support material claims before any page fetch.
- **Progress regression**: compare the plan before and after `replan`; completed step results and evidence should not silently return to `pending`.
- **Blocked-source loop**: compare fetch status and URL repetition. A 403/404/timeout should produce a structured source-unavailable state, not unbounded LLM improvisation.
- **Missing fast path**: compare input complexity with the mandatory plan depth, tool budget, and number of review cycles.
- **Budget without governance**: compare configured iteration/token/cost limits with invocation Events, usage, and terminal reasons. Keep execution time, retry backoff, and queue delay separate.
- **Final-answer leakage**: compare the first population of the final-output field with synthesis execution and business completion.
- **Implicit synthesis routing**: inspect conditional and unconditional edges together; if synthesis is reachable before completion, require a persisted incomplete-state guard and verify it on a successful path.
- **Shared conversation contamination**: inspect whether later steps see earlier prompts/results without a step ID, source identity, or attempt boundary.
- **Opaque nested work**: if a child Run or Agent invocation affects the result, require durable parent/child or invocation provenance before judging its contribution.

## 5. Separate Three Kinds of Findings

Every finding must be placed in one category:

1. **Observed Agent design finding**: the persisted path demonstrates the behavior independently of infrastructure. Example: a verifier passes after counting a metadata tool result as evidence.
2. **External service incident**: the path stopped because a model, network, provider, or remote source failed. State whether the Graph design had a fallback; do not use the incident to excuse earlier design behavior.
3. **Untested or unavailable path**: a node, branch, verifier success path, synthesis step, or recovery route did not execute or lacks persisted evidence. Do not infer its quality from prompts or source alone.

If source inspection is needed to explain an implementation mechanism, verify runtime/source correspondence, read applicable `AGENTS.md`, inspect only the relevant files, and report source conclusions separately.

## 6. Verdict

Use a design verdict independent of the service-oriented Run verdict:

- `pass`: no critical design dimension fails; observed path satisfies its contracts and completes with durable evidence
- `warn`: design is coherent but has bounded gaps, efficiency concerns, or untested non-critical branches
- `fail`: any critical dimension fails, such as weak evidence gating, progress loss on replan, premature final output, or an unbounded/non-convergent loop
- `unknown`: required path, checkpoint, artifact, or historical Session evidence is missing

Do not average away a critical failure. A Run can have `side_effects=pass` and still have `evidence_contract=fail`; a completed runtime can still have an incomplete Agent plan.

## 7. Report Template

```markdown
Agent design verdict: pass | warn | fail | unknown

Scope:
- Graph / Run / historical Session:
- Input and finish point:
- Path coverage and first terminal boundary:

Observed path:
- ...

Design dimensions:
- decomposition: pass|warn|fail|unknown — evidence
- state_contract: pass|warn|fail|unknown — evidence
- tool_source_strategy: pass|warn|fail|unknown — evidence
- evidence_provenance: pass|warn|fail|unknown — evidence
- loop_termination: pass|warn|fail|unknown — evidence
- retry_replan: pass|warn|fail|unknown — evidence
- completion: pass|warn|fail|unknown — evidence
- context_isolation: pass|warn|fail|unknown — evidence
- efficiency_governance: pass|warn|fail|unknown — evidence
- agent_resilience: pass|warn|fail|unknown — evidence

Findings:
1. [API-persisted] ...
2. [API-persisted] ...
3. [source-derived, if any] ...

Separate incident:
- External service failure and its effect on coverage:

Untested or missing evidence:
- ...

Handoff:
- Smallest high-value Graph/implementation changes:
- Use `weaveflow-graph-create` or `weaveflow-graph-code` for authorized changes.
```
