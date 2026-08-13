import { END_NODE_REF, graphEdgeId } from "../../../lib/graphEditor";
import type {
  GraphDefinition,
  InitialStateRequirements,
  InitialStateRequirement,
  RegistryInfo,
  StateCapabilityDefinition,
  WarningRecord,
} from "../../../types";
import { hasFilledInitialStatePath, parseInitialStateText } from "./runInputModel";
import { lintComponentBindings, lintStateModules } from "./lintStateBindings";
import { lintSupervisorTopology } from "./lintSupervisor";
import type { GraphLintIssue, GraphLintSeverity } from "./lintTypes";

export type { GraphLintIssue, GraphLintSeverity } from "./lintTypes";

interface GraphLintInput {
  definition: GraphDefinition | null;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
  analysisError?: string;
  registry?: RegistryInfo | null;
}

export function buildGraphLintIssues({
  definition,
  initialStateText,
  initialRequirements,
  analysisError = "",
  registry = null,
}: GraphLintInput): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];

  if (!definition) {
    return [
      {
        id: "graph-json-invalid",
        severity: "error",
        message: "Graph definition JSON is invalid.",
      },
    ];
  }

  if (definition.version !== "2.0") {
    issues.push({
      id: "graph-version-invalid",
      severity: "error",
      message: `Graph version must be "2.0"; received "${definition.version ?? ""}".`,
      path: "version",
    });
  }

  const selectedModules = lintStateModules(definition, registry, issues);
  const selectedCapabilities = new Map<string, StateCapabilityDefinition>();
  for (const module of selectedModules) {
    for (const capability of module.capabilities ?? []) selectedCapabilities.set(capability.id, capability);
  }
  const rootCapabilities = new Map<string, string>();

  const nodeIDs = new Set<string>();
  const duplicateNodeIDs = new Set<string>();
  for (const node of definition.nodes) {
    if (!node.id.trim()) {
      issues.push({
        id: `node-id-missing-${issues.length}`,
        severity: "error",
        message: "A node is missing its ID.",
      });
      continue;
    }
    if (nodeIDs.has(node.id)) duplicateNodeIDs.add(node.id);
    nodeIDs.add(node.id);
    if (!node.type?.trim()) {
      issues.push({
        id: `node-type-missing-${node.id}`,
        severity: "error",
        message: `Node "${node.id}" is missing a type.`,
        nodeID: node.id,
      });
    }
    const nodeType = registry?.node_types.find((item) => item.type === node.type);
    if (registry && node.type?.trim() && !nodeType) {
      issues.push({
        id: `node-type-unknown-${node.id}`,
        severity: "error",
        message: `Node "${node.id}" uses unregistered type "${node.type}".`,
        nodeID: node.id,
      });
    }
    issues.push(...lintComponentBindings({
      component: `Node "${node.id}"`,
      stableID: `node-${node.id}`,
      bindings: node.state,
      ports: nodeType?.state_ports,
      dynamicPorts: nodeType?.dynamic_state_ports,
      selectedModules,
      selectedCapabilities,
      registeredCapabilities: registry?.capabilities ?? [],
      rootCapabilities,
      nodeID: node.id,
    }));
  }

  for (const nodeID of duplicateNodeIDs) {
    issues.push({
      id: `node-id-duplicate-${nodeID}`,
      severity: "error",
      message: `Duplicate node ID "${nodeID}".`,
      nodeID,
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
  } else if (!nodeIDs.has(definition.entry_point)) {
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
  } else if (definition.finish_point && !nodeIDs.has(definition.finish_point)) {
    issues.push({
      id: "graph-finish-invalid",
      severity: "error",
      message: `Finish point "${definition.finish_point}" does not reference an existing node.`,
      path: "finish_point",
    });
  }

  const degree = new Map<string, number>();
  for (const nodeID of nodeIDs) degree.set(nodeID, 0);
  const edgePairs = new Map<string, number>();
  for (const [index, edge] of (definition.edges ?? []).entries()) {
    const edgeID = graphEdgeId(edge, index);
    const pairKey = `${edge.from}\u0000${edge.to}\u0000${edge.failure ? "failure" : "normal"}`;
    const duplicateOf = edgePairs.get(pairKey);
    if (duplicateOf !== undefined) {
      issues.push({
        id: `edge-duplicate-${edge.from}-${edge.to}-${index}`,
        severity: "error",
        message: `Duplicate edge "${edge.from}" -> "${edge.to}".`,
        edgeID,
      });
    } else {
      edgePairs.set(pairKey, index);
    }
    if (!nodeIDs.has(edge.from)) {
      issues.push({
        id: `edge-source-missing-${edgeID}`,
        severity: "error",
        message: `Edge source "${edge.from}" does not reference an existing node.`,
        edgeID,
      });
    } else {
      degree.set(edge.from, (degree.get(edge.from) ?? 0) + 1);
    }
    if (edge.to === END_NODE_REF) {
      // Graph Definition v2 accepts "__end__" as the explicit terminal target.
    } else if (!nodeIDs.has(edge.to)) {
      issues.push({
        id: `edge-target-missing-${edgeID}`,
        severity: "error",
        message: `Edge target "${edge.to}" does not reference an existing node.`,
        edgeID,
      });
    } else {
      degree.set(edge.to, (degree.get(edge.to) ?? 0) + 1);
    }
    if (edge.condition) {
      const condition = registry?.conditions.find((item) => item.type === edge.condition?.type);
      if (registry && !condition) {
        issues.push({
          id: `condition-type-unknown-${edgeID}`,
          severity: "error",
          message: `Edge condition "${edge.condition.type}" is not registered.`,
          edgeID,
        });
      }
      issues.push(...lintComponentBindings({
        component: `Condition "${edge.condition.type}"`,
        stableID: `condition-${edgeID}`,
        bindings: edge.condition.state,
        ports: condition?.state_ports,
        dynamicPorts: condition?.dynamic_state_ports,
        selectedModules,
        selectedCapabilities,
        registeredCapabilities: registry?.capabilities ?? [],
        rootCapabilities,
        nodeID: edge.from,
        edgeID,
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
          nodeID: node.id,
        });
      }
    }
  }

  issues.push(...lintSupervisorTopology(definition));

  const parsedInitialState = parseInitialStateText(initialStateText);
  if (parsedInitialState.error !== null) {
    issues.push({
      id: "initial-state-json-invalid",
      severity: "error",
      message: `Initial state JSON is invalid: ${parsedInitialState.error}`,
      path: "initial_state",
    });
  } else if (initialRequirements) {
    for (const requirement of initialRequirements.required ?? []) {
      if (!hasFilledInitialStatePath(parsedInitialState.root, requirement.path, requirement.type)) {
        issues.push({
          id: `initial-state-missing-${requirement.path}`,
          severity: "error",
          message: `Initial state must provide a valid value for "${requirement.path}".`,
          path: requirement.path,
        });
      }
    }
  }

  for (const requirement of initialRequirements?.unresolved ?? []) {
    issues.push(requirementIssue("unresolved", requirement, "warn"));
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

export function issueCounts(issues: GraphLintIssue[]): { errors: number; warnings: number } {
  return {
    errors: issues.filter((issue) => issue.severity === "error").length,
    warnings: issues.filter((issue) => issue.severity === "warn").length,
  };
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
    nodeID: requirement.nodes?.[0],
    path: requirement.path,
  };
}

function warningIssue(warning: WarningRecord): GraphLintIssue {
  const stableID = [warning.code, warning.node_id, warning.other_node_id, warning.path, warning.message]
    .filter(Boolean)
    .join(":");
  return {
    id: stableID || "warning",
    severity: "warn",
    message: warning.message || warning.code || "Graph analysis warning.",
    nodeID: warning.node_id,
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
