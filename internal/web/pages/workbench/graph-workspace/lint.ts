import { graphEdgeId } from "../../../lib/graphEditor";
import type { GraphDefinition, InitialStateRequirements, InitialStateRequirement, WarningRecord } from "../../../types";

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
}: {
  definition: GraphDefinition | null;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
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
    for (const pathIssue of lintStatePathReferences(node.id, node.config ?? {})) {
      issues.push(pathIssue);
    }
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

  if (!definition.finish_point?.trim()) {
    issues.push({
      id: "graph-finish-missing",
      severity: "error",
      message: "Finish point is missing.",
      path: "finish_point",
    });
  } else if (!nodeIds.has(definition.finish_point)) {
    issues.push({
      id: "graph-finish-invalid",
      severity: "error",
      message: `Finish point "${definition.finish_point}" does not reference an existing node.`,
      path: "finish_point",
    });
  }

  const degree = new Map<string, number>();
  for (const nodeId of nodeIds) degree.set(nodeId, 0);
  for (const [index, edge] of (definition.edges ?? []).entries()) {
    const edgeId = graphEdgeId(edge, index);
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
    if (!nodeIds.has(edge.to)) {
      issues.push({
        id: `edge-target-missing-${edgeId}`,
        severity: "error",
        message: `Edge target "${edge.to}" does not reference an existing node.`,
        edgeId,
      });
    } else {
      degree.set(edge.to, (degree.get(edge.to) ?? 0) + 1);
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

  return dedupeIssues(issues);
}

export function issueCounts(issues: GraphLintIssue[]): { errors: number; warnings: number } {
  return {
    errors: issues.filter((issue) => issue.severity === "error").length,
    warnings: issues.filter((issue) => issue.severity === "warn").length,
  };
}

function lintStatePathReferences(nodeId: string, config: Record<string, unknown>): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];
  const visit = (value: unknown, keyPath: string) => {
    if (!isRecord(value)) return;
    for (const [key, raw] of Object.entries(value)) {
      const lower = key.toLowerCase();
      const nextKeyPath = keyPath ? `${keyPath}.${key}` : key;
      if (typeof raw === "string" && isStatePathConfigKey(lower)) {
        const message = validateStatePath(raw);
        if (message) {
          issues.push({
            id: `node-state-path-${nodeId}-${nextKeyPath}`,
            severity: "warn",
            message: `Node "${nodeId}" config "${nextKeyPath}" has an invalid state path: ${message}`,
            nodeId,
            path: nextKeyPath,
          });
        }
      } else if (isRecord(raw) && isPathMappingKey(lower)) {
        for (const [from, to] of Object.entries(raw)) {
          for (const [label, pathValue] of [
            ["key", from],
            ["value", to],
          ] as const) {
            if (typeof pathValue !== "string") continue;
            const message = validateStatePath(pathValue);
            if (message) {
              issues.push({
                id: `node-state-map-${nodeId}-${nextKeyPath}-${label}-${pathValue}`,
                severity: "warn",
                message: `Node "${nodeId}" mapping "${nextKeyPath}" has an invalid ${label} path "${pathValue}": ${message}`,
                nodeId,
                path: nextKeyPath,
              });
            }
          }
        }
      } else if (isRecord(raw)) {
        visit(raw, nextKeyPath);
      }
    }
  };

  visit(config, "");
  return issues;
}

function isStatePathConfigKey(key: string): boolean {
  return key === "state_key" || key.endsWith("_path") || key.endsWith("state_path") || key.includes("state_path");
}

function isPathMappingKey(key: string): boolean {
  return key === "input_map" || key === "output_map" || key.endsWith("_map");
}

function validateStatePath(path: string): string {
  const parts = path.split(".").map((part) => part.trim());
  if (parts.length === 0 || !parts[0]) return "path is empty";
  if (!["shared", "scopes", "internal", "runtime"].includes(parts[0])) {
    return `section must be shared, scopes, internal, or runtime`;
  }
  if (parts.some((part) => !part)) return "path contains an empty segment";
  return "";
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
