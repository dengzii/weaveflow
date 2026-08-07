import { ChevronDown, Download, FilePlus2, FileUp, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { formatTime } from "../../../lib/utils";
import type { GraphDefinition } from "../../../types";
import type { LocalGraph } from "../../../lib/localGraphs";
import { graphScriptBadgeCount } from "./graphWorkspaceModel";

export function GraphTitleMenu({
  activeCacheID,
  definition,
  graphs,
  graphID,
  open,
  graphSwitchDisabled,
  onCreateGraph,
  onDeleteGraph,
  onExportGraph,
  onImportGraph,
  onLoadGraph,
  onOpenChange,
}: {
  activeCacheID: string;
  definition: GraphDefinition | null;
  graphs: LocalGraph[];
  graphID: string;
  open: boolean;
  graphSwitchDisabled: boolean;
  onCreateGraph: () => void;
  onDeleteGraph: () => void;
  onExportGraph: () => void;
  onImportGraph: () => void;
  onLoadGraph: (graph: LocalGraph) => void;
  onOpenChange: (open: boolean) => void;
}) {
  const title = definition?.name || graphID || "Untitled graph";
  const scriptBadgeCount = graphScriptBadgeCount(definition);

  return (
    <div data-graph-title-menu className="relative min-w-0">
      <button
        type="button"
        className="flex max-w-[360px] min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left hover:bg-accent"
        onClick={() => onOpenChange(!open)}
        aria-expanded={open}
        title={scriptBadgeCount > 0 ? `${title} (${scriptBadgeCount} scripts)` : title}
      >
        <span className="min-w-0 truncate text-sm font-semibold">{title}</span>
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
              className="h-8 w-8"
              onClick={onImportGraph}
              disabled={graphSwitchDisabled}
              title="Import graph"
              aria-label="Import graph"
            >
              <FileUp className="h-4 w-4" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              onClick={onExportGraph}
              disabled={!definition}
              title="Export graph"
              aria-label="Export graph"
            >
              <Download className="h-4 w-4" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={onDeleteGraph}
              disabled={!activeCacheID}
              title="Delete graph"
              aria-label="Delete graph"
              className="ml-auto"
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>

          <div className="max-h-80 overflow-auto">
            {graphs.length === 0 ? (
              <div className="px-3 py-3 text-sm text-muted-foreground">No graphs.</div>
            ) : (
              graphs.map((graph) => {
                const graphBadgeCount = graph.definition ? graphScriptBadgeCount(graph.definition) : 0;
                return (
                  <button
                    key={graph.id}
                    type="button"
                    className={`grid w-full gap-1 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-accent ${
                      graph.id === activeCacheID ? "bg-accent" : ""
                    } ${graphSwitchDisabled ? "cursor-not-allowed opacity-50 hover:bg-transparent" : ""}`}
                    onClick={() => onLoadGraph(graph)}
                    disabled={graphSwitchDisabled}
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-sm font-medium">{graph.title}</span>
                      <ScriptCountBadge count={graphBadgeCount} />
                    </div>
                    <div className="truncate text-xs text-muted-foreground">
                      {graph.nodeCount} nodes / {formatTime(graph.updatedAt)}
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
