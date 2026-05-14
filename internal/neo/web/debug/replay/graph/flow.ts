import { Position, type Edge, type Node } from "@xyflow/react";
import type { CSSProperties } from "react";
import type {
  FlowNodeData,
  GraphEdgeMeta,
  GraphNodeMeta,
  GraphProjection,
  NodeEventSummary,
  SourceGraph,
} from "./types";
import { SYNTHETIC_END_ID, SYNTHETIC_START_ID } from "./types";
import { hasTokenUsageMetrics } from "./utils";
import { buildNodeLabel } from "./NodeLabel";

export function buildBaseFlow(
  sourceGraph: SourceGraph,
  projection: GraphProjection,
  runId = "",
  sourceId = "",
  cacheDir = ""
): {
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
} {
  return {
    nodes: sourceGraph.nodes.map((node) => {
      const summary = projection.nodeEventSummaries.get(node.id);
      return {
        id: node.id,
        position: { x: 0, y: 0 },
        draggable: true,
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
        data: {
          label: buildNodeLabel(node, sourceGraph, projection, summary, runId, sourceId, cacheDir),
          meta: node,
          elkHeight: nodeElkHeight(node, summary),
          elkWidth: nodeElkWidth(node),
        },
        style: nodeStyle(node, sourceGraph, projection),
      };
    }),
    edges: sourceGraph.edges.map((edge) => ({
      id: edge.id,
      source: edge.from,
      target: edge.to,
      data: { conditional: edge.conditional },
      label: edge.label || undefined,
      labelStyle: edge.label
        ? {
            fill: edge.conditional ? "#c084fc" : "#94a3b8",
            fontSize: 10.5,
            fontWeight: 700,
          }
        : undefined,
      labelShowBg: edge.conditional,
      labelBgPadding: edge.conditional ? [12, 6] : undefined,
      labelBgBorderRadius: edge.conditional ? 8 : undefined,
      labelBgStyle: edge.conditional
        ? {
            fill: "rgba(88, 28, 135, 0.25)",
            stroke: "rgba(167, 139, 250, 0.35)",
            strokeWidth: 1,
          }
        : undefined,
      animated: projection.currentEdgeId === edge.id,
      style: edgeStyle(edge, projection),
      markerEnd: {
        type: "arrowclosed",
        color: edgeColor(edge, projection),
      },
    })),
  };
}

export function applyProjectionToNodes(
  nodes: Node<FlowNodeData>[],
  sourceGraph: SourceGraph,
  projection: GraphProjection,
  runId = "",
  sourceId = "",
  cacheDir = ""
): Node<FlowNodeData>[] {
  return nodes.map((node) => {
    const summary = projection.nodeEventSummaries.get(node.data.meta.id);
    return {
      ...node,
      data: {
        ...node.data,
        label: buildNodeLabel(
          node.data.meta,
          sourceGraph,
          projection,
          summary,
          runId,
          sourceId,
          cacheDir
        ),
      },
      style: nodeStyle(node.data.meta, sourceGraph, projection),
    };
  });
}

export function applyProjectionToEdges(edges: Edge[], projection: GraphProjection): Edge[] {
  return edges.map((edge) => ({
    ...edge,
    animated: projection.currentEdgeId === edge.id,
    style: edgeStyle(
      {
        id: edge.id,
        from: edge.source,
        to: edge.target,
        label: typeof edge.label === "string" ? edge.label : "",
        conditional: Boolean((edge.data as { conditional?: boolean } | undefined)?.conditional),
      },
      projection
    ),
    markerEnd: {
      type: "arrowclosed",
      color: edgeColor(
        {
          id: edge.id,
          from: edge.source,
          to: edge.target,
          label: typeof edge.label === "string" ? edge.label : "",
          conditional: Boolean((edge.data as { conditional?: boolean } | undefined)?.conditional),
        },
        projection
      ),
    },
  }));
}

function nodeElkWidth(node: GraphNodeMeta): number {
  return node.type === "start" || node.type === "end" ? 100 : 280;
}

function nodeElkHeight(node: GraphNodeMeta, summary?: NodeEventSummary): number {
  if (node.type === "start" || node.type === "end") return 44;
  const base = 96;
  if (!summary) return base;
  const hasText = summary.llmReasoning || summary.llmContent;
  const hasUsage = hasTokenUsageMetrics(summary);
  const hasCalls = summary.functionCalls.length > 0 || summary.toolCalls.length > 0;
  const stateArtifactCount = [
    summary.contractInput,
    summary.contractOutputPatch,
    summary.contractMergedState,
  ].filter(Boolean).length;
  const artifactCount = summary.artifacts.length;
  const hasStatePatch = summary.statePatch.eventCount > 0 || summary.statePatch.changeCount > 0;
  if (!hasText && !hasUsage && !hasCalls && !artifactCount && !hasStatePatch && !stateArtifactCount) {
    return base;
  }
  const textSections = (summary.llmReasoning ? 1 : 0) + (summary.llmContent ? 1 : 0);
  return (
    base +
    (hasUsage ? 40 : 0) +
    textSections * 46 +
    (hasCalls ? 44 : 0) +
    (hasStatePatch ? 52 : 0) +
    stateArtifactCount * 82 +
    artifactCount * 34
  );
}

function nodeStyle(
  node: GraphNodeMeta,
  sourceGraph: SourceGraph,
  projection: GraphProjection
): CSSProperties {
  const isCurrent = projection.currentNodeId === node.id;
  const isFailed = projection.failedNodeIds.has(node.id);
  const isCompleted = projection.completedNodeIds.has(node.id);
  const isVisited = projection.visitedNodeIds.has(node.id);
  const isEntry = sourceGraph.entry_point === node.id;
  const isStart = node.id === SYNTHETIC_START_ID || node.type === "start";
  const isEnd = node.id === SYNTHETIC_END_ID || node.type === "end";

  let border = "rgba(148, 163, 184, 0.18)"; // slate-400/18
  let background = "linear-gradient(180deg, #1e293b, #1a2335 60%, #162032)";
  let color = "#e2e8f0"; // slate-200
  let shadow = "0 4px 16px rgba(0, 0, 0, 0.35)";

  if (isStart) {
    background = "#071e24";
    border = "rgba(6, 182, 212, 0.45)";
  }
  if (isEnd) {
    background = "#130c24";
    border = "rgba(139, 92, 246, 0.45)";
  }

  if (isVisited) {
    background = "linear-gradient(180deg, #162034, #1a2a42 60%, #1c3050)";
    border = "rgba(59, 130, 246, 0.4)";
  }
  if (isCompleted) {
    background = "linear-gradient(180deg, #112318, #14291c 60%, #153020)";
    border = "rgba(34, 197, 94, 0.4)";
  }
  if (isFailed) {
    background = "linear-gradient(180deg, #2a1218, #2c1016 60%, #2e0e12)";
    border = "rgba(239, 68, 68, 0.45)";
    color = "#fca5a5"; // red-300
  }
  if (isCurrent) {
    background = "linear-gradient(180deg, #2b1e08, #2d1906 58%, #2e1503)";
    border = "rgba(245, 158, 11, 0.7)";
    shadow = "0 0 0 3px rgba(245, 158, 11, 0.1), 0 6px 18px rgba(0, 0, 0, 0.5)";
  } else if (isEntry) {
    shadow = "0 4px 16px rgba(0, 0, 0, 0.4)";
  }

  const isCompact = isStart || isEnd;
  return {
    width: isCompact ? 100 : 260,
    borderRadius: isCompact ? 8 : 12,
    border: `1.5px solid ${border}`,
    background,
    color,
    padding: isCompact ? "6px 8px" : "8px 10px",
    boxShadow: shadow,
  };
}

function edgeStyle(edge: GraphEdgeMeta, projection: GraphProjection): CSSProperties {
  const color = edgeColor(edge, projection);
  const isCurrent = projection.currentEdgeId === edge.id;
  const isTraversed = projection.traversedEdgeIds.has(edge.id);
  return {
    stroke: color,
    strokeWidth: isCurrent ? 2.75 : isTraversed ? 2.2 : edge.conditional ? 1.8 : 1.35,
    strokeDasharray: isCurrent ? "7 5" : edge.conditional ? "5 4" : undefined,
    opacity: isTraversed || edge.conditional ? 1 : 0.55,
  };
}

function edgeColor(edge: GraphEdgeMeta, projection: GraphProjection): string {
  if (projection.currentEdgeId === edge.id) return "#fbbf24"; // amber-400
  if (projection.traversedEdgeIds.has(edge.id)) {
    return edge.conditional ? "#c084fc" : "#38bdf8"; // purple-400, sky-400
  }
  return edge.conditional ? "#a78bfa" : "#475569"; // violet-400, slate-600
}
