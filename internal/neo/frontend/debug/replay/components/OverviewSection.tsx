import type { RunDetail } from "../types";
import { formatTime, formatDuration, statusVariant } from "../utils";
import { Badge } from "../../../components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";

export function OverviewSection({ detail }: { detail: RunDetail }) {
  const { run, summary, source } = detail;
  const cards: [string, string][] = [
    ["Graph", summary.graph_ref || run.graph_id || "-"],
    ["Source", source.name || summary.source_name || "-"],
    ["Node", run.current_node_id || "-"],
    ["Step", run.last_step_id || "-"],
    ["Started", formatTime(run.started_at)],
    ["Duration", formatDuration(summary.duration_ms)],
  ];

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="text-base">运行概览</CardTitle>
          <Badge variant={statusVariant(run.status)}>{run.status}</Badge>
        </div>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-3 gap-3">
          {cards.map(([label, value]) => (
            <div key={label}>
              <div className="text-xs text-muted-foreground mb-0.5">{label}</div>
              <div className="text-sm font-medium truncate" title={value}>{value}</div>
            </div>
          ))}
        </div>
        {run.error_message && (
          <pre className="mt-3 text-xs p-3 rounded-lg bg-destructive/10 text-destructive border border-destructive/20 overflow-auto whitespace-pre-wrap">
            {run.error_message}
          </pre>
        )}
      </CardContent>
    </Card>
  );
}
