import type { CSSProperties, ReactNode } from "react";
import { Position, type Edge, type Node } from "@xyflow/react";
import type { ReplayItem, RunDetail } from "../replay/types";

interface GraphNodeMeta {
  id: string;
  name: string;
  type: string;
  description: string;
}

interface GraphEdgeMeta {
  id: string;
  from: string;
  to: string;
  label: string;
  conditional: boolean;
}

export interface SourceGraph {
  entry_point?: string;
  finish_point?: string;
  nodes: GraphNodeMeta[];
  edges: GraphEdgeMeta[];
}

export interface GraphProjection {
  currentNodeId: string;
  currentEdgeId: string;
  visitedNodeIds: Set<string>;
  completedNodeIds: Set<string>;
  failedNodeIds: Set<string>;
  traversedEdgeIds: Set<string>;
}

export interface FlowNodeData {
  label: ReactNode;
  meta: GraphNodeMeta;
}

const SYNTHETIC_START_ID = "__wf_start__";
const SYNTHETIC_END_ID = "__wf_end__";

export function parseSourceGraph(raw: unknown): SourceGraph | null {
  if (!raw || typeof raw !== "object") return null;
  const value = raw as Record<string, unknown>;
  const entryPoint = stringValue(value.entry_point);
  const finishPoint = stringValue(value.finish_point);
  const rawNodes = Array.isArray(value.nodes) ? value.nodes : [];
  const rawEdges = Array.isArray(value.edges) ? value.edges : [];

  const nodes = rawNodes
    .map((node) => {
      if (!node || typeof node !== "object") return null;
      const item = node as Record<string, unknown>;
      const id = stringValue(item.id);
      if (!id) return null;
      return {
        id,
        name: stringValue(item.name) || id,
        type: stringValue(item.type) || "node",
        description: stringValue(item.description),
      };
    })
    .filter((item): item is GraphNodeMeta => Boolean(item));

  const nodeIds = new Set(nodes.map((item) => item.id));
  const hasEntryNode = entryPoint && nodeIds.has(entryPoint);
  const needsEndNode =
    finishPoint === "__end__" ||
    rawEdges.some((edge) => {
      if (!edge || typeof edge !== "object") return false;
      return stringValue((edge as Record<string, unknown>).to) === "__end__";
    });

  if (hasEntryNode) {
    nodes.unshift({
      id: SYNTHETIC_START_ID,
      name: "START",
      type: "start",
      description: "Graph entry",
    });
    nodeIds.add(SYNTHETIC_START_ID);
  }

  if (needsEndNode || (finishPoint && nodeIds.has(finishPoint))) {
    nodes.push({
      id: SYNTHETIC_END_ID,
      name: "END",
      type: "end",
      description: "Graph exit",
    });
    nodeIds.add(SYNTHETIC_END_ID);
  }

  const edges = rawEdges
    .map((edge, index) => {
      if (!edge || typeof edge !== "object") return null;
      const item = edge as Record<string, unknown>;
      const from = stringValue(item.from);
      const rawTo = stringValue(item.to);
      const to = rawTo === "__end__" ? SYNTHETIC_END_ID : rawTo;
      if (!from || !to || !nodeIds.has(from) || !nodeIds.has(to)) return null;
      const condition = objectValue(item.condition);
      return {
        id: `${from}-->${to}#${index}`,
        from,
        to,
        label: conditionLabel(condition),
        conditional: Boolean(condition),
      };
    })
    .filter((item): item is GraphEdgeMeta => Boolean(item));

  if (hasEntryNode) {
    edges.unshift({
      id: `${SYNTHETIC_START_ID}-->${entryPoint}#synthetic`,
      from: SYNTHETIC_START_ID,
      to: entryPoint,
      label: "",
      conditional: false,
    });
  }

  if (
    finishPoint &&
    finishPoint !== "__end__" &&
    nodeIds.has(finishPoint) &&
    nodeIds.has(SYNTHETIC_END_ID) &&
    !edges.some((edge) => edge.from === finishPoint && edge.to === SYNTHETIC_END_ID)
  ) {
    edges.push({
      id: `${finishPoint}-->${SYNTHETIC_END_ID}#synthetic`,
      from: finishPoint,
      to: SYNTHETIC_END_ID,
      label: "",
      conditional: false,
    });
  }

  return {
    entry_point: hasEntryNode ? SYNTHETIC_START_ID : entryPoint,
    finish_point: nodeIds.has(SYNTHETIC_END_ID) ? SYNTHETIC_END_ID : finishPoint,
    nodes,
    edges,
  };
}

export function buildProjection(
  detail: RunDetail,
  sourceGraph: SourceGraph | null,
  replayIndex: number
): GraphProjection {
  const validNodeIds = new Set(sourceGraph?.nodes.map((item) => item.id) ?? []);
  const limited = detail.replay.slice(0, Math.max(0, replayIndex + 1));
  const visitedNodeIds = new Set<string>();
  const completedNodeIds = new Set<string>();
  const failedNodeIds = new Set<string>();
  const traversedEdgeIds = new Set<string>();
  const nodeTrail: string[] = [];
  let currentNodeId = "";

  for (const item of limited) {
    const nodeId = replayNodeId(item, validNodeIds);
    if (nodeId) {
      visitedNodeIds.add(nodeId);
      currentNodeId = nodeId;
      if (nodeTrail[nodeTrail.length - 1] !== nodeId) nodeTrail.push(nodeId);
    }

    const eventType = String(item.event.type ?? "").toLowerCase();
    if (nodeId && eventType.includes("finished")) completedNodeIds.add(nodeId);
    if (nodeId && eventType.includes("failed")) failedNodeIds.add(nodeId);
  }

  const edgeIndex = new Map<string, string>();
  for (const edge of sourceGraph?.edges ?? []) {
    edgeIndex.set(`${edge.from}-->${edge.to}`, edge.id);
  }

  let currentEdgeId = "";
  for (let index = 1; index < nodeTrail.length; index += 1) {
    const edgeId = edgeIndex.get(`${nodeTrail[index - 1]}-->${nodeTrail[index]}`);
    if (!edgeId) continue;
    traversedEdgeIds.add(edgeId);
    currentEdgeId = edgeId;
  }

  return {
    currentNodeId,
    currentEdgeId,
    visitedNodeIds,
    completedNodeIds,
    failedNodeIds,
    traversedEdgeIds,
  };
}

export function buildBaseFlow(sourceGraph: SourceGraph, projection: GraphProjection): {
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
} {
  return {
    nodes: sourceGraph.nodes.map((node) => ({
      id: node.id,
      position: { x: 0, y: 0 },
      draggable: true,
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      data: { label: buildNodeLabel(node, sourceGraph, projection), meta: node },
      style: nodeStyle(node, sourceGraph, projection),
    })),
    edges: sourceGraph.edges.map((edge) => ({
      id: edge.id,
      source: edge.from,
      target: edge.to,
      data: { conditional: edge.conditional },
      label: edge.label || undefined,
      labelStyle: edge.label
        ? {
            fill: edge.conditional ? "#e9d5ff" : "#cbd5e1",
            fontSize: 10.5,
            fontWeight: 700,
          }
        : undefined,
      labelShowBg: edge.conditional,
      labelBgPadding: edge.conditional ? [12, 6] : undefined,
      labelBgBorderRadius: edge.conditional ? 8 : undefined,
      labelBgStyle: edge.conditional
        ? {
            fill: "rgba(76, 29, 149, 0.94)",
            stroke: "rgba(196, 181, 253, 0.38)",
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
  projection: GraphProjection
): Node<FlowNodeData>[] {
  return nodes.map((node) => ({
    ...node,
    data: {
      ...node.data,
      label: buildNodeLabel(node.data.meta, sourceGraph, projection),
    },
    style: nodeStyle(node.data.meta, sourceGraph, projection),
  }));
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

function buildNodeLabel(
  node: GraphNodeMeta,
  sourceGraph: SourceGraph,
  projection: GraphProjection
) {
  const isCurrent = projection.currentNodeId === node.id;
  const isEntry = sourceGraph.entry_point === node.id;
  const isFinish = sourceGraph.finish_point === node.id;
  const isFailed = projection.failedNodeIds.has(node.id);
  const isCompleted = projection.completedNodeIds.has(node.id);
  const isVisited = projection.visitedNodeIds.has(node.id);
  const isStart = node.id === SYNTHETIC_START_ID || node.type === "start";
  const isEnd = node.id === SYNTHETIC_END_ID || node.type === "end";
  const statusLabel = isCurrent
    ? "LIVE"
    : isStart
      ? "START"
      : isEnd
        ? "END"
    : isFailed
      ? "FAILED"
      : isCompleted
        ? "DONE"
        : isVisited
          ? "SEEN"
          : "IDLE";
  const statusClass = isCurrent
    ? "bg-amber-400/18 text-amber-200 ring-1 ring-amber-300/35"
    : isStart
      ? "bg-cyan-400/18 text-cyan-200 ring-1 ring-cyan-300/35"
      : isEnd
        ? "bg-fuchsia-400/18 text-fuchsia-200 ring-1 ring-fuchsia-300/35"
    : isFailed
      ? "bg-rose-400/18 text-rose-200 ring-1 ring-rose-300/30"
      : isCompleted
        ? "bg-emerald-400/18 text-emerald-200 ring-1 ring-emerald-300/30"
        : isVisited
          ? "bg-sky-400/18 text-sky-200 ring-1 ring-sky-300/30"
          : "bg-slate-400/12 text-slate-300 ring-1 ring-slate-400/18";
  const typeClass =
    isStart
      ? "text-cyan-100 bg-cyan-400/14"
      : isEnd
        ? "text-fuchsia-100 bg-fuchsia-400/14"
        : isEntry || isFinish
      ? "text-fuchsia-200 bg-fuchsia-400/14"
      : "text-slate-300 bg-slate-400/10";

  return (
    <div className="min-w-[180px]">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold tracking-tight text-white">{node.name}</div>
          <div className="mt-1 flex items-center gap-1.5 text-[10px] uppercase tracking-[0.16em] text-slate-400">
            {isEntry ? <span>ENTRY</span> : null}
            {isFinish ? <span>FINISH</span> : null}
            <span>{node.type}</span>
          </div>
        </div>
        <span
          className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${statusClass}`}
        >
          {statusLabel}
        </span>
      </div>

      <div className="mt-3 flex items-center gap-2">
        <span
          className={`rounded-full px-2 py-1 text-[10px] font-medium uppercase tracking-[0.16em] ${typeClass}`}
        >
          {node.type}
        </span>
        {node.description ? (
          <span className="truncate text-[11px] text-slate-400">{node.description}</span>
        ) : (
          <span className="text-[11px] text-slate-500">graph node</span>
        )}
      </div>

      <div className="mt-3 h-px w-full bg-gradient-to-r from-white/12 via-white/6 to-transparent" />

      <div className="mt-3 truncate font-mono text-[10px] text-slate-500">{node.id}</div>
      <div className="mt-1 text-[10px] uppercase tracking-[0.16em] text-slate-500">
        {isEntry && isFinish ? "entry · finish" : isEntry ? "entry point" : isFinish ? "finish point" : "runtime node"}
      </div>
    </div>
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

  let border = "rgba(51, 65, 85, 0.95)";
  let background =
    "linear-gradient(180deg, rgba(15,23,42,0.98), rgba(15,23,42,0.92) 58%, rgba(8,15,30,0.96))";
  let color = "#f8fafc";
  let shadow = "0 18px 40px rgba(2, 6, 23, 0.42)";

  if (isStart) {
    background =
      "linear-gradient(180deg, rgba(8,51,68,0.98), rgba(14,116,144,0.90) 58%, rgba(6,95,70,0.82))";
    border = "rgba(103, 232, 249, 0.82)";
  }
  if (isEnd) {
    background =
      "linear-gradient(180deg, rgba(88,28,135,0.98), rgba(126,34,206,0.90) 58%, rgba(91,33,182,0.82))";
    border = "rgba(216, 180, 254, 0.82)";
  }

  if (isVisited) {
    background =
      "linear-gradient(180deg, rgba(8,47,73,0.98), rgba(12,74,110,0.94) 56%, rgba(15,118,110,0.86))";
    border = "rgba(56, 189, 248, 0.75)";
  }
  if (isCompleted) {
    background =
      "linear-gradient(180deg, rgba(20,83,45,0.98), rgba(22,101,52,0.92) 60%, rgba(6,78,59,0.88))";
    border = "rgba(74, 222, 128, 0.75)";
  }
  if (isFailed) {
    background =
      "linear-gradient(180deg, rgba(127,29,29,0.98), rgba(153,27,27,0.94) 60%, rgba(136,19,55,0.88))";
    border = "rgba(248, 113, 113, 0.78)";
    color = "#fef2f2";
  }
  if (isCurrent) {
    background =
      "linear-gradient(180deg, rgba(120,53,15,0.98), rgba(146,64,14,0.94) 58%, rgba(161,98,7,0.86))";
    border = "rgba(251, 191, 36, 0.95)";
    shadow = "0 0 0 3px rgba(245, 158, 11, 0.22), 0 24px 48px rgba(120, 53, 15, 0.34)";
  } else if (isEntry) {
    shadow = "0 18px 40px rgba(14, 116, 144, 0.22)";
  }

  return {
    width: 260,
    borderRadius: 20,
    border: `1.5px solid ${border}`,
    background,
    color,
    padding: "2px",
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
  if (projection.currentEdgeId === edge.id) return "#f59e0b";
  if (projection.traversedEdgeIds.has(edge.id)) {
    return edge.conditional ? "#e879f9" : "#38bdf8";
  }
  return edge.conditional ? "#c084fc" : "#64748b";
}

function replayNodeId(item: ReplayItem, validNodeIds: Set<string>): string {
  const eventType = String(item.event.type ?? "").toLowerCase();
  if (!eventType.includes("node")) return "";
  const nodeId = String(item.event.node_id ?? "").trim();
  if (!nodeId || !validNodeIds.has(nodeId)) return "";
  return nodeId;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function objectValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : null;
}

function conditionLabel(condition: Record<string, unknown> | null): string {
  if (!condition) return "";
  const type = stringValue(condition.type);
  const config = objectValue(condition.config);

  if (type === "expression" || type === "expression_conditions") {
    const singleExpression =
      stringValue(config?.expression) ||
      stringValue(config?.expr) ||
      stringValue(config?.value);
    if (singleExpression) return singleExpression;

    const expressions = Array.isArray(config?.expressions) ? config.expressions : [];
    const rendered = expressions
      .map((item) => renderExpression(objectValue(item)))
      .filter(Boolean);
    if (rendered.length > 0) {
      const joiner = stringValue(config?.match).toLowerCase() === "any" ? " OR " : " AND ";
      return rendered.join(joiner);
    }
  }

  return type;
}

function renderExpression(expression: Record<string, unknown> | null): string {
  if (!expression) return "";
  const op = stringValue(expression.op);
  const value1 = stringValue(expression.value1);
  const value2 = stringValue(expression.value2);
  const operator = operatorLabel(op);

  if (value1 && value2 && operator) {
    return `${value1} ${operator} ${value2}`;
  }
  if (value1 && value2) {
    return `${value1} ${op || "?"} ${value2}`;
  }
  return "";
}

function operatorLabel(op: string): string {
  switch (op) {
    case "equals":
      return "==";
    case "not_equals":
      return "!=";
    case "gt":
      return ">";
    case "gte":
      return ">=";
    case "lt":
      return "<";
    case "lte":
      return "<=";
    case "contains":
      return "contains";
    case "in":
      return "in";
    default:
      return op;
  }
}
