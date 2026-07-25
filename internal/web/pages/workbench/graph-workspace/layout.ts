import dagre from "dagre";
import { graphEdgeId, withNodePositions, type NodePosition } from "../../../lib/graphEditor";
import { analyzeVirtualGraphLoop } from "../../../lib/loopPresentation";
import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import type { GraphDefinition } from "../../../types";

const nodeWidth = 190;
const nodeHeight = 76;

export function autoLayoutGraph(
  definition: GraphDefinition,
  virtualNodeIds: string[],
  virtualEdges: VirtualGraphEdge[],
  virtualLoops: VirtualGraphLoop[] = []
): GraphDefinition {
  const graph = new dagre.graphlib.Graph();
  graph.setGraph({
    rankdir: "LR",
    align: "UL",
    nodesep: 54,
    ranksep: 88,
    marginx: 32,
    marginy: 32,
  });
  graph.setDefaultEdgeLabel(() => ({}));

  const ids = new Set<string>();
  for (const id of virtualNodeIds) {
    ids.add(id);
    graph.setNode(id, { width: nodeWidth, height: nodeHeight });
  }
  for (const node of definition.nodes) {
    ids.add(node.id);
    graph.setNode(node.id, { width: nodeWidth, height: nodeHeight });
  }
  for (const loop of virtualLoops) {
    if (loop.nodeIds.length > 0) continue;
    ids.add(loop.id);
    graph.setNode(loop.id, { width: nodeWidth + 60, height: nodeHeight + 74 });
  }

  for (const edge of virtualEdges) {
    if (ids.has(edge.from) && ids.has(edge.to)) graph.setEdge(edge.from, edge.to);
  }
  const hiddenBackEdgeIds = new Set(
    virtualLoops.flatMap((loop) => [...analyzeVirtualGraphLoop(definition, loop).backEdgeIds])
  );
  for (const [index, edge] of (definition.edges ?? []).entries()) {
    if (hiddenBackEdgeIds.has(graphEdgeId(edge, index))) continue;
    if (ids.has(edge.from) && ids.has(edge.to)) graph.setEdge(edge.from, edge.to);
  }

  dagre.layout(graph);

  const positions = new Map<string, NodePosition>();
  for (const id of ids) {
    const node = graph.node(id) as { x?: number; y?: number } | undefined;
    if (!node || typeof node.x !== "number" || typeof node.y !== "number") continue;
    positions.set(id, {
      x: Math.round(node.x - nodeWidth / 2),
      y: Math.round(node.y - nodeHeight / 2),
    });
  }

  return withNodePositions(definition, positions);
}
