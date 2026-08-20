# Graph Definition

Construct Graph Definition v2 from the live registry. Do not maintain a private list of node or condition schemas.

This reference is self-contained. Do not inspect implementation code to fill gaps; use only public API responses and explicitly report missing context.

## Discovery Sources

Read `GET /registry` and unwrap `data`. Treat these fields as authoritative:

- `graph_schema`: envelope, node, edge, condition, and binding shape.
- `node_types`: registered node IDs, config schemas, State Ports, and capabilities.
- `conditions`: registered condition IDs and their config/state contracts.
- `state_modules`: available state schemas, ports, reducers, and access rules.
- `capabilities`: runtime features, providers, models, and tool availability.

Read `GET /runtime/tools` when a graph uses tools. A listed tool is not permission to expose secrets, access unrelated data, or mutate the host.

## Definition Rules

- Keep `graph_id` stable when modifying an existing Graph; use a new ID only when a separate Graph is requested.
- Keep Graph version and semantic identity consistent with the server contract. Do not invent node types, ports, conditions, or state modules from memory.
- Bind state paths through node or condition `state` objects. A path in `config` is configuration data, not a State Port binding.
- Register every state module used by the definition. Match declared ports to node inputs, outputs, reducers, and dynamic sends.
- Keep node IDs, condition IDs, and edge endpoints unique and valid. Ensure the topology has valid Start/End reachability according to the live schema.
- Ensure every required input has an initial-state source, an upstream binding, or an explicit Trigger candidate.
- For parallel branches, use disjoint output paths or a declared reducer before merging. Do not rely on map iteration order for conflict resolution.
- Dynamic/custom node reads and writes require registered types plus matching State Ports and definition bindings exposed by the registry schema.

## Model And Tool Separation

The registry validates whether a node accepts `config.model_id`; Graph Session settings validate whether that model is enabled and runnable. Check both surfaces. The current settings model includes provider, `api_format`, model ID, base URL, extra body, credential presence, tool permissions, and tool approvals. Keep Graph Settings as the source for model and credential configuration; do not introduce a duplicate Profile source.

For Codex-backed nodes, verify the server-side provider/API contract from the live registry and settings. Do not assume CLI `config.toml` is the Graph Session credential source.

## Validation Order

1. Decode the definition against `graph_schema`.
2. Check node and condition IDs against the live registry.
3. Check state modules, State Ports, bindings, edges, reducers, and conditions.
4. Call `POST /graphs/:graph_id/analysis/initial-state-requirements` with intended Trigger candidates.
5. Resolve all returned requirements before Session creation.
6. Treat Session creation as the separate settings, credential, and runtime-installation validation boundary.

Analysis builds the candidate graph and computes direct/Trigger initial-state requirements. It does not prove credentials, provider connectivity, model availability, or Run success.

## Session Identity

Preserve the returned Graph ID, graph version, semantic graph hash, snapshot hash, request ID, and Graph Session ID. Run start and resume must use the exact compatible Session; a newer Session does not alter an older Run's execution identity.

After creation, read `GET /graphs/:graph_id/sessions/:session_id` and compare the persisted definition, settings, required state, semantic hash, and snapshot hash with the intended context. Do not rely only on the creation response.
