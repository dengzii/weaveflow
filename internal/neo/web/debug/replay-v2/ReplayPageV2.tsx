import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRightLeft,
  Database,
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
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { cn } from "../../lib/utils";

const DEFAULT_CACHE_DIR =
  (document.body.dataset.defaultCacheDir as string | undefined) ?? "";

export function ReplayPageV2() {
  const [inputDir, setInputDir] = useState(DEFAULT_CACHE_DIR);
  const [cacheDir, setCacheDir] = useState(DEFAULT_CACHE_DIR);
  const [status, setStatus] = useState({ message: "准备加载", summary: "" });
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [selectedSourceId, setSelectedSourceId] = useState("");
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [replayIndex, setReplayIndex] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);
  const [layoutVersion, setLayoutVersion] = useState(0);

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

  async function selectRun(dir: string, runId: string, sourceId: string) {
    stopReplay();
    setSelectedRunId(runId);
    setSelectedSourceId(sourceId);
    setReplayIndex(0);
    setStatus({ message: "加载运行详情中...", summary: "" });

    try {
      const url = buildUrl(`/api/run/${encodeURIComponent(runId)}`, dir, { source: sourceId });
      const runDetail = await api<RunDetail>(url);
      setDetail(runDetail);
      setStatus({
        message: `已加载 run ${runId}`,
        summary: `${runDetail.summary.source_name} · ${runDetail.summary.graph_ref || "-"}`,
      });
    } catch (error) {
      setDetail(null);
      setStatus({ message: `加载失败: ${(error as Error).message}`, summary: "" });
    }
  }

  async function loadRuns(dir: string) {
    stopReplay();
    setCacheDir(dir);
    setStatus({ message: "扫描缓存目录中...", summary: "" });

    try {
      const data = await api<RunsResponse>(buildUrl("/api/runs", dir));
      const runList = data.runs ?? [];
      setRuns(runList);
      setStatus({
        message: `已扫描 ${data.cache_dir}`,
        summary: `${data.sources?.length ?? 0} 个缓存源 · ${runList.length} 个 runs`,
      });

      if (!runList.length) {
        setDetail(null);
        return;
      }

      const preserved = runList.find(
        (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
      );
      const target = preserved ?? runList[0];
      await selectRun(dir, target.run.run_id, target.source_id);
    } catch (error) {
      setRuns([]);
      setDetail(null);
      setStatus({ message: `加载失败: ${(error as Error).message}`, summary: "" });
    }
  }

  useEffect(() => {
    if (DEFAULT_CACHE_DIR) loadRuns(DEFAULT_CACHE_DIR);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const current = detail?.replay?.[replayIndex] ?? null;

  return (
    <div className="dark relative h-full overflow-hidden bg-background text-foreground">
      <ReplayGraphCanvas
        detail={detail}
        replayIndex={replayIndex}
        layoutVersion={layoutVersion}
      />

      <div className="pointer-events-none absolute inset-x-0 top-0 z-20 p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="pointer-events-auto w-[390px] rounded-[22px] border border-slate-700/70 bg-slate-950/88 p-3 text-slate-50 shadow-2xl backdrop-blur-xl">
            <div className="flex items-center gap-2">
              <Link to="/">
                <Button variant="ghost" size="icon" className="h-8 w-8 rounded-full text-slate-200 hover:bg-slate-800">
                  <ArrowLeft className="h-4 w-4" />
                </Button>
              </Link>
              <div className="flex min-w-0 flex-1 items-center gap-2">
                <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-slate-50 text-slate-950 shadow-lg">
                  <Database className="h-4 w-4" />
                </div>
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold text-white">Replay Debugger V2</div>
                  <div className="truncate text-xs text-slate-300">React Flow + ELK.js</div>
                </div>
              </div>
              <Link to="/debug/replay">
                <Button
                  variant="outline"
                  size="sm"
                  className="h-8 rounded-full border-slate-600 bg-slate-900 text-slate-50 hover:bg-slate-800"
                >
                  <ArrowRightLeft className="h-3.5 w-3.5" />
                  旧版
                </Button>
              </Link>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 rounded-full border-slate-600 bg-slate-900 text-slate-50 hover:bg-slate-800"
                onClick={() => setLayoutVersion((value) => value + 1)}
              >
                <Rows3 className="h-3.5 w-3.5" />
                自动布局
              </Button>
            </div>

            <form
              className="mt-3 flex items-center gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                loadRuns(inputDir.trim());
              }}
            >
              <Input
                value={inputDir}
                onChange={(event) => setInputDir(event.target.value)}
                placeholder="缓存目录路径..."
                spellCheck={false}
                className="h-9 rounded-full border-slate-700 bg-slate-900/80 font-mono text-xs text-slate-50 placeholder:text-slate-400"
              />
              <Button
                type="submit"
                size="sm"
                variant="outline"
                className="h-9 rounded-full border-slate-600 bg-slate-900 text-slate-50 hover:bg-slate-800"
              >
                <RefreshCw className="h-3.5 w-3.5" />
              </Button>
            </form>

            <div className="mt-2 rounded-xl border border-slate-700/70 bg-slate-900/80 px-3 py-2">
              <div className="truncate text-xs font-medium text-slate-100">{status.message}</div>
              {status.summary ? (
                <div className="truncate pt-0.5 text-[11px] text-slate-400">{status.summary}</div>
              ) : null}
            </div>

            {detail ? (
              <div className="mt-3 space-y-2.5">
                <div className="grid grid-cols-3 gap-2">
                  <Metric label="Graph" value={detail.summary.graph_ref || detail.run.graph_id || "-"} />
                  <Metric label="Source" value={detail.source.name || detail.summary.source_name || "-"} />
                  <Metric label="Duration" value={formatDuration(detail.summary.duration_ms)} />
                </div>

                <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 p-3">
                  <div className="flex items-center justify-between gap-2">
                    <div>
                      <div className="text-xs font-medium text-slate-400">Playback</div>
                      <div className="text-sm font-semibold text-white">
                        {detail.replay.length ? `${replayIndex + 1}/${detail.replay.length}` : "0/0"}
                      </div>
                    </div>
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        size="icon"
                        variant="ghost"
                        className="h-8 w-8 rounded-full"
                        onClick={() => handleReplayIndex(replayIndex - 1)}
                        disabled={replayIndex <= 0}
                      >
                        <SkipBack className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        className="h-8 rounded-full px-3"
                        onClick={toggleReplay}
                        disabled={detail.replay.length === 0}
                      >
                        {isPlaying ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
                      </Button>
                      <Button
                        type="button"
                        size="icon"
                        variant="ghost"
                        className="h-8 w-8 rounded-full"
                        onClick={() => handleReplayIndex(replayIndex + 1)}
                        disabled={replayIndex >= detail.replay.length - 1}
                      >
                        <SkipForward className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>
                  <input
                    type="range"
                    className="mt-3 w-full accent-amber-400"
                    min={0}
                    max={Math.max(0, detail.replay.length - 1)}
                    value={replayIndex}
                    onChange={(event) => handleReplayIndex(Number(event.target.value))}
                  />
                </div>

                <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 p-3">
                  <div className="mb-2 flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold text-white">{current?.title || "暂无事件"}</div>
                      <div className="truncate pt-1 text-xs text-slate-400">
                        {current?.subtitle || current?.event.node_id || current?.event.type || ""}
                      </div>
                    </div>
                    <Badge variant="outline" className="shrink-0 rounded-full border-slate-600 bg-slate-800 text-slate-100">
                      {current?.event.type || detail.run.status}
                    </Badge>
                  </div>
                  <div className="grid grid-cols-2 gap-2 text-[11px] text-slate-400">
                    <div className="rounded-xl bg-slate-800/90 px-2.5 py-2">
                      <div className="uppercase tracking-[0.14em]">Time</div>
                      <div className="mt-1 truncate text-slate-100">{formatTime(current?.timestamp)}</div>
                    </div>
                    <div className="rounded-xl bg-slate-800/90 px-2.5 py-2">
                      <div className="uppercase tracking-[0.14em]">Run Status</div>
                      <div className="mt-1 truncate text-slate-100">{detail.run.status}</div>
                    </div>
                  </div>
                </div>

                <div className="rounded-xl border border-slate-700/70 bg-slate-900/78 p-2">
                  <div className="mb-2 flex items-center justify-between px-1">
                    <div className="text-xs font-medium text-slate-400">Runs</div>
                    <Badge variant="secondary" className="rounded-full bg-slate-800 text-slate-100">
                      {runs.length}
                    </Badge>
                  </div>
                  <div className="max-h-40 space-y-1 overflow-y-auto pr-1">
                    {runs.map((item) => (
                      <button
                        key={`${item.source_id}:${item.run.run_id}`}
                        type="button"
                        onClick={() => selectRun(cacheDir, item.run.run_id, item.source_id)}
                        className={cn(
                          "w-full rounded-2xl border px-3 py-2 text-left transition",
                          item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                            ? "border-amber-400/60 bg-amber-500/16 text-white shadow-lg"
                            : "border-slate-700 bg-slate-950/60 text-slate-100 hover:bg-slate-800/80"
                        )}
                      >
                        <div className="truncate text-sm font-medium">{item.source_name}</div>
                        <div
                          className={cn(
                            "truncate pt-0.5 text-[11px]",
                            item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                              ? "text-amber-100/80"
                              : "text-slate-400"
                          )}
                        >
                          {item.run.run_id}
                        </div>
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ) : (
              <div className="mt-3 rounded-xl border border-dashed border-slate-700 bg-slate-900/60 px-4 py-7 text-center text-sm text-slate-300">
                填写缓存目录后加载运行数据
              </div>
            )}
          </div>

          <div className="pointer-events-auto hidden w-[340px] rounded-[22px] border border-slate-700/70 bg-slate-950/88 p-3 text-slate-50 shadow-2xl backdrop-blur-xl 2xl:block">
            <div>
              <div className="text-sm font-semibold text-white">Event Payload</div>
              <div className="text-xs text-slate-300">当前事件的原始数据</div>
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
