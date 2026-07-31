import { ChevronDown, FilePlus2, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { formatTime } from "../../../lib/utils";
import type { GraphDefinition } from "../../../types";
import type { LocalGraphDraft } from "../../../lib/localGraphs";
import { graphScriptBadgeCount } from "./graphWorkspaceModel";

export function GraphTitleMenu({
  activeDraftID,
  definition,
  drafts,
  graphID,
  open,
  graphSwitchDisabled,
  unsaved,
  onCreateGraph,
  onDeleteGraph,
  onLoadDraft,
  onOpenChange,
}: {
  activeDraftID: string;
  definition: GraphDefinition | null;
  drafts: LocalGraphDraft[];
  graphID: string;
  open: boolean;
  graphSwitchDisabled: boolean;
  unsaved: boolean;
  onCreateGraph: () => void;
  onDeleteGraph: () => void;
  onLoadDraft: (draft: LocalGraphDraft) => void;
  onOpenChange: (open: boolean) => void;
}) {
  const title = definition?.name || graphID || "Untitled graph";
  const displayTitle = unsaved ? `*${title}` : title;
  const scriptBadgeCount = graphScriptBadgeCount(definition);

  return (
    <div data-graph-title-menu className="relative min-w-0">
      <button
        type="button"
        className="flex max-w-[360px] min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left hover:bg-accent"
        onClick={() => onOpenChange(!open)}
        aria-expanded={open}
        title={scriptBadgeCount > 0 ? `${displayTitle} (${scriptBadgeCount} scripts)` : displayTitle}
      >
        <span className="min-w-0 truncate text-sm font-semibold">{displayTitle}</span>
        <ScriptCountBadge count={scriptBadgeCount} />
        <ChevronDown className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`} />
      </button>

      {open ? (
        <div className="absolute left-0 top-12 z-50 w-80 overflow-hidden rounded-md border border-border bg-panel shadow-lg">
          <div className="flex items-center gap-2 border-b border-border p-2">
            <Button type="button" variant="outline" size="sm" onClick={onCreateGraph} disabled={graphSwitchDisabled}>
              <FilePlus2 className="h-4 w-4" />
              New graph
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={onDeleteGraph}
              disabled={!activeDraftID}
              title="Delete graph"
              aria-label="Delete graph"
              className="ml-auto"
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>

          <div className="max-h-80 overflow-auto">
            {drafts.length === 0 ? (
              <div className="px-3 py-3 text-sm text-muted-foreground">No local graphs.</div>
            ) : (
              drafts.map((draft) => {
                const draftScriptBadgeCount = graphScriptBadgeCount(draft.definition);
                return (
                  <button
                    key={draft.id}
                    type="button"
                    className={`grid w-full gap-1 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-accent ${
                      draft.id === activeDraftID ? "bg-accent" : ""
                    } ${graphSwitchDisabled ? "cursor-not-allowed opacity-50 hover:bg-transparent" : ""}`}
                    onClick={() => onLoadDraft(draft)}
                    disabled={graphSwitchDisabled}
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-sm font-medium">{draft.title}</span>
                      <ScriptCountBadge count={draftScriptBadgeCount} />
                    </div>
                    <div className="truncate text-xs text-muted-foreground">
                      {draft.definition.nodes.length} nodes / {formatTime(draft.updatedAt)}
                    </div>
                  </button>
                );
              })
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function ScriptCountBadge({ count }: { count: number }) {
  if (count <= 0) return null;
  const label = count > 99 ? "99+" : String(count);
  return (
    <span
      className="inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold leading-none text-destructive-foreground"
      title={`${count} pre/post scripts`}
    >
      {label}
    </span>
  );
}
