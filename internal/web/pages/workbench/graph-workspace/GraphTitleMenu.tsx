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
  onDeleteGraph: (graph: LocalGraph) => void;
  onExportGraph: (graph: LocalGraph) => void;
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
          </div>

          <div className="max-h-80 overflow-auto">
            {graphs.length === 0 ? (
              <div className="px-3 py-3 text-sm text-muted-foreground">No graphs.</div>
            ) : (
              graphs.map((graph) => {
                const graphBadgeCount = graph.definition ? graphScriptBadgeCount(graph.definition) : 0;
                return (
                  <div
                    key={graph.id}
                    className={`group grid grid-cols-[minmax(0,1fr)_auto] border-b border-border last:border-b-0 hover:bg-accent ${
                      graph.id === activeCacheID ? "bg-accent" : ""
                    } ${graphSwitchDisabled ? "cursor-not-allowed opacity-50 hover:bg-transparent" : ""}`}
                  >
                    <button
                      type="button"
                      className="grid min-w-0 gap-1 px-3 py-2 text-left"
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
                    <div className="flex items-center gap-0.5 pr-2 opacity-0 pointer-events-none transition-opacity group-hover:opacity-100 group-hover:pointer-events-auto group-focus-within:opacity-100 group-focus-within:pointer-events-auto">
                      <button
                        type="button"
                        className="flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground"
                        onClick={() => onExportGraph(graph)}
                        title={`Export ${graph.title}`}
                        aria-label={`Export ${graph.title}`}
                      >
                        <Download className="h-3.5 w-3.5" />
                      </button>
                      <button
                        type="button"
                        className="flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:pointer-events-none disabled:opacity-50"
                        onClick={() => onDeleteGraph(graph)}
                        disabled={graphSwitchDisabled}
                        title={`Delete ${graph.title}`}
                        aria-label={`Delete ${graph.title}`}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </div>
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
