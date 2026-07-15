import type { VirtualGraphEdge } from "../../../components/GraphCanvas";
import { END_NODE_REF, START_NODE_REF, graphEdgeId } from "../../../lib/graphEditor";
import { parseJSON } from "../../../lib/utils";
import type { GraphDefinition, GraphNodeSpec, NodeTypeSchema, RegistryInfo } from "../../../types";
import { fallbackNodeTypes } from "./constants";
import type { VirtualNodeKind } from "./types";

export function validateGraph(definition: GraphDefinition | null, registry?: RegistryInfo | null): string {
  if (!definition) return "invalid json";
  if (definition.version !== "2.0") return "version must be 2.0";
  if ((definition.state_modules ?? []).length === 0) return "state modules required";
  if (definition.nodes.length === 0) return "no nodes";
  const nodeIds = new Set(definition.nodes.map((node) => node.id));
  if (definition.nodes.some((node) => !node.id || !node.type)) return "node required";
  for (const node of definition.nodes) {
    const nodeType = registry?.node_types.find((item) => item.type === node.type);
    for (const port of nodeType?.state_ports ?? []) {
      if (port.required && !node.state?.[port.name]?.path.trim()) {
        return `node ${node.id} requires state binding ${port.name}`;
      }
    }
    if (node.type === "conversation_input") {
      const content = typeof node.config?.content === "string" ? node.config.content.trim() : "";
      const inputPath = node.state?.input?.path.trim() ?? "";
      const pendingInputPath = node.state?.pending_input?.path.trim() ?? "";
      if (!content && !inputPath && !pendingInputPath) {
        return `node ${node.id} requires state binding pending_input when content and input are empty`;
      }
    }
  }
  if (nodeIds.size !== definition.nodes.length) return "duplicate nodes";
  if (definition.entry_point && !nodeIds.has(definition.entry_point)) return "missing entry";
  if (definition.finish_point && !nodeIds.has(definition.finish_point)) return "missing finish";
  if (!definition.finish_point && !(definition.edges ?? []).some((edge) => edge.to === END_NODE_REF)) return "missing finish";
  const edgePairs = new Set<string>();
  for (const edge of definition.edges ?? []) {
    const edgeKey = `${edge.from}\u0000${edge.to}`;
    if (edgePairs.has(edgeKey)) return "duplicate edges";
    edgePairs.add(edgeKey);
    if (!nodeIds.has(edge.from)) return "missing source";
    if (edge.to !== END_NODE_REF && !nodeIds.has(edge.to)) return "missing target";
    if (edge.condition) {
      const condition = registry?.conditions.find((item) => item.type === edge.condition?.type);
      for (const port of condition?.state_ports ?? []) {
        if (port.required && !edge.condition.state?.[port.name]?.path.trim()) {
          return `condition ${edge.condition.type} requires state binding ${port.name}`;
        }
      }
    }
  }
  return "";
}

export function parseJSONObject(value: string): Record<string, unknown> {
  const parsed = parseJSON<unknown>(value);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("json object required");
  }
  return parsed as Record<string, unknown>;
}

export function findLastEdgeId(definition: GraphDefinition, from: string, to: string): string | null {
  const source = from.trim();
  const target = to.trim();
  const edges = definition.edges ?? [];
  for (let index = edges.length - 1; index >= 0; index -= 1) {
    const edge = edges[index];
    if (edge.from === source && edge.to === target) {
      return graphEdgeId(edge, index);
    }
  }
  return null;
}

export function virtualEdgeId(from: string, to: string, kind: VirtualGraphEdge["kind"]): string {
  return `virtual:${kind}:${from}->${to}`;
}

export function lastVirtualEdge(edges: VirtualGraphEdge[], kind: VirtualGraphEdge["kind"]): VirtualGraphEdge | undefined {
  for (let index = edges.length - 1; index >= 0; index -= 1) {
    if (edges[index].kind === kind) return edges[index];
  }
  return undefined;
}

export function displayNodeRef(nodeID: string, definition: GraphDefinition | null, virtualNodes: GraphNodeSpec[]): string {
  const virtualNode = virtualNodes.find((node) => node.id === nodeID);
  if (virtualNode) return virtualNode.name || virtualNode.id;
  const node = definition?.nodes.find((item) => item.id === nodeID);
  return node?.name || nodeID;
}

export function realNodeTypes(nodeTypes: NodeTypeSchema[]): NodeTypeSchema[] {
  const result = nodeTypes.filter((nodeType) => !isVirtualNodeType(nodeType.type));
  return result.length ? result : fallbackNodeTypes;
}

export function isVirtualNodeType(type?: string): type is VirtualNodeKind {
  return type === "start" || type === "end";
}

export function isVirtualNodeId(nodeID: string): boolean {
  return Boolean(virtualNodeKind(nodeID));
}

export function virtualNodeSpec(nodeID: string): GraphNodeSpec {
  const kind = virtualNodeKind(nodeID);
  return {
    id: nodeID,
    name: virtualNodeLabel(nodeID),
    type: kind ?? "node",
    config: {},
  };
}

export function virtualNodeLabel(nodeID: string): string {
  const kind = virtualNodeKind(nodeID);
  const index = virtualNodeIndex(nodeID);
  const label = kind === "start" ? "Start" : "End";
  return index > 1 ? `${label} ${index}` : label;
}

export function nextVirtualNodeId(kind: VirtualNodeKind, nodeIDs: string[]): string {
  const base = kind === "start" ? START_NODE_REF : END_NODE_REF;
  const used = new Set(nodeIDs);
  if (!used.has(base)) return base;
  for (let index = 2; index < 1000; index += 1) {
    const id = `${base}:${index}`;
    if (!used.has(id)) return id;
  }
  return `${base}:${Date.now().toString(36)}`;
}

export function virtualNodeKind(nodeID: string): VirtualNodeKind | undefined {
  if (nodeID === START_NODE_REF || nodeID.startsWith(`${START_NODE_REF}:`)) return "start";
  if (nodeID === END_NODE_REF || nodeID.startsWith(`${END_NODE_REF}:`)) return "end";
  return undefined;
}

function virtualNodeIndex(nodeID: string): number {
  const kind = virtualNodeKind(nodeID);
  const base = kind === "start" ? START_NODE_REF : kind === "end" ? END_NODE_REF : "";
  const prefix = `${base}:`;
  if (!base || nodeID === base || !nodeID.startsWith(prefix)) return 1;
  const index = Number(nodeID.slice(prefix.length));
  return Number.isInteger(index) && index > 1 ? index : 1;
}
