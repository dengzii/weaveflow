import {
  Ban,
  Check,
  Circle,
  Clock3,
  Loader2,
  MessageCircle,
  Pause,
  Play,
  Trash2,
  Webhook,
  X,
} from "lucide-react";
import { cn, formatDateTime } from "../../lib/utils";
import type { RunRecord, RunStatus, TriggerType } from "../../types";
import { isActiveRunStatus } from "./workbenchRunModel";

export function RunList({
  runs,
  runTriggerTypes,
  selectedRunID,
  loading = false,
  actionsDisabled = false,
  onSelectRun,
  onDeleteRun,
}: {
  runs: RunRecord[];
  runTriggerTypes?: Partial<Record<string, TriggerType>>;
  selectedRunID?: string;
  loading?: boolean;
  actionsDisabled?: boolean;
  onSelectRun?: (runID: string) => void;
  onDeleteRun?: (runID: string) => void;
}) {
  return (
    <div aria-label="Run list" className="flex min-h-0 min-w-0 flex-col">
      <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
        <span className="text-xs font-semibold">Run</span>
        {loading ? (
          <span className="workbench-status-skeleton ml-auto h-3 w-5 rounded" aria-hidden="true" />
        ) : (
          <span className="ml-auto text-xs text-muted-foreground">{runs.length}</span>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {loading ? (
          <RunListSkeleton />
        ) : runs.length === 0 ? (
          <div className="p-3 text-sm text-muted-foreground">No runs</div>
        ) : (
          <ul className="divide-y divide-border">
            {runs.map((run) => {
              const active = run.run_id === selectedRunID;
              const canDelete = Boolean(onDeleteRun) && !actionsDisabled && !isActiveRunStatus(run.status);
              const triggerType = runTriggerTypes?.[run.run_id];
              return (
                <li
                  key={run.run_id}
                  className={cn("grid grid-cols-[minmax(0,1fr)_2rem]", active && "bg-accent text-accent-foreground")}
                >
                  <button
                    type="button"
                    onClick={() => onSelectRun?.(run.run_id)}
                    aria-pressed={active}
                    className="min-w-0 px-3 py-1.5 text-left text-xs hover:bg-accent/40"
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <RunSourceIcon triggerType={triggerType} />
                      <RunStatusIcon status={run.status} />
                      <span className="min-w-0 flex-1 truncate font-mono" title={run.run_id}>
                        {run.run_id}
                      </span>
                      <span className="shrink-0 tabular-nums text-muted-foreground" title={run.started_at}>
                        {formatDateTime(run.started_at)}
                      </span>
                    </div>
                  </button>
                  {onDeleteRun ? (
                    <button
                      type="button"
                      onClick={() => {
                        if (canDelete) onDeleteRun(run.run_id);
                      }}
                      disabled={!canDelete}
                      title={canDelete
                        ? "Delete run"
                        : actionsDisabled
                          ? "Run operation in progress"
                          : "Stop run before deleting"}
                      aria-label={`Delete run ${run.run_id}`}
                      className="m-1 flex h-7 w-7 items-center justify-center self-center rounded text-destructive hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-35"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  ) : (
                    <span />
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

function RunListSkeleton() {
  return (
    <div className="grid gap-1 p-2" aria-label="Loading runs">
      {["first", "second", "third", "fourth"].map((item) => (
        <div key={item} className="flex h-8 items-center gap-2 rounded-md px-2">
          <div className="workbench-status-skeleton h-5 w-5 rounded-md" />
          <div className="workbench-status-skeleton h-3 flex-1 rounded" />
          <div className="workbench-status-skeleton h-3 w-12 rounded" />
        </div>
      ))}
    </div>
  );
}

function RunSourceIcon({ triggerType }: { triggerType?: TriggerType }) {
  let SourceIcon = Play;
  let label = "Run";
  if (triggerType === "chat") {
    SourceIcon = MessageCircle;
    label = "Chat";
  } else if (triggerType === "webhook") {
    SourceIcon = Webhook;
    label = "Webhook";
  } else if (triggerType === "schedule") {
    SourceIcon = Clock3;
    label = "Schedule";
  }
  return (
    <span
      data-run-source={triggerType ?? "direct"}
      aria-label={label}
      title={label}
      className={cn(
        "flex h-5 w-5 shrink-0 items-center justify-center rounded-md border border-border bg-muted/60 shadow-sm",
        triggerType ? "text-primary" : "text-muted-foreground"
      )}
    >
      <SourceIcon className="h-3.5 w-3.5" />
    </span>
  );
}

function RunStatusIcon({ status }: { status: RunStatus }) {
  let StatusIcon = Circle;
  let iconClassName = "text-muted-foreground";
  switch (status) {
    case "pending":
      StatusIcon = Clock3;
      break;
    case "running":
      StatusIcon = Loader2;
      iconClassName = "animate-spin text-cyan-700 dark:text-cyan-300";
      break;
    case "paused":
      StatusIcon = Pause;
      iconClassName = "text-amber-700 dark:text-amber-300";
      break;
    case "completed":
      StatusIcon = Check;
      iconClassName = "text-emerald-700 dark:text-emerald-300";
      break;
    case "failed":
      StatusIcon = X;
      iconClassName = "text-destructive";
      break;
    case "canceled":
      StatusIcon = Ban;
      iconClassName = "text-destructive";
      break;
  }
  return (
    <span
      data-run-status={status}
      aria-label={status}
      title={status}
      className="flex h-4 w-4 shrink-0 items-center justify-center"
    >
      <StatusIcon className={cn("h-3.5 w-3.5", iconClassName)} />
    </span>
  );
}
