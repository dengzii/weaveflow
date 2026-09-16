---
name: weaveflow-graph-agent-quality
description: "Evaluate a WeaveFlow Graph Agent's design from its exact persisted Run path. Use only in the WeaveFlow repository/workspace or when the user explicitly identifies WeaveFlow, a WeaveFlow server, or a WeaveFlow Graph Agent/Run. Do not use for agent, graph, workflow, tool, or evidence-quality reviews in unrelated products, nor for operational Run recovery or static source-only review."
---

# WeaveFlow Graph Agent Quality

Evaluate Agent and Graph design from persisted execution evidence. The goal is to explain whether the design produces a bounded, efficient, evidence-grounded, coherent workflow on the path that actually ran; do not collapse this into service health or terminal Run status. Use the public debug HTTP API with management authentication, unwrap successful `data` envelopes, and never print credentials or large state bodies.

## Activation Scope

Use this skill only when at least one condition is true:

- The current workspace is positively identified as the WeaveFlow project and the task concerns a WeaveFlow Graph Agent's persisted behavior.
- The user explicitly names WeaveFlow, a WeaveFlow debug server, or a WeaveFlow Graph Agent, Session, or Run.
- The user explicitly invokes this skill.

Do not activate it for generic agent evaluation, workflow design review, planning quality, tool strategy, evidence quality, loops, or efficiency in another product. Outside a WeaveFlow workspace, an explicit WeaveFlow product identity is required; if identity is ambiguous, do not use this skill.

Read [references/agent-quality-method.md](references/agent-quality-method.md) for the full rubric and report template.

## Inputs And Outputs

- **Inputs**: debug server base URL, management authentication, Graph ID and Run ID; use selection criteria only when the Run ID is unknown. An existing comparison Run ID is optional.
- **Outputs**: exact historical Session context, Run lineage and invocation trace, intended-versus-observed contract comparisons, design and efficiency findings, quality dimensions, uncovered paths, confidence, and implementation handoff notes.
- **Evidence boundary**: Run, Event, Checkpoint, Artifact, Session, registry, and settings responses are API-persisted facts. Source inspection may explain a mechanism only after the API path is complete and must be labeled source-derived.

## Workflow

1. Read the Run inspection and its exact immutable Graph Session. Record root/parent/child/fork provenance and follow child Runs that materially contribute to the result. Do not use the latest Graph definition as a substitute; record hash or Session drift separately.
2. Page every persisted Event endpoint for each in-scope Run until the cursor is empty or repeats. Read the Checkpoints and only the Artifact bodies needed to explain state, model/tool invocations, outputs, verifier decisions, and usage.
3. Reconstruct the path as `input → node/condition → Step attempt or Agent invocation → state/checkpoint → output`, including retries, loops, replans, child Runs, branch decisions, and the first terminal boundary.
4. Compare the path with the historical definition: prompts and acceptance criteria, State Port bindings, shared versus per-step state, edge conditions, budgets, verifier/reviewer gates, fallback routes, and final-output bindings.
5. When an existing comparison Run uses the same Graph snapshot, use the read-only Compare endpoint to locate state, Step, Event, and Artifact differences; still inspect both paths independently.
6. Apply the design rubric in the reference. Keep Agent design findings separate from external service failures and from nodes that never executed.
7. Report the evidence-backed verdict and the smallest high-value design changes. Do not mutate a Graph or Run from this skill.

## Boundaries

- Use `weaveflow-graph-debug` for operational Run diagnosis, retention investigation, side-effect resolution, pause/cancel/resume, Fork, Compare-led recovery, or other controls.
- Use `weaveflow-graph-create` for authoring or replacing Graph Definitions, Sessions, settings, or Triggers.
- Use `weaveflow-graph-code` for implementation changes after the design finding is accepted.
- Investigation and existing-Run comparison are read-only. Do not create a Fork, resume, cancel, delete, replace, or otherwise mutate runtime state from this skill.
- A successful tool call, HTTP response, or terminal Run does not prove that an Agent design met its research, business, or evidence contract.
- Do not call a final synthesis or unexecuted branch good or bad; mark it untested and state what evidence is missing.
