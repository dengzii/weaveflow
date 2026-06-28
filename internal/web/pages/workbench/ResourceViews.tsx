import { Badge } from "../../components/ui/badge";
import { cn, formatTime, stringifyJSON } from "../../lib/utils";
import type { ArtifactDetail, CheckpointDetail } from "../../types";
import { InfoRows } from "./shared";

export function ResourceDetail({
  checkpoint,
  artifact,
}: {
  checkpoint: CheckpointDetail | null;
  artifact: ArtifactDetail | null;
}) {
  if (artifact) {
    const preview = artifact.text ?? artifact.data_base64 ?? "";
    return (
      <div className="rounded-md border border-border bg-panel p-3">
        <div className="mb-3 flex items-center gap-2">
          <Badge>{artifact.artifact.type || "artifact"}</Badge>
          <span className="truncate text-sm font-medium">{artifact.artifact.id}</span>
        </div>
        <InfoRows
          rows={[
            ["node", artifact.artifact.node_id || ""],
            ["step", artifact.artifact.step_id || ""],
            ["mime", artifact.artifact.mime_type || ""],
            ["size", String(artifact.size)],
          ]}
        />
        <pre className="mt-3 max-h-[420px] overflow-auto rounded bg-muted p-3 text-xs">
          {preview || stringifyJSON({ artifact: artifact.artifact, size: artifact.size })}
        </pre>
      </div>
    );
  }

  if (checkpoint) {
    return (
      <div className="rounded-md border border-border bg-panel p-3">
        <div className="mb-3 flex items-center gap-2">
          <Badge>{checkpoint.record.stage}</Badge>
          <span className="truncate text-sm font-medium">{checkpoint.record.checkpoint_id}</span>
        </div>
        <InfoRows
          rows={[
            ["node", checkpoint.record.node_id],
            ["step", checkpoint.record.step_id],
            ["codec", checkpoint.record.state_codec],
            ["created", formatTime(checkpoint.record.created_at)],
          ]}
        />
        <pre className="mt-3 max-h-[420px] overflow-auto rounded bg-muted p-3 text-xs">
          {stringifyJSON({
            runtime: checkpoint.runtime ?? null,
            business: checkpoint.business ?? null,
            artifacts: checkpoint.artifacts ?? [],
            snapshot: checkpoint.snapshot ?? null,
          })}
        </pre>
      </div>
    );
  }

  return <div className="rounded-md border border-border bg-panel p-3 text-sm text-muted-foreground">Select a checkpoint or artifact</div>;
}

export function ResourceList<T>({
  items,
  empty,
  selectedId,
  onSelect,
}: {
  items: Array<{ id: string; meta: string; source: T }>;
  empty: string;
  selectedId?: string;
  onSelect?: (item: { id: string; meta: string; source: T }) => void;
}) {
  if (items.length === 0) {
    return <div className="text-sm text-muted-foreground">{empty}</div>;
  }
  return (
    <div className="grid gap-2">
      {items.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => onSelect?.(item)}
          className={cn(
            "min-w-0 rounded-md border border-border bg-panel p-3 text-left hover:bg-accent",
            selectedId === item.id && "border-primary bg-accent"
          )}
        >
          <div className="truncate text-sm font-medium">{item.id}</div>
          <div className="mt-1 truncate text-xs text-muted-foreground">{item.meta}</div>
        </button>
      ))}
    </div>
  );
}
