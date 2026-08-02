import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import { END_NODE_REF, START_NODE_REF } from "../../../lib/graphEditor";
import { cloneJSONValue, isPlainRecord } from "../../../lib/utils";
import type { GraphDefinition, GraphNodeSpec } from "../../../types";
import { defaultVirtualNodeIds } from "./constants";
import { withCleanTriggerCanvasPositions } from "./triggerCanvas";
import { virtualEdgeId } from "./utils";

export function graphCanvasViewportStorageKey(
  graphID: string,
  graphVersion: string,
  activeDraftID: string,
  definition: GraphDefinition | null
): string {
  return [
    activeDraftID || "server",
    graphID || definition?.name || "graph",
    graphVersion || definition?.version || "1.0",
  ]
    .map((part) => encodeURIComponent(part.trim() || "-"))
    .join(":");
}

export function graphScriptBadgeCount(definition: GraphDefinition | null): number {
  if (!definition) return 0;
  const metadata = isPlainRecord(definition.metadata) ? definition.metadata : undefined;
  return scriptGroupCount(definition) + scriptGroupCount(metadata) + scriptGroupCount(metadata?.web);
}

export function virtualEdgesFromDefinition(
  definition: GraphDefinition | null,
  virtualNodeIDs: string[]
): VirtualGraphEdge[] {
  if (!definition) return [];
  const visible = new Set(virtualNodeIDs);
  const edges: VirtualGraphEdge[] = [];
  if (definition.entry_point && visible.has(START_NODE_REF)) {
    edges.push({
      id: virtualEdgeId(START_NODE_REF, definition.entry_point, "entry"),
      from: START_NODE_REF,
      to: definition.entry_point,
      kind: "entry",
    });
  }
  if (definition.finish_point && visible.has(END_NODE_REF)) {
    edges.push({
      id: virtualEdgeId(definition.finish_point, END_NODE_REF, "finish"),
      from: definition.finish_point,
      to: END_NODE_REF,
      kind: "finish",
    });
  }
  return edges;
}

export function mergeVirtualEdges(primary: VirtualGraphEdge[], secondary: VirtualGraphEdge[]): VirtualGraphEdge[] {
  const seen = new Set<string>();
  const result: VirtualGraphEdge[] = [];
  const addOrReplace = (edge: VirtualGraphEdge) => {
    if (!seen.has(edge.id)) {
      seen.add(edge.id);
      result.push(edge);
      return;
    }
    const index = result.findIndex((item) => item.id === edge.id);
    if (index >= 0) result[index] = { ...result[index], ...edge };
  };
  for (const edge of primary) addOrReplace(edge);
  for (const edge of secondary) addOrReplace(edge);
  return result;
}

export function upsertVirtualEdge(
  edges: VirtualGraphEdge[],
  previousEdge: VirtualGraphEdge,
  nextEdge: VirtualGraphEdge
): VirtualGraphEdge[] {
  const remaining = edges.filter((edge) => {
    if (edge.id === previousEdge.id || edge.id === nextEdge.id) return false;
    return !(nextEdge.kind === "entry" && edge.kind === "entry" && edge.from === nextEdge.from);
  });
  return [...remaining, nextEdge];
}

export function withSavedGraphWorkspaceState(
  definition: GraphDefinition,
  virtualNodeIDs: string[],
  virtualEdges: VirtualGraphEdge[],
  virtualLoops: VirtualGraphLoop[],
  validTriggerIDs?: string[]
): GraphDefinition {
  const metadata = { ...(definition.metadata ?? {}) };
  const web = isPlainRecord(metadata.web) ? { ...metadata.web } : {};
  web.virtual_node_ids = virtualNodeIDs;
  web.virtual_edges = virtualEdges.map((edge) => ({
    id: edge.id,
    from: edge.from,
    to: edge.to,
    kind: edge.kind,
    condition: edge.condition,
  }));
  web.virtual_loops = virtualLoops.map((loop) => ({
    id: loop.id,
    name: loop.name,
    node_ids: loop.nodeIds,
  }));
  metadata.web = web;
  const next = { ...definition, metadata };
  return validTriggerIDs ? withCleanTriggerCanvasPositions(next, validTriggerIDs) : next;
}

export function savedGraphWorkspaceState(definition: GraphDefinition): {
  virtualNodeIDs: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
} {
  const web = isPlainRecord(definition.metadata?.web) ? definition.metadata.web : undefined;
  const rawNodeIDs = Array.isArray(web?.virtual_node_ids) ? web.virtual_node_ids : [];
  const virtualNodeIDs = rawNodeIDs.filter((item): item is string => typeof item === "string" && item.trim() !== "");
  const rawEdges = Array.isArray(web?.virtual_edges) ? web.virtual_edges : [];
  const virtualEdges = rawEdges.filter(isVirtualGraphEdge);
  const rawLoops = Array.isArray(web?.virtual_loops) ? web.virtual_loops : [];
  const virtualLoops = rawLoops.map(parseVirtualGraphLoop).filter((loop): loop is VirtualGraphLoop => Boolean(loop));
  return {
    virtualNodeIDs: virtualNodeIDs.length ? virtualNodeIDs : defaultVirtualNodeIds,
    virtualEdges,
    virtualLoops,
  };
}

export function normalizeVirtualLoop(loop: VirtualGraphLoop): VirtualGraphLoop {
  return {
    id: loop.id.trim(),
    name: loop.name?.trim(),
    nodeIds: uniqueStrings(loop.nodeIds),
  };
}

export function uniqueNodeID(baseID: string, nodes: GraphNodeSpec[]): string {
  const used = new Set(nodes.map((node) => node.id));
  if (!used.has(baseID)) return baseID;
  for (let index = 2; index < 1000; index += 1) {
    const nodeID = `${baseID}_${index}`;
    if (!used.has(nodeID)) return nodeID;
  }
  return `${baseID}_${Date.now().toString(36)}`;
}

export function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const item = value.trim();
    if (!item || seen.has(item)) continue;
    seen.add(item);
    result.push(item);
  }
  return result;
}

export function cloneJSONRecord(value: unknown): Record<string, unknown> {
  if (!isPlainRecord(value)) return {};
  return cloneJSONValue(value) as Record<string, unknown>;
}

export function autoSaveSignature(
  definition: GraphDefinition,
  graphID: string,
  graphVersion: string,
  virtualNodeIDs: string[],
  virtualEdges: VirtualGraphEdge[],
  virtualLoops: VirtualGraphLoop[],
  validTriggerIDs?: string[]
): string {
  return JSON.stringify({
    graphId: graphID,
    graphVersion,
    definition: withSavedGraphWorkspaceState(
      definition,
      virtualNodeIDs,
      virtualEdges,
      virtualLoops,
      validTriggerIDs
    ),
  });
}

function scriptGroupCount(value: unknown): number {
  if (!isPlainRecord(value)) return 0;
  let count = 0;
  count += scriptValueCount(value.pre);
  count += scriptValueCount(value.post);
  count += scriptValueCount(value.before);
  count += scriptValueCount(value.after);
  count += scriptValueCount(value.pre_script);
  count += scriptValueCount(value.post_script);
  count += scriptValueCount(value.pre_scripts);
  count += scriptValueCount(value.post_scripts);
  count += scriptValueCount(value.preScript);
  count += scriptValueCount(value.postScript);
  count += scriptValueCount(value.preScripts);
  count += scriptValueCount(value.postScripts);
  count += scriptContainerCount(value.scripts);
  count += scriptContainerCount(value.hooks);
  return count;
}

function scriptContainerCount(value: unknown): number {
  if (!isPlainRecord(value)) return 0;
  return (
    scriptValueCount(value.pre) +
    scriptValueCount(value.post) +
    scriptValueCount(value.before) +
    scriptValueCount(value.after) +
    scriptValueCount(value.pre_script) +
    scriptValueCount(value.post_script)
  );
}

function scriptValueCount(value: unknown): number {
  if (typeof value === "string") return value.trim() ? 1 : 0;
  if (Array.isArray(value)) return value.reduce((total, item) => total + scriptValueCount(item), 0);
  if (isPlainRecord(value)) return Object.keys(value).length > 0 ? 1 : 0;
  return value ? 1 : 0;
}

function isVirtualGraphEdge(value: unknown): value is VirtualGraphEdge {
  if (!isPlainRecord(value)) return false;
  const condition = value.condition;
  return (
    typeof value.id === "string" &&
    typeof value.from === "string" &&
    typeof value.to === "string" &&
    (value.kind === "entry" || value.kind === "finish") &&
    (condition === undefined || isGraphConditionSpec(condition))
  );
}

function parseVirtualGraphLoop(value: unknown): VirtualGraphLoop | null {
  if (!isPlainRecord(value)) return null;
  const nodeIDs = value.nodeIds ?? value.node_ids;
  if (
    typeof value.id !== "string" ||
    (value.name !== undefined && typeof value.name !== "string") ||
    !Array.isArray(nodeIDs) ||
    !nodeIDs.every((item) => typeof item === "string")
  ) {
    return null;
  }
  return normalizeVirtualLoop({
    id: value.id,
    name: value.name,
    nodeIds: nodeIDs,
  });
}

function isGraphConditionSpec(value: unknown): boolean {
  if (!isPlainRecord(value)) return false;
  return typeof value.type === "string" && (value.config === undefined || isPlainRecord(value.config));
}
