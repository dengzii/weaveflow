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
  RefreshCw,
  SkipBack,
  SkipForward,
  Rows3,
} from "lucide-react";
import { api, buildUrl } from "../replay/api";
import type { RunDetail, RunSummary, RunsResponse } from "../replay/types";
import { formatDuration, formatTime, prettyJSON } from "../replay/utils";
import { ReplayGraphCanvas } from "./ReplayGraphCanvas";
import { MermaidGraphCanvas } from "./MermaidGraphCanvas";
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
    setStatus({ message: "Loading run detail...", summary: "" });

    try {
      const url = buildUrl(`/api/run/${encodeURIComponent(item.run.run_id)}`, baseDir, {
        source: item.source_id,
      });
      const runDetail = await api<RunDetail>(url);
      setDetail(runDetail);
      setStatus({
        message: `Loaded run ${item.run.run_id}`,
        summary: `${runDetail.summary.source_name} | ${runDetail.summary.graph_ref || "-"}`,
      });
    } catch (error) {
      setDetail(null);
      setStatus({ message: `Load failed: ${(error as Error).message}`, summary: "" });
    }
  }

  async function loadRuns(baseDir: string) {
    stopReplay();
    setStatus({ message: "Scanning Neo runs...", summary: "" });

    try {
      const data = await api<RunsResponse>(buildUrl("/api/runs", baseDir));
      const runList = data.runs ?? [];
      setRuns(runList);
      setStatus({
        message: `Scanned ${data.cache_dir}`,
        summary: `${data.sources?.length ?? 0} sources | ${runList.length} runs`,
      });

      if (!runList.length) {
        setDetail(null);
        return;
      }

      const preserved = runList.find(
        (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
      );
      const target = preserved ?? runList[0];
      await selectRun(baseDir, target);
    } catch (error) {
      setRuns([]);
      setDetail(null);
      setStatus({ message: `Load failed: ${(error as Error).message}`, summary: "" });
    }
  }

  useEffect(() => {
    void loadRuns(DEFAULT_CACHE_DIR);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const current = detail?.replay?.[replayIndex] ?? null;
  const selectedRunValue =
    selectedRunId && selectedSourceId ? `${selectedSourceId}::${selectedRunId}` : undefined;

  return (
    <div className="dark relative h-full overflow-hidden bg-background text-foreground">
      {viewMode === "flow" ? (
        <ReplayGraphCanvas
          detail={detail}
          replayIndex={replayIndex}
          layoutVersion={layoutVersion}
        />
      ) : (
        <MermaidGraphCanvas detail={detail} replayIndex={replayIndex} />
      )}

      <div className="pointer-events-none absolute inset-x-0 top-0 z-20 p-3">
        <div className="flex items-start justify-between gap-3">
          {sidebarCollapsed ? (
            <div className="pointer-events-auto flex flex-col gap-2">
              <div className="flex flex-col gap-1 rounded-[18px] border border-slate-700/70 bg-slate-950/88 p-2 shadow-2xl backdrop-blur-xl">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 rounded-full text-slate-200 hover:bg-slate-800"
                  onClick={() => setSidebarCollapsed(false)}
                  title="展开侧边栏"
                >
                  <ChevronRight className="h-4 w-4" />
                </Button>
                <div className="flex h-8 w-8 items-center justify-center rounded-xl bg-slate-50 text-slate-950">
                  <Database className="h-4 w-4" />
                </div>
              </div>
            </div>
          ) : (
          <div className="pointer-events-auto w-fit max-w-[520px] min-w-[320px] rounded-[22px] border border-slate-700/70 bg-slate-950/88 p-3 text-slate-50 shadow-2xl backdrop-blur-xl">
            {/* Header */}
            <div className="flex items-center gap-1.5">
              <Link to="/">
                <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0 rounded-full text-slate-200 hover:bg-slate-800">
                  <ArrowLeft className="h-3.5 w-3.5" />
                </Button>
              </Link>
              <div className="flex min-w-0 flex-1 items-center gap-1.5">
                <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-slate-50 text-slate-950">
                  <Database className="h-3.5 w-3.5" />
                </div>
                <span className="truncate text-sm font-semibold text-white">Replay V2</span>
              </div>
              <Link to="/debug/replay">
                <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0 rounded-full text-slate-400 hover:bg-slate-800 hover:text-slate-50" title="Classic view">
                  <ArrowRightLeft className="h-3.5 w-3.5" />
                </Button>
              </Link>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className={`h-7 w-7 shrink-0 rounded-full hover:bg-slate-800 ${viewMode === "mermaid" ? "text-violet-400" : "text-slate-400 hover:text-slate-50"}`}
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
                  className="h-7 w-7 shrink-0 rounded-full text-slate-400 hover:bg-slate-800 hover:text-slate-50"
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
                className="h-7 w-7 shrink-0 rounded-full text-slate-400 hover:bg-slate-800 hover:text-slate-50"
                onClick={() => setSidebarCollapsed(true)}
                title="收起侧边栏"
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </Button>
            </div>

            {/* Run selector */}
            <div className="mt-2 flex items-center gap-1.5">
              <Select
                value={selectedRunValue}
                onValueChange={(value) => {
                  const target = runs.find((item) => runOptionValue(item) === value);
                  if (target) void selectRun(DEFAULT_CACHE_DIR, target);
                }}
              >
                <SelectTrigger className="h-8 rounded-full border-slate-700 bg-slate-900/80 text-xs text-slate-50">
                  <SelectValue placeholder="Select a run" />
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
                className="h-8 w-8 shrink-0 rounded-full text-slate-400 hover:bg-slate-800 hover:text-slate-50"
                onClick={() => void loadRuns(DEFAULT_CACHE_DIR)}
              >
                <RefreshCw className="h-3.5 w-3.5" />
              </Button>
            </div>
            <div className="mt-1 truncate px-1 text-[11px] text-slate-500">{status.message}</div>

            {detail ? (
              <div className="mt-2 space-y-2">
                {/* Metrics */}
                <div className="grid grid-cols-2 gap-1.5">
                  <Metric label="Graph" value={detail.summary.graph_ref || detail.run.graph_id || "-"} />
                  <Metric label="Duration" value={formatDuration(detail.summary.duration_ms)} />
                </div>

                {/* Playback */}
                <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 px-3 py-2">
                  <div className="flex items-center gap-2">
                    <span className="text-xs tabular-nums text-slate-400">
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

                {/* Current event */}
                <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold text-white">
                        {current?.title || "No event"}
                      </div>
                      {(current?.subtitle || current?.event.node_id) ? (
                        <div className="truncate text-[11px] text-slate-400">
                          {current?.subtitle || current?.event.node_id}
                        </div>
                      ) : null}
                    </div>
                    <div className="flex shrink-0 flex-col items-end gap-1">
                      <Badge variant="outline" className="rounded-full border-slate-600 bg-slate-800 text-[10px] text-slate-100">
                        {current?.event.type || detail.run.status}
                      </Badge>
                      {current?.timestamp ? (
                        <span className="text-[10px] text-slate-500">{formatTime(current.timestamp)}</span>
                      ) : null}
                    </div>
                  </div>
                </div>

                {/* Runs list */}
                <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 p-1.5">
                  <div className="max-h-36 space-y-0.5 overflow-y-auto">
                    {runs.map((item) => (
                      <button
                        key={`${item.source_id}:${item.run.run_id}`}
                        type="button"
                        onClick={() => void selectRun(DEFAULT_CACHE_DIR, item)}
                        className={cn(
                          "flex w-full items-center gap-2 rounded-xl px-2.5 py-1.5 text-left transition",
                          item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                            ? "bg-amber-500/16 text-white"
                            : "text-slate-300 hover:bg-slate-800/80"
                        )}
                      >
                        <span className="min-w-0 flex-1 truncate text-xs font-medium">{item.source_name || item.source_id}</span>
                        <span className={cn(
                          "shrink-0 truncate text-[10px]",
                          item.run.run_id === selectedRunId && item.source_id === selectedSourceId ? "text-amber-300/80" : "text-slate-500"
                        )}>
                          {item.run.run_id.slice(0, 8)}
                        </span>
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ) : (
              <div className="mt-3 rounded-xl border border-dashed border-slate-700 bg-slate-900/60 px-4 py-6 text-center text-sm text-slate-400">
                Select a run to load replay data.
              </div>
            )}
          </div>
          )}

          <div className="pointer-events-auto hidden w-[340px] rounded-[22px] border border-slate-700/70 bg-slate-950/88 p-3 text-slate-50 shadow-2xl backdrop-blur-xl 2xl:block">
            <div>
              <div className="text-sm font-semibold text-white">Event Payload</div>
              <div className="text-xs text-slate-300">Raw payload for the current event.</div>
            </div>
            <pre className="mt-3 max-h-[360px] overflow-auto rounded-xl border border-slate-700 bg-slate-900 p-3 font-mono text-[11px] leading-5 text-slate-100">
              {prettyJSON(current?.event.payload)}
            </pre>
          </div>
        </div>
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 px-3 py-2">
      <div className="text-[11px] uppercase tracking-[0.14em] text-slate-400">{label}</div>
      <div className="truncate pt-1 text-sm font-semibold text-white">{value}</div>
    </div>
  );
}
