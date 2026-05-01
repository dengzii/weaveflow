import { useState, useEffect, useCallback, useRef } from "react";
import type { RunSummary, RunDetail, RunsResponse } from "./types";
import { api, buildUrl } from "./api";
import { RunCard } from "./components/RunCard";
import { OverviewSection } from "./components/OverviewSection";
import { ReplaySection } from "./components/ReplaySection";
import { StepsSection } from "./components/StepsSection";
import { CheckpointsPanel } from "./components/CheckpointsPanel";
import { ArtifactsPanel } from "./components/ArtifactsPanel";
import { SourceSection } from "./components/SourceSection";
import { Link } from "react-router-dom";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { RefreshCw, Database, ArrowLeft } from "lucide-react";

const DEFAULT_CACHE_DIR =
  (document.body.dataset.defaultCacheDir as string | undefined) ?? "";

export function ReplayPage() {
  const [inputDir, setInputDir] = useState(DEFAULT_CACHE_DIR);
  const [cacheDir, setCacheDir] = useState(DEFAULT_CACHE_DIR);
  const [status, setStatus] = useState({ message: "准备加载", summary: "" });
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [selectedSourceId, setSelectedSourceId] = useState("");
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [replayIndex, setReplayIndex] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);

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

  const stopReplay = useCallback(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
    setIsPlaying(false);
  }, []);

  const handleReplayIndex = useCallback(
    (index: number) => {
      stopReplay();
      const len = replayLengthRef.current;
      if (!len) return;
      setReplayIndex(Math.max(0, Math.min(index, len - 1)));
    },
    [stopReplay]
  );

  const toggleReplay = useCallback(() => {
    if (timerRef.current) {
      stopReplay();
      return;
    }
    if (!replayLengthRef.current) return;
    setIsPlaying(true);
    timerRef.current = window.setInterval(() => {
      setReplayIndex((prev) => {
        if (!replayLengthRef.current || prev >= replayLengthRef.current - 1) {
          clearInterval(timerRef.current!);
          timerRef.current = null;
          setIsPlaying(false);
          return prev;
        }
        return prev + 1;
      });
    }, 900);
  }, [stopReplay]);

  const selectRun = useCallback(
    async (dir: string, runId: string, sourceId: string) => {
      stopReplay();
      setSelectedRunId(runId);
      setSelectedSourceId(sourceId);
      setReplayIndex(0);
      setStatus({ message: "加载运行详情中...", summary: "" });
      try {
        const url = buildUrl(
          `/api/run/${encodeURIComponent(runId)}`,
          dir,
          { source: sourceId }
        );
        const runDetail = await api<RunDetail>(url);
        setDetail(runDetail);
        setStatus({
          message: `已加载 run ${runId}`,
          summary: `${runDetail.summary.source_name} · ${runDetail.summary.graph_ref || "-"}`,
        });
      } catch (err) {
        setDetail(null);
        setStatus({ message: `加载失败: ${(err as Error).message}`, summary: "" });
      }
    },
    [stopReplay]
  );

  const loadRuns = useCallback(
    async (dir: string) => {
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
        await selectRun(
          dir,
          (preserved ?? runList[0]).run.run_id,
          (preserved ?? runList[0]).source_id
        );
      } catch (err) {
        setRuns([]);
        setDetail(null);
        setStatus({ message: `加载失败: ${(err as Error).message}`, summary: "" });
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [stopReplay, selectRun]
  );

  useEffect(() => {
    if (DEFAULT_CACHE_DIR) loadRuns(DEFAULT_CACHE_DIR);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <header className="flex items-center gap-4 px-6 h-14 border-b border-border shrink-0">
        <Link to="/">
          <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0">
            <ArrowLeft className="h-4 w-4" />
          </Button>
        </Link>
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Database className="h-4 w-4 text-muted-foreground" />
          Graph 运行回放
        </div>
        <form
          className="flex items-center gap-2 flex-1 max-w-xl"
          onSubmit={(e) => {
            e.preventDefault();
            loadRuns(inputDir.trim());
          }}
        >
          <Input
            value={inputDir}
            onChange={(e) => setInputDir(e.target.value)}
            placeholder="缓存目录路径..."
            spellCheck={false}
            className="h-8 text-xs font-mono"
          />
          <Button type="submit" size="sm" variant="outline" className="h-8 gap-1.5 shrink-0">
            <RefreshCw className="h-3.5 w-3.5" />
            刷新
          </Button>
        </form>
        <div className="text-xs text-muted-foreground ml-auto">
          {status.message}
          {status.summary && (
            <span className="ml-2 text-foreground/60">{status.summary}</span>
          )}
        </div>
      </header>

      {/* Main layout */}
      <div className="flex flex-1 overflow-hidden">
        {/* Run list sidebar */}
        <aside className="w-64 shrink-0 border-r border-border flex flex-col bg-sidebar">
          <div className="flex items-center justify-between px-4 py-2.5 border-b border-sidebar-border">
            <span className="text-xs font-medium text-sidebar-foreground">运行列表</span>
            <span className="text-xs bg-muted text-muted-foreground px-1.5 py-0.5 rounded-full">
              {runs.length}
            </span>
          </div>
          <div className="flex-1 overflow-y-auto p-2 space-y-1">
            {runs.length === 0 ? (
              <div className="text-xs text-muted-foreground text-center py-6">暂无 run 数据</div>
            ) : (
              runs.map((item) => (
                <RunCard
                  key={`${item.source_id}:${item.run.run_id}`}
                  item={item}
                  isActive={
                    item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                  }
                  onClick={() => selectRun(cacheDir, item.run.run_id, item.source_id)}
                />
              ))
            )}
          </div>
        </aside>

        {/* Detail content */}
        <section className="flex-1 overflow-y-auto p-4 space-y-4">
          {!detail ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <Database className="h-8 w-8 text-muted-foreground mx-auto mb-3" />
                <p className="text-sm font-medium">暂无数据</p>
                <p className="text-xs text-muted-foreground mt-1">
                  填写缓存目录后点击刷新
                </p>
              </div>
            </div>
          ) : (
            <>
              <OverviewSection detail={detail} />
              <ReplaySection
                detail={detail}
                replayIndex={replayIndex}
                isPlaying={isPlaying}
                onIndexChange={handleReplayIndex}
                onTogglePlay={toggleReplay}
              />
              <StepsSection detail={detail} />
              <div className="grid grid-cols-2 gap-4">
                <CheckpointsPanel
                  key={`${detail.run.run_id}-cp`}
                  detail={detail}
                  cacheDir={cacheDir}
                />
                <ArtifactsPanel
                  key={`${detail.run.run_id}-art`}
                  detail={detail}
                  cacheDir={cacheDir}
                />
              </div>
              <SourceSection detail={detail} />
            </>
          )}
        </section>
      </div>
    </div>
  );
}
