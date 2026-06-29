import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Search, X } from "lucide-react";
import { GraphCanvas, type VirtualGraphEdge } from "../../components/GraphCanvas";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import {
  END_NODE_REF,
  START_NODE_REF,
  addGraphEdge,
  addNodeToGraph,
  createGraphDefinition,
  graphEdgeId,
  graphNodePositions,
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
  pickInitialLocalGraphDraft,
  readLocalGraphDrafts,
  saveLocalGraphDraft,
  type LocalGraphDraft,
  writeLastLocalGraphDraftId,
} from "../../lib/localGraphs";
import { formatTime, stringifyJSON } from "../../lib/utils";
import type {
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  InitialStateRequirements,
  NodeTypeSchema,
  RegistryInfo,
  StepRecord,
} from "../../types";
import { CanvasContextMenu } from "./graph-workspace/CanvasContextMenu";
import { defaultVirtualNodeIds, fallbackNodeTypes, virtualNodeTypes } from "./graph-workspace/constants";
import { autoLayoutGraph } from "./graph-workspace/layout";
import { buildGraphLintIssues, type GraphLintIssue } from "./graph-workspace/lint";
import { ToastStack, type ToastRecord } from "./graph-workspace/ToastStack";
import { GraphBrowserPanel } from "./graph-workspace/GraphBrowserPanel";
import { GraphInspectorPanel } from "./graph-workspace/GraphInspectorPanel";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./graph-workspace/types";
import {
  findLastEdgeId,
  isVirtualNodeId,
  isVirtualNodeType,
  lastVirtualEdge,
  nextVirtualNodeId,
  parseJSONObject,
  realNodeTypes,
  virtualEdgeId,
  virtualNodeKind,
  virtualNodeLabel,
  virtualNodeSpec,
} from "./graph-workspace/utils";

const autoSaveWindowMs = 3000;

interface GraphWorkspaceProps {
  definition: GraphDefinition | null;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  steps: StepRecord[];
  selectedRunId: string;
  registry: RegistryInfo | null;
  graphId: string;
  graphVersion: string;
  graphSwitchDisabled: boolean;
  toasts: ToastRecord[];
  onGraphId: (value: string) => void;
  onGraphVersion: (value: string) => void;
  onDefinitionText: (value: string) => void;
  onInitialStateText: (value: string) => void;
  onDismissToast: (id: string) => void;
  onGraphSwitch: () => boolean;
  onLocalGraphLoaded?: () => void;
}

export const GraphWorkspace = memo(function GraphWorkspace({
  definition,
  initialStateText,
  initialRequirements,
  initialRequirementsError,
  steps,
  selectedRunId,
  registry,
  graphId,
  graphVersion,
  graphSwitchDisabled,
  toasts,
  onGraphId,
  onGraphVersion,
  onDefinitionText,
  onInitialStateText,
  onDismissToast,
  onGraphSwitch,
  onLocalGraphLoaded,
}: GraphWorkspaceProps) {
  const [drafts, setDrafts] = useState<LocalGraphDraft[]>([]);
  const [activeDraftId, setActiveDraftId] = useState("");
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null);
  const [nodeTypeQuery, setNodeTypeQuery] = useState("");
  const [nodeQuery, setNodeQuery] = useState("");
  const [nodeConfigText, setNodeConfigText] = useState("{}");
  const [edgeConfigText, setEdgeConfigText] = useState("{}");
  const [localStatus, setLocalStatus] = useState("local ready");
  const [leftCollapsed, setLeftCollapsed] = useState(false);
  const [nodeTypesOpen, setNodeTypesOpen] = useState(false);
  const [contextMenu, setContextMenu] = useState<CanvasContextMenuState | null>(null);
  const [virtualNodeIds, setVirtualNodeIds] = useState<string[]>(defaultVirtualNodeIds);
  const [virtualEdges, setVirtualEdges] = useState<VirtualGraphEdge[]>([]);
  const [fitViewSignal, setFitViewSignal] = useState(0);
  const [focusNodeId, setFocusNodeId] = useState<string | undefined>();
  const [focusNodeSignal, setFocusNodeSignal] = useState(0);
  const [canvasSearchOpen, setCanvasSearchOpen] = useState(false);
  const [canvasSearchQuery, setCanvasSearchQuery] = useState("");
  const [canvasSearchIndex, setCanvasSearchIndex] = useState(0);
  const autoLoadedDraftRef = useRef(false);
  const autoSaveHydratedRef = useRef(false);
  const autoSaveTimerRef = useRef<number | null>(null);
  const activeDraftIdRef = useRef("");
  const lastSavedSignatureRef = useRef("");
  const searchInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    setDrafts(readLocalGraphDrafts());
  }, []);

  useEffect(() => {
    if (autoLoadedDraftRef.current) return;
    autoLoadedDraftRef.current = true;
    const nextDrafts = readLocalGraphDrafts();
    setDrafts(nextDrafts);
    const draft = pickInitialLocalGraphDraft(nextDrafts);
    if (draft) loadDraft(draft);
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

  useEffect(() => {
    if (!canvasSearchOpen) return;
    window.setTimeout(() => searchInputRef.current?.focus(), 0);
  }, [canvasSearchOpen]);

  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      if ((event.ctrlKey || event.metaKey) && key === "f") {
        event.preventDefault();
        setCanvasSearchOpen(true);
        return;
      }
      if (isEditableKeyboardTarget(event.target)) return;
      if ((event.ctrlKey || event.metaKey) && key === "s") {
        event.preventDefault();
        saveLocal();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && key === "d") {
        event.preventDefault();
        duplicateSelectedNode();
        return;
      }
      if (event.key === "Delete" || event.key === "Backspace") {
        if (selectedEdgeId) {
          event.preventDefault();
          deleteSelectedEdge(selectedEdgeId);
          return;
        }
        if (selectedNodeId) {
          event.preventDefault();
          deleteSelectedNode(selectedNodeId);
        }
      }
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  });

  useEffect(() => {
    if (!definition) return;
    const signature = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges);
    if (!autoSaveHydratedRef.current) {
      autoSaveHydratedRef.current = true;
      lastSavedSignatureRef.current = signature;
      return;
    }
    if (signature === lastSavedSignatureRef.current) {
      return;
    }
    setLocalStatus("autosave queued");
    if (autoSaveTimerRef.current !== null) {
      window.clearTimeout(autoSaveTimerRef.current);
    }
    autoSaveTimerRef.current = window.setTimeout(() => {
      autoSaveTimerRef.current = null;
      saveLocal({ mode: "auto" });
    }, autoSaveWindowMs);
    return () => {
      if (autoSaveTimerRef.current !== null) {
        window.clearTimeout(autoSaveTimerRef.current);
        autoSaveTimerRef.current = null;
      }
    };
  }, [definition, graphId, graphVersion, virtualEdges, virtualNodeIds]);

  useEffect(() => {
    activeDraftIdRef.current = activeDraftId;
  }, [activeDraftId]);

  useEffect(() => () => {
    if (autoSaveTimerRef.current !== null) {
      window.clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = null;
    }
  }, []);

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
  const semanticVirtualEdges = useMemo(
    () => virtualEdgesFromDefinition(definition, virtualNodeIds),
    [definition, virtualNodeIds]
  );
  const displayVirtualEdges = useMemo(
    () => mergeVirtualEdges(semanticVirtualEdges, virtualEdges),
    [semanticVirtualEdges, virtualEdges]
  );
  const lintIssues = useMemo(
    () => buildGraphLintIssues({ definition, initialStateText, initialRequirements }),
    [definition, initialRequirements, initialStateText]
  );
  const selectedVirtualEdge = useMemo(
    () => displayVirtualEdges.find((edge) => edge.id === selectedEdgeId) ?? null,
    [displayVirtualEdges, selectedEdgeId]
  );
  const inspectorMode = selectedEdge || selectedVirtualEdge ? "edge" : selectedVirtualNode ? "virtual" : selectedNode ? "node" : "graph";
  const searchableNodes = useMemo(
    () => [...visibleVirtualNodes, ...(definition?.nodes ?? [])],
    [definition, visibleVirtualNodes]
  );
  const canvasSearchMatches = useMemo(() => {
    const query = canvasSearchQuery.trim().toLowerCase();
    if (!query) return [];
    return searchableNodes.filter((node) =>
      `${node.id} ${node.name ?? ""} ${node.type ?? ""} ${node.description ?? ""}`.toLowerCase().includes(query)
    );
  }, [canvasSearchQuery, searchableNodes]);
  const highlightedNodeIds = useMemo(
    () => canvasSearchMatches.map((node) => node.id),
    [canvasSearchMatches]
  );

  useEffect(() => {
    if (!canvasSearchOpen || !canvasSearchQuery.trim()) return;
    const first = canvasSearchMatches[0];
    setCanvasSearchIndex(0);
    if (first) focusCanvasNode(first.id);
  }, [canvasSearchMatches, canvasSearchOpen, canvasSearchQuery]);

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
    if (!onGraphSwitch()) {
      setLocalStatus("run active");
      return;
    }
    const nextName = `debug_graph_${Date.now().toString(36)}`;
    const next = createGraphDefinition(nextName, defaultGraphNodeType);
    onGraphId(next.name || nextName);
    onGraphVersion(next.version || "1.0");
    onDefinitionText(stringifyJSON(next));
    setActiveDraftId("");
    activeDraftIdRef.current = "";
    lastSavedSignatureRef.current = "";
    autoSaveHydratedRef.current = true;
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setVirtualNodeIds(defaultVirtualNodeIds);
    setVirtualEdges([]);
    setLocalStatus("new graph");
  }

  function saveLocal(options: { mode?: "manual" | "auto" } = {}) {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    if (autoSaveTimerRef.current !== null) {
      window.clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = null;
    }
    const draft = saveLocalGraphDraft({
      id: activeDraftIdRef.current || undefined,
      title: definition.name || graphId,
      graphId,
      graphVersion,
      definition: withSavedGraphWorkspaceState(definition, virtualNodeIds, virtualEdges),
    });
    setActiveDraftId(draft.id);
    setDrafts(readLocalGraphDrafts());
    lastSavedSignatureRef.current = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges);
    setLocalStatus(`${options.mode === "auto" ? "autosaved" : "saved"} ${formatTime(draft.updatedAt)}`);
  }

  function loadDraft(draft: LocalGraphDraft) {
    if (!onGraphSwitch()) {
      setLocalStatus("run active");
      return;
    }
    loadDraftWithoutGuard(draft);
  }

  function loadDraftWithoutGuard(draft: LocalGraphDraft) {
    if (autoSaveTimerRef.current !== null) {
      window.clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = null;
    }
    autoSaveHydratedRef.current = false;
    writeLastLocalGraphDraftId(draft.id);
    onLocalGraphLoaded?.();
    setActiveDraftId(draft.id);
    onGraphId(draft.graphId);
    onGraphVersion(draft.graphVersion);
    onDefinitionText(stringifyJSON(draft.definition));
    const savedState = savedGraphWorkspaceState(draft.definition);
    lastSavedSignatureRef.current = autoSaveSignature(draft.definition, draft.graphId, draft.graphVersion, savedState.virtualNodeIds, savedState.virtualEdges);
    setVirtualNodeIds(savedState.virtualNodeIds);
    setVirtualEdges(savedState.virtualEdges);
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setLocalStatus(`loaded ${draft.title}`);
    window.setTimeout(() => setFitViewSignal((value) => value + 1), 80);
  }

  function duplicateDraft() {
    if (!activeDraftId) return;
    if (!onGraphSwitch()) {
      setLocalStatus("run active");
      return;
    }
    const draft = duplicateLocalGraphDraft(activeDraftId);
    if (!draft) return;
    setDrafts(readLocalGraphDrafts());
    loadDraftWithoutGuard(draft);
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
    if (displayVirtualEdges.some((edge) => edge.id === edgeId)) {
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
    const edge = displayVirtualEdges.find((item) => item.id === edgeId);
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
    const removedEdges = displayVirtualEdges.filter((edge) => edge.from === nodeID || edge.to === nodeID);
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

  function focusCanvasNode(nodeID: string) {
    setSelectedNodeId(nodeID);
    setSelectedEdgeId(null);
    setFocusNodeId(nodeID);
    setFocusNodeSignal((value) => value + 1);
  }

  function focusSearchMatch(direction: 1 | -1) {
    if (canvasSearchMatches.length === 0) return;
    const nextIndex =
      (canvasSearchIndex + direction + canvasSearchMatches.length) % canvasSearchMatches.length;
    setCanvasSearchIndex(nextIndex);
    focusCanvasNode(canvasSearchMatches[nextIndex].id);
  }

  function duplicateSelectedNode() {
    if (!definition || !selectedNode || isVirtualNodeId(selectedNode.id)) return;
    const nextId = uniqueNodeId(`${selectedNode.id}_copy`, definition.nodes);
    const positions = graphNodePositions(definition);
    const sourcePosition = positions.get(selectedNode.id) ?? { x: 0, y: 0 };
    const nodeCopy: GraphNodeSpec = {
      ...selectedNode,
      id: nextId,
      name: selectedNode.name ? `${selectedNode.name} copy` : nextId,
      config: cloneJSONRecord(selectedNode.config ?? {}),
    };
    const next = withNodePosition(
      {
        ...definition,
        nodes: [...definition.nodes, nodeCopy],
      },
      nextId,
      { x: sourcePosition.x + 40, y: sourcePosition.y + 40 }
    );
    setDefinition(next);
    setSelectedNodeId(nextId);
    setSelectedEdgeId(null);
    setLocalStatus("node duplicated");
  }

  function applyAutoLayout() {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    setDefinition(autoLayoutGraph(definition, virtualNodeIds, displayVirtualEdges));
    setFitViewSignal((value) => value + 1);
    setLocalStatus("auto layout applied");
  }

  function selectLintIssue(issue: GraphLintIssue) {
    if (issue.nodeId) {
      focusCanvasNode(issue.nodeId);
      return;
    }
    if (issue.edgeId) {
      setSelectedEdgeId(issue.edgeId);
      setSelectedNodeId(null);
      return;
    }
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
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
      <GraphBrowserPanel
        activeDraftId={activeDraftId}
        creatableNodeTypes={creatableNodeTypes}
        definition={definition}
        drafts={drafts}
        filteredNodes={filteredNodes}
        filteredNodeTypes={filteredNodeTypes}
        graphSwitchDisabled={graphSwitchDisabled}
        leftCollapsed={leftCollapsed}
        nodeQuery={nodeQuery}
        nodeTypeQuery={nodeTypeQuery}
        nodeTypesOpen={nodeTypesOpen}
        selectedNodeId={selectedNodeId}
        virtualNodeIds={virtualNodeIds}
        onAddNode={addNode}
        onAutoLayout={applyAutoLayout}
        onCollapseChange={setLeftCollapsed}
        onCreateGraph={createGraph}
        onDeleteDraft={deleteDraft}
        onDeleteNode={deleteSelectedNode}
        onDuplicateDraft={duplicateDraft}
        onLoadDraft={loadDraft}
        onNodeQuery={setNodeQuery}
        onNodeTypeQuery={setNodeTypeQuery}
        onNodeTypesOpen={setNodeTypesOpen}
        onSaveLocal={saveLocal}
        onSelectNode={(nodeId) => {
          setSelectedNodeId(nodeId);
          setSelectedEdgeId(null);
        }}
      />

      <section className="relative min-h-0 bg-canvas">
        <GraphCanvas
          definition={definition}
          steps={steps}
          selectedRunId={selectedRunId}
          editable
          selectedNodeId={selectedNodeId ?? undefined}
          selectedEdgeId={selectedEdgeId ?? undefined}
          fitViewSignal={fitViewSignal}
          focusNodeId={focusNodeId}
          focusNodeSignal={focusNodeSignal}
          highlightedNodeIds={highlightedNodeIds}
          onSelectNode={setSelectedNodeId}
          onSelectEdge={setSelectedEdgeId}
          onNodePositionChange={moveNode}
          onConnectNodes={connectNodes}
          onCreateNodeAt={openCreateMenu}
          onNodeContextMenu={openNodeMenu}
          onEdgeContextMenu={openEdgeMenu}
          virtualNodeIds={virtualNodeIds}
          virtualEdges={displayVirtualEdges}
        />
        {canvasSearchOpen ? (
          <div className="absolute left-1/2 top-4 z-40 flex w-[min(420px,calc(100%-2rem))] -translate-x-1/2 items-center gap-2 rounded-md border border-border bg-panel p-2 shadow-lg">
            <Search className="h-4 w-4 shrink-0 text-muted-foreground" />
            <Input
              ref={searchInputRef}
              value={canvasSearchQuery}
              onChange={(event) => setCanvasSearchQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Escape") {
                  event.preventDefault();
                  setCanvasSearchOpen(false);
                  return;
                }
                if (event.key === "Enter") {
                  event.preventDefault();
                  focusSearchMatch(event.shiftKey ? -1 : 1);
                }
              }}
              placeholder="Search nodes"
              className="h-8"
            />
            <span className="w-16 shrink-0 text-right text-xs text-muted-foreground">
              {canvasSearchQuery.trim()
                ? `${canvasSearchMatches.length ? canvasSearchIndex + 1 : 0}/${canvasSearchMatches.length}`
                : "0/0"}
            </span>
            <Button variant="ghost" size="icon" onClick={() => setCanvasSearchOpen(false)} title="Close search">
              <X className="h-4 w-4" />
            </Button>
          </div>
        ) : null}
        <ToastStack toasts={toasts} onDismiss={onDismissToast} />
      </section>

      <GraphInspectorPanel
        conditions={conditions}
        definition={definition}
        edgeConfigText={edgeConfigText}
        initialRequirements={initialRequirements}
        initialRequirementsError={initialRequirementsError}
        initialStateText={initialStateText}
        inspectorMode={inspectorMode}
        inspectorTitle={inspectorTitle}
        lintIssues={lintIssues}
        nodeConfigText={nodeConfigText}
        paletteNodeTypes={paletteNodeTypes}
        selectedEdge={selectedEdge}
        selectedNode={selectedNode}
        selectedVirtualEdge={selectedVirtualEdge}
        visibleVirtualNodes={visibleVirtualNodes}
        onApplyEdgeConfig={applyEdgeConfig}
        onApplyNodeConfig={applyNodeConfig}
        onChangeEdge={changeSelectedEdge}
        onChangeEdgeConfigText={setEdgeConfigText}
        onChangeGraphField={changeGraphField}
        onChangeInitialStateText={onInitialStateText}
        onChangeNode={changeSelectedNode}
        onChangeNodeConfigText={setNodeConfigText}
        onChangeNodeId={changeSelectedNodeId}
        onDeleteEdge={deleteSelectedEdge}
        onDeleteNode={deleteSelectedNode}
        onSelectLintIssue={selectLintIssue}
      />

      {contextMenu ? (
        <CanvasContextMenu
          contextMenu={contextMenu}
          paletteNodeTypes={paletteNodeTypes}
          onAddNode={addNode}
          onAddVirtualNode={addVirtualNode}
          onClose={() => setContextMenu(null)}
          onDeleteEdge={deleteSelectedEdge}
          onDeleteNode={deleteSelectedNode}
        />
      ) : null}
    </div>
  );
});

function virtualEdgesFromDefinition(
  definition: GraphDefinition | null,
  virtualNodeIds: string[]
): VirtualGraphEdge[] {
  if (!definition) return [];
  const visible = new Set(virtualNodeIds);
  const edges: VirtualGraphEdge[] = [];
  if (definition.entry_point && visible.has(START_NODE_REF)) {
    edges.push({
      id: virtualEdgeId(START_NODE_REF, definition.entry_point, "entry"),
      from: START_NODE_REF,
      to: definition.entry_point,
      kind: "entry",
    });
  }
  if (definition.finish_point && visible.has(END_NODE_REF)) {
    edges.push({
      id: virtualEdgeId(definition.finish_point, END_NODE_REF, "finish"),
      from: definition.finish_point,
      to: END_NODE_REF,
      kind: "finish",
    });
  }
  return edges;
}

function mergeVirtualEdges(primary: VirtualGraphEdge[], secondary: VirtualGraphEdge[]): VirtualGraphEdge[] {
  const seen = new Set<string>();
  const result: VirtualGraphEdge[] = [];
  for (const edge of [...primary, ...secondary]) {
    if (seen.has(edge.id)) continue;
    seen.add(edge.id);
    result.push(edge);
  }
  return result;
}

function withSavedGraphWorkspaceState(
  definition: GraphDefinition,
  virtualNodeIds: string[],
  virtualEdges: VirtualGraphEdge[]
): GraphDefinition {
  const metadata = { ...(definition.metadata ?? {}) };
  const web = isRecord(metadata.web) ? { ...metadata.web } : {};
  web.virtual_node_ids = virtualNodeIds;
  web.virtual_edges = virtualEdges.map((edge) => ({
    id: edge.id,
    from: edge.from,
    to: edge.to,
    kind: edge.kind,
  }));
  metadata.web = web;
  return { ...definition, metadata };
}

function savedGraphWorkspaceState(definition: GraphDefinition): {
  virtualNodeIds: string[];
  virtualEdges: VirtualGraphEdge[];
} {
  const web = isRecord(definition.metadata?.web) ? definition.metadata.web : undefined;
  const rawNodeIds = Array.isArray(web?.virtual_node_ids) ? web.virtual_node_ids : [];
  const virtualNodeIds = rawNodeIds.filter((item): item is string => typeof item === "string" && item.trim() !== "");
  const rawEdges = Array.isArray(web?.virtual_edges) ? web.virtual_edges : [];
  const virtualEdges = rawEdges.filter(isVirtualGraphEdge);
  return {
    virtualNodeIds: virtualNodeIds.length ? virtualNodeIds : defaultVirtualNodeIds,
    virtualEdges,
  };
}

function isVirtualGraphEdge(value: unknown): value is VirtualGraphEdge {
  if (!isRecord(value)) return false;
  return (
    typeof value.id === "string" &&
    typeof value.from === "string" &&
    typeof value.to === "string" &&
    (value.kind === "entry" || value.kind === "finish")
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function uniqueNodeId(baseID: string, nodes: GraphNodeSpec[]): string {
  const used = new Set(nodes.map((node) => node.id));
  if (!used.has(baseID)) return baseID;
  for (let index = 2; index < 1000; index += 1) {
    const id = `${baseID}_${index}`;
    if (!used.has(id)) return id;
  }
  return `${baseID}_${Date.now().toString(36)}`;
}

function cloneJSONRecord(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) return {};
  try {
    return JSON.parse(JSON.stringify(value)) as Record<string, unknown>;
  } catch {
    return { ...value };
  }
}

function autoSaveSignature(
  definition: GraphDefinition,
  graphId: string,
  graphVersion: string,
  virtualNodeIds: string[],
  virtualEdges: VirtualGraphEdge[]
): string {
  return JSON.stringify({
    graphId,
    graphVersion,
    definition: withSavedGraphWorkspaceState(definition, virtualNodeIds, virtualEdges),
  });
}

function isEditableKeyboardTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tagName = target.tagName.toLowerCase();
  return tagName === "input" || tagName === "textarea" || tagName === "select";
}

