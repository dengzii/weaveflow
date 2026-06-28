import {
  ChevronDown,
  ChevronRight,
  CircleDot,
  Copy,
  FilePlus2,
  ListTree,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Save,
  Search,
  Trash2,
} from "lucide-react";
import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { formatTime, cn } from "../../../lib/utils";
import type { LocalGraphDraft } from "../../../lib/localGraphs";
import type { GraphDefinition, GraphNodeSpec, NodeTypeSchema } from "../../../types";

interface GraphBrowserPanelProps {
  activeDraftId: string;
  creatableNodeTypes: NodeTypeSchema[];
  definition: GraphDefinition | null;
  drafts: LocalGraphDraft[];
  filteredNodes: GraphNodeSpec[];
  filteredNodeTypes: NodeTypeSchema[];
  leftCollapsed: boolean;
  nodeQuery: string;
  nodeTypeQuery: string;
  nodeTypesOpen: boolean;
  selectedNodeId: string | null;
  virtualNodeIds: string[];
  onAddNode: (nodeType: NodeTypeSchema) => void;
  onCollapseChange: (collapsed: boolean) => void;
  onCreateGraph: () => void;
  onDeleteDraft: () => void;
  onDeleteNode: (nodeId: string) => void;
  onDuplicateDraft: () => void;
  onLoadDraft: (draft: LocalGraphDraft) => void;
  onNodeQuery: (value: string) => void;
  onNodeTypeQuery: (value: string) => void;
  onNodeTypesOpen: (open: boolean) => void;
  onSaveLocal: () => void;
  onSelectNode: (nodeId: string) => void;
}

export function GraphBrowserPanel({
  activeDraftId,
  creatableNodeTypes,
  definition,
  drafts,
  filteredNodes,
  filteredNodeTypes,
  leftCollapsed,
  nodeQuery,
  nodeTypeQuery,
  nodeTypesOpen,
  selectedNodeId,
  virtualNodeIds,
  onAddNode,
  onCollapseChange,
  onCreateGraph,
  onDeleteDraft,
  onDeleteNode,
  onDuplicateDraft,
  onLoadDraft,
  onNodeQuery,
  onNodeTypeQuery,
  onNodeTypesOpen,
  onSaveLocal,
  onSelectNode,
}: GraphBrowserPanelProps) {
  return (
    <section className="flex min-h-0 flex-col border-r border-border bg-panel">
      {leftCollapsed ? (
        <div className="flex h-full flex-col items-center gap-2 py-2">
          <Button variant="ghost" size="icon" onClick={() => onCollapseChange(false)} title="Expand left panel">
            <PanelLeftOpen className="h-4 w-4" />
          </Button>
          <button
            className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
            onClick={() => onCollapseChange(false)}
            title="Graphs"
          >
            <ListTree className="h-4 w-4" />
          </button>
          <button
            className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
            onClick={() => onCollapseChange(false)}
            title="Nodes"
          >
            <CircleDot className="h-4 w-4" />
          </button>
        </div>
      ) : (
        <>
          <div className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3">
            <ListTree className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-semibold">Graph Browser</span>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => onCollapseChange(true)}
              title="Collapse left panel"
              className="ml-auto"
            >
              <PanelLeftClose className="h-4 w-4" />
            </Button>
          </div>

          <div className="flex items-center gap-2 border-b border-border p-3">
            <Button variant="outline" size="sm" onClick={onCreateGraph} title="New graph">
              <FilePlus2 className="h-4 w-4" />
              New
            </Button>
            <Button size="sm" onClick={onSaveLocal} disabled={!definition} title="Save local">
              <Save className="h-4 w-4" />
              Save
            </Button>
            <Button variant="ghost" size="icon" onClick={onDuplicateDraft} disabled={!activeDraftId} title="Duplicate">
              <Copy className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" onClick={onDeleteDraft} disabled={!activeDraftId} title="Delete local">
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>

          <div className="max-h-40 overflow-auto border-b border-border">
            {drafts.length === 0 ? (
              <div className="px-3 py-3 text-sm text-muted-foreground">No local graphs</div>
            ) : (
              drafts.map((draft) => (
                <button
                  key={draft.id}
                  className={cn(
                    "grid w-full gap-1 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-accent",
                    draft.id === activeDraftId && "bg-accent"
                  )}
                  onClick={() => onLoadDraft(draft)}
                >
                  <div className="truncate text-sm font-medium">{draft.title}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {draft.definition.nodes.length} nodes / {formatTime(draft.updatedAt)}
                  </div>
                </button>
              ))
            )}
          </div>

          <div className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3">
            <CircleDot className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-semibold">Nodes</span>
            <Badge className="ml-auto">{(definition?.nodes.length ?? 0) + virtualNodeIds.length}</Badge>
          </div>
          <div className="border-b border-border p-3">
            <Input value={nodeQuery} onChange={(event) => onNodeQuery(event.target.value)} placeholder="Search nodes" />
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {filteredNodes.length === 0 ? (
              <div className="px-3 py-3 text-sm text-muted-foreground">No nodes</div>
            ) : (
              filteredNodes.map((node) => (
                <div
                  key={node.id}
                  className={cn(
                    "flex w-full min-w-0 items-center gap-2 border-b border-border px-3 py-2 text-left hover:bg-accent",
                    node.id === selectedNodeId && "bg-accent"
                  )}
                >
                  <button className="min-w-0 flex-1 text-left" onClick={() => onSelectNode(node.id)}>
                    <span className="block truncate text-sm font-medium">{node.name || node.id}</span>
                    <span className="block truncate font-mono text-[11px] text-muted-foreground">{node.id}</span>
                  </button>
                  <Badge className="max-w-28 truncate">{node.type}</Badge>
                  <button
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => onDeleteNode(node.id)}
                    title="Delete node"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              ))
            )}
          </div>

          <button
            className="flex h-11 shrink-0 items-center gap-2 border-t border-border px-3 text-left hover:bg-accent"
            onClick={() => onNodeTypesOpen(!nodeTypesOpen)}
          >
            {nodeTypesOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
            <Plus className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-semibold">Node Types</span>
            <Badge className="ml-auto">{creatableNodeTypes.length}</Badge>
          </button>
          {nodeTypesOpen ? (
            <div className="max-h-80 shrink-0 overflow-auto border-t border-border">
              <div className="border-b border-border p-3">
                <div className="relative">
                  <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                  <Input
                    value={nodeTypeQuery}
                    onChange={(event) => onNodeTypeQuery(event.target.value)}
                    placeholder="Search node types"
                    className="pl-8"
                  />
                </div>
              </div>
              {filteredNodeTypes.length === 0 ? (
                <div className="px-3 py-3 text-sm text-muted-foreground">No node types</div>
              ) : (
                filteredNodeTypes.map((nodeType) => (
                  <button
                    key={nodeType.type}
                    className="grid w-full gap-1 border-b border-border px-3 py-2 text-left hover:bg-accent"
                    onClick={() => onAddNode(nodeType)}
                    title={nodeType.description || nodeType.type}
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-sm font-medium">{nodeType.title || nodeType.type}</span>
                      <Plus className="ml-auto h-3.5 w-3.5 text-muted-foreground" />
                    </div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">{nodeType.type}</div>
                  </button>
                ))
              )}
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}
