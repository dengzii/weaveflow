import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRightLeft,
  ChevronLeft,
  ChevronRight,
  Database,
  GitGraph,
  Pause,
  Play,
  Radio,
  RefreshCw,
  SkipBack,
  SkipForward,
  Rows3,
} from "lucide-react";
import { api, buildUrl } from "../replay/api";
import type {
  RunDetail,
  RunSummary,
  RunsResponse,
} from "../replay/types";
import { formatDuration, formatTime, prettyJSON } from "../replay/utils";
import { ReplayGraphCanvas } from "./ReplayGraphCanvas";
import { MermaidGraphCanvas } from "./MermaidGraphCanvas";
import { parseSourceGraph, JsonTree } from "./graph";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/select";
import { cn } from "../../lib/utils";
import { useLiveMode, visibleLiveStatus } from "./useLiveMode";

const DEFAULT_CACHE_DIR =
  (document.body.dataset.defaultCacheDir as string | undefined)?.trim() || "neo_data";

function runOptionValue(item: RunSummary): string {
  return `${item.source_id}::${item.run.run_id}`;
}

function runOptionLabel(item: RunSummary): string {
  return `${item.source_name || item.source_id} | ${item.run.run_id}`;
}

export function ReplayPageV2() {
  const [status, setStatus] = useState({ message: "Preparing", summary: "" });
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [selectedSourceId, setSelectedSourceId] = useState("");
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [replayIndex, setReplayIndex] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);
  const [layoutVersion, setLayoutVersion] = useState(0);
  const [viewMode, setViewMode] = useState<"flow" | "mermaid">("flow");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(320);
  const sidebarWidthRef = useRef(320);
  const resizeDragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const timerRef = useRef<number | null>(null);
  const replayLengthRef = useRef(0);

  useEffect(() => {
    replayLengthRef.current = detail?.replay?.length ?? 0;
  }, [detail]);

  useEffect(() => {
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, []);

  function stopReplay() {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
    setIsPlaying(false);
  }

  const {
    mode,
    modeRef,
    isLiveMode,
    liveState,
    liveSocketState,
    liveDuration,
    liveEventsListRef,
    enterLiveMode,
    exitLiveMode,
    liveBadge,
  } = useLiveMode({
    cacheDir: DEFAULT_CACHE_DIR,
    setDetail,
    setReplayIndex,
    setStatus,
    onEnterLive: () => {
      stopReplay();
      setDetail(null);
      setReplayIndex(0);
    },
    onExitLive: async () => {
      stopReplay();
      const target =
        runs.find(
          (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
        ) ?? runs[0];
      if (target) {
        await selectRun(DEFAULT_CACHE_DIR, target);
        return;
      }
      setDetail(null);
      setStatus({ message: "No replay runs available.", summary: "" });
    },
  });

  useEffect(() => {
    if (mode !== "live") return;
    const container = liveEventsListRef.current;
    if (!container) return;
    const target = container.querySelector<HTMLElement>(`[data-live-event-index="${replayIndex}"]`);
    target?.scrollIntoView({ block: "nearest" });
  }, [mode, replayIndex, detail?.replay.length, liveEventsListRef]);

  function onResizePointerDown(e: React.PointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    resizeDragRef.current = { startX: e.clientX, startWidth: sidebarWidthRef.current };
  }

  function onResizePointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!resizeDragRef.current) return;
    const { startX, startWidth } = resizeDragRef.current;
    const next = Math.max(260, Math.min(680, startWidth + (e.clientX - startX)));
    sidebarWidthRef.current = next;
    setSidebarWidth(next);
  }

  function onResizePointerUp() {
    resizeDragRef.current = null;
  }

  function handleReplayIndex(index: number) {
    stopReplay();
    const length = replayLengthRef.current;
    if (!length) return;
    setReplayIndex(Math.max(0, Math.min(index, length - 1)));
  }

  function toggleReplay() {
    if (timerRef.current) {
      stopReplay();
      return;
    }
    if (!replayLengthRef.current) return;
    setIsPlaying(true);
    timerRef.current = window.setInterval(() => {
      setReplayIndex((current) => {
        if (!replayLengthRef.current || current >= replayLengthRef.current - 1) {
          if (timerRef.current) clearInterval(timerRef.current);
          timerRef.current = null;
          setIsPlaying(false);
          return current;
        }
        return current + 1;
      });
    }, 900);
  }

  async function selectRun(baseDir: string, item: RunSummary) {
    stopReplay();
    setSelectedRunId(item.run.run_id);
    setSelectedSourceId(item.source_id);
    setReplayIndex(0);
    setStatus({ message: "Loading replay detail...", summary: "" });

    try {
      const url = buildUrl(`/api/run/${encodeURIComponent(item.run.run_id)}`, baseDir, {
        source: item.source_id,
      });
      const runDetail = await api<RunDetail>(url);
      setDetail(runDetail);
      setStatus({
        message: "Replay detail loaded.",
        summary: `${runDetail.summary.source_name} | ${runDetail.summary.graph_ref || "-"}`,
      });
    } catch (error) {
      setDetail(null);
      setStatus({ message: `Load failed: ${(error as Error).message}`, summary: "" });
    }
  }

  async function loadRuns(baseDir: string) {
    stopReplay();
    setStatus({ message: "Scanning replay cache...", summary: "" });

    try {
      const data = await api<RunsResponse>(buildUrl("/api/runs", baseDir));
      const runList = data.runs ?? [];
      setRuns(runList);
      setStatus({
        message: `Scanned ${data.cache_dir}`,
        summary: `${data.sources?.length ?? 0} sources | ${runList.length} runs`,
      });

      if (!runList.length) {
        if (modeRef.current !== "live") setDetail(null);
        return;
      }
      if (modeRef.current === "live") return;

      const preserved = runList.find(
        (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
      );
      const target = preserved ?? runList[0];
      await selectRun(baseDir, target);
    } catch (error) {
      setRuns([]);
      if (modeRef.current !== "live") setDetail(null);
      setStatus({ message: `Load failed: ${(error as Error).message}`, summary: "" });
    }
  }

  useEffect(() => {
    void loadRuns(DEFAULT_CACHE_DIR);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const current = detail?.replay?.[replayIndex] ?? null;

  const artifactPayload = (() => {
    if (current?.event.type !== "artifact.created" || !detail) return null;
    const payload = (current.event.payload ?? {}) as Record<string, unknown>;
    const id = String(payload.artifact_id ?? "");
    if (!id) return null;
    return {
      id,
      mimeType: String(payload.mime_type ?? ""),
      artifactType: String(payload.type ?? ""),
      runId: detail.run.run_id,
      sourceId: detail.source.id,
    };
  })();
  const selectedRunValue =
    selectedRunId && selectedSourceId ? `${selectedSourceId}::${selectedRunId}` : undefined;
  const sourceGraph = detail ? parseSourceGraph(detail.source.graph) : null;
  const visibleNodeCount =
    sourceGraph?.nodes.filter((node) => node.type !== "start" && node.type !== "end").length ?? 0;
  const edgeCount = sourceGraph?.edges.length ?? 0;
  const liveGraphRef =
    liveState?.graph_ref || detail?.summary.graph_ref || detail?.run.graph_id || "-";
  const liveSourceName = liveState?.source_name || detail?.summary.source_name || "Neo Agent";
  const liveGraphJSON = detail?.source.graph ?? liveState?.graph;
  const liveStatusText = isLiveMode ? visibleLiveStatus(status.message, liveSocketState) : "";

  return (
    <div className="relative h-full overflow-hidden bg-background text-foreground">
      {viewMode === "flow" ? (
        <ReplayGraphCanvas
          detail={detail}
          replayIndex={replayIndex}
          layoutVersion={layoutVersion}
        />
      ) : (
        <MermaidGraphCanvas detail={detail} replayIndex={replayIndex} />
      )}

      <div className="pointer-events-none absolute inset-0 z-20">
        <div className="flex h-full items-start justify-between gap-3">
          {sidebarCollapsed ? (
            <div className="pointer-events-auto flex flex-col gap-2">
              <div className="flex flex-col gap-1 border border-l-0 border-border bg-background/92 p-2 shadow-2xl backdrop-blur-xl">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 rounded-full text-foreground hover:bg-muted"
                  onClick={() => setSidebarCollapsed(false)}
                  title="展开侧边栏"
                >
                  <ChevronRight className="h-4 w-4" />
                </Button>
                <div className="flex h-8 w-8 items-center justify-center rounded-xl bg-foreground text-background">
                  <Database className="h-4 w-4" />
                </div>
              </div>
            </div>
          ) : (
          <div
            className="pointer-events-auto relative flex h-full self-stretch flex-col overflow-hidden border border-l-0 border-border bg-background/92 text-foreground shadow-2xl backdrop-blur-xl"
            style={{ width: sidebarWidth }}
          >
            {/* resize handle */}
            <div
              className="absolute right-0 top-0 z-30 h-full w-1 cursor-ew-resize transition-colors hover:bg-muted/50 active:bg-muted"
              onPointerDown={onResizePointerDown}
              onPointerMove={onResizePointerMove}
              onPointerUp={onResizePointerUp}
              onPointerCancel={onResizePointerUp}
            />
            {/* Header */}
            <div className="flex items-center gap-1.5 border-b border-border px-3 py-3">
              <Link to="/">
                <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0 rounded-full text-foreground hover:bg-muted">
                  <ArrowLeft className="h-3.5 w-3.5" />
                </Button>
              </Link>
              <div className="flex min-w-0 flex-1 items-center gap-1.5">
                <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-foreground text-background">
                  <Database className="h-3.5 w-3.5" />
                </div>
                <span className="truncate text-sm font-semibold text-foreground">Replay</span>
                {isLiveMode ? (
                  <Badge
                    variant="outline"
                    className={cn(
                      "rounded-full border-border bg-card text-[10px] uppercase tracking-[0.14em] text-rose-300"
                    )}
                  >
                    Live
                  </Badge>
                ) : null}
              </div>
              <Link to="/debug/replay/old">
                <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground" title="Old view">
                  <ArrowRightLeft className="h-3.5 w-3.5" />
                </Button>
              </Link>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className={cn(
                  "h-7 w-7 shrink-0 rounded-full hover:bg-muted",
                  isLiveMode ? "text-rose-400" : "text-muted-foreground hover:text-foreground"
                )}
                onClick={() => {
                  if (isLiveMode) {
                    void exitLiveMode();
                  } else {
                    enterLiveMode();
                  }
                }}
                title={isLiveMode ? "Leave live mode" : "Enter live mode"}
              >
                <Radio className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className={`h-7 w-7 shrink-0 rounded-full hover:bg-muted ${viewMode === "mermaid" ? "text-violet-400" : "text-muted-foreground hover:text-foreground"}`}
                onClick={() => setViewMode((m) => (m === "flow" ? "mermaid" : "flow"))}
                title={viewMode === "mermaid" ? "Switch to Flow" : "Switch to Mermaid"}
              >
                <GitGraph className="h-3.5 w-3.5" />
              </Button>
              {viewMode === "flow" ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
                  onClick={() => setLayoutVersion((value) => value + 1)}
                  title="Re-layout"
                >
                  <Rows3 className="h-3.5 w-3.5" />
                </Button>
              ) : null}
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-7 w-7 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
                onClick={() => setSidebarCollapsed(true)}
                title="收起侧边栏"
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </Button>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
              {isLiveMode ? (
                <div className="border-b border-border pb-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate text-xs font-semibold text-rose-200">
                        {liveSourceName}
                      </div>
                      <div className="truncate text-[11px] text-rose-200/65">{liveGraphRef}</div>
                    </div>
                    <Badge
                      variant="outline"
                      className={cn(
                        "rounded-full border-rose-400/30 bg-rose-950/40 text-[10px] uppercase tracking-[0.14em]",
                        liveSocketState === "disconnected"
                          ? "text-muted-foreground"
                          : liveState?.running
                            ? "text-rose-300"
                            : "text-amber-200"
                      )}
                    >
                      {liveBadge}
                    </Badge>
                  </div>
                </div>
              ) : (
                <div className="flex items-center gap-1.5 border-b border-border pb-3">
                  <Select
                    value={selectedRunValue}
                    onValueChange={(value) => {
                      const target = runs.find((item) => runOptionValue(item) === value);
                      if (target) void selectRun(DEFAULT_CACHE_DIR, target);
                    }}
                  >
                    <SelectTrigger className="h-8 rounded-full border-border bg-card/80 text-xs text-foreground">
                      <SelectValue placeholder="Select a replay" />
                    </SelectTrigger>
                    <SelectContent>
                      {runs.map((item) => (
                        <SelectItem key={runOptionValue(item)} value={runOptionValue(item)}>
                          {runOptionLabel(item)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="h-8 w-8 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
                    onClick={() => void loadRuns(DEFAULT_CACHE_DIR)}
                  >
                    <RefreshCw className="h-3.5 w-3.5" />
                  </Button>
                </div>
              )}
              {!isLiveMode || liveStatusText ? (
                <div className="mt-1 flex items-center gap-2 px-1">
                  <span className="flex-1 truncate text-[11px] text-muted-foreground">
                    {isLiveMode ? liveStatusText : status.message}
                  </span>
                </div>
              ) : null}

              {detail ? (
                <div className="mt-3 divide-y divide-border">
                  {isLiveMode ? (
                    <div className="grid grid-cols-2 gap-x-4 gap-y-2 pb-3">
                      <Metric
                        label="Nodes / Edges"
                        value={`${visibleNodeCount} / ${edgeCount}`}
                        className={liveState?.running ? undefined : "col-span-2"}
                      />
                      {liveState?.running ? (
                        <Metric
                          label="Duration"
                          value={formatDuration(liveDuration)}
                          live
                        />
                      ) : null}
                    </div>
                  ) : (
                    <div className="grid grid-cols-2 gap-x-4 gap-y-2 pb-3">
                      <Metric
                        label="Graph"
                        value={detail.summary.graph_ref || detail.run.graph_id || "-"}
                      />
                      <Metric
                        label="Duration"
                        value={formatDuration(detail.summary.duration_ms)}
                      />
                    </div>
                  )}

                  {!isLiveMode ? (
                    <div className="py-3">
                      <div className="flex items-center gap-2">
                        <span className="text-xs tabular-nums text-muted-foreground">
                          {detail.replay.length ? `${replayIndex + 1}/${detail.replay.length}` : "0/0"}
                        </span>
                        <div className="flex flex-1 items-center justify-end gap-1">
                          <Button type="button" size="icon" variant="ghost" className="h-7 w-7 rounded-full" onClick={() => handleReplayIndex(replayIndex - 1)} disabled={replayIndex <= 0}>
                            <SkipBack className="h-3.5 w-3.5" />
                          </Button>
                          <Button type="button" size="icon" variant="outline" className="h-7 w-7 rounded-full" onClick={toggleReplay} disabled={detail.replay.length === 0}>
                            {isPlaying ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
                          </Button>
                          <Button type="button" size="icon" variant="ghost" className="h-7 w-7 rounded-full" onClick={() => handleReplayIndex(replayIndex + 1)} disabled={replayIndex >= detail.replay.length - 1}>
                            <SkipForward className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </div>
                      <input
                        type="range"
                        className="mt-2 w-full accent-amber-400"
                        min={0}
                        max={Math.max(0, detail.replay.length - 1)}
                        value={replayIndex}
                        onChange={(event) => handleReplayIndex(Number(event.target.value))}
                      />
                    </div>
                  ) : null}

                  {/* Current event */}
                  <div className="py-3">
                    <div className="flex items-center justify-between gap-2">
                      <div className="min-w-0">
                        <div className="truncate text-sm font-semibold text-foreground">
                          {current?.title || (isLiveMode ? "Waiting for next event" : "No event")}
                        </div>
                        {(current?.subtitle || current?.event.node_id) ? (
                          <div className="truncate text-[11px] text-muted-foreground">
                            {current?.subtitle || current?.event.node_id}
                          </div>
                        ) : null}
                      </div>
                      <div className="flex shrink-0 flex-col items-end gap-1">
                        <Badge variant="outline" className="rounded-full border-border bg-muted text-[10px] text-foreground">
                          {current?.event.type || detail.run.status}
                        </Badge>
                        {current?.timestamp ? (
                          <span className="text-[10px] text-muted-foreground">{formatTime(current.timestamp)}</span>
                        ) : null}
                      </div>
                    </div>
                  </div>

                  {artifactPayload ? (
                    <div className="py-3">
                      <div className="mb-1.5 flex items-center gap-2">
                        <span className="text-xs font-medium text-muted-foreground">Artifact</span>
                        <span className="rounded-full bg-violet-500/15 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-[0.12em] text-violet-300 ring-1 ring-violet-400/25">
                          {artifactPayload.artifactType || artifactPayload.mimeType || "file"}
                        </span>
                      </div>
                      <div className="mb-2 font-mono text-[10px] text-muted-foreground truncate">{artifactPayload.id}</div>
                      <ArtifactPreviewSection
                        runId={artifactPayload.runId}
                        sourceId={artifactPayload.sourceId}
                        artifactId={artifactPayload.id}
                        mimeType={artifactPayload.mimeType}
                      />
                    </div>
                  ) : null}

                  {isLiveMode ? (
                    <>
                      <div className="py-3">
                        <div className="mb-2 flex items-center justify-between gap-2">
                          <span className="text-xs font-medium text-muted-foreground">Events</span>
                          <span className="rounded-full bg-muted px-1.5 py-0.5 text-[9px] tabular-nums text-muted-foreground">{detail.replay.length}</span>
                        </div>
                        <div ref={liveEventsListRef} className="max-h-[220px] space-y-1 overflow-auto">
                          {detail.replay.length ? (
                            detail.replay.map((item, index) => (
                              <button
                                key={`${item.index}:${item.event.id || item.timestamp}:${item.event.type}`}
                                data-live-event-index={index}
                                type="button"
                                onClick={() => setReplayIndex(index)}
                                className={cn(
                                  "block w-full rounded-lg px-2.5 py-2 text-left transition",
                                  index === replayIndex
                                    ? "bg-amber-500/16 text-foreground"
                                    : "text-muted-foreground hover:bg-muted/50"
                                )}
                              >
                                <div className="truncate text-xs font-medium">
                                  {item.title || item.event.type || "Event"}
                                </div>
                                <div className="mt-0.5 flex items-center justify-between gap-2 text-[10px] text-muted-foreground">
                                  <span className="min-w-0 flex-1 truncate">
                                    {item.subtitle || item.event.node_id || item.event.type}
                                  </span>
                                  <span className="shrink-0">{formatTime(item.timestamp)}</span>
                                </div>
                              </button>
                            ))
                          ) : (
                            <div className="px-2.5 py-3 text-xs text-muted-foreground">
                              Waiting for live events.
                            </div>
                          )}
                        </div>
                      </div>

                      <div className="py-3">
                        <div className="mb-2 text-xs font-medium text-muted-foreground">Current Event Payload</div>
                        <pre className="max-h-[180px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-5 text-foreground">
                          {prettyJSON(current?.event.payload)}
                        </pre>
                      </div>

                      <div className="py-3">
                        <div className="mb-2 text-xs font-medium text-muted-foreground">Graph JSON</div>
                        <pre className="max-h-[240px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-5 text-foreground">
                          {prettyJSON(liveGraphJSON)}
                        </pre>
                      </div>
                    </>
                  ) : null}

                  {!isLiveMode ? (
                    <div className="py-3">
                      <div className="max-h-36 space-y-0.5 overflow-y-auto">
                        {runs.map((item) => (
                          <button
                            key={`${item.source_id}:${item.run.run_id}`}
                            type="button"
                            onClick={() => void selectRun(DEFAULT_CACHE_DIR, item)}
                            className={cn(
                              "flex w-full items-center gap-2 rounded-xl px-2.5 py-1.5 text-left transition",
                              item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                                ? "bg-amber-500/16 text-foreground"
                                : "text-muted-foreground hover:bg-muted/50"
                            )}
                          >
                            <span className="min-w-0 flex-1 truncate text-xs font-medium">
                              {item.source_name || item.source_id}
                            </span>
                            <span
                              className={cn(
                                "shrink-0 truncate text-[10px]",
                                item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                                  ? "text-amber-300/80"
                                  : "text-muted-foreground"
                              )}
                            >
                              {item.run.run_id.slice(0, 8)}
                            </span>
                          </button>
                        ))}
                      </div>
                    </div>
                  ) : null}
                </div>
              ) : (
                <div className="mt-3 rounded-xl border border-dashed border-border bg-card/60 px-4 py-6 text-center text-sm text-muted-foreground">
                  {isLiveMode
                    ? "Waiting for live graph snapshot. Start a new request to stream the graph here."
                    : "Select a replay to load graph history."}
                </div>
              )}
            </div>
          </div>
          )}

          {!isLiveMode ? (
            <div className="pointer-events-auto mt-3 mr-3 hidden w-[340px] rounded-[22px] border border-border bg-background/92 p-3 text-foreground shadow-2xl backdrop-blur-xl 2xl:block">
              <div>
                <div className="text-sm font-semibold text-foreground">Event Payload</div>
                <div className="text-xs text-muted-foreground">Raw payload for the current event.</div>
              </div>
              <pre className="mt-3 max-h-[360px] overflow-auto rounded-xl border border-border bg-card p-3 font-mono text-[11px] leading-5 text-foreground">
                {prettyJSON(current?.event.payload)}
              </pre>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

type ArtifactDetail = {
  bytes: number;
  encoding: "text" | "json" | "base64";
  payload: unknown;
  truncated?: boolean;
};

function ArtifactPreviewSection({
  runId,
  sourceId,
  artifactId,
  mimeType,
}: {
  runId: string;
  sourceId: string;
  artifactId: string;
  mimeType: string;
}) {
  const [detail, setDetail] = useState<ArtifactDetail | null>(null);
  const [fetchError, setFetchError] = useState("");
  const [loading, setLoading] = useState(false);

  const isImage = mimeType.startsWith("image/");
  const artifactPath = `/api/run/${encodeURIComponent(runId)}/artifact/${encodeURIComponent(artifactId)}`;
  const query = sourceId && sourceId !== "live" ? { source: sourceId } : {};
  const detailUrl = buildUrl(artifactPath, DEFAULT_CACHE_DIR, query);
  const downloadUrl = buildUrl(artifactPath, DEFAULT_CACHE_DIR, {
    ...query,
    download: "1",
  });

  useEffect(() => {
    if (isImage || !runId || !artifactId) return;
    let cancelled = false;
    setLoading(true);
    setDetail(null);
    setFetchError("");
    api<ArtifactDetail>(detailUrl)
      .then((data) => {
        if (!cancelled) setDetail(data);
      })
      .catch((err: unknown) => {
        if (!cancelled) setFetchError((err as Error).message ?? String(err));
      })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [detailUrl, isImage, runId, artifactId]);

  if (isImage) {
    return (
      <div className="overflow-hidden rounded-lg border border-border bg-card">
        <img src={downloadUrl} alt={artifactId} className="max-h-[240px] w-full object-contain" />
      </div>
    );
  }

  if (loading) return <div className="text-[11px] text-muted-foreground">Loading…</div>;
  if (fetchError) return <div className="text-[11px] text-rose-400">{fetchError}</div>;

  if (detail) {
    const { encoding, payload, truncated } = detail;
    if (encoding === "json") {
      return (
        <div className="max-h-[200px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-[1.65]">
          <JsonTree data={payload} truncated={truncated} />
        </div>
      );
    }
    if (encoding === "text") {
      return (
        <pre className="max-h-[200px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-5 text-foreground whitespace-pre-wrap break-words">
          {String(payload ?? "")}{truncated ? "\n…<truncated>" : ""}
        </pre>
      );
    }
    return (
      <div className="flex items-center gap-3">
        <span className="text-[11px] text-muted-foreground">{detail.bytes} bytes</span>
        <a href={downloadUrl} target="_blank" rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-[11px] text-sky-400 hover:text-sky-300">
          ↓ Download
        </a>
      </div>
    );
  }

  return (
    <a href={downloadUrl} target="_blank" rel="noopener noreferrer"
      className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-muted/60 px-3 py-2 text-[11px] text-sky-400 hover:bg-muted hover:text-sky-300 transition-colors">
      ↓ Download {mimeType || "artifact"}
    </a>
  );
}

function Metric({
  label,
  value,
  live,
  className,
}: {
  label: string;
  value: string;
  live?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
        {label}
        {live && <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-rose-400" />}
      </div>
      <div className="truncate pt-1 text-sm font-semibold text-foreground">{value}</div>
    </div>
  );
}
