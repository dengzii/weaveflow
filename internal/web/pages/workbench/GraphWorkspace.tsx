import { useEffect, useMemo, useState } from "react";
import {
  Braces,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Copy,
  FileJson,
  FilePlus2,
  ListTree,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Save,
  Search,
  Trash2,
} from "lucide-react";
import { GraphCanvas, type VirtualGraphEdge } from "../../components/GraphCanvas";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import { Textarea } from "../../components/ui/textarea";
import {
  addGraphEdge,
  addNodeToGraph,
  createGraphDefinition,
  END_NODE_REF,
  START_NODE_REF,
  graphEdgeId,
  removeGraphEdge,
  removeGraphNode,
  renameGraphNode,
  updateGraphEdge,
  updateGraphNode,
  withNodePosition,
  type NodePosition,
} from "../../lib/graphEditor";
import {
  deleteLocalGraphDraft,
  duplicateLocalGraphDraft,
  readLocalGraphDrafts,
  saveLocalGraphDraft,
  type LocalGraphDraft,
} from "../../lib/localGraphs";
import { cn, formatTime, parseJSON, stringifyJSON } from "../../lib/utils";
import type {
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  InitialStateRequirements,
  NodeTypeSchema,
  RegistryInfo,
  RunRecord,
  RuntimeEvent,
  StepRecord,
} from "../../types";

interface GraphWorkspaceProps {
  definition: GraphDefinition | null;
  definitionText: string;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
  selectedRun: RunRecord | null;
  steps: StepRecord[];
  events: RuntimeEvent[];
  registry: RegistryInfo | null;
  graphId: string;
  graphVersion: string;
  onGraphId: (value: string) => void;
  onGraphVersion: (value: string) => void;
  onDefinitionText: (value: string) => void;
  onInitialStateText: (value: string) => void;
  onFormatDefinition: () => void;
}

type CanvasContextMenu =
  | { kind: "pane"; screen: NodePosition; position: NodePosition }
  | { kind: "node"; screen: NodePosition; nodeId: string }
  | { kind: "edge"; screen: NodePosition; edgeId: string };

type VirtualNodeKind = "start" | "end";

const defaultVirtualNodeIds = [START_NODE_REF, END_NODE_REF];
const virtualNodeTypes: NodeTypeSchema[] = [
  { type: "start", title: "Start", description: "Graph entry" },
  { type: "end", title: "End", description: "Graph finish" },
];
const fallbackNodeTypes: NodeTypeSchema[] = [{ type: "node", title: "Node" }];

export function GraphWorkspace({
  definition,
  definitionText,
  initialStateText,
  initialRequirements,
  selectedRun,
  steps,
  events,
  registry,
  graphId,
  graphVersion,
  onGraphId,
  onGraphVersion,
  onDefinitionText,
  onInitialStateText,
  onFormatDefinition,
}: GraphWorkspaceProps) {
  const [drafts, setDrafts] = useState<LocalGraphDraft[]>([]);
  const [activeDraftId, setActiveDraftId] = useState("");
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null);
  const [nodeTypeQuery, setNodeTypeQuery] = useState("");
  const [nodeQuery, setNodeQuery] = useState("");
  const [nodeConfigText, setNodeConfigText] = useState("{}");
  const [edgeConfigText, setEdgeConfigText] = useState("{}");
  const [edgeSource, setEdgeSource] = useState("");
  const [edgeTarget, setEdgeTarget] = useState("");
  const [edgeConditionType, setEdgeConditionType] = useState("");
  const [localStatus, setLocalStatus] = useState("local ready");
  const [leftCollapsed, setLeftCollapsed] = useState(false);
  const [nodeTypesOpen, setNodeTypesOpen] = useState(false);
  const [contextMenu, setContextMenu] = useState<CanvasContextMenu | null>(null);
  const [virtualNodeIds, setVirtualNodeIds] = useState<string[]>(defaultVirtualNodeIds);
  const [virtualEdges, setVirtualEdges] = useState<VirtualGraphEdge[]>([]);

  useEffect(() => {
    setDrafts(readLocalGraphDrafts());
  }, []);

  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    window.addEventListener("click", close);
    window.addEventListener("keydown", handleKey);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("keydown", handleKey);
    };
  }, [contextMenu]);

  const nodeTypes = registry?.node_types;
  const paletteNodeTypes = useMemo(
    () => realNodeTypes(nodeTypes?.length ? nodeTypes : fallbackNodeTypes),
    [nodeTypes]
  );
  const creatableNodeTypes = useMemo(() => [...virtualNodeTypes, ...paletteNodeTypes], [paletteNodeTypes]);
  const defaultGraphNodeType = paletteNodeTypes[0];
  const conditions = registry?.conditions ?? [];
  const selectedNode = useMemo(
    () => definition?.nodes.find((node) => node.id === selectedNodeId) ?? null,
    [definition, selectedNodeId]
  );
  const visibleVirtualNodes = useMemo(() => virtualNodeIds.map(virtualNodeSpec), [virtualNodeIds]);
  const selectedVirtualNode = useMemo(
    () => visibleVirtualNodes.find((node) => node.id === selectedNodeId) ?? null,
    [selectedNodeId, visibleVirtualNodes]
  );
  const selectedEdge = useMemo(() => {
    if (!definition || !selectedEdgeId) return null;
    return (
      (definition.edges ?? [])
        .map((edge, index) => ({ edge, id: graphEdgeId(edge, index) }))
        .find((item) => item.id === selectedEdgeId)?.edge ?? null
    );
  }, [definition, selectedEdgeId]);
  const selectedVirtualEdge = useMemo(
    () => virtualEdges.find((edge) => edge.id === selectedEdgeId) ?? null,
    [selectedEdgeId, virtualEdges]
  );
  const inspectorMode = selectedEdge || selectedVirtualEdge ? "edge" : selectedVirtualNode ? "virtual" : selectedNode ? "node" : "graph";

  const graphProblem = useMemo(() => validateGraph(definition), [definition]);
  const filteredNodeTypes = useMemo(() => {
    const query = nodeTypeQuery.trim().toLowerCase();
    if (!query) return creatableNodeTypes;
    return creatableNodeTypes.filter((nodeType) =>
      `${nodeType.title ?? ""} ${nodeType.type} ${nodeType.description ?? ""}`.toLowerCase().includes(query)
    );
  }, [creatableNodeTypes, nodeTypeQuery]);
  const filteredNodes = useMemo(() => {
    const query = nodeQuery.trim().toLowerCase();
    const nodes = [...visibleVirtualNodes, ...(definition?.nodes ?? [])];
    if (!query) return nodes;
    return nodes.filter((node) => `${node.id} ${node.name ?? ""} ${node.type ?? ""}`.toLowerCase().includes(query));
  }, [definition, nodeQuery, visibleVirtualNodes]);

  useEffect(() => {
    if (!selectedNode) {
      setNodeConfigText("{}");
      return;
    }
    setNodeConfigText(stringifyJSON(selectedNode.config ?? {}));
  }, [selectedNode]);

  useEffect(() => {
    if (!selectedEdge?.condition) {
      setEdgeConfigText("{}");
      return;
    }
    setEdgeConfigText(stringifyJSON(selectedEdge.condition.config ?? {}));
  }, [selectedEdge]);

  useEffect(() => {
    const nodes = definition?.nodes ?? [];
    if (nodes.length === 0) {
      setEdgeSource("");
      setEdgeTarget("");
      return;
    }
    setEdgeSource((current) =>
      current && nodes.some((node) => node.id === current)
        ? current
        : selectedNodeId && nodes.some((node) => node.id === selectedNodeId)
          ? selectedNodeId
          : nodes[0].id
    );
    setEdgeTarget((current) =>
      current && nodes.some((node) => node.id === current)
        ? current
        : nodes.find((node) => node.id !== selectedNodeId)?.id ?? nodes[0]?.id ?? ""
    );
  }, [definition, selectedNodeId]);

  function setDefinition(next: GraphDefinition) {
    onDefinitionText(stringifyJSON(next));
  }

  function updateDefinition(update: (current: GraphDefinition) => GraphDefinition) {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    setDefinition(update(definition));
  }

  function createGraph() {
    const nextName = `debug_graph_${Date.now().toString(36)}`;
    const next = createGraphDefinition(nextName, defaultGraphNodeType);
    onGraphId(next.name || nextName);
    onGraphVersion(next.version || "1.0");
    onDefinitionText(stringifyJSON(next));
    setActiveDraftId("");
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setVirtualEdges([]);
    setLocalStatus("new graph");
  }

  function saveLocal() {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    const draft = saveLocalGraphDraft({
      id: activeDraftId || undefined,
      title: definition.name || graphId,
      graphId,
      graphVersion,
      definition,
    });
    setActiveDraftId(draft.id);
    setDrafts(readLocalGraphDrafts());
    setLocalStatus(`saved ${formatTime(draft.updatedAt)}`);
  }

  function loadDraft(draft: LocalGraphDraft) {
    setActiveDraftId(draft.id);
    onGraphId(draft.graphId);
    onGraphVersion(draft.graphVersion);
    onDefinitionText(stringifyJSON(draft.definition));
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setVirtualEdges([]);
    setLocalStatus(`loaded ${draft.title}`);
  }

  function duplicateDraft() {
    if (!activeDraftId) return;
    const draft = duplicateLocalGraphDraft(activeDraftId);
    if (!draft) return;
    setDrafts(readLocalGraphDrafts());
    loadDraft(draft);
  }

  function deleteDraft() {
    if (!activeDraftId) return;
    const nextDrafts = deleteLocalGraphDraft(activeDraftId);
    setDrafts(nextDrafts);
    setActiveDraftId("");
    setLocalStatus("draft deleted");
  }

  function addNode(nodeType: NodeTypeSchema, position?: NodePosition) {
    if (isVirtualNodeType(nodeType.type)) {
      addVirtualNode(nodeType.type, position);
      return;
    }
    if (!definition) {
      let next = createGraphDefinition(graphId || "debug_graph", nodeType);
      const node = next.nodes[0];
      if (position && node) next = withNodePosition(next, node.id, position);
      onDefinitionText(stringifyJSON(next));
      setSelectedNodeId(next.nodes[0]?.id ?? null);
      setSelectedEdgeId(null);
      setContextMenu(null);
      return;
    }
    const next = addNodeToGraph(definition, nodeType, position);
    const node = next.nodes.at(-1);
    setDefinition(next);
    setSelectedNodeId(node?.id ?? null);
    setSelectedEdgeId(null);
    setContextMenu(null);
    setLocalStatus(position ? "node created" : "node added");
  }

  function addVirtualNode(kind: VirtualNodeKind, position?: NodePosition) {
    const nodeID = nextVirtualNodeId(kind, virtualNodeIds);
    setVirtualNodeIds((current) => (current.includes(nodeID) ? current : [...current, nodeID]));
    if (definition && position) {
      setDefinition(withNodePosition(definition, nodeID, position));
    }
    setSelectedNodeId(nodeID);
    setSelectedEdgeId(null);
    setContextMenu(null);
    setLocalStatus(`${virtualNodeLabel(nodeID)} ready`);
  }

  function changeGraphField<Key extends keyof GraphDefinition>(key: Key, value: GraphDefinition[Key]) {
    updateDefinition((current) => ({ ...current, [key]: value }));
  }

  function changeSelectedNode(update: (node: GraphNodeSpec) => GraphNodeSpec) {
    if (!selectedNode) return;
    updateDefinition((current) => updateGraphNode(current, selectedNode.id, update));
  }

  function changeSelectedNodeId(value: string) {
    if (!definition || !selectedNode) return;
    const nextID = value.trim();
    if (!nextID) {
      setLocalStatus("node id required");
      return;
    }
    if (definition.nodes.some((node) => node.id === nextID && node.id !== selectedNode.id)) {
      setLocalStatus("node id already exists");
      return;
    }
    setDefinition(renameGraphNode(definition, selectedNode.id, nextID));
    setSelectedNodeId(nextID);
  }

  function applyNodeConfig() {
    if (!selectedNode) return;
    try {
      const config = parseJSONObject(nodeConfigText);
      changeSelectedNode((node) => ({ ...node, config }));
      setLocalStatus("node config applied");
    } catch (err) {
      setLocalStatus(err instanceof Error ? err.message : String(err));
    }
  }

  function deleteSelectedNode(nodeID = selectedNodeId) {
    if (!nodeID) return;
    if (isVirtualNodeId(nodeID)) {
      removeVirtualEdgesForNode(nodeID);
      setVirtualNodeIds((current) => current.filter((id) => id !== nodeID));
      setSelectedNodeId(null);
      setSelectedEdgeId(null);
      setContextMenu(null);
      setLocalStatus(`${virtualNodeLabel(nodeID)} hidden`);
      return;
    }
    removeVirtualEdgesForNode(nodeID);
    updateDefinition((current) => removeGraphNode(current, nodeID));
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setContextMenu(null);
    setLocalStatus("node deleted");
  }

  function addEdge() {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    const next = addGraphEdge(definition, edgeSource, edgeTarget, edgeConditionType);
    const edgeId = findLastEdgeId(next, edgeSource, edgeTarget);
    setDefinition(next);
    setSelectedNodeId(null);
    setSelectedEdgeId(edgeId);
    setLocalStatus(next === definition ? "invalid edge" : "edge added");
  }

  function changeSelectedEdge(update: (edge: GraphEdgeSpec) => GraphEdgeSpec) {
    if (!definition || !selectedEdgeId) return;
    const previousIndex = (definition.edges ?? []).findIndex((edge, index) => graphEdgeId(edge, index) === selectedEdgeId);
    const next = updateGraphEdge(definition, selectedEdgeId, update);
    const nextEdge = previousIndex >= 0 ? next.edges?.[previousIndex] : undefined;
    setDefinition(next);
    setSelectedEdgeId(nextEdge ? graphEdgeId(nextEdge, previousIndex) : null);
  }

  function applyEdgeConfig() {
    if (!selectedEdge?.condition) return;
    try {
      const config = parseJSONObject(edgeConfigText);
      changeSelectedEdge((edge) => ({
        ...edge,
        condition: edge.condition ? { ...edge.condition, config } : edge.condition,
      }));
      setLocalStatus("edge config applied");
    } catch (err) {
      setLocalStatus(err instanceof Error ? err.message : String(err));
    }
  }

  function deleteSelectedEdge(edgeId = selectedEdgeId) {
    if (!edgeId) return;
    if (virtualEdges.some((edge) => edge.id === edgeId)) {
      deleteVirtualEdge(edgeId);
      return;
    }
    updateDefinition((current) => removeGraphEdge(current, edgeId));
    setSelectedEdgeId(null);
    setContextMenu(null);
    setLocalStatus("edge deleted");
  }

  function addVirtualEdge(edge: Omit<VirtualGraphEdge, "id">): string {
    const nextEdge = { ...edge, id: virtualEdgeId(edge.from, edge.to, edge.kind) };
    setVirtualEdges((current) => {
      const next = current.filter((item) =>
        edge.kind === "entry" ? item.from !== edge.from : item.to !== edge.to
      );
      return [...next, nextEdge];
    });
    setSelectedEdgeId(nextEdge.id);
    return nextEdge.id;
  }

  function deleteVirtualEdge(edgeId: string) {
    const edge = virtualEdges.find((item) => item.id === edgeId);
    const remainingEdges = virtualEdges.filter((item) => item.id !== edgeId);
    setVirtualEdges(remainingEdges);
    if (edge && definition) {
      if (edge.kind === "entry" && definition.entry_point === edge.to) {
        setDefinition({ ...definition, entry_point: lastVirtualEdge(remainingEdges, "entry")?.to });
      }
      if (edge.kind === "finish" && definition.finish_point === edge.from) {
        setDefinition({ ...definition, finish_point: lastVirtualEdge(remainingEdges, "finish")?.from });
      }
    }
    setSelectedEdgeId(null);
    setContextMenu(null);
    setLocalStatus("edge deleted");
  }

  function removeVirtualEdgesForNode(nodeID: string) {
    const removedEdges = virtualEdges.filter((edge) => edge.from === nodeID || edge.to === nodeID);
    if (removedEdges.length === 0) return;
    const remainingEdges = virtualEdges.filter((edge) => edge.from !== nodeID && edge.to !== nodeID);
    setVirtualEdges(remainingEdges);
    if (definition) {
      let next = definition;
      if (removedEdges.some((edge) => edge.kind === "entry" && definition.entry_point === edge.to)) {
        next = { ...next, entry_point: lastVirtualEdge(remainingEdges, "entry")?.to };
      }
      if (removedEdges.some((edge) => edge.kind === "finish" && definition.finish_point === edge.from)) {
        next = { ...next, finish_point: lastVirtualEdge(remainingEdges, "finish")?.from };
      }
      if (next !== definition) setDefinition(next);
    }
  }

  function connectNodes(source: string, target: string) {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    const sourceKind = virtualNodeKind(source);
    const targetKind = virtualNodeKind(target);
    if (sourceKind === "end" || targetKind === "start" || (sourceKind === "start" && targetKind === "end")) {
      setLocalStatus("invalid virtual edge");
      return;
    }
    if (sourceKind === "start") {
      const edgeId = addVirtualEdge({ from: source, to: target, kind: "entry" });
      changeGraphField("entry_point", target);
      setSelectedNodeId(null);
      setSelectedEdgeId(edgeId);
      setLocalStatus("entry updated");
      return;
    }
    if (targetKind === "end") {
      const edgeId = addVirtualEdge({ from: source, to: target, kind: "finish" });
      changeGraphField("finish_point", source);
      setSelectedNodeId(null);
      setSelectedEdgeId(edgeId);
      setLocalStatus("finish updated");
      return;
    }
    const next = addGraphEdge(definition, source, target);
    const edgeId = findLastEdgeId(next, source, target);
    setDefinition(next);
    setSelectedNodeId(null);
    setSelectedEdgeId(edgeId);
    setLocalStatus("edge connected");
  }

  function openCreateMenu(position: NodePosition, screen: NodePosition) {
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setContextMenu({ kind: "pane", position, screen });
  }

  function openNodeMenu(nodeId: string, screen: NodePosition) {
    setContextMenu({ kind: "node", nodeId, screen });
  }

  function openEdgeMenu(edgeId: string, screen: NodePosition) {
    setContextMenu({ kind: "edge", edgeId, screen });
  }

  function moveNode(nodeID: string, position: NodePosition) {
    updateDefinition((current) => withNodePosition(current, nodeID, position));
  }

  const leftWidth = leftCollapsed ? "48px" : "320px";
  const inspectorTitle =
    inspectorMode === "edge"
      ? "Edge Properties"
      : inspectorMode === "virtual"
        ? selectedVirtualNode?.name ?? "Virtual Node"
        : inspectorMode === "node"
          ? selectedNode?.name || selectedNode?.id || "Node Properties"
          : "Graph Properties";

  return (
    <div
      className="relative grid h-full min-h-0"
      style={{ gridTemplateColumns: `${leftWidth} minmax(0,1fr) 380px` }}
    >
      <section className="flex min-h-0 flex-col border-r border-border bg-panel">
        {leftCollapsed ? (
          <div className="flex h-full flex-col items-center gap-2 py-2">
            <Button variant="ghost" size="icon" onClick={() => setLeftCollapsed(false)} title="Expand left panel">
              <PanelLeftOpen className="h-4 w-4" />
            </Button>
            <button
              className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
              onClick={() => setLeftCollapsed(false)}
              title="Graphs"
            >
              <ListTree className="h-4 w-4" />
            </button>
            <button
              className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
              onClick={() => setLeftCollapsed(false)}
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
                onClick={() => setLeftCollapsed(true)}
                title="Collapse left panel"
                className="ml-auto"
              >
                <PanelLeftClose className="h-4 w-4" />
              </Button>
            </div>

            <div className="flex items-center gap-2 border-b border-border p-3">
              <Button variant="outline" size="sm" onClick={createGraph} title="New graph">
                <FilePlus2 className="h-4 w-4" />
                New
              </Button>
              <Button size="sm" onClick={saveLocal} disabled={!definition} title="Save local">
                <Save className="h-4 w-4" />
                Save
              </Button>
              <Button variant="ghost" size="icon" onClick={duplicateDraft} disabled={!activeDraftId} title="Duplicate">
                <Copy className="h-4 w-4" />
              </Button>
              <Button variant="ghost" size="icon" onClick={deleteDraft} disabled={!activeDraftId} title="Delete local">
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
                    onClick={() => loadDraft(draft)}
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
              <Input value={nodeQuery} onChange={(event) => setNodeQuery(event.target.value)} placeholder="Search nodes" />
            </div>
            <div className="min-h-0 flex-1 overflow-auto">
              {filteredNodes.length === 0 ? (
                <div className="px-3 py-3 text-sm text-muted-foreground">No nodes</div>
              ) : (
                filteredNodes.map((node) => (
                  <button
                    key={node.id}
                    className={cn(
                      "flex w-full min-w-0 items-center gap-2 border-b border-border px-3 py-2 text-left hover:bg-accent",
                      node.id === selectedNodeId && "bg-accent"
                    )}
                    onClick={() => {
                      setSelectedNodeId(node.id);
                      setSelectedEdgeId(null);
                    }}
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{node.name || node.id}</span>
                      <span className="block truncate font-mono text-[11px] text-muted-foreground">{node.id}</span>
                    </span>
                    <Badge className="max-w-28 truncate">{node.type}</Badge>
                    <button
                      className="text-muted-foreground hover:text-destructive"
                      onClick={(event) => {
                        event.stopPropagation();
                        deleteSelectedNode(node.id);
                      }}
                      title="Delete node"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </button>
                ))
              )}
            </div>

            <button
              className="flex h-11 shrink-0 items-center gap-2 border-t border-border px-3 text-left hover:bg-accent"
              onClick={() => setNodeTypesOpen((open) => !open)}
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
                      onChange={(event) => setNodeTypeQuery(event.target.value)}
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
                      onClick={() => addNode(nodeType)}
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

      <section className="relative min-h-0 bg-canvas">
        <GraphCanvas
          definition={definition}
          steps={steps}
          events={events}
          editable
          selectedNodeId={selectedNodeId ?? undefined}
          selectedEdgeId={selectedEdgeId ?? undefined}
          onSelectNode={setSelectedNodeId}
          onSelectEdge={setSelectedEdgeId}
          onNodePositionChange={moveNode}
          onConnectNodes={connectNodes}
          onCreateNodeAt={openCreateMenu}
            onNodeContextMenu={openNodeMenu}
            onEdgeContextMenu={openEdgeMenu}
            virtualNodeIds={virtualNodeIds}
            virtualEdges={virtualEdges}
          />
      </section>

      <section className="min-h-0 overflow-auto border-l border-border bg-panel">
        <PanelHeader icon={FileJson} title={inspectorTitle} />
        {inspectorMode === "graph" ? (
          <>
            <InspectorBlock title="Basic">
              <div className="grid grid-cols-2 gap-2">
                <Field label="Graph ID">
                  <Input value={graphId} onChange={(event) => onGraphId(event.target.value)} />
                </Field>
                <Field label="Version">
                  <Input value={graphVersion} onChange={(event) => onGraphVersion(event.target.value)} />
                </Field>
              </div>
              <Field label="Name">
                <Input
                  value={definition?.name ?? ""}
                  onChange={(event) => changeGraphField("name", event.target.value)}
                  disabled={!definition}
                />
              </Field>
              <Field label="Description">
                <Textarea
                  value={definition?.description ?? ""}
                  onChange={(event) => changeGraphField("description", event.target.value)}
                  disabled={!definition}
                  className="h-20 text-xs"
                />
              </Field>
              <div className="grid grid-cols-2 gap-2">
                <Field label="Entry">
                  <NodeSelect
                    value={definition?.entry_point ?? ""}
                    nodes={definition?.nodes ?? []}
                    onChange={(value) => changeGraphField("entry_point", value)}
                    disabled={!definition}
                  />
                </Field>
                <Field label="Finish">
                  <NodeSelect
                    value={definition?.finish_point ?? ""}
                    nodes={definition?.nodes ?? []}
                    onChange={(value) => changeGraphField("finish_point", value)}
                    disabled={!definition}
                  />
                </Field>
              </div>
              <InfoRows
                rows={[
                  ["nodes", String(definition?.nodes.length ?? 0)],
                  ["edges", String(definition?.edges?.length ?? 0)],
                  ["status", graphProblem || "valid"],
                  ["local", localStatus],
                ]}
              />
            </InspectorBlock>

            <InspectorBlock title="New Edge">
              <div className="grid grid-cols-2 gap-2">
                <Field label="From">
                  <NodeSelect value={edgeSource} nodes={definition?.nodes ?? []} onChange={setEdgeSource} />
                </Field>
                <Field label="To">
                  <NodeSelect value={edgeTarget} nodes={definition?.nodes ?? []} onChange={setEdgeTarget} />
                </Field>
              </div>
              <Field label="Condition">
                <Select value={edgeConditionType} onChange={(event) => setEdgeConditionType(event.target.value)}>
                  <option value="">direct</option>
                  {conditions.map((condition) => (
                    <option key={condition.type} value={condition.type}>
                      {condition.title || condition.type}
                    </option>
                  ))}
                </Select>
              </Field>
              <Button variant="outline" size="sm" onClick={addEdge} disabled={!definition || !edgeSource || !edgeTarget}>
                <Plus className="h-4 w-4" />
                Add Edge
              </Button>
            </InspectorBlock>

            <InspectorBlock title="Run Input">
              <div className="mb-3 flex items-center gap-2">
                <Badge tone={selectedRun ? statusTone(selectedRun.status) : "neutral"}>
                  {selectedRun?.status ?? "idle"}
                </Badge>
                <span className="truncate text-xs text-muted-foreground">{selectedRun?.run_id ?? "no run"}</span>
              </div>
              <InitialStateRequirementList requirements={initialRequirements} />
              <Textarea
                value={initialStateText}
                onChange={(event) => onInitialStateText(event.target.value)}
                spellCheck={false}
                className="h-32 text-xs"
              />
            </InspectorBlock>

            <InspectorBlock
              title="Graph JSON"
              action={
                <Button variant="outline" size="sm" onClick={onFormatDefinition} title="Format JSON">
                  <Braces className="h-4 w-4" />
                  Format
                </Button>
              }
            >
              <Textarea
                value={definitionText}
                onChange={(event) => onDefinitionText(event.target.value)}
                spellCheck={false}
                className="h-72 text-xs"
              />
            </InspectorBlock>
          </>
        ) : null}

        {inspectorMode === "node" && selectedNode ? (
          <>
            <InspectorBlock title="Node Properties">
              <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
                <Field label="ID">
                  <Input value={selectedNode.id} onChange={(event) => changeSelectedNodeId(event.target.value)} />
                </Field>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => deleteSelectedNode(selectedNode.id)}
                  title="Delete node"
                  className="mt-5"
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
              <Field label="Name">
                <Input
                  value={selectedNode.name ?? ""}
                  onChange={(event) => changeSelectedNode((node) => ({ ...node, name: event.target.value }))}
                />
              </Field>
              <Field label="Type">
                <Select
                  value={selectedNode.type ?? ""}
                  onChange={(event) =>
                    changeSelectedNode((node) => {
                      const schema = paletteNodeTypes.find((item) => item.type === event.target.value);
                      return {
                        ...node,
                        type: event.target.value,
                        name: node.name || schema?.title || node.name,
                      };
                    })
                  }
                >
                  {selectedNode.type && !paletteNodeTypes.some((item) => item.type === selectedNode.type) ? (
                    <option value={selectedNode.type}>{selectedNode.type}</option>
                  ) : null}
                  {paletteNodeTypes.map((nodeType) => (
                    <option key={nodeType.type} value={nodeType.type}>
                      {nodeType.title || nodeType.type}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="Description">
                <Textarea
                  value={selectedNode.description ?? ""}
                  onChange={(event) => changeSelectedNode((node) => ({ ...node, description: event.target.value }))}
                  className="h-20 text-xs"
                />
              </Field>
            </InspectorBlock>

            <InspectorBlock title="Config">
              <Textarea
                value={nodeConfigText}
                onChange={(event) => setNodeConfigText(event.target.value)}
                spellCheck={false}
                className="h-56 text-xs"
              />
              <Button variant="outline" size="sm" onClick={applyNodeConfig}>
                <Braces className="h-4 w-4" />
                Apply Config
              </Button>
            </InspectorBlock>
          </>
        ) : null}

        {inspectorMode === "edge" && (selectedEdge || selectedVirtualEdge) ? (
          <>
            <InspectorBlock title="Edge Properties">
              <div className="mb-2 flex items-center gap-2">
                <Badge>{selectedVirtualEdge ? selectedVirtualEdge.kind : selectedEdge?.condition?.type || "direct"}</Badge>
                <Button variant="ghost" size="icon" onClick={() => deleteSelectedEdge()} title="Delete edge" className="ml-auto">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
              {selectedVirtualEdge ? (
                <InfoRows
                  rows={[
                    ["from", displayNodeRef(selectedVirtualEdge.from, definition, visibleVirtualNodes)],
                    ["to", displayNodeRef(selectedVirtualEdge.to, definition, visibleVirtualNodes)],
                  ]}
                />
              ) : selectedEdge ? (
                <>
                  <div className="grid grid-cols-2 gap-2">
                    <Field label="From">
                      <NodeSelect
                        value={selectedEdge.from}
                        nodes={definition?.nodes ?? []}
                        onChange={(value) => changeSelectedEdge((edge) => ({ ...edge, from: value }))}
                      />
                    </Field>
                    <Field label="To">
                      <NodeSelect
                        value={selectedEdge.to}
                        nodes={definition?.nodes ?? []}
                        onChange={(value) => changeSelectedEdge((edge) => ({ ...edge, to: value }))}
                      />
                    </Field>
                  </div>
                  <Field label="Condition">
                    <Select
                      value={selectedEdge.condition?.type ?? ""}
                      onChange={(event) => {
                        const value = event.target.value;
                        changeSelectedEdge((edge) => ({
                          ...edge,
                          condition: value ? { type: value, config: edge.condition?.config ?? {} } : undefined,
                        }));
                      }}
                    >
                      <option value="">direct</option>
                      {selectedEdge.condition?.type &&
                      !conditions.some((condition) => condition.type === selectedEdge.condition?.type) ? (
                        <option value={selectedEdge.condition.type}>{selectedEdge.condition.type}</option>
                      ) : null}
                      {conditions.map((condition) => (
                        <option key={condition.type} value={condition.type}>
                          {condition.title || condition.type}
                        </option>
                      ))}
                    </Select>
                  </Field>
                </>
              ) : null}
            </InspectorBlock>

            {selectedEdge?.condition && !selectedVirtualEdge ? (
              <InspectorBlock title="Condition Config">
                <Textarea
                  value={edgeConfigText}
                  onChange={(event) => setEdgeConfigText(event.target.value)}
                  spellCheck={false}
                  className="h-44 text-xs"
                />
                <Button variant="outline" size="sm" onClick={applyEdgeConfig}>
                  <Braces className="h-4 w-4" />
                  Apply Condition
                </Button>
              </InspectorBlock>
            ) : null}
          </>
        ) : null}
      </section>

      {contextMenu ? (
        <div
          className="fixed z-50 w-64 overflow-hidden rounded-md border border-border bg-panel shadow-lg"
          style={{ left: contextMenu.screen.x, top: contextMenu.screen.y }}
          onClick={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.preventDefault()}
        >
          {contextMenu.kind === "pane" ? (
            <div>
              <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase text-muted-foreground">
                Create Node
              </div>
              {virtualNodeTypes.map((nodeType) => (
                <button
                  key={nodeType.type}
                  className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left hover:bg-accent"
                  onClick={() => addVirtualNode(nodeType.type as VirtualNodeKind, contextMenu.position)}
                >
                  <Plus className="h-4 w-4 text-muted-foreground" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{nodeType.title || nodeType.type}</span>
                    <span className="block truncate font-mono text-[11px] text-muted-foreground">virtual:{nodeType.type}</span>
                  </span>
                </button>
              ))}
              {paletteNodeTypes.slice(0, 8).map((nodeType) => (
                <button
                  key={nodeType.type}
                  className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left hover:bg-accent"
                  onClick={() => addNode(nodeType, contextMenu.position)}
                >
                  <Plus className="h-4 w-4 text-muted-foreground" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{nodeType.title || nodeType.type}</span>
                    <span className="block truncate font-mono text-[11px] text-muted-foreground">{nodeType.type}</span>
                  </span>
                </button>
              ))}
            </div>
          ) : null}

          {contextMenu.kind === "node" ? (
            <div>
              <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase text-muted-foreground">
                Node
              </div>
              <button
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent"
                onClick={() => setContextMenu(null)}
              >
                <FileJson className="h-4 w-4 text-muted-foreground" />
                Edit
              </button>
              <button
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-destructive hover:bg-accent"
                onClick={() => deleteSelectedNode(contextMenu.nodeId)}
              >
                <Trash2 className="h-4 w-4" />
                Delete
              </button>
            </div>
          ) : null}

          {contextMenu.kind === "edge" ? (
            <div>
              <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase text-muted-foreground">
                Edge
              </div>
              <button
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent"
                onClick={() => setContextMenu(null)}
              >
                <FileJson className="h-4 w-4 text-muted-foreground" />
                Edit
              </button>
              <button
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-destructive hover:bg-accent"
                onClick={() => deleteSelectedEdge(contextMenu.edgeId)}
              >
                <Trash2 className="h-4 w-4" />
                Delete
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function NodeSelect({
  value,
  nodes,
  disabled = false,
  onChange,
}: {
  value: string;
  nodes: GraphNodeSpec[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <Select value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled}>
      <option value="">-</option>
      {nodes.map((node) => (
        <option key={node.id} value={node.id}>
          {node.name || node.id}
        </option>
      ))}
    </Select>
  );
}

function InspectorBlock({
  title,
  action,
  children,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="grid gap-3 border-b border-border p-3 last:border-b-0">
      <div className="flex min-h-8 items-center gap-2">
        <div className="text-xs font-semibold uppercase text-muted-foreground">{title}</div>
        {action ? <div className="ml-auto">{action}</div> : null}
      </div>
      {children}
    </section>
  );
}

function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("grid gap-1 text-sm", className)}>
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}

function PanelHeader({
  icon: Icon,
  title,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
}) {
  return (
    <div className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3">
      <Icon className="h-4 w-4 text-muted-foreground" />
      <span className="text-sm font-semibold">{title}</span>
    </div>
  );
}

function InfoRows({ rows }: { rows: Array<[string, string]> }) {
  return (
    <div className="grid gap-1">
      {rows.map(([label, value]) => (
        <div key={label} className="grid grid-cols-[84px_minmax(0,1fr)] gap-2 text-xs">
          <span className="text-muted-foreground">{label}</span>
          <span className="truncate font-mono">{value || "-"}</span>
        </div>
      ))}
    </div>
  );
}

function InitialStateRequirementList({ requirements }: { requirements: InitialStateRequirements | null }) {
  const required = requirements?.required ?? [];
  const provided = requirements?.provided_by_upstream ?? [];
  const unresolved = requirements?.unresolved ?? [];
  if (!requirements) {
    return <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">Requirements unavailable</div>;
  }
  if (required.length === 0 && provided.length === 0 && unresolved.length === 0) {
    return <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">No required initial state</div>;
  }
  return (
    <div className="grid gap-2">
      {required.length > 0 ? (
        <RequirementGroup title="Required" tone="warn" items={required} />
      ) : null}
      {unresolved.length > 0 ? (
        <RequirementGroup title="Unresolved" tone="danger" items={unresolved} />
      ) : null}
      {provided.length > 0 ? (
        <RequirementGroup title="Provided" tone="ok" items={provided} />
      ) : null}
    </div>
  );
}

function RequirementGroup({
  title,
  tone,
  items,
}: {
  title: string;
  tone: "ok" | "warn" | "danger";
  items: Array<{ path: string; nodes?: string[]; sources?: string[]; type?: string; description?: string; message?: string }>;
}) {
  return (
    <div className="rounded-md border border-border bg-muted p-2">
      <div className="mb-2 flex items-center gap-2">
        <Badge tone={tone}>{title}</Badge>
        <span className="text-xs text-muted-foreground">{items.length}</span>
      </div>
      <div className="grid gap-1">
        {items.map((item) => (
          <div key={`${title}-${item.path}`} className="min-w-0 text-xs">
            <div className="truncate font-mono text-foreground">{item.path}</div>
            <div className="truncate text-muted-foreground">
              {[item.type, item.nodes?.length ? `nodes:${item.nodes.join(",")}` : "", item.sources?.length ? `sources:${item.sources.join(",")}` : ""]
                .filter(Boolean)
                .join(" / ")}
            </div>
            {item.message || item.description ? (
              <div className="line-clamp-2 text-muted-foreground">{item.message || item.description}</div>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}

function statusTone(status?: string): "neutral" | "ok" | "warn" | "danger" | "live" {
  switch (status) {
    case "completed":
      return "ok";
    case "running":
    case "pending":
      return "live";
    case "paused":
      return "warn";
    case "failed":
    case "canceled":
      return "danger";
    default:
      return "neutral";
  }
}

function validateGraph(definition: GraphDefinition | null): string {
  if (!definition) return "invalid json";
  if (definition.nodes.length === 0) return "no nodes";
  const nodeIds = new Set(definition.nodes.map((node) => node.id));
  if (definition.nodes.some((node) => !node.id || !node.type)) return "node required";
  if (nodeIds.size !== definition.nodes.length) return "duplicate nodes";
  if (definition.entry_point && !nodeIds.has(definition.entry_point)) return "missing entry";
  if (definition.finish_point && !nodeIds.has(definition.finish_point)) return "missing finish";
  for (const edge of definition.edges ?? []) {
    if (!nodeIds.has(edge.from)) return "missing source";
    if (!nodeIds.has(edge.to)) return "missing target";
  }
  return "";
}

function parseJSONObject(value: string): Record<string, unknown> {
  const parsed = parseJSON<unknown>(value);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("json object required");
  }
  return parsed as Record<string, unknown>;
}

function findLastEdgeId(definition: GraphDefinition, from: string, to: string): string | null {
  const source = from.trim();
  const target = to.trim();
  const edges = definition.edges ?? [];
  for (let index = edges.length - 1; index >= 0; index -= 1) {
    const edge = edges[index];
    if (edge.from === source && edge.to === target) {
      return graphEdgeId(edge, index);
    }
  }
  return null;
}

function virtualEdgeId(from: string, to: string, kind: VirtualGraphEdge["kind"]): string {
  return `virtual:${kind}:${from}->${to}`;
}

function lastVirtualEdge(edges: VirtualGraphEdge[], kind: VirtualGraphEdge["kind"]): VirtualGraphEdge | undefined {
  for (let index = edges.length - 1; index >= 0; index -= 1) {
    if (edges[index].kind === kind) return edges[index];
  }
  return undefined;
}

function displayNodeRef(nodeID: string, definition: GraphDefinition | null, virtualNodes: GraphNodeSpec[]): string {
  const virtualNode = virtualNodes.find((node) => node.id === nodeID);
  if (virtualNode) return virtualNode.name || virtualNode.id;
  const node = definition?.nodes.find((item) => item.id === nodeID);
  return node?.name || nodeID;
}

function realNodeTypes(nodeTypes: NodeTypeSchema[]): NodeTypeSchema[] {
  const result = nodeTypes.filter((nodeType) => !isVirtualNodeType(nodeType.type));
  return result.length ? result : fallbackNodeTypes;
}

function isVirtualNodeType(type?: string): type is VirtualNodeKind {
  return type === "start" || type === "end";
}

function isVirtualNodeId(nodeID: string): boolean {
  return Boolean(virtualNodeKind(nodeID));
}

function virtualNodeSpec(nodeID: string): GraphNodeSpec {
  const kind = virtualNodeKind(nodeID);
  return {
    id: nodeID,
    name: virtualNodeLabel(nodeID),
    type: kind ?? "node",
    config: {},
  };
}

function virtualNodeLabel(nodeID: string): string {
  const kind = virtualNodeKind(nodeID);
  const index = virtualNodeIndex(nodeID);
  const label = kind === "start" ? "Start" : "End";
  return index > 1 ? `${label} ${index}` : label;
}

function nextVirtualNodeId(kind: VirtualNodeKind, nodeIDs: string[]): string {
  const base = kind === "start" ? START_NODE_REF : END_NODE_REF;
  const used = new Set(nodeIDs);
  if (!used.has(base)) return base;
  for (let index = 2; index < 1000; index += 1) {
    const id = `${base}:${index}`;
    if (!used.has(id)) return id;
  }
  return `${base}:${Date.now().toString(36)}`;
}

function virtualNodeKind(nodeID: string): VirtualNodeKind | undefined {
  if (nodeID === START_NODE_REF || nodeID.startsWith(`${START_NODE_REF}:`)) return "start";
  if (nodeID === END_NODE_REF || nodeID.startsWith(`${END_NODE_REF}:`)) return "end";
  return undefined;
}

function virtualNodeIndex(nodeID: string): number {
  const kind = virtualNodeKind(nodeID);
  const base = kind === "start" ? START_NODE_REF : kind === "end" ? END_NODE_REF : "";
  const prefix = `${base}:`;
  if (!base || nodeID === base || !nodeID.startsWith(prefix)) return 1;
  const index = Number(nodeID.slice(prefix.length));
  return Number.isInteger(index) && index > 1 ? index : 1;
}
