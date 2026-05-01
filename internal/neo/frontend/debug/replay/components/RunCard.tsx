import type { RunSummary } from "../types";
import { formatTime, statusVariant } from "../utils";
import { Badge } from "../../../components/ui/badge";
import { cn } from "../../../lib/utils";

export function RunCard({
  item,
  isActive,
  onClick,
}: {
  item: RunSummary;
  isActive: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "w-full text-left px-3 py-2.5 rounded-lg transition-colors border text-sm",
        isActive
          ? "bg-sidebar-accent border-sidebar-primary/20 text-sidebar-accent-foreground"
          : "border-transparent hover:bg-sidebar-accent/50 text-sidebar-foreground"
      )}
    >
      <div className="flex items-center justify-between gap-2 mb-1">
        <span className="font-medium truncate">
          {item.graph_ref || item.run.graph_id || item.source_name}
        </span>
        <Badge variant={statusVariant(item.run.status)} className="shrink-0 text-xs">
          {item.run.status}
        </Badge>
      </div>
      <div className="space-y-0.5 text-xs text-muted-foreground">
        <div className="truncate">{item.source_name} · {item.instance_id || item.source_id}</div>
        <div className="font-mono truncate opacity-70">{item.run.run_id}</div>
        <div>
          节点 {item.run.current_node_id || item.run.entry_node_id || "-"} ·{" "}
          步骤 {item.step_count} · 事件 {item.event_count}
        </div>
        <div>{formatTime(item.run.started_at)}</div>
      </div>
    </button>
  );
}
