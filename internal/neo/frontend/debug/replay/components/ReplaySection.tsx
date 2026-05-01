import { useEffect, useRef } from "react";
import type { RunDetail, ReplayItem } from "../types";
import { formatTime, prettyJSON, statusDotClass } from "../utils";
import { Button } from "../../../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";
import { cn } from "../../../lib/utils";
import { Play, Pause, SkipBack, SkipForward } from "lucide-react";

function StatusDot({ level }: { level: string }) {
  return (
    <span className={cn("h-2 w-2 rounded-full shrink-0 mt-0.5", statusDotClass(level))} />
  );
}

export function ReplaySection({
  detail,
  replayIndex,
  isPlaying,
  onIndexChange,
  onTogglePlay,
}: {
  detail: RunDetail;
  replayIndex: number;
  isPlaying: boolean;
  onIndexChange: (i: number) => void;
  onTogglePlay: () => void;
}) {
  const replay: ReplayItem[] = detail.replay ?? [];
  const current = replay[replayIndex];
  const activeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [replayIndex]);

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="text-base">事件回放</CardTitle>
          <div className="flex items-center gap-1">
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="h-7 w-7"
              onClick={() => onIndexChange(replayIndex - 1)}
              disabled={replayIndex <= 0}
            >
              <SkipBack className="h-3.5 w-3.5" />
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 gap-1.5 px-2.5"
              onClick={onTogglePlay}
              disabled={replay.length === 0}
            >
              {isPlaying ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
              {isPlaying ? "暂停" : "播放"}
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="h-7 w-7"
              onClick={() => onIndexChange(replayIndex + 1)}
              disabled={replayIndex >= replay.length - 1}
            >
              <SkipForward className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {/* Slider */}
        <div className="flex items-center gap-3">
          <input
            type="range"
            className="flex-1 accent-primary"
            min={0}
            max={Math.max(0, replay.length - 1)}
            value={replayIndex}
            onChange={(e) => onIndexChange(Number(e.target.value))}
          />
          <span className="text-xs text-muted-foreground font-mono w-16 text-right">
            {replay.length ? `${replayIndex + 1}/${replay.length}` : "0/0"}
          </span>
        </div>

        {/* Current event preview */}
        {current ? (
          <div className="flex items-start gap-2.5 p-3 rounded-lg bg-muted/40 border border-border/50">
            <StatusDot level={current.level} />
            <div className="min-w-0">
              <div className="text-sm font-medium">{current.title}</div>
              <div className="text-xs text-muted-foreground truncate">
                {current.subtitle || current.event.node_id || ""}
              </div>
            </div>
          </div>
        ) : (
          <div className="text-sm text-muted-foreground text-center py-2">暂无事件</div>
        )}

        {/* Two-column: event list + JSON */}
        <div className="grid grid-cols-2 gap-3">
          <div className="overflow-y-auto max-h-60 space-y-px rounded-lg border border-border/50">
            {replay.length === 0 ? (
              <div className="text-xs text-muted-foreground text-center py-4">暂无事件</div>
            ) : (
              replay.map((item, idx) => (
                <button
                  key={item.index}
                  ref={idx === replayIndex ? activeRef : undefined}
                  onClick={() => onIndexChange(idx)}
                  className={cn(
                    "w-full text-left flex items-start gap-2 px-3 py-2 text-xs transition-colors",
                    idx === replayIndex
                      ? "bg-accent text-accent-foreground"
                      : "hover:bg-muted/50"
                  )}
                >
                  <StatusDot level={item.level} />
                  <div className="flex-1 min-w-0">
                    <div className="font-medium truncate">{item.title}</div>
                    <div className="text-muted-foreground truncate">
                      {item.subtitle || item.event.type}
                    </div>
                  </div>
                  <div className="text-muted-foreground shrink-0 text-right">
                    {formatTime(item.timestamp)}
                  </div>
                </button>
              ))
            )}
          </div>
          <pre className="text-xs font-mono bg-muted/40 rounded-lg p-3 overflow-auto max-h-60 border border-border/50">
            {current ? prettyJSON(current.event.payload) : ""}
          </pre>
        </div>
      </CardContent>
    </Card>
  );
}
