import { memo, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronDown, FilePlus2, Search, Trash2, X } from "lucide-react";
import {
  GraphCanvas,
  hasStoredGraphCanvasViewport,
  type VirtualGraphEdge,
  type VirtualGraphLoop,
} from "../../components/GraphCanvas";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import {
  END_NODE_REF,
  START_NODE_REF,
  addGraphEdge,
  addNodeToGraph,
  createGraphDefinition,
  findGraphEdgeIndex,
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
import { defaultVirtualNodeIds, fallbackNodeTypes } from "./graph-workspace/constants";
import { autoLayoutGraph } from "./graph-workspace/layout";
import { buildGraphLintIssues, type GraphLintIssue } from "./graph-workspace/lint";
import { ToastStack, type ToastRecord } from "./graph-workspace/ToastStack";
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
  const [selectedLoopId, setSelectedLoopId] = useState<string | null>(null);
  const [nodeConfigText, setNodeConfigText] = useState("{}");
  const [edgeConfigText, setEdgeConfigText] = useState("{}");
  const [localStatus, setLocalStatus] = useState("local ready");
  const [contextMenu, setContextMenu] = useState<CanvasContextMenuState | null>(null);
  const [graphMenuOpen, setGraphMenuOpen] = useState(false);
  const [titleSlot, setTitleSlot] = useState<HTMLElement | null>(null);
  const [virtualNodeIds, setVirtualNodeIds] = useState<string[]>(defaultVirtualNodeIds);
  const [virtualEdges, setVirtualEdges] = useState<VirtualGraphEdge[]>([]);
  const [virtualLoops, setVirtualLoops] = useState<VirtualGraphLoop[]>([]);
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
    setTitleSlot(document.getElementById("graph-title-slot"));
  }, []);

  useEffect(() => {
    if (!graphMenuOpen) return;
    const closeOnPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Element && target.closest("[data-graph-title-menu]")) return;
      setGraphMenuOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setGraphMenuOpen(false);
    };
    window.addEventListener("pointerdown", closeOnPointerDown);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOnPointerDown);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [graphMenuOpen]);

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
        if (selectedLoopId) {
          event.preventDefault();
          deleteVirtualLoop(selectedLoopId);
          return;
        }
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
    const signature = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges, virtualLoops);
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
  }, [definition, graphId, graphVersion, virtualEdges, virtualLoops, virtualNodeIds]);

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
  const selectedVirtualLoop = useMemo(
    () => virtualLoops.find((loop) => loop.id === selectedLoopId) ?? null,
    [selectedLoopId, virtualLoops]
  );
  const inspectorMode = selectedEdge || selectedVirtualEdge ? "edge" : selectedVirtualLoop ? "loop" : selectedVirtualNode ? "virtual" : selectedNode ? "node" : "graph";
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
  const isUnsaved = useMemo(() => {
    if (!definition) return false;
    const currentSignature = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges, virtualLoops);
    return currentSignature !== lastSavedSignatureRef.current;
  }, [definition, graphId, graphVersion, virtualEdges, virtualLoops, virtualNodeIds]);

  useEffect(() => {
    if (!canvasSearchOpen || !canvasSearchQuery.trim()) return;
    const first = canvasSearchMatches[0];
    setCanvasSearchIndex(0);
    if (first) focusCanvasNode(first.id);
  }, [canvasSearchMatches, canvasSearchOpen, canvasSearchQuery]);

  useEffect(() => {
    if (!selectedNode) {
      setNodeConfigText("{}");
      return;
    }
    setNodeConfigText(stringifyJSON(selectedNode.config ?? {}));
  }, [selectedNode]);

  useEffect(() => {
    const condition = selectedEdge?.condition ?? selectedVirtualEdge?.condition;
    if (!condition) {
      setEdgeConfigText("{}");
      return;
    }
    setEdgeConfigText(stringifyJSON(condition.config ?? {}));
  }, [selectedEdge, selectedVirtualEdge]);

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
    setSelectedLoopId(null);
    setVirtualNodeIds(defaultVirtualNodeIds);
    setVirtualEdges([]);
    setVirtualLoops([]);
    setLocalStatus("new graph");
    setGraphMenuOpen(false);
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
      definition: withSavedGraphWorkspaceState(definition, virtualNodeIds, virtualEdges, virtualLoops),
    });
    setActiveDraftId(draft.id);
    setDrafts(readLocalGraphDrafts());
    lastSavedSignatureRef.current = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges, virtualLoops);
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
    lastSavedSignatureRef.current = autoSaveSignature(draft.definition, draft.graphId, draft.graphVersion, savedState.virtualNodeIds, savedState.virtualEdges, savedState.virtualLoops);
    setVirtualNodeIds(savedState.virtualNodeIds);
    setVirtualEdges(savedState.virtualEdges);
    setVirtualLoops(savedState.virtualLoops);
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
    setLocalStatus(`loaded ${draft.title}`);
    setGraphMenuOpen(false);
    const viewportKey = graphCanvasViewportStorageKey(draft.graphId, draft.graphVersion, draft.id, draft.definition);
    if (!hasStoredGraphCanvasViewport(viewportKey)) {
      window.setTimeout(() => setFitViewSignal((value) => value + 1), 80);
    }
  }

  function deleteDraft() {
    if (!activeDraftId) return;
    const nextDrafts = deleteLocalGraphDraft(activeDraftId);
    setDrafts(nextDrafts);
    setActiveDraftId("");
    setLocalStatus("draft deleted");
  }

  function deleteCurrentGraph() {
    deleteDraft();
    setGraphMenuOpen(false);
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
      setSelectedLoopId(null);
      setContextMenu(null);
      return;
    }
    const next = addNodeToGraph(definition, nodeType, position);
    const node = next.nodes.at(-1);
    setDefinition(next);
    setSelectedNodeId(node?.id ?? null);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
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
    setSelectedLoopId(null);
    setContextMenu(null);
    setLocalStatus(`${virtualNodeLabel(nodeID)} ready`);
  }

  function addVirtualLoop(position?: NodePosition) {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    const loopID = nextVirtualLoopId(virtualLoops);
    const loop: VirtualGraphLoop = {
      id: loopID,
      name: "Loop",
      nodeIds: [],
    };
    setVirtualLoops((current) => [...current, loop]);
    if (position) {
      setDefinition(withNodePosition(definition, loopID, position));
    }
    setSelectedLoopId(loopID);
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setContextMenu(null);
    setLocalStatus("loop created");
  }

  function changeSelectedVirtualLoop(update: (loop: VirtualGraphLoop) => VirtualGraphLoop) {
    if (!selectedVirtualLoop) return;
    setVirtualLoops((current) =>
      current.map((loop) => (loop.id === selectedVirtualLoop.id ? normalizeVirtualLoop(update({ ...loop })) : loop))
    );
  }

  function deleteVirtualLoop(loopID = selectedLoopId) {
    if (!loopID) return;
    setVirtualLoops((current) => current.filter((loop) => loop.id !== loopID));
    if (selectedLoopId === loopID) setSelectedLoopId(null);
    setContextMenu(null);
    setLocalStatus("loop deleted");
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
    const oldID = selectedNode.id;
    setDefinition(renameGraphNode(definition, selectedNode.id, nextID));
    setVirtualLoops((current) =>
      current.map((loop) => ({
        ...loop,
        nodeIds: loop.nodeIds.map((nodeID) => (nodeID === oldID ? nextID : nodeID)),
      }))
    );
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
      setSelectedLoopId(null);
      setContextMenu(null);
      setLocalStatus(`${virtualNodeLabel(nodeID)} hidden`);
      return;
    }
    removeVirtualEdgesForNode(nodeID);
    setVirtualLoops((current) =>
      current.map((loop) => ({ ...loop, nodeIds: loop.nodeIds.filter((memberID) => memberID !== nodeID) }))
    );
    updateDefinition((current) => removeGraphNode(current, nodeID));
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
    setContextMenu(null);
    setLocalStatus("node deleted");
  }

  function changeSelectedEdge(update: (edge: GraphEdgeSpec) => GraphEdgeSpec) {
    if (!definition || !selectedEdgeId) return;
    const previousIndex = (definition.edges ?? []).findIndex((edge, index) => graphEdgeId(edge, index) === selectedEdgeId);
    let next = updateGraphEdge(definition, selectedEdgeId, update);
    if (next === definition) {
      setLocalStatus("edge already exists");
      return;
    }
    const nextEdge = previousIndex >= 0 ? next.edges?.[previousIndex] : undefined;
    if (nextEdge?.to === END_NODE_REF && next.finish_point === nextEdge.from) {
      next = { ...next, finish_point: undefined };
    }
    setDefinition(next);
    setSelectedEdgeId(nextEdge ? graphEdgeId(nextEdge, previousIndex) : null);
  }

  function changeSelectedVirtualEdge(update: (edge: VirtualGraphEdge) => VirtualGraphEdge) {
    if (!definition || !selectedVirtualEdge) return;
    const updated = update({ ...selectedVirtualEdge });
    const sourceKind = virtualNodeKind(updated.from);
    const targetKind = virtualNodeKind(updated.to);
    if (updated.kind === "entry" && (sourceKind !== "start" || targetKind)) {
      setLocalStatus("invalid entry edge");
      return;
    }
    if (updated.kind === "finish" && (sourceKind || targetKind !== "end")) {
      setLocalStatus("invalid finish edge");
      return;
    }

    if (updated.kind === "finish") {
      const graphEdge: GraphEdgeSpec = {
        from: updated.from,
        to: END_NODE_REF,
        condition: updated.condition,
      };
      const existingIndex = (definition.edges ?? []).findIndex((edge) => edge.from === graphEdge.from && edge.to === graphEdge.to);
      const nextEdges = [...(definition.edges ?? [])];
      const nextIndex = existingIndex >= 0 ? existingIndex : nextEdges.length;
      nextEdges[nextIndex] = graphEdge;
      setVirtualEdges((current) => current.filter((edge) => edge.id !== selectedVirtualEdge.id));
      setDefinition({
        ...definition,
        finish_point: definition.finish_point === selectedVirtualEdge.from ? undefined : definition.finish_point,
        edges: nextEdges,
      });
      setSelectedEdgeId(graphEdgeId(graphEdge, nextIndex));
      setLocalStatus("edge updated");
      return;
    }

    const nextEdge = {
      ...updated,
      id: virtualEdgeId(updated.from, updated.to, updated.kind),
    };
    if (updated.kind === "entry" && definition.entry_point === updated.to && selectedVirtualEdge.to !== updated.to) {
      setLocalStatus("edge already exists");
      return;
    }
    setVirtualEdges((current) => upsertVirtualEdge(current, selectedVirtualEdge, nextEdge));
    setDefinition(
      nextEdge.kind === "entry"
        ? { ...definition, entry_point: nextEdge.to }
        : {
            ...definition,
            finish_point: nextEdge.kind === "finish" ? nextEdge.from : definition.finish_point,
          }
    );
    setSelectedEdgeId(nextEdge.id);
    setLocalStatus("edge updated");
  }

  function applyEdgeConfig() {
    if (!selectedEdge?.condition && !selectedVirtualEdge?.condition) return;
    try {
      const config = parseJSONObject(edgeConfigText);
      if (selectedVirtualEdge) {
        changeSelectedVirtualEdge((edge) => ({
          ...edge,
          condition: edge.condition ? { ...edge.condition, config } : edge.condition,
        }));
      } else {
        changeSelectedEdge((edge) => ({
          ...edge,
          condition: edge.condition ? { ...edge.condition, config } : edge.condition,
        }));
      }
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
    setSelectedLoopId(null);
    setContextMenu(null);
    setLocalStatus("edge deleted");
  }

  function addVirtualEdge(edge: Omit<VirtualGraphEdge, "id">): string {
    const nextEdge = { ...edge, id: virtualEdgeId(edge.from, edge.to, edge.kind) };
    setVirtualEdges((current) => {
      const next = current.filter((item) => {
        if (item.id === nextEdge.id) return false;
        if (nextEdge.kind === "entry" && item.kind === "entry" && item.from === nextEdge.from) return false;
        return true;
      });
      return [...next, nextEdge];
    });
    setSelectedEdgeId(nextEdge.id);
    setSelectedLoopId(null);
    return nextEdge.id;
  }

  function deleteVirtualEdge(edgeId: string) {
    const edge = displayVirtualEdges.find((item) => item.id === edgeId);
    const remainingEdges = virtualEdges.filter((item) => item.id !== edgeId);
    setVirtualEdges(remainingEdges);
    if (edge && definition) {
      if (edge.kind === "entry") {
        setDefinition({ ...definition, entry_point: lastVirtualEdge(remainingEdges, "entry")?.to });
      }
      if (edge.kind === "finish" && definition.finish_point === edge.from) {
        setDefinition({ ...definition, finish_point: lastVirtualEdge(remainingEdges, "finish")?.from });
      }
    }
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
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
      if (definition.entry_point === target) {
        const existingEdge = displayVirtualEdges.find((edge) => edge.kind === "entry" && edge.from === source && edge.to === target);
        setSelectedNodeId(null);
        setSelectedEdgeId(existingEdge?.id ?? null);
        setSelectedLoopId(null);
        setLocalStatus("edge already exists");
        return;
      }
      const edgeId = addVirtualEdge({ from: source, to: target, kind: "entry" });
      setDefinition({ ...definition, entry_point: target });
      setSelectedNodeId(null);
      setSelectedEdgeId(edgeId);
      setSelectedLoopId(null);
      setLocalStatus("entry connected");
      return;
    }
    if (targetKind === "end") {
      const existingEdgeIndex = findGraphEdgeIndex(definition, source, END_NODE_REF);
      if (existingEdgeIndex >= 0) {
        const existingEdge = definition.edges?.[existingEdgeIndex];
        setSelectedNodeId(null);
        setSelectedEdgeId(existingEdge ? graphEdgeId(existingEdge, existingEdgeIndex) : null);
        setSelectedLoopId(null);
        setLocalStatus("edge already exists");
        return;
      }
      const next = addGraphEdge(definition, source, END_NODE_REF);
      const edgeId = findLastEdgeId(next, source, END_NODE_REF);
      setDefinition({
        ...next,
        finish_point: next.finish_point === source ? undefined : next.finish_point,
      });
      setSelectedNodeId(null);
      setSelectedEdgeId(edgeId);
      setSelectedLoopId(null);
      setLocalStatus("edge connected");
      return;
    }
    const existingEdgeIndex = findGraphEdgeIndex(definition, source, target);
    if (existingEdgeIndex >= 0) {
      const existingEdge = definition.edges?.[existingEdgeIndex];
      setSelectedNodeId(null);
      setSelectedEdgeId(existingEdge ? graphEdgeId(existingEdge, existingEdgeIndex) : null);
      setSelectedLoopId(null);
      setLocalStatus("edge already exists");
      return;
    }
    const next = addGraphEdge(definition, source, target);
    const edgeId = findLastEdgeId(next, source, target);
    setDefinition(next);
    setSelectedNodeId(null);
    setSelectedEdgeId(edgeId);
    setSelectedLoopId(null);
    setLocalStatus("edge connected");
  }

  function openCreateMenu(position: NodePosition, screen: NodePosition) {
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
    setContextMenu({ kind: "pane", position, screen });
  }

  function openNodeMenu(nodeId: string, screen: NodePosition) {
    setContextMenu({ kind: "node", nodeId, screen });
  }

  function openEdgeMenu(edgeId: string, screen: NodePosition) {
    setContextMenu({ kind: "edge", edgeId, screen });
  }

  function openLoopMenu(loopId: string, screen: NodePosition) {
    setContextMenu({ kind: "loop", loopId, screen });
  }

  function moveNode(nodeID: string, position: NodePosition) {
    updateDefinition((current) => withNodePosition(current, nodeID, position));
  }

  function handleLoopDrag(loopId: string, delta: NodePosition) {
    const loop = virtualLoops.find((g) => g.id === loopId);
    if (!loop || !definition) return;
    const positions = graphNodePositions(definition);
    let next = definition;
    for (const nodeId of loop.nodeIds) {
      const pos = positions.get(nodeId);
      if (pos) {
        next = withNodePosition(next, nodeId, { x: pos.x + delta.x, y: pos.y + delta.y });
      }
    }
    setDefinition(next);
  }

  function focusCanvasNode(nodeID: string) {
    setSelectedNodeId(nodeID);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
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
    setSelectedLoopId(null);
    setLocalStatus("node duplicated");
  }

  function applyAutoLayout() {
    if (!definition) {
      setLocalStatus("invalid graph json");
      return;
    }
    setDefinition(autoLayoutGraph(definition, virtualNodeIds, displayVirtualEdges, virtualLoops));
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
      setSelectedLoopId(null);
      return;
    }
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
  }

  const inspectorTitle =
    inspectorMode === "edge"
      ? "Edge Properties"
      : inspectorMode === "loop"
        ? selectedVirtualLoop?.name || "Loop Properties"
      : inspectorMode === "virtual"
        ? selectedVirtualNode?.name ?? "Virtual Node"
        : inspectorMode === "node"
          ? selectedNode?.name || selectedNode?.id || "Node Properties"
          : "Graph Properties";

  return (
    <div
      className="relative grid h-full min-h-0"
      style={{ gridTemplateColumns: "minmax(0,1fr) 380px" }}
    >
      {titleSlot
        ? createPortal(
            <GraphTitleMenu
              activeDraftId={activeDraftId}
              definition={definition}
              drafts={drafts}
              graphId={graphId}
              graphMenuOpen={graphMenuOpen}
              graphSwitchDisabled={graphSwitchDisabled}
              unsaved={isUnsaved}
              onCreateGraph={createGraph}
              onDeleteGraph={deleteCurrentGraph}
              onLoadDraft={loadDraft}
              onOpenChange={setGraphMenuOpen}
            />,
            titleSlot
          )
        : null}

      <section className="relative min-h-0 bg-canvas">
        <GraphCanvas
          definition={definition}
          steps={steps}
          selectedRunId={selectedRunId}
          editable
          selectedNodeId={selectedNodeId ?? undefined}
          selectedEdgeId={selectedEdgeId ?? undefined}
          selectedLoopId={selectedLoopId ?? undefined}
          fitViewSignal={fitViewSignal}
          focusNodeId={focusNodeId}
          focusNodeSignal={focusNodeSignal}
          viewportStorageKey={graphCanvasViewportStorageKey(graphId, graphVersion, activeDraftId, definition)}
          highlightedNodeIds={highlightedNodeIds}
          onSelectNode={setSelectedNodeId}
          onSelectEdge={setSelectedEdgeId}
          onSelectLoop={setSelectedLoopId}
          onNodePositionChange={moveNode}
          onAutoLayout={applyAutoLayout}
          onConnectNodes={connectNodes}
          onCreateNodeAt={openCreateMenu}
          onNodeContextMenu={openNodeMenu}
          onEdgeContextMenu={openEdgeMenu}
          onLoopContextMenu={openLoopMenu}
          onLoopDrag={handleLoopDrag}
          virtualNodeIds={virtualNodeIds}
          virtualEdges={displayVirtualEdges}
          virtualLoops={virtualLoops}
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
        selectedVirtualLoop={selectedVirtualLoop}
        selectedVirtualEdge={selectedVirtualEdge}
        visibleVirtualNodes={visibleVirtualNodes}
        onApplyEdgeConfig={applyEdgeConfig}
        onApplyNodeConfig={applyNodeConfig}
        onChangeEdge={changeSelectedEdge}
        onChangeEdgeConfigText={setEdgeConfigText}
        onChangeVirtualLoop={changeSelectedVirtualLoop}
        onChangeVirtualEdge={changeSelectedVirtualEdge}
        onChangeGraphField={changeGraphField}
        onChangeInitialStateText={onInitialStateText}
        onChangeNode={changeSelectedNode}
        onChangeNodeConfigText={setNodeConfigText}
        onChangeNodeId={changeSelectedNodeId}
        onDeleteEdge={deleteSelectedEdge}
        onDeleteLoop={deleteVirtualLoop}
        onDeleteNode={deleteSelectedNode}
        onSelectLintIssue={selectLintIssue}
      />

      {contextMenu ? (
        <CanvasContextMenu
          contextMenu={contextMenu}
          paletteNodeTypes={paletteNodeTypes}
          onAddLoop={addVirtualLoop}
          onAddNode={addNode}
          onAddVirtualNode={addVirtualNode}
          onClose={() => setContextMenu(null)}
          onDeleteEdge={deleteSelectedEdge}
          onDeleteLoop={deleteVirtualLoop}
          onDeleteNode={deleteSelectedNode}
        />
      ) : null}
    </div>
  );
});

function graphCanvasViewportStorageKey(
  graphId: string,
  graphVersion: string,
  activeDraftId: string,
  definition: GraphDefinition | null
): string {
  return [
    activeDraftId || "server",
    graphId || definition?.name || "graph",
    graphVersion || definition?.version || "1.0",
  ]
    .map((part) => encodeURIComponent(part.trim() || "-"))
    .join(":");
}

function GraphTitleMenu({
  activeDraftId,
  definition,
  drafts,
  graphId,
  graphMenuOpen,
  graphSwitchDisabled,
  unsaved,
  onCreateGraph,
  onDeleteGraph,
  onLoadDraft,
  onOpenChange,
}: {
  activeDraftId: string;
  definition: GraphDefinition | null;
  drafts: LocalGraphDraft[];
  graphId: string;
  graphMenuOpen: boolean;
  graphSwitchDisabled: boolean;
  unsaved: boolean;
  onCreateGraph: () => void;
  onDeleteGraph: () => void;
  onLoadDraft: (draft: LocalGraphDraft) => void;
  onOpenChange: (open: boolean) => void;
}) {
  const title = definition?.name || graphId || "Untitled graph";
  const displayTitle = unsaved ? `*${title}` : title;

  return (
    <div data-graph-title-menu className="relative min-w-0">
      <button
        type="button"
        className="flex max-w-[360px] min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left hover:bg-accent"
        onClick={() => onOpenChange(!graphMenuOpen)}
        aria-expanded={graphMenuOpen}
        title={displayTitle}
      >
        <span className="truncate text-sm font-semibold">{displayTitle}</span>
        <ChevronDown className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${graphMenuOpen ? "rotate-180" : ""}`} />
      </button>

      {graphMenuOpen ? (
        <div className="absolute left-0 top-12 z-50 w-80 overflow-hidden rounded-md border border-border bg-panel shadow-lg">
          <div className="flex items-center gap-2 border-b border-border p-2">
            <Button variant="outline" size="sm" onClick={onCreateGraph} disabled={graphSwitchDisabled} title="New graph">
              <FilePlus2 className="h-4 w-4" />
              New
            </Button>
            <Button
              variant="ghost"
              size="icon"
              onClick={onDeleteGraph}
              disabled={!activeDraftId}
              title="Delete graph"
              aria-label="Delete graph"
              className="ml-auto"
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>

          <div className="max-h-80 overflow-auto">
            {drafts.length === 0 ? (
              <div className="px-3 py-3 text-sm text-muted-foreground">No local graphs</div>
            ) : (
              drafts.map((draft) => (
                <button
                  key={draft.id}
                  type="button"
                  className={`grid w-full gap-1 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-accent ${
                    draft.id === activeDraftId ? "bg-accent" : ""
                  } ${graphSwitchDisabled ? "cursor-not-allowed opacity-50 hover:bg-transparent" : ""}`}
                  onClick={() => onLoadDraft(draft)}
                  disabled={graphSwitchDisabled}
                >
                  <div className="truncate text-sm font-medium">{draft.title}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {draft.definition.nodes.length} nodes / {formatTime(draft.updatedAt)}
                  </div>
                </button>
              ))
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}

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
  const addOrReplace = (edge: VirtualGraphEdge) => {
    if (!seen.has(edge.id)) {
      seen.add(edge.id);
      result.push(edge);
      return;
    }
    const index = result.findIndex((item) => item.id === edge.id);
    if (index >= 0) result[index] = { ...result[index], ...edge };
  };
  for (const edge of primary) {
    addOrReplace(edge);
  }
  for (const edge of secondary) {
    addOrReplace(edge);
  }
  return result;
}

function upsertVirtualEdge(
  edges: VirtualGraphEdge[],
  previousEdge: VirtualGraphEdge,
  nextEdge: VirtualGraphEdge
): VirtualGraphEdge[] {
  const next = edges.filter((edge) => {
    if (edge.id === previousEdge.id) return false;
    if (edge.id === nextEdge.id) return false;
    if (nextEdge.kind === "entry" && edge.kind === "entry" && edge.from === nextEdge.from) return false;
    return true;
  });
  return [...next, nextEdge];
}

function withSavedGraphWorkspaceState(
  definition: GraphDefinition,
  virtualNodeIds: string[],
  virtualEdges: VirtualGraphEdge[],
  virtualLoops: VirtualGraphLoop[]
): GraphDefinition {
  const metadata = { ...(definition.metadata ?? {}) };
  const web = isRecord(metadata.web) ? { ...metadata.web } : {};
  web.virtual_node_ids = virtualNodeIds;
  web.virtual_edges = virtualEdges.map((edge) => ({
    id: edge.id,
    from: edge.from,
    to: edge.to,
    kind: edge.kind,
    condition: edge.condition,
  }));
  web.virtual_loops = virtualLoops.map((loop) => ({
    id: loop.id,
    name: loop.name,
    node_ids: loop.nodeIds,
  }));
  metadata.web = web;
  return { ...definition, metadata };
}

function savedGraphWorkspaceState(definition: GraphDefinition): {
  virtualNodeIds: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
} {
  const web = isRecord(definition.metadata?.web) ? definition.metadata.web : undefined;
  const rawNodeIds = Array.isArray(web?.virtual_node_ids) ? web.virtual_node_ids : [];
  const virtualNodeIds = rawNodeIds.filter((item): item is string => typeof item === "string" && item.trim() !== "");
  const rawEdges = Array.isArray(web?.virtual_edges) ? web.virtual_edges : [];
  const virtualEdges = rawEdges.filter(isVirtualGraphEdge);
  const rawLoops = Array.isArray(web?.virtual_loops) ? web.virtual_loops : Array.isArray(web?.virtual_groups) ? web.virtual_groups : [];
  const virtualLoops = rawLoops.map(parseVirtualGraphLoop).filter((loop): loop is VirtualGraphLoop => Boolean(loop));
  return {
    virtualNodeIds: virtualNodeIds.length ? virtualNodeIds : defaultVirtualNodeIds,
    virtualEdges,
    virtualLoops,
  };
}

function isVirtualGraphEdge(value: unknown): value is VirtualGraphEdge {
  if (!isRecord(value)) return false;
  const condition = value.condition;
  return (
    typeof value.id === "string" &&
    typeof value.from === "string" &&
    typeof value.to === "string" &&
    (value.kind === "entry" || value.kind === "finish") &&
    (condition === undefined || isGraphConditionSpec(condition))
  );
}

function parseVirtualGraphLoop(value: unknown): VirtualGraphLoop | null {
  if (!isRecord(value)) return null;
  const nodeIds = value.nodeIds ?? value.node_ids;
  if (
    typeof value.id !== "string" ||
    (value.name !== undefined && typeof value.name !== "string") ||
    !Array.isArray(nodeIds) ||
    !nodeIds.every((item) => typeof item === "string")
  ) {
    return null;
  }
  return normalizeVirtualLoop({
    id: value.id,
    name: value.name,
    nodeIds,
  });
}

function normalizeVirtualLoop(loop: VirtualGraphLoop): VirtualGraphLoop {
  return {
    id: loop.id.trim(),
    name: loop.name?.trim(),
    nodeIds: uniqueStrings(loop.nodeIds),
  };
}

function isGraphConditionSpec(value: unknown): boolean {
  if (!isRecord(value)) return false;
  return typeof value.type === "string" && (value.config === undefined || isRecord(value.config));
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

function nextVirtualLoopId(loops: VirtualGraphLoop[]): string {
  const used = new Set(loops.map((loop) => loop.id));
  for (let index = 1; index < 1000; index += 1) {
    const id = index === 1 ? "loop" : `loop:${index}`;
    if (!used.has(id)) return id;
  }
  return `loop:${Date.now().toString(36)}`;
}

function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const item = value.trim();
    if (!item || seen.has(item)) continue;
    seen.add(item);
    result.push(item);
  }
  return result;
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
  virtualEdges: VirtualGraphEdge[],
  virtualLoops: VirtualGraphLoop[]
): string {
  return JSON.stringify({
    graphId,
    graphVersion,
    definition: withSavedGraphWorkspaceState(definition, virtualNodeIds, virtualEdges, virtualLoops),
  });
}

function isEditableKeyboardTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tagName = target.tagName.toLowerCase();
  return tagName === "input" || tagName === "textarea" || tagName === "select";
}
