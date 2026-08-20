---
name: weaveflow-graph-create
description: "Author, understand, validate, install, and configure WeaveFlow Graph Definition v2 through the public debug server HTTP API. Use for live registry discovery, graph JSON and State Port reasoning, initial-state analysis, model/tool settings, immutable Graph Sessions, Chat setup, and Trigger replacement or invocation. This skill is source-independent: do not inspect a code repository, implementation files, or repository instructions."
---

# WeaveFlow Graph Create

Operate only through the public HTTP API and this skill's references. Treat `GET /registry`, Graph detail responses, analysis responses, and Session responses as the complete source of truth. Do not read source code, local project files, git history, or repository-specific instructions.

Read [references/graph-definition.md](references/graph-definition.md) before composing or interpreting a definition and [references/server-api.md](references/server-api.md) before making requests.

## Inputs And Outputs

- **Inputs**: server base URL, management authentication, a Graph ID or new-Graph intent, business goal, optional existing definition, expected inputs/outputs, model/tool needs, and explicit authorization for state-changing calls.
- **Outputs**: a semantic Graph context summary, validated definition, initial-state contract, exact Session identity, sanitized settings, Trigger result, or a precise API-derived blocking error.
- **Handoff**: use `weaveflow-graph-debug` after a Run starts. If the public API cannot express or expose the required capability, report an API capability gap without inspecting implementation code.

## Build The Graph Context

Before writing or changing JSON, build a concise context model from API data:

1. Read `GET /registry` and record Graph schema version, state modules, node types, conditions, capabilities, Chat Channels, and each relevant node's config schema plus input/output State Ports.
2. Read `GET /runtime/tools` when tools are involved and map each requested tool to node config, permissions, approvals, inputs, outputs, and side-effect risk.
3. For an existing Graph, read `GET /graphs/:graph_id` and summarize its exact latest Session: purpose, entry/finish points, node roles, edge/condition flow, state modules, bindings, required initial state, model/tool settings, Triggers, hashes, and active Sessions.
4. Translate the business request into a contract table: external inputs, state paths and types, producer node, consumer node, reducer/merge behavior, final outputs, model/tool dependencies, interrupt/resume points, and Trigger-provided values.
5. Mark unknowns explicitly. Resolve schema or registration unknowns through registry/API responses; ask the user only for missing business intent, credentials, or authorization.

## Workflow

1. Resolve the exact base URL and management Bearer token from user-provided or configured connection context. Never print the token or start/reconfigure a server.
2. Compose the definition only from the context model and live registry. Keep state paths in node/condition `state` bindings, never in `config`; do not invent node types, ports, conditions, schemas, or capabilities.
3. Call `POST /graphs/:graph_id/analysis/initial-state-requirements` with the complete intended definition and Trigger candidates. Reconcile every schema, build, topology, binding, Trigger, and required-state finding into the context model.
4. When installation is authorized, create one immutable Session with `POST /graphs/:graph_id/sessions`, including the definition, `graph_version`, unique `request_id`, complete settings, and create/overwrite head fields required by the API.
5. Verify the response identity and re-read `GET /graphs/:graph_id/sessions/:session_id`. Confirm that the persisted definition, settings, required state, graph hash, and snapshot hash match the intended context.
6. For Chat setup, use the Chat Channel setup-session API until confirmed, then atomically install the complete Trigger set. Preserve unrelated Triggers and use only sanitized API responses.
7. Invoke a Trigger or start the exact Session only when requested. Pass the returned Run ID and Session ID to `weaveflow-graph-debug`.

## Safety And Stop Conditions

- Candidate analysis is the non-installing boundary. Do not create exploratory Sessions merely to inspect a definition.
- Session creation, settings changes, Chat setup, Trigger replacement/invocation, Run start, and deletion are state-changing. Stop without explicit authorization.
- Never overwrite without the current `expected_graph_session_id` when required. Stop on a head conflict and re-read the Graph context.
- Never infer hidden defaults, private schemas, implementation behavior, or historical Session content. If the API omits required context, report the missing endpoint/field.
- Respect Session retention and warn before repeated creation can prune inactive historical diagnostics.

## Report

Report the API-derived Graph context, base URL, Graph ID, analysis status, exact Session ID/version/hashes, persisted-context verification, sanitized settings, Trigger/Chat changes, Run ID if invoked, unknowns, and endpoints consulted.
