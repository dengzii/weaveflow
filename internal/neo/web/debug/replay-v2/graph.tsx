import type { CSSProperties, ReactNode } from "react";
import { useEffect, useState } from "react";
import { Position, type Edge, type Node } from "@xyflow/react";
import type { ReplayItem, RunDetail } from "../replay/types";

interface ArtifactDetail {
  bytes: number;
  encoding: "text" | "json" | "base64";
  payload: unknown;
  truncated?: boolean;
}

export interface GraphNodeMeta {
  id: string;
  name: string;
  type: string;
  description: string;
  config: Record<string, unknown> | null;
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

export interface NodeEventSummary {
  durationMs: number; // -1 = not yet finished
  llmReasoning: string;
  llmContent: string;
  functionCalls: { name: string; args: string }[];
  toolCalls: { name: string; status: "pending" | "done" | "failed" }[];
  artifacts: { id: string; type: string; mimeType: string }[];
}

export interface GraphProjection {
  currentNodeId: string;
  currentEdgeId: string;
  visitedNodeIds: Set<string>;
  completedNodeIds: Set<string>;
  failedNodeIds: Set<string>;
  traversedEdgeIds: Set<string>;
  nodeEventSummaries: Map<string, NodeEventSummary>;
}

export interface FlowNodeData {
  label: ReactNode;
  meta: GraphNodeMeta;
  elkHeight: number;
  elkWidth: number;
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
        config: objectValue(item.config),
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
      config: null,
    });
    nodeIds.add(SYNTHETIC_START_ID);
  }

  if (needsEndNode || (finishPoint && nodeIds.has(finishPoint))) {
    nodes.push({
      id: SYNTHETIC_END_ID,
      name: "END",
      type: "end",
      description: "Graph exit",
      config: null,
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

  // Propagate to synthetic END when the last real node is completed and connects to it
  if (
    currentNodeId &&
    completedNodeIds.has(currentNodeId) &&
    edgeIndex.has(`${currentNodeId}-->${SYNTHETIC_END_ID}`)
  ) {
    nodeTrail.push(SYNTHETIC_END_ID);
    visitedNodeIds.add(SYNTHETIC_END_ID);
    completedNodeIds.add(SYNTHETIC_END_ID);
    currentNodeId = SYNTHETIC_END_ID;
  }

  let currentEdgeId = "";
  for (let index = 1; index < nodeTrail.length; index += 1) {
    const edgeId = edgeIndex.get(`${nodeTrail[index - 1]}-->${nodeTrail[index]}`);
    if (!edgeId) continue;
    traversedEdgeIds.add(edgeId);
    currentEdgeId = edgeId;
  }

  const nodeEventSummaries = collectNodeEvents(limited, validNodeIds);

  return {
    currentNodeId,
    currentEdgeId,
    visitedNodeIds,
    completedNodeIds,
    failedNodeIds,
    traversedEdgeIds,
    nodeEventSummaries,
  };
}

export function buildBaseFlow(
  sourceGraph: SourceGraph,
  projection: GraphProjection,
  runId = "",
  sourceId = ""
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
          label: buildNodeLabel(node, sourceGraph, projection, summary, runId, sourceId),
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
  projection: GraphProjection,
  runId = "",
  sourceId = ""
): Node<FlowNodeData>[] {
  return nodes.map((node) => {
    const summary = projection.nodeEventSummaries.get(node.data.meta.id);
    return {
      ...node,
      data: {
        ...node.data,
        label: buildNodeLabel(node.data.meta, sourceGraph, projection, summary, runId, sourceId),
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

function buildNodeLabel(
  node: GraphNodeMeta,
  sourceGraph: SourceGraph,
  projection: GraphProjection,
  summary?: NodeEventSummary,
  runId = "",
  sourceId = ""
) {
  const isCurrent = projection.currentNodeId === node.id;
  const isEntry = sourceGraph.entry_point === node.id;
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
  if (isStart || isEnd) {
    return (
      <div className="text-center text-[11px] font-semibold uppercase tracking-[0.16em] text-white/90">
        {node.name}
      </div>
    );
  }

  const durationLabel = summary && summary.durationMs >= 0 ? formatNodeDuration(summary.durationMs) : "";

  const hasEvents = Boolean(summary && (
    summary.llmReasoning || summary.llmContent || summary.functionCalls.length > 0 || summary.toolCalls.length > 0 || summary.artifacts.length > 0
  ));

  const configEntries = node.config
    ? Object.entries(node.config).filter(([, v]) => v !== null && v !== undefined && v !== "")
    : [];

  return (
    <div className="min-w-[180px]">
      {/* Name + description tooltip + status */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1">
          <div className="truncate text-[13px] font-semibold tracking-tight text-white">{node.name}</div>
          {node.description ? (
            <span className="group/tip relative shrink-0 cursor-default select-none text-[11px] text-slate-600 hover:text-slate-400">
              ⓘ
              <span className="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 -translate-x-1/2 whitespace-nowrap rounded-md bg-slate-800 px-2.5 py-1.5 text-[11px] leading-snug text-slate-200 opacity-0 shadow-xl ring-1 ring-white/10 transition-opacity group-hover/tip:opacity-100">
                {node.description}
              </span>
            </span>
          ) : null}
        </div>
        <div className="flex shrink-0 flex-col items-end gap-0.5">
          <span className={`rounded-full px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-[0.12em] ${statusClass}`}>
            {statusLabel}
          </span>
          {durationLabel ? (
            <span className="text-[9px] tabular-nums text-slate-500">{durationLabel}</span>
          ) : null}
        </div>
      </div>

      {/* Config key-value pairs */}
      {configEntries.length > 0 ? (
        <CollapsibleConfig entries={configEntries} />
      ) : null}

      {/* Content area — only rendered when events exist */}
      {hasEvents ? (
        <>
          <div className="mt-2 h-px w-full bg-white/8" />
          <div className="mt-2 space-y-2">
            {summary!.llmReasoning ? (
              <CollapsibleSection
                label="Reasoning"
                text={summary!.llmReasoning}
                labelClass="text-violet-400/70"
                textClass="text-slate-300"
              />
            ) : null}
            {summary!.llmContent ? (
              <CollapsibleSection
                label="Response"
                text={summary!.llmContent}
                labelClass="text-sky-400/70"
                textClass="text-slate-100"
              />
            ) : null}
            {(summary!.functionCalls.length > 0 || summary!.toolCalls.length > 0) ? (
              <div className="flex flex-wrap gap-x-3 gap-y-0.5">
                {summary!.functionCalls.slice(0, 3).map((fc, i) => (
                  <span key={i} className="font-mono text-[10px] text-amber-300/80">⚡ {fc.name}</span>
                ))}
                {summary!.toolCalls.slice(0, 4).map((tc, i) => (
                  <span key={i} className={`font-mono text-[10px] ${tc.status === "done" ? "text-emerald-400/80" : tc.status === "failed" ? "text-rose-400/80" : "text-slate-400"}`}>
                    {tc.status === "done" ? "✓" : tc.status === "failed" ? "✗" : "·"} {tc.name}
                  </span>
                ))}
              </div>
            ) : null}
            {summary!.artifacts.length > 0 ? (
              runId ? (
                <div className="space-y-0.5">
                  {summary!.artifacts.slice(0, 3).map((a, i) => (
                    <ArtifactToggleView key={i} artifact={a} runId={runId} sourceId={sourceId} />
                  ))}
                </div>
              ) : (
                <div className="flex flex-wrap gap-x-3 gap-y-0.5">
                  {summary!.artifacts.slice(0, 3).map((a, i) => (
                    <span key={i} className="font-mono text-[10px] text-violet-300/80">⬡ {a.type || "artifact"}</span>
                  ))}
                </div>
              )
            ) : null}
          </div>
        </>
      ) : null}
    </div>
  );
}

function nodeElkWidth(node: GraphNodeMeta): number {
  return node.type === "start" || node.type === "end" ? 100 : 260;
}

function nodeElkHeight(node: GraphNodeMeta, summary?: NodeEventSummary): number {
  if (node.type === "start" || node.type === "end") return 44;
  const hasDuration = summary ? summary.durationMs >= 0 : false;
  const base = 80 + (hasDuration ? 14 : 0);
  if (!summary) return base;
  const hasText = summary.llmReasoning || summary.llmContent;
  const hasCalls = summary.functionCalls.length > 0 || summary.toolCalls.length > 0;
  const artifactCount = summary.artifacts.length;
  if (!hasText && !hasCalls && !artifactCount) return base;
  const textSections = (summary.llmReasoning ? 1 : 0) + (summary.llmContent ? 1 : 0);
  return base + textSections * 36 + (hasCalls ? 24 : 0) + artifactCount * 20;
}

function collectNodeEvents(events: ReplayItem[], validNodeIds: Set<string>): Map<string, NodeEventSummary> {
  const summaries = new Map<string, NodeEventSummary>();
  const nodeFirstTime = new Map<string, number>();

  function ensure(nodeId: string): NodeEventSummary {
    let s = summaries.get(nodeId);
    if (!s) {
      s = { durationMs: -1, llmReasoning: "", llmContent: "", functionCalls: [], toolCalls: [], artifacts: [] };
      summaries.set(nodeId, s);
    }
    return s;
  }

  for (const item of events) {
    const nodeId = String(item.event.node_id ?? "").trim();
    if (!nodeId || !validNodeIds.has(nodeId)) continue;

    const eventType = String(item.event.type ?? "");
    const payload = (item.event.payload ?? {}) as Record<string, unknown>;
    const ts = item.event.timestamp || item.timestamp;

    // Track node duration: record first timestamp, detect finish
    if (ts) {
      const t = new Date(ts).getTime();
      if (!isNaN(t)) {
        if (!nodeFirstTime.has(nodeId)) nodeFirstTime.set(nodeId, t);
        const typeLower = eventType.toLowerCase();
        if (typeLower.includes("node") && typeLower.includes("finished")) {
          const start = nodeFirstTime.get(nodeId)!;
          ensure(nodeId).durationMs = Math.max(0, t - start);
        }
      }
    }

    if (eventType === "llm.reasoning") {
      ensure(nodeId).llmReasoning = String(payload.text ?? "");
    } else if (eventType === "llm.reasoning_chunk") {
      ensure(nodeId).llmReasoning += String(payload.text ?? "");
    } else if (eventType === "llm.content") {
      ensure(nodeId).llmContent = String(payload.text ?? "");
    } else if (eventType === "llm.content_chunk") {
      ensure(nodeId).llmContent += String(payload.text ?? "");
    } else if (eventType === "llm.function_call") {
      const name = String(payload.name ?? payload.function_name ?? "");
      if (!name) continue;
      const rawArgs = payload.arguments ?? payload.args ?? "";
      ensure(nodeId).functionCalls.push({ name, args: formatFuncArgs(rawArgs) });
    } else if (eventType === "tool.called" || eventType === "tool.started") {
      const name = String(payload.function_name ?? payload.name ?? payload.tool_name ?? payload.tool ?? "");
      if (!name) continue;
      ensure(nodeId).toolCalls.push({ name, status: "pending" });
    } else if (eventType === "tool.returned") {
      const pending = [...ensure(nodeId).toolCalls].reverse().find((t) => t.status === "pending");
      if (pending) pending.status = "done";
    } else if (eventType === "tool.failed") {
      const pending = [...ensure(nodeId).toolCalls].reverse().find((t) => t.status === "pending");
      if (pending) pending.status = "failed";
    } else if (eventType === "artifact.created") {
      const id = String(payload.artifact_id ?? "");
      const type = String(payload.type ?? "");
      const mimeType = String(payload.mime_type ?? "");
      if (id) ensure(nodeId).artifacts.push({ id, type, mimeType });
    }
  }

  return summaries;
}

export function NodeInfoPanel({
  node,
  summary,
  runId = "",
  sourceId = "",
}: {
  node: GraphNodeMeta;
  summary: NodeEventSummary | null | undefined;
  runId?: string;
  sourceId?: string;
}) {
  const configEntries = node.config
    ? Object.entries(node.config).filter(([, v]) => v !== null && v !== undefined && v !== "")
    : [];
  const hasContent =
    configEntries.length > 0 ||
    (summary?.durationMs !== undefined && summary.durationMs >= 0) ||
    summary?.llmReasoning ||
    summary?.llmContent ||
    (summary?.functionCalls.length ?? 0) > 0 ||
    (summary?.toolCalls.length ?? 0) > 0 ||
    (summary?.artifacts.length ?? 0) > 0;

  if (!hasContent) return null;

  return (
    <div className="space-y-2">
      {summary?.durationMs !== undefined && summary.durationMs >= 0 ? (
        <div className="flex items-center gap-1.5">
          <span className="text-[9px] uppercase tracking-[0.14em] text-slate-500">Duration</span>
          <span className="text-[11px] font-medium tabular-nums text-slate-200">{formatNodeDuration(summary.durationMs)}</span>
        </div>
      ) : null}
      {configEntries.length > 0 ? <CollapsibleConfig entries={configEntries} /> : null}
      {summary?.llmReasoning ? (
        <CollapsibleSection
          label="Reasoning"
          text={summary.llmReasoning}
          labelClass="text-violet-400/70"
          textClass="text-slate-300"
        />
      ) : null}
      {summary?.llmContent ? (
        <CollapsibleSection
          label="Response"
          text={summary.llmContent}
          labelClass="text-sky-400/70"
          textClass="text-slate-100"
        />
      ) : null}
      {(summary?.functionCalls.length ?? 0) > 0 || (summary?.toolCalls.length ?? 0) > 0 ? (
        <div className="flex flex-wrap gap-x-3 gap-y-0.5">
          {summary!.functionCalls.slice(0, 3).map((fc, i) => (
            <span key={i} className="font-mono text-[10px] text-amber-300/80">⚡ {fc.name}</span>
          ))}
          {summary!.toolCalls.slice(0, 4).map((tc, i) => (
            <span key={i} className={`font-mono text-[10px] ${tc.status === "done" ? "text-emerald-400/80" : tc.status === "failed" ? "text-rose-400/80" : "text-slate-400"}`}>
              {tc.status === "done" ? "✓" : tc.status === "failed" ? "✗" : "·"} {tc.name}
            </span>
          ))}
        </div>
      ) : null}
      {(summary?.artifacts.length ?? 0) > 0 ? (
        runId ? (
          <div className="space-y-0.5">
            {summary!.artifacts.map((a, i) => (
              <ArtifactToggleView key={i} artifact={a} runId={runId} sourceId={sourceId} />
            ))}
          </div>
        ) : (
          <div className="flex flex-wrap gap-x-3 gap-y-0.5">
            {summary!.artifacts.map((a, i) => (
              <span key={i} className="font-mono text-[10px] text-violet-300/80">⬡ {a.type || "artifact"}</span>
            ))}
          </div>
        )
      ) : null}
    </div>
  );
}

const CONFIG_VALUE_THRESHOLD = 40;

function CollapsibleConfig({ entries }: { entries: [string, unknown][] }) {
  return (
    <div className="mt-1.5 space-y-0.5">
      {entries.map(([k, v]) => (
        <ConfigEntry key={k} k={k} v={v} />
      ))}
    </div>
  );
}

function ConfigEntry({ k, v }: { k: string; v: unknown }) {
  const [open, setOpen] = useState(false);
  const full = formatConfigValue(v);
  const isLong = full.length > CONFIG_VALUE_THRESHOLD;
  return (
    <div className="text-[10px]">
      <div className="flex items-baseline justify-between gap-1.5">
        <span className="shrink-0 text-slate-500">{k}</span>
        {isLong ? (
          <button
            type="button"
            className="min-w-0 text-right text-slate-300 hover:text-slate-100"
            onClick={(e) => { e.stopPropagation(); setOpen((o) => !o); }}
          >
            {open ? full : `${full.slice(0, CONFIG_VALUE_THRESHOLD)}…`}
            <span className="ml-1 text-[9px] text-slate-500">{open ? "▴" : "▸"}</span>
          </button>
        ) : (
          <span className="text-right text-slate-300">{full}</span>
        )}
      </div>
    </div>
  );
}

function CollapsibleSection({
  label,
  text,
  labelClass,
  textClass,
}: {
  label: string;
  text: string;
  labelClass: string;
  textClass: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button
        type="button"
        className="flex w-full items-center justify-between gap-1"
        onClick={(e) => { e.stopPropagation(); setOpen((o) => !o); }}
      >
        <span className={`text-[9px] font-medium uppercase tracking-[0.14em] ${labelClass}`}>{label}</span>
        <span className="text-[9px] text-slate-500">{open ? "▾" : "▸"}</span>
      </button>
      {open ? (
        <div className={`nodrag nowheel mt-0.5 max-h-[120px] overflow-y-auto whitespace-pre-wrap break-words text-[11px] leading-[1.55] ${textClass}`}>
          {text}
        </div>
      ) : (
        <p className={`mt-0.5 line-clamp-1 text-[11px] leading-[1.55] ${textClass}`}>{text}</p>
      )}
    </div>
  );
}

function ArtifactToggleView({
  artifact,
  runId,
  sourceId,
}: {
  artifact: { id: string; type: string; mimeType: string };
  runId: string;
  sourceId: string;
}) {
  const [open, setOpen] = useState(false);
  const [fetchState, setFetchState] = useState<"idle" | "loading" | "done" | "error">("idle");
  const [detail, setDetail] = useState<ArtifactDetail | null>(null);
  const [errorMsg, setErrorMsg] = useState("");

  const isImage = artifact.mimeType.startsWith("image/");

  const params = new URLSearchParams();
  if (sourceId && sourceId !== "live") params.set("source", sourceId);
  const baseUrl = `/api/run/${encodeURIComponent(runId)}/artifact/${encodeURIComponent(artifact.id)}`;
  const detailUrl = `${baseUrl}?${params}`;
  const dlParams = new URLSearchParams(params);
  dlParams.set("download", "1");
  const downloadUrl = `${baseUrl}?${dlParams}`;

  useEffect(() => {
    if (!open || isImage || fetchState !== "idle") return;
    let cancelled = false;
    setFetchState("loading");
    fetch(detailUrl)
      .then((r) => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json(); })
      .then((json: unknown) => {
        if (!cancelled) {
          setDetail((json as { data: ArtifactDetail }).data);
          setFetchState("done");
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) { setErrorMsg((err as Error).message ?? "Error"); setFetchState("error"); }
      });
    return () => { cancelled = true; };
  }, [open, isImage, fetchState, detailUrl]);

  function renderDetail() {
    if (!detail) return null;
    const { encoding, payload, truncated } = detail;
    if (encoding === "json") {
      const text = JSON.stringify(payload, null, 2) + (truncated ? "\n…<truncated>" : "");
      return (
        <pre className="nodrag nowheel mt-1 max-h-[100px] overflow-auto rounded-lg bg-slate-900/70 p-2 font-mono text-[10px] leading-[1.4] text-slate-100 whitespace-pre-wrap break-words">
          {text}
        </pre>
      );
    }
    if (encoding === "text") {
      return (
        <pre className="nodrag nowheel mt-1 max-h-[100px] overflow-auto rounded-lg bg-slate-900/70 p-2 font-mono text-[10px] leading-[1.4] text-slate-100 whitespace-pre-wrap break-words">
          {String(payload ?? "")}
          {truncated ? "\n…<truncated>" : ""}
        </pre>
      );
    }
    // base64 binary
    return (
      <div className="mt-1 flex items-center gap-2">
        <span className="text-[10px] text-slate-500">{detail.bytes} bytes</span>
        <a href={downloadUrl} target="_blank" rel="noopener noreferrer" className="text-[10px] text-sky-400 hover:text-sky-300">↓ Download</a>
      </div>
    );
  }

  return (
    <div>
      <button
        type="button"
        className="flex w-full items-center justify-between gap-1 text-left"
        onClick={(e) => { e.stopPropagation(); setOpen((o) => !o); }}
      >
        <span className="font-mono text-[10px] text-violet-300/80">⬡ {artifact.type || "artifact"}</span>
        <span className="text-[9px] text-slate-500">{open ? "▾" : "▸"}</span>
      </button>
      {open ? (
        isImage ? (
          <div className="mt-1 overflow-hidden rounded-lg border border-slate-700/50 bg-slate-900">
            <img src={downloadUrl} alt={artifact.id} className="max-h-[120px] w-full object-contain" />
          </div>
        ) : fetchState === "loading" ? (
          <div className="mt-1 text-[10px] text-slate-500">Loading…</div>
        ) : fetchState === "error" ? (
          <div className="mt-1 text-[10px] text-rose-400">{errorMsg}</div>
        ) : fetchState === "done" ? (
          renderDetail()
        ) : null
      ) : null}
    </div>
  );
}

function formatNodeDuration(ms: number): string {
  if (ms < 0) return "";
  if (ms < 1000) return "< 1s";
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60000);
  const s = Math.floor((ms % 60000) / 1000);
  return s > 0 ? `${m}m ${s}s` : `${m}m`;
}

function formatConfigValue(v: unknown): string {
  if (typeof v === "string") return v.length > 60 ? `${v.slice(0, 60)}…` : v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return `[${v.slice(0, 3).map(String).join(", ")}${v.length > 3 ? "…" : ""}]`;
  if (typeof v === "object" && v !== null) return JSON.stringify(v).slice(0, 60);
  return String(v ?? "");
}

function formatFuncArgs(raw: unknown): string {
  if (!raw) return "";
  let obj: Record<string, unknown> | null = null;
  if (typeof raw === "string") {
    try { obj = JSON.parse(raw) as Record<string, unknown>; } catch { return raw.slice(0, 30); }
  } else if (typeof raw === "object") {
    obj = raw as Record<string, unknown>;
  }
  if (!obj) return String(raw).slice(0, 30);
  return Object.entries(obj)
    .slice(0, 2)
    .map(([k, v]) => {
      const val = typeof v === "string" ? `"${v.slice(0, 16)}"` : String(v).slice(0, 16);
      return `${k}=${val}`;
    })
    .join(", ");
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
    background = "rgba(8, 52, 68, 1)";
    border = "rgba(103, 232, 249, 0.75)";
  }
  if (isEnd) {
    background = "rgba(76, 29, 149, 1)";
    border = "rgba(192, 132, 252, 0.75)";
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

  const isCompact = isStart || isEnd;
  return {
    width: isCompact ? 100 : 260,
    borderRadius: isCompact ? 8 : 12,
    border: `1.5px solid ${border}`,
    background,
    color,
    padding: isCompact ? "8px 10px" : "10px 12px",
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

export function buildMermaidDiagram(
  sourceGraph: SourceGraph,
  projection?: GraphProjection
): string {
  const lines: string[] = ["flowchart LR"];

  lines.push(
    "  classDef wfCurrent fill:#92400e,stroke:#fbbf24,stroke-width:2px,color:#fef3c7"
  );
  lines.push(
    "  classDef wfDone fill:#14532d,stroke:#4ade80,stroke-width:2px,color:#dcfce7"
  );
  lines.push(
    "  classDef wfVisited fill:#082f49,stroke:#38bdf8,stroke-width:1.5px,color:#e0f2fe"
  );
  lines.push(
    "  classDef wfFailed fill:#7f1d1d,stroke:#f87171,stroke-width:2px,color:#fef2f2"
  );
  lines.push(
    "  classDef wfStart fill:#083444,stroke:#67e8f9,stroke-width:2px,color:#ecfeff"
  );
  lines.push(
    "  classDef wfEnd fill:#581c87,stroke:#d8b4fe,stroke-width:2px,color:#faf5ff"
  );

  const idMap = new Map<string, string>();
  sourceGraph.nodes.forEach((node, i) => idMap.set(node.id, `N${i}`));

  for (const node of sourceGraph.nodes) {
    const mid = idMap.get(node.id)!;
    const lbl = `"${mermaidEscape(node.name)}"`;
    if (node.type === "start" || node.type === "end") {
      lines.push(`  ${mid}([${lbl}])`);
    } else {
      lines.push(`  ${mid}[${lbl}]`);
    }
  }

  for (const edge of sourceGraph.edges) {
    const from = idMap.get(edge.from);
    const to = idMap.get(edge.to);
    if (!from || !to) continue;
    if (edge.label) {
      lines.push(`  ${from} -->|"${mermaidEscape(edge.label)}"| ${to}`);
    } else {
      lines.push(`  ${from} --> ${to}`);
    }
  }

  if (projection) {
    for (const node of sourceGraph.nodes) {
      const mid = idMap.get(node.id)!;
      let cls = "";
      if (projection.currentNodeId === node.id) cls = "wfCurrent";
      else if (projection.failedNodeIds.has(node.id)) cls = "wfFailed";
      else if (projection.completedNodeIds.has(node.id)) cls = "wfDone";
      else if (projection.visitedNodeIds.has(node.id)) cls = "wfVisited";
      else if (node.type === "start") cls = "wfStart";
      else if (node.type === "end") cls = "wfEnd";
      if (cls) lines.push(`  class ${mid} ${cls}`);
    }
  }

  return lines.join("\n");
}

function mermaidEscape(text: string): string {
  return text
    .replace(/"/g, "#quot;")
    .replace(/</g, "#lt;")
    .replace(/>/g, "#gt;");
}
