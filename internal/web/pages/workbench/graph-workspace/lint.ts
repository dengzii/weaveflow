import { END_NODE_REF, dynamicStatePortForName, graphEdgeId, resolveDefaultStatePath } from "../../../lib/graphEditor";
import type {
  DynamicStatePortDefinition,
  GraphDefinition,
  InitialStateRequirements,
  InitialStateRequirement,
  RegistryInfo,
  StateBinding,
  StateCapabilityDefinition,
  StateModuleDefinition,
  StatePortDefinition,
  WarningRecord,
} from "../../../types";

export type GraphLintSeverity = "error" | "warn";

export interface GraphLintIssue {
  id: string;
  severity: GraphLintSeverity;
  message: string;
  nodeId?: string;
  edgeId?: string;
  path?: string;
}

export function buildGraphLintIssues({
  definition,
  initialStateText,
  initialRequirements,
  analysisError = "",
  registry = null,
}: {
  definition: GraphDefinition | null;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
  analysisError?: string;
  registry?: RegistryInfo | null;
}): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];

  if (!definition) {
    return [
      {
        id: "graph-json-invalid",
        severity: "error",
        message: "Graph JSON is invalid.",
      },
    ];
  }

  if (definition.version !== "2.0") {
    issues.push({
      id: "graph-version-invalid",
      severity: "error",
      message: `Graph version must be "2.0", got "${definition.version ?? ""}".`,
      path: "version",
    });
  }

  const selectedModules = lintStateModules(definition, registry, issues);
  const selectedCapabilities = new Map<string, StateCapabilityDefinition>();
  for (const module of selectedModules) {
    for (const capability of module.capabilities ?? []) selectedCapabilities.set(capability.id, capability);
  }
  const rootCapabilities = new Map<string, string>();

  const nodeIds = new Set<string>();
  const duplicateNodeIds = new Set<string>();
  for (const node of definition.nodes) {
    if (!node.id.trim()) {
      issues.push({
        id: `node-id-missing-${issues.length}`,
        severity: "error",
        message: "A node is missing its ID.",
      });
      continue;
    }
    if (nodeIds.has(node.id)) duplicateNodeIds.add(node.id);
    nodeIds.add(node.id);
    if (!node.type?.trim()) {
      issues.push({
        id: `node-type-missing-${node.id}`,
        severity: "error",
        message: `Node "${node.id}" is missing a type.`,
        nodeId: node.id,
      });
    }
    const nodeType = registry?.node_types.find((item) => item.type === node.type);
    if (registry && node.type?.trim() && !nodeType) {
      issues.push({
        id: `node-type-unknown-${node.id}`,
        severity: "error",
        message: `Node "${node.id}" uses unregistered type "${node.type}".`,
        nodeId: node.id,
      });
    }
    issues.push(...lintComponentBindings({
      component: `Node "${node.id}"`,
      stableId: `node-${node.id}`,
      bindings: node.state,
      ports: nodeType?.state_ports,
      dynamicPorts: nodeType?.dynamic_state_ports,
      selectedModules,
      selectedCapabilities,
      registeredCapabilities: registry?.capabilities ?? [],
      rootCapabilities,
      nodeId: node.id,
    }));
  }

  for (const nodeId of duplicateNodeIds) {
    issues.push({
      id: `node-id-duplicate-${nodeId}`,
      severity: "error",
      message: `Duplicate node ID "${nodeId}".`,
      nodeId,
    });
  }

  if (definition.nodes.length === 0) {
    issues.push({
      id: "graph-nodes-empty",
      severity: "error",
      message: "Graph has no nodes.",
    });
  }

  if (!definition.entry_point?.trim()) {
    issues.push({
      id: "graph-entry-missing",
      severity: "error",
      message: "Entry point is missing.",
      path: "entry_point",
    });
  } else if (!nodeIds.has(definition.entry_point)) {
    issues.push({
      id: "graph-entry-invalid",
      severity: "error",
      message: `Entry point "${definition.entry_point}" does not reference an existing node.`,
      path: "entry_point",
    });
  }

  const hasEndEdge = (definition.edges ?? []).some((edge) => edge.to === END_NODE_REF);
  if (!definition.finish_point?.trim() && !hasEndEdge) {
    issues.push({
      id: "graph-finish-missing",
      severity: "error",
      message: "Finish point is missing.",
      path: "finish_point",
    });
  } else if (definition.finish_point && !nodeIds.has(definition.finish_point)) {
    issues.push({
      id: "graph-finish-invalid",
      severity: "error",
      message: `Finish point "${definition.finish_point}" does not reference an existing node.`,
      path: "finish_point",
    });
  }

  const degree = new Map<string, number>();
  for (const nodeId of nodeIds) degree.set(nodeId, 0);
  const edgePairs = new Map<string, number>();
  for (const [index, edge] of (definition.edges ?? []).entries()) {
    const edgeId = graphEdgeId(edge, index);
    const pairKey = `${edge.from}\u0000${edge.to}`;
    const duplicateOf = edgePairs.get(pairKey);
    if (duplicateOf !== undefined) {
      issues.push({
        id: `edge-duplicate-${edge.from}-${edge.to}-${index}`,
        severity: "error",
        message: `Duplicate edge "${edge.from}" -> "${edge.to}".`,
        edgeId,
      });
    } else {
      edgePairs.set(pairKey, index);
    }
    if (!nodeIds.has(edge.from)) {
      issues.push({
        id: `edge-source-missing-${edgeId}`,
        severity: "error",
        message: `Edge source "${edge.from}" does not reference an existing node.`,
        edgeId,
      });
    } else {
      degree.set(edge.from, (degree.get(edge.from) ?? 0) + 1);
    }
    if (edge.to === END_NODE_REF) {
      // The backend DSL accepts "__end__" as the explicit graph terminal target.
    } else if (!nodeIds.has(edge.to)) {
      issues.push({
        id: `edge-target-missing-${edgeId}`,
        severity: "error",
        message: `Edge target "${edge.to}" does not reference an existing node.`,
        edgeId,
      });
    } else {
      degree.set(edge.to, (degree.get(edge.to) ?? 0) + 1);
    }
    if (edge.condition) {
      const condition = registry?.conditions.find((item) => item.type === edge.condition?.type);
      if (registry && !condition) {
        issues.push({
          id: `condition-type-unknown-${edgeId}`,
          severity: "error",
          message: `Edge condition "${edge.condition.type}" is not registered.`,
          edgeId,
        });
      }
      issues.push(...lintComponentBindings({
        component: `Condition "${edge.condition.type}"`,
        stableId: `condition-${edgeId}`,
        bindings: edge.condition.state,
        ports: condition?.state_ports,
        dynamicPorts: condition?.dynamic_state_ports,
        selectedModules,
        selectedCapabilities,
        registeredCapabilities: registry?.capabilities ?? [],
        rootCapabilities,
        nodeId: edge.from,
        edgeId,
      }));
    }
  }

  if (definition.nodes.length > 1) {
    for (const node of definition.nodes) {
      if ((degree.get(node.id) ?? 0) === 0) {
        issues.push({
          id: `node-isolated-${node.id}`,
          severity: "warn",
          message: `Node "${node.id}" is isolated.`,
          nodeId: node.id,
        });
      }
    }
  }

  issues.push(...lintSupervisorTopology(definition));

  const parsedInitialState = parseInitialState(initialStateText);
  if (!parsedInitialState.ok) {
    issues.push({
      id: "initial-state-json-invalid",
      severity: "error",
      message: `Initial state JSON is invalid: ${parsedInitialState.error}`,
      path: "initial_state",
    });
  } else if (initialRequirements) {
    for (const requirement of initialRequirements.required ?? []) {
      if (!hasFilledInitialStatePath(parsedInitialState.value, requirement.path, requirement.type)) {
        issues.push({
          id: `initial-state-missing-${requirement.path}`,
          severity: "error",
          message: `Initial state is missing "${requirement.path}".`,
          path: requirement.path,
        });
      }
    }
  }

  for (const requirement of initialRequirements?.unresolved ?? []) {
    issues.push(requirementIssue("unresolved", requirement, "error"));
  }
  for (const warning of initialRequirements?.warnings ?? []) {
    issues.push(warningIssue(warning));
  }

  if (analysisError.trim()) {
    issues.push({
      id: "graph-contract-analysis-error",
      severity: "error",
      message: analysisError.trim(),
      path: "state",
    });
  }

  return dedupeIssues(issues);
}

function lintSupervisorTopology(definition: GraphDefinition): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];
  const workers = definition.nodes.filter((node) => node.type === "supervisor_worker");
  const workerByMemberID = new Map<string, typeof workers[number]>();
  for (const worker of workers) {
    const workerID = configString(worker.config, "worker_id");
    if (!workerID) continue;
    const key = workerID.toLowerCase();
    const existing = workerByMemberID.get(key);
    if (existing) {
      issues.push({
        id: `supervisor-worker-duplicate-${key}-${worker.id}`,
        severity: "error",
        message: `Supervisor Worker nodes "${existing.id}" and "${worker.id}" share worker_id "${workerID}".`,
        nodeId: worker.id,
        path: "worker_id",
      });
      continue;
    }
    workerByMemberID.set(key, worker);
  }

  for (const supervisor of definition.nodes.filter((node) => node.type === "supervisor")) {
    const rawMembers = supervisor.config?.members;
    const members = Array.isArray(rawMembers) ? rawMembers.filter(isRecord) : [];
    if (members.length === 0) {
      issues.push({
        id: `supervisor-members-empty-${supervisor.id}`,
        severity: "error",
        message: `Supervisor "${supervisor.id}" needs at least one configured member.`,
        nodeId: supervisor.id,
        path: "members",
      });
    }
    const seen = new Set<string>();
    for (const member of members) {
      const memberID = recordString(member, "id");
      if (!memberID) continue;
      const memberKey = memberID.toLowerCase();
      if (seen.has(memberKey)) {
        issues.push({
          id: `supervisor-member-duplicate-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor "${supervisor.id}" has duplicate member id "${memberID}".`,
          nodeId: supervisor.id,
          path: "members",
        });
        continue;
      }
      seen.add(memberKey);

      const worker = workerByMemberID.get(memberKey);
      if (!worker) {
        issues.push({
          id: `supervisor-worker-missing-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor member "${memberID}" has no matching Supervisor Worker node.`,
          nodeId: supervisor.id,
          path: "members",
        });
        continue;
      }

      const routeEdgeIndex = (definition.edges ?? []).findIndex((edge) =>
        edge.from === supervisor.id
        && edge.to === worker.id
        && edge.condition?.type === "supervisor_route_equals"
        && configString(edge.condition.config, "worker_id").toLowerCase() === memberKey
      );
      if (routeEdgeIndex < 0) {
        issues.push({
          id: `supervisor-route-missing-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor member "${memberID}" needs a supervisor_route_equals edge to worker node "${worker.id}".`,
          nodeId: supervisor.id,
          path: "members",
        });
      }

      const returnsToSupervisor = (definition.edges ?? []).some((edge) =>
        edge.from === worker.id && edge.to === supervisor.id && !edge.condition
      );
      if (!returnsToSupervisor) {
        issues.push({
          id: `supervisor-return-missing-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor worker "${worker.id}" needs a direct edge back to "${supervisor.id}".`,
          nodeId: worker.id,
        });
      }
    }

    const hasSynthesisFallback = (definition.edges ?? []).some((edge) =>
      edge.from === supervisor.id
      && !edge.condition
      && definition.nodes.some((node) => node.id === edge.to && node.type === "supervisor_synthesis")
    );
    if (!hasSynthesisFallback) {
      issues.push({
        id: `supervisor-synthesis-missing-${supervisor.id}`,
        severity: "error",
        message: `Supervisor "${supervisor.id}" needs a direct fallback edge to a Supervisor Synthesis node.`,
        nodeId: supervisor.id,
      });
    }
  }
  return issues;
}

export function issueCounts(issues: GraphLintIssue[]): { errors: number; warnings: number } {
  return {
    errors: issues.filter((issue) => issue.severity === "error").length,
    warnings: issues.filter((issue) => issue.severity === "warn").length,
  };
}

function lintStateModules(
  definition: GraphDefinition,
  registry: RegistryInfo | null,
  issues: GraphLintIssue[]
): StateModuleDefinition[] {
  const refs = definition.state_modules ?? [];
  if (refs.length === 0) {
    issues.push({
      id: "graph-state-modules-missing",
      severity: "error",
      message: "Graph must reference at least one state module.",
      path: "state_modules",
    });
    return [];
  }

  const selected: StateModuleDefinition[] = [];
  const seen = new Set<string>();
  for (const [index, ref] of refs.entries()) {
    const name = ref.name?.trim();
    const version = ref.version?.trim();
    const key = `${name}\u0000${version}`;
    if (!name || !version) {
      issues.push({
        id: `graph-state-module-invalid-${index}`,
        severity: "error",
        message: "State module name and version are required.",
        path: "state_modules",
      });
      continue;
    }
    if (seen.has(key)) {
      issues.push({
        id: `graph-state-module-duplicate-${name}-${version}`,
        severity: "error",
        message: `State module "${name}" version "${version}" is duplicated.`,
        path: "state_modules",
      });
      continue;
    }
    seen.add(key);
    if (!registry) continue;
    const module = registry.state_modules.find((item) => item.name === name && item.version === version);
    if (!module) {
      issues.push({
        id: `graph-state-module-unknown-${name}-${version}`,
        severity: "error",
        message: `State module "${name}" version "${version}" is not registered.`,
        path: "state_modules",
      });
      continue;
    }
    selected.push(module);
  }
  return selected;
}

function lintComponentBindings({
  component,
  stableId,
  bindings,
  ports,
  dynamicPorts,
  selectedModules,
  selectedCapabilities,
  registeredCapabilities,
  rootCapabilities,
  nodeId,
  edgeId,
}: {
  component: string;
  stableId: string;
  bindings: Record<string, StateBinding> | undefined;
  ports: StatePortDefinition[] | undefined;
  dynamicPorts: DynamicStatePortDefinition | undefined;
  selectedModules: StateModuleDefinition[];
  selectedCapabilities: Map<string, StateCapabilityDefinition>;
  registeredCapabilities: StateCapabilityDefinition[];
  rootCapabilities: Map<string, string>;
  nodeId?: string;
  edgeId?: string;
}): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];
  const bindingMap: Record<string, unknown> = isRecord(bindings) ? { ...bindings } : {};
  const portMap = new Map((ports ?? []).map((port) => [port.name, port]));
  const contractPaths = new Map<string, { type: string; merge: string }>();

  if (ports || dynamicPorts) {
    for (const port of ports ?? []) {
      if (!bindingMap[port.name]) {
        const defaultPath = resolveDefaultStatePath(port.default_path, nodeId ?? "");
        if (defaultPath) bindingMap[port.name] = { path: defaultPath };
      }
      const path = bindingPath(bindingMap[port.name]);
      if (port.required && !path) {
        issues.push(bindingIssue(
          `${stableId}-binding-required-${port.name}`,
          `${component} requires state binding "${port.name}".`,
          `state.${port.name}`,
          nodeId,
          edgeId
        ));
      }
    }
    let dynamicCount = 0;
    for (const name of Object.keys(bindingMap)) {
      if (portMap.has(name)) continue;
      const dynamicPort = dynamicStatePortForName(name, dynamicPorts);
      if (dynamicPort) {
        dynamicCount += 1;
        portMap.set(name, dynamicPort);
        if (!bindingPath(bindingMap[name])) {
          issues.push(bindingIssue(
            `${stableId}-binding-required-${name}`,
            `${component} requires a path for dynamic state binding "${name}".`,
            `state.${name}`,
            nodeId,
            edgeId
          ));
        }
      } else {
        issues.push(bindingIssue(
          `${stableId}-binding-unknown-${name}`,
          `${component} binds unknown state port "${name}".`,
          `state.${name}`,
          nodeId,
          edgeId
        ));
      }
    }
    if (dynamicPorts && dynamicCount < (dynamicPorts.min_ports ?? 0)) {
      issues.push(bindingIssue(
        `${stableId}-binding-dynamic-min`,
        `${component} requires at least ${dynamicPorts.min_ports ?? 0} dynamic state binding(s).`,
        "state",
        nodeId,
        edgeId
      ));
    }
    if (dynamicPorts?.max_ports && dynamicCount > dynamicPorts.max_ports) {
      issues.push(bindingIssue(
        `${stableId}-binding-dynamic-max`,
        `${component} allows at most ${dynamicPorts.max_ports} dynamic state binding(s).`,
        "state",
        nodeId,
        edgeId
      ));
    }
  }

  for (const [name, rawBinding] of Object.entries(bindingMap)) {
    const path = bindingPath(rawBinding);
    if (!path) continue;
    const pathError = validateBindingPath(path);
    if (pathError) {
      issues.push(bindingIssue(
        `${stableId}-binding-path-${name}`,
        `${component} state binding "${name}" is invalid: ${pathError}`,
        `state.${name}`,
        nodeId,
        edgeId
      ));
      continue;
    }
    const port = portMap.get(name);
    if (!port) continue;

    if (port.capability) {
      const capability = selectedCapabilities.get(port.capability);
      if (!capability) {
        const registered = registeredCapabilities.some((item) => item.id === port.capability);
        issues.push(bindingIssue(
          `${stableId}-binding-capability-${name}`,
          registered
            ? `${component} state port "${name}" capability "${port.capability}" belongs to an unreferenced state module.`
            : `${component} state port "${name}" capability "${port.capability}" is not registered.`,
          `state.${name}`,
          nodeId,
          edgeId
        ));
        continue;
      }
      const existingCapability = rootCapabilities.get(path);
      if (existingCapability && existingCapability !== port.capability) {
        issues.push(bindingIssue(
          `${stableId}-binding-capability-conflict-${name}`,
          `State path "${path}" is bound to incompatible capabilities "${existingCapability}" and "${port.capability}".`,
          `state.${name}`,
          nodeId,
          edgeId
        ));
      } else {
        rootCapabilities.set(path, port.capability);
      }
      const exactField = moduleField(selectedModules, path);
      if (exactField && !schemasCompatible(exactField.schema, capability.schema)) {
        issues.push(bindingIssue(
          `${stableId}-binding-schema-${name}`,
          `${component} state port "${name}" capability schema conflicts with module field "${path}".`,
          `state.${name}`,
          nodeId,
          edgeId
        ));
      }
      for (const reference of port.contract?.fields ?? []) {
        const field = capability.fields.find((item) => item.name === reference.path);
        if (!field) {
          issues.push(bindingIssue(
            `${stableId}-binding-contract-${name}-${reference.path}`,
            `${component} state port "${name}" references missing capability field "${reference.path}".`,
            `state.${name}`,
            nodeId,
            edgeId
          ));
          continue;
        }
        const expandedPath = `${path}.${reference.path}`;
        const expandedModuleField = moduleField(selectedModules, expandedPath);
        if (expandedModuleField && !schemasCompatible(expandedModuleField.schema, field.schema)) {
          issues.push(bindingIssue(
            `${stableId}-binding-schema-${name}-${reference.path}`,
            `${component} state port "${name}" capability field "${expandedPath}" conflicts with module field "${expandedPath}".`,
            `state.${name}`,
            nodeId,
            edgeId
          ));
        }
        mergeContractPath(
          expandedPath,
          schemaType(field.schema),
          field.merge_strategy || "replace",
          component,
          stableId,
          name,
          contractPaths,
          issues,
          nodeId,
          edgeId
        );
      }
      continue;
    }

    const exactField = moduleField(selectedModules, path);
    if (exactField && !schemasCompatible(exactField.schema, port.schema)) {
      issues.push(bindingIssue(
        `${stableId}-binding-schema-${name}`,
        `${component} state port "${name}" type "${schemaType(port.schema)}" conflicts with module field "${path}" type "${schemaType(exactField.schema)}".`,
        `state.${name}`,
        nodeId,
        edgeId
      ));
    }
    mergeContractPath(
      path,
      schemaType(port.schema),
      port.merge_strategy || "replace",
      component,
      stableId,
      name,
      contractPaths,
      issues,
      nodeId,
      edgeId
    );
  }
  return issues;
}

function mergeContractPath(
  path: string,
  type: string,
  merge: string,
  component: string,
  stableId: string,
  portName: string,
  contractPaths: Map<string, { type: string; merge: string }>,
  issues: GraphLintIssue[],
  nodeId?: string,
  edgeId?: string
) {
  const existing = contractPaths.get(path);
  if (!existing) {
    contractPaths.set(path, { type, merge });
    return;
  }
  if (existing.type && type && existing.type !== type) {
    issues.push(bindingIssue(
      `${stableId}-binding-contract-type-${portName}-${path}`,
      `${component} resolves incompatible schema types "${existing.type}" and "${type}" at "${path}".`,
      `state.${portName}`,
      nodeId,
      edgeId
    ));
  }
  if (existing.merge !== merge) {
    issues.push(bindingIssue(
      `${stableId}-binding-contract-merge-${portName}-${path}`,
      `${component} resolves incompatible merge strategies "${existing.merge}" and "${merge}" at "${path}".`,
      `state.${portName}`,
      nodeId,
      edgeId
    ));
  }
}

function bindingIssue(
  id: string,
  message: string,
  path: string,
  nodeId?: string,
  edgeId?: string
): GraphLintIssue {
  return { id, severity: "error", message, path, nodeId, edgeId };
}

function bindingPath(value: unknown): string {
  return isRecord(value) && typeof value.path === "string" ? value.path.trim() : "";
}

function validateBindingPath(path: string): string {
  const parts = path.split(".").map((part) => part.trim());
  if (parts.length < 2) return "a state section root cannot be bound";
  if (!["shared", "scopes", "internal", "runtime"].includes(parts[0])) {
    return "section must be shared, scopes, internal, or runtime";
  }
  if (parts.some((part) => !part)) return "path contains an empty segment";
  if (parts[0] === "internal" || parts[0] === "runtime") return `section "${parts[0]}" is reserved`;
  return "";
}

function moduleField(modules: StateModuleDefinition[], path: string) {
  for (const module of modules) {
    const field = module.fields?.find((item) => item.path === path);
    if (field) return field;
  }
  return undefined;
}

function schemasCompatible(left: Record<string, unknown> | undefined, right: Record<string, unknown> | undefined): boolean {
  const leftType = schemaType(left);
  const rightType = schemaType(right);
  return !leftType || !rightType || leftType === rightType;
}

function schemaType(schema: Record<string, unknown> | undefined): string {
  return typeof schema?.type === "string" ? schema.type.trim() : "";
}

function parseInitialState(text: string):
  | { ok: true; value: unknown }
  | { ok: false; error: string } {
  try {
    return { ok: true, value: JSON.parse(text) as unknown };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

function hasFilledInitialStatePath(initialState: unknown, path: string, type?: string): boolean {
  const value = valueAtStatePath(initialState, path);
  if (!value.exists) return false;
  if (value.value === null || value.value === undefined) return false;
  if ((type ?? "").toLowerCase() === "string") {
    return typeof value.value === "string" && value.value.trim().length > 0;
  }
  if (typeof value.value === "string") return value.value.trim().length > 0;
  return true;
}

function valueAtStatePath(root: unknown, path: string): { exists: boolean; value?: unknown } {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  let current = root;
  for (const part of parts) {
    if (!current || typeof current !== "object" || Array.isArray(current)) {
      return { exists: false };
    }
    if (!Object.prototype.hasOwnProperty.call(current, part)) {
      return { exists: false };
    }
    current = (current as Record<string, unknown>)[part];
  }
  return { exists: true, value: current };
}

function requirementIssue(
  prefix: string,
  requirement: InitialStateRequirement,
  severity: GraphLintSeverity
): GraphLintIssue {
  return {
    id: `${prefix}-${requirement.path}`,
    severity,
    message: requirement.message || `State path "${requirement.path}" is unresolved.`,
    nodeId: requirement.nodes?.[0],
    path: requirement.path,
  };
}

function warningIssue(warning: WarningRecord): GraphLintIssue {
  const stableId = [warning.code, warning.node_id, warning.other_node_id, warning.path, warning.message]
    .filter(Boolean)
    .join(":");
  return {
    id: stableId || "warning",
    severity: "warn",
    message: warning.message || warning.code || "Graph analysis warning.",
    nodeId: warning.node_id,
    path: warning.path,
  };
}

function dedupeIssues(issues: GraphLintIssue[]): GraphLintIssue[] {
  const seen = new Set<string>();
  const result: GraphLintIssue[] = [];
  for (const issue of issues) {
    const key = `${issue.severity}:${issue.id}:${issue.message}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(issue);
  }
  return result;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function configString(config: Record<string, unknown> | undefined, key: string): string {
  if (!config) return "";
  return typeof config[key] === "string" ? config[key].trim() : "";
}

function recordString(record: Record<string, unknown>, key: string): string {
  return typeof record[key] === "string" ? record[key].trim() : "";
}
