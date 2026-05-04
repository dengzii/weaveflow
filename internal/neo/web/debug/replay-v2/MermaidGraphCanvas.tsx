import { useEffect, useMemo, useRef, useState } from "react";
import type { RunDetail } from "../replay/types";
import { buildMermaidDiagram, buildProjection, NodeInfoPanel, parseSourceGraph } from "./graph";

let mermaidInitPromise: Promise<(typeof import("mermaid"))["default"]> | null = null;
let renderCounter = 0;

async function initMermaid() {
  if (!mermaidInitPromise) {
    mermaidInitPromise = import("mermaid").then(({ default: mermaid }) => {
      mermaid.initialize({
        startOnLoad: false,
        theme: "dark",
        htmlLabels: false,
        flowchart: { curve: "basis", useMaxWidth: false },
        securityLevel: "loose",
      });
      return mermaid;
    });
  }
  return mermaidInitPromise;
}

export function MermaidGraphCanvas({
  detail,
  replayIndex,
}: {
  detail: RunDetail | null;
  replayIndex: number;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const [transform, setTransform] = useState({ x: 0, y: 0, scale: 1 });
  const [isDragging, setIsDragging] = useState(false);
  const dragRef = useRef<{ sx: number; sy: number; tx: number; ty: number } | null>(null);
  const renderTokenRef = useRef(0);
  const renderQueueRef = useRef(Promise.resolve());
  const transformRef = useRef(transform);
  transformRef.current = transform;

  useEffect(() => {
    setTransform({ x: 0, y: 0, scale: 1 });
  }, [detail?.run.run_id]);

  // non-passive wheel to prevent page scroll; zoom toward cursor
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    function onWheel(e: WheelEvent) {
      e.preventDefault();
      const rect = el!.getBoundingClientRect();
      // cursor relative to viewport center (which is transformOrigin)
      const ox = e.clientX - rect.left - rect.width / 2;
      const oy = e.clientY - rect.top - rect.height / 2;
      const factor = e.deltaY > 0 ? 0.9 : 1.1;
      setTransform((prev) => {
        const scale = Math.min(8, Math.max(0.1, prev.scale * factor));
        const ratio = scale / prev.scale;
        return {
          x: ox - (ox - prev.x) * ratio,
          y: oy - (oy - prev.y) * ratio,
          scale,
        };
      });
    }
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  function onPointerDown(e: React.PointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    const t = transformRef.current;
    dragRef.current = { sx: e.clientX, sy: e.clientY, tx: t.x, ty: t.y };
    setIsDragging(true);
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function onPointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!dragRef.current) return;
    const { tx, ty, sx, sy } = dragRef.current;
    const dx = e.clientX - sx;
    const dy = e.clientY - sy;
    setTransform((prev) => ({ ...prev, x: tx + dx, y: ty + dy }));
  }

  function onPointerUp() {
    dragRef.current = null;
    setIsDragging(false);
  }

  const sourceGraph = useMemo(
    () => (detail ? parseSourceGraph(detail.source.graph) : null),
    [detail]
  );

  const projection = useMemo(
    () => (detail && sourceGraph ? buildProjection(detail, sourceGraph, replayIndex) : null),
    [detail, sourceGraph, replayIndex]
  );

  const currentNode = useMemo(() => {
    const node = sourceGraph?.nodes.find((n) => n.id === projection?.currentNodeId) ?? null;
    if (node?.type === "start" || node?.type === "end") return null;
    return node;
  }, [sourceGraph, projection]);

  const currentSummary = projection?.nodeEventSummaries.get(currentNode?.id ?? "") ?? null;

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    if (!sourceGraph || sourceGraph.nodes.length === 0) {
      container.innerHTML = "";
      return;
    }

    const proj = buildProjection(detail!, sourceGraph, replayIndex);
    const diagram = buildMermaidDiagram(sourceGraph, proj);
    const id = `wf_mermaid_${++renderCounter}`;
    let active = true;
    const renderToken = ++renderTokenRef.current;

    renderQueueRef.current = renderQueueRef.current
      .catch(() => undefined)
      .then(async () => {
        if (!active || renderToken !== renderTokenRef.current) return;
        try {
          const mermaid = await initMermaid();
          if (!active || renderToken !== renderTokenRef.current) return;
          const { svg, bindFunctions } = await mermaid.render(id, diagram);
          if (!active || renderToken !== renderTokenRef.current || !containerRef.current) return;
          containerRef.current.innerHTML = svg;
          try {
            bindFunctions?.(containerRef.current);
          } catch {
            // Mermaid bindFunctions may fail in some environments.
          }
        } catch (err) {
          if (!active || renderToken !== renderTokenRef.current || !containerRef.current) return;
          containerRef.current.innerHTML = `<div style="color:#f87171;padding:16px;font-size:13px">${String(err)}</div>`;
        }
      });

    return () => {
      active = false;
    };
  }, [detail, replayIndex]);

  return (
    <div
      ref={viewportRef}
      className="absolute inset-0 overflow-hidden"
      style={{ cursor: isDragging ? "grabbing" : "grab" }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    >
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(56,189,248,0.14),transparent_28%),radial-gradient(circle_at_top_right,rgba(251,191,36,0.10),transparent_22%),linear-gradient(180deg,#0b1120,#111a2e_58%,#162235)]" />
      {/* transform origin is viewport center; x/y are offsets from center */}
      <div
        className="pointer-events-none absolute inset-0 flex items-center justify-center"
      >
        <div
          style={{
            transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})`,
            transformOrigin: "center center",
          }}
        >
          <div ref={containerRef} className="[&_svg]:block [&_svg]:h-auto [&_svg]:w-auto" />
        </div>
      </div>

      {currentNode ? (
        <div className="pointer-events-auto absolute bottom-6 right-6 z-20 w-[280px] rounded-xl border border-slate-700/70 bg-slate-950/90 p-3 text-slate-50 shadow-2xl backdrop-blur-xl">
          <div className="mb-2 flex items-center justify-between gap-2">
            <div className="truncate text-sm font-semibold text-white">{currentNode.name}</div>
            <span className="shrink-0 rounded-full bg-amber-400/18 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-[0.12em] text-amber-200 ring-1 ring-amber-300/35">
              LIVE
            </span>
          </div>
          <NodeInfoPanel
            node={currentNode}
            summary={currentSummary}
            runId={detail?.run.run_id}
            sourceId={detail?.source.id}
          />
        </div>
      ) : null}
    </div>
  );
}
