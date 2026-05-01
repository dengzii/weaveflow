import { useState, useEffect, useCallback } from "react";
import type { RunDetail } from "../types";
import { formatTime, prettyJSON } from "../utils";
import { api, buildUrl } from "../api";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";
import { cn } from "../../../lib/utils";

export function CheckpointsPanel({
  detail,
  cacheDir,
}: {
  detail: RunDetail;
  cacheDir: string;
}) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [json, setJson] = useState("");

  const load = useCallback(
    async (id: string) => {
      setSelectedId(id);
      setJson("加载中...");
      try {
        const url = buildUrl(
          `/api/run/${encodeURIComponent(detail.run.run_id)}/checkpoint/${encodeURIComponent(id)}`,
          cacheDir,
          { source: detail.source.id }
        );
        const data = await api<unknown>(url);
        setJson(prettyJSON(data));
      } catch (err) {
        setJson(String((err as Error).message ?? err));
      }
    },
    [detail, cacheDir]
  );

  useEffect(() => {
    const lastId = detail.checkpoints.at(-1)?.record?.checkpoint_id;
    if (lastId) load(lastId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const checkpoints = detail.checkpoints ?? [];

  return (
    <Card className="flex flex-col">
      <CardHeader className="pb-2">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">Checkpoints</CardTitle>
          <span className="text-xs bg-muted text-muted-foreground px-2 py-0.5 rounded-full">
            {checkpoints.length}
          </span>
        </div>
      </CardHeader>
      <CardContent className="p-0 flex flex-col flex-1">
        <div className="overflow-y-auto max-h-40 divide-y divide-border/50">
          {checkpoints.length === 0 ? (
            <div className="text-sm text-muted-foreground text-center py-4">暂无 checkpoint</div>
          ) : (
            checkpoints.map((item) => (
              <button
                key={item.record.checkpoint_id}
                onClick={() => load(item.record.checkpoint_id)}
                className={cn(
                  "w-full text-left flex items-center justify-between px-4 py-2.5 text-xs transition-colors",
                  selectedId === item.record.checkpoint_id
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-muted/50"
                )}
              >
                <div className="min-w-0">
                  <div className="font-medium">{item.record.stage} · {item.record.node_id}</div>
                  <div className="font-mono text-muted-foreground truncate">
                    {item.record.checkpoint_id}
                  </div>
                </div>
                <div className="text-muted-foreground shrink-0 ml-2">
                  {formatTime(item.record.created_at)}
                </div>
              </button>
            ))
          )}
        </div>
        <pre className="text-xs font-mono border-t border-border/50 p-3 overflow-auto max-h-48 bg-muted/30 flex-1">
          {json}
        </pre>
      </CardContent>
    </Card>
  );
}
