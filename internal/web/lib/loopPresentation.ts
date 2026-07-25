import type { GraphConditionSpec, GraphDefinition, GraphEdgeSpec } from "../types";
import { graphEdgeId } from "./graphEditor";

export interface VirtualGraphLoop {
  id: string;
  name?: string;
  nodeIds: string[];
  automatic?: boolean;
}

export interface VirtualLoopAnalysis {
  nodeIds: string[];
  nodeIdSet: Set<string>;
  loopStartId: string;
  loopEndIds: string[];
  conditionNodeIds: string[];
  backEdgeIds: Set<string>;
  conditionLabels: string[];
  nextNodeIds: string[];
}

export interface LoopDisplayEdge {
  edge: GraphEdgeSpec;
  id: string;
  selectionId?: string;
  source: string;
  target: string;
  sourceHandle?: string;
  targetHandle?: string;
  showLabel?: boolean;
  contained?: boolean;
}

export const loopStartHandleId = "loop-start";
export const loopStartInnerHandleId = "loop-start-inner";
export const loopContinueHandleId = "loop-continue";
export const loopEndHandleId = "loop-end";
export const loopEndInnerHandleId = "loop-end-inner";

export function detectVirtualGraphLoops(definition: GraphDefinition | null): VirtualGraphLoop[] {
  if (!definition || definition.nodes.length === 0) return [];

  const nodeOrder = new Map(definition.nodes.map((node, index) => [node.id, index]));
  const nodeIds = new Set(nodeOrder.keys());
  const outgoing = new Map<string, string[]>();
  const selfLoopNodeIds = new Set<string>();
  for (const edge of definition.edges ?? []) {
    if (!nodeIds.has(edge.from) || !nodeIds.has(edge.to)) continue;
    outgoing.set(edge.from, [...(outgoing.get(edge.from) ?? []), edge.to]);
    if (edge.from === edge.to) selfLoopNodeIds.add(edge.from);
  }

  const indexByNode = new Map<string, number>();
  const lowLinkByNode = new Map<string, number>();
  const stack: string[] = [];
  const onStack = new Set<string>();
  const components: string[][] = [];
  let nextIndex = 0;

  function visit(nodeId: string) {
    indexByNode.set(nodeId, nextIndex);
    lowLinkByNode.set(nodeId, nextIndex);
    nextIndex += 1;
    stack.push(nodeId);
    onStack.add(nodeId);

    for (const targetId of outgoing.get(nodeId) ?? []) {
      if (!indexByNode.has(targetId)) {
        visit(targetId);
        lowLinkByNode.set(nodeId, Math.min(lowLinkByNode.get(nodeId)!, lowLinkByNode.get(targetId)!));
      } else if (onStack.has(targetId)) {
        lowLinkByNode.set(nodeId, Math.min(lowLinkByNode.get(nodeId)!, indexByNode.get(targetId)!));
      }
    }

    if (lowLinkByNode.get(nodeId) !== indexByNode.get(nodeId)) return;
    const component: string[] = [];
    while (stack.length > 0) {
      const memberId = stack.pop()!;
      onStack.delete(memberId);
      component.push(memberId);
      if (memberId === nodeId) break;
    }
    components.push(component);
  }

  for (const node of definition.nodes) {
    if (!indexByNode.has(node.id)) visit(node.id);
  }

  return components
    .filter((component) => component.length > 1 || selfLoopNodeIds.has(component[0]))
    .map((component) => component.sort((left, right) => nodeOrder.get(left)! - nodeOrder.get(right)!))
    .sort((left, right) => nodeOrder.get(left[0])! - nodeOrder.get(right[0])!)
    .map((component) => {
      const analysis = analyzeVirtualGraphLoop(definition, { nodeIds: component });
      return {
        id: `loop:auto:${analysis.loopStartId || component[0]}`,
        name: "Loop",
        nodeIds: component,
        automatic: true,
      };
    });
}

export function mergeVirtualGraphLoops(
  explicitLoops: VirtualGraphLoop[],
  automaticLoops: VirtualGraphLoop[]
): VirtualGraphLoop[] {
  const explicitIds = new Set(explicitLoops.map((loop) => loop.id));
  const claimedNodeIds = new Set(explicitLoops.flatMap((loop) => loop.nodeIds));
  return [
    ...explicitLoops,
    ...automaticLoops.filter(
      (loop) => !explicitIds.has(loop.id) && !loop.nodeIds.some((nodeId) => claimedNodeIds.has(nodeId))
    ),
  ];
}

export function analyzeVirtualGraphLoop(
  definition: GraphDefinition | null,
  loop: Pick<VirtualGraphLoop, "nodeIds">
): VirtualLoopAnalysis {
  const graphNodeIds = new Set(definition?.nodes.map((node) => node.id) ?? []);
  const nodeIds = uniqueStrings(loop.nodeIds).filter((nodeId) => graphNodeIds.has(nodeId));
  const nodeIdSet = new Set(nodeIds);
  const indexedEdges = (definition?.edges ?? []).map((edge, index) => ({ edge, id: graphEdgeId(edge, index) }));
  const internalEdges = indexedEdges.filter(({ edge }) => nodeIdSet.has(edge.from) && nodeIdSet.has(edge.to));
  const incomingEdges = indexedEdges.filter(({ edge }) => !nodeIdSet.has(edge.from) && nodeIdSet.has(edge.to));
  const outgoingEdges = indexedEdges.filter(({ edge }) => nodeIdSet.has(edge.from) && !nodeIdSet.has(edge.to));
  const entryPoint = definition?.entry_point;
  const loopStartId = incomingEdges[0]?.edge.to
    || (entryPoint && nodeIdSet.has(entryPoint) ? entryPoint : "")
    || nodeIds[0]
    || "";
  const backEdgeIds = findBackEdgeIds(nodeIds, loopStartId, internalEdges);
  const loopEndIds = uniqueStrings(outgoingEdges.map(({ edge }) => edge.from));
  const fallbackLoopEndIds = uniqueStrings([
    ...(definition?.finish_point && nodeIdSet.has(definition.finish_point) ? [definition.finish_point] : []),
    ...internalEdges.filter(({ id }) => backEdgeIds.has(id)).map(({ edge }) => edge.from),
  ]);
  const conditionEdges = [...internalEdges, ...outgoingEdges].filter(({ edge }) => Boolean(edge.condition));
  const conditionNodeIds = uniqueStrings(conditionEdges.map(({ edge }) => edge.from));
  const conditionLabels = uniqueStrings([
    ...internalEdges
      .filter(({ edge }) => Boolean(edge.condition))
      .map(({ edge }) => `continue · ${conditionDisplayLabel(edge.condition!)}`),
    ...outgoingEdges
      .filter(({ edge }) => Boolean(edge.condition))
      .map(({ edge }) => `exit · ${conditionDisplayLabel(edge.condition!)}`),
  ]);

  return {
    nodeIds,
    nodeIdSet,
    loopStartId,
    loopEndIds: loopEndIds.length > 0 ? loopEndIds : fallbackLoopEndIds.length > 0 ? fallbackLoopEndIds : loopStartId ? [loopStartId] : [],
    conditionNodeIds,
    backEdgeIds,
    conditionLabels,
    nextNodeIds: uniqueStrings(outgoingEdges.map(({ edge }) => edge.to)),
  };
}

export function graphEdgesForLoopDisplay(
  definition: GraphDefinition,
  loops: VirtualGraphLoop[]
): LoopDisplayEdge[] {
  const presentations = loops.map((loop) => ({ loop, analysis: analyzeVirtualGraphLoop(definition, loop) }));
  return (definition.edges ?? []).flatMap((edge, index) =>
    buildEdgeSegmentsForLoopDisplay(edge, graphEdgeId(edge, index), presentations)
  );
}

export function edgeSegmentsForLoopDisplay(
  definition: GraphDefinition,
  edge: GraphEdgeSpec,
  id: string,
  loops: VirtualGraphLoop[]
): LoopDisplayEdge[] {
  const presentations = loops.map((loop) => ({ loop, analysis: analyzeVirtualGraphLoop(definition, loop) }));
  return buildEdgeSegmentsForLoopDisplay(edge, id, presentations);
}

function buildEdgeSegmentsForLoopDisplay(
  edge: GraphEdgeSpec,
  id: string,
  presentations: Array<{ loop: VirtualGraphLoop; analysis: VirtualLoopAnalysis }>
): LoopDisplayEdge[] {
  const backEdgeLoop = presentations.find(({ analysis }) => analysis.backEdgeIds.has(id));
  if (backEdgeLoop) {
    return [{
      edge,
      id,
      selectionId: id,
      source: edge.from,
      target: backEdgeLoop.loop.id,
      targetHandle: loopContinueHandleId,
      contained: true,
    }];
  }

  const sourceLoop = presentations.find(
    ({ analysis }) => analysis.nodeIdSet.has(edge.from) && !analysis.nodeIdSet.has(edge.to)
  );
  const targetLoop = presentations.find(
    ({ analysis }) => !analysis.nodeIdSet.has(edge.from) && analysis.nodeIdSet.has(edge.to)
  );

  if (sourceLoop && targetLoop) {
    return [
      {
        edge,
        id: segmentId(id, "end"),
        selectionId: id,
        source: edge.from,
        target: sourceLoop.loop.id,
        targetHandle: loopEndInnerHandleId,
        contained: true,
      },
      {
        edge,
        id,
        selectionId: id,
        source: sourceLoop.loop.id,
        target: targetLoop.loop.id,
        sourceHandle: loopEndHandleId,
        targetHandle: loopStartHandleId,
        showLabel: false,
      },
      {
        edge,
        id: segmentId(id, "start"),
        selectionId: id,
        source: targetLoop.loop.id,
        target: edge.to,
        sourceHandle: loopStartInnerHandleId,
        showLabel: false,
        contained: true,
      },
    ];
  }

  if (sourceLoop) {
    return [
      {
        edge,
        id: segmentId(id, "end"),
        selectionId: id,
        source: edge.from,
        target: sourceLoop.loop.id,
        targetHandle: loopEndInnerHandleId,
        contained: true,
      },
      {
        edge,
        id,
        selectionId: id,
        source: sourceLoop.loop.id,
        target: edge.to,
        sourceHandle: loopEndHandleId,
        showLabel: false,
      },
    ];
  }

  if (targetLoop) {
    return [
      {
        edge,
        id,
        selectionId: id,
        source: edge.from,
        target: targetLoop.loop.id,
        targetHandle: loopStartHandleId,
      },
      {
        edge,
        id: segmentId(id, "start"),
        selectionId: id,
        source: targetLoop.loop.id,
        target: edge.to,
        sourceHandle: loopStartInnerHandleId,
        showLabel: false,
        contained: true,
      },
    ];
  }

  return [{ edge, id, selectionId: id, source: edge.from, target: edge.to }];
}

function segmentId(id: string, segment: string): string {
  return `${id}:loop-${segment}`;
}

export function conditionDisplayLabel(condition: GraphConditionSpec): string {
  const config = condition.config ?? {};
  if (condition.type === "expression_conditions") {
    const count = Array.isArray(config.expressions) ? config.expressions.length : 0;
    const match = typeof config.match === "string" ? config.match : "all";
    return count > 0 ? `${match} expressions (${count})` : "expression conditions";
  }

  const scalarEntries = Object.entries(config).filter(([, value]) =>
    typeof value === "string" || typeof value === "number" || typeof value === "boolean"
  );
  if (condition.type.endsWith("_equals") && scalarEntries.length === 1) {
    const [key, value] = scalarEntries[0];
    const subject = humanizeIdentifier(condition.type.slice(0, -"_equals".length));
    const keyLabel = humanizeIdentifier(key);
    return subject === keyLabel || subject.endsWith(` ${keyLabel}`)
      ? `${subject} = ${String(value)}`
      : `${subject} · ${keyLabel} = ${String(value)}`;
  }
  return humanizeIdentifier(condition.type);
}

function findBackEdgeIds(
  nodeIds: string[],
  loopStartId: string,
  internalEdges: Array<{ edge: GraphEdgeSpec; id: string }>
): Set<string> {
  const outgoing = new Map<string, Array<{ edge: GraphEdgeSpec; id: string }>>();
  for (const item of internalEdges) {
    outgoing.set(item.edge.from, [...(outgoing.get(item.edge.from) ?? []), item]);
  }

  const state = new Map<string, "visiting" | "visited">();
  const backEdgeIds = new Set<string>();
  function visit(nodeId: string) {
    state.set(nodeId, "visiting");
    for (const item of outgoing.get(nodeId) ?? []) {
      const targetState = state.get(item.edge.to);
      if (targetState === "visiting") {
        backEdgeIds.add(item.id);
      } else if (!targetState) {
        visit(item.edge.to);
      }
    }
    state.set(nodeId, "visited");
  }

  if (loopStartId) visit(loopStartId);
  for (const nodeId of nodeIds) {
    if (!state.has(nodeId)) visit(nodeId);
  }
  return backEdgeIds;
}

function humanizeIdentifier(value: string): string {
  return value
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .toLowerCase();
}

function uniqueStrings(values: string[]): string[] {
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
