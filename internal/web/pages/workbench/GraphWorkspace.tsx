import { memo, useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { createPortal } from "react-dom";
import { ChevronDown, FilePlus2, Search, Trash2, X } from "lucide-react";
import { createTrigger, deleteTrigger, listTriggers, updateTrigger } from "../../api";
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
import { detectVirtualGraphLoops, mergeVirtualGraphLoops } from "../../lib/loopPresentation";
import type {
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  GraphSettings,
  GraphSettingsUpdate,
  InitialStateRequirements,
  NodeTypeSchema,
  RegistryInfo,
  StepRecord,
  ToolDefinition,
  Trigger,
  TriggerType,
} from "../../types";
import { CanvasContextMenu } from "./graph-workspace/CanvasContextMenu";
import { defaultVirtualNodeIds, fallbackNodeTypes } from "./graph-workspace/constants";
import { autoLayoutGraph } from "./graph-workspace/layout";
import { buildGraphLintIssues, type GraphLintIssue } from "./graph-workspace/lint";
import { ToastStack, type ToastRecord } from "./graph-workspace/ToastStack";
import { GraphInspectorPanel } from "./graph-workspace/GraphInspectorPanel";
import { TriggerInspector } from "./graph-workspace/TriggerInspector";
import {
  projectTriggerCanvasNodes,
  withCleanTriggerCanvasPositions,
  withTriggerCanvasPosition,
} from "./graph-workspace/triggerCanvas";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./graph-workspace/types";
import { buildTriggerPayload, triggerEditorValues, triggerStatePathSuggestions } from "./triggerEditor";
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
const inspectorWidthStorageKey = "weaveflow.graphWorkspace.inspectorWidth";
const defaultInspectorWidth = 380;
const minInspectorWidth = 320;
const maxInspectorWidth = 720;
const minCanvasWidth = 360;

interface GraphWorkspaceProps {
  definition: GraphDefinition | null;
  definitionText: string;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  steps: StepRecord[];
  selectedRunId: string;
  registry: RegistryInfo | null;
  toolDefinitions: ToolDefinition[];
  graphSettings: GraphSettings | null;
  onUpdateGraphSettings: (settings: GraphSettingsUpdate) => Promise<GraphSettings>;
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
  definitionText,
  initialStateText,
  initialRequirements,
  initialRequirementsError,
  steps,
  selectedRunId,
  registry,
  toolDefinitions,
  graphSettings,
  onUpdateGraphSettings,
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
  const [graphTriggers, setGraphTriggers] = useState<Trigger[]>([]);
  const [triggersHydrated, setTriggersHydrated] = useState(false);
  const [triggerLoadError, setTriggerLoadError] = useState("");
  const [selectedTriggerId, setSelectedTriggerId] = useState<string | null>(null);
  const [nodeConfigText, setNodeConfigText] = useState("{}");
  const [edgeConfigText, setEdgeConfigText] = useState("{}");
  const [, setLocalStatus] = useState("local ready");
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
  const [inspectorWidth, setInspectorWidth] = useState(readStoredInspectorWidth);
  const autoLoadedDraftRef = useRef(false);
  const autoSaveHydratedRef = useRef(false);
  const autoSaveTimerRef = useRef<number | null>(null);
  const activeDraftIdRef = useRef("");
  const lastSavedSignatureRef = useRef("");
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const workspaceRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLElement | null>(null);
  const triggerRequestRef = useRef(0);

  const refreshGraphTriggers = useCallback(async () => {
    const request = ++triggerRequestRef.current;
    const targetGraphID = graphId.trim();
    if (!targetGraphID) {
      setGraphTriggers([]);
      setTriggersHydrated(true);
      setTriggerLoadError("");
      return [];
    }
    try {
      const items = await listTriggers();
      if (request !== triggerRequestRef.current) return null;
      const matched = items.filter((item) => item.target?.graph_id?.trim() === targetGraphID);
      setGraphTriggers(matched);
      setTriggersHydrated(true);
      setTriggerLoadError("");
      return matched;
    } catch (err) {
      if (request !== triggerRequestRef.current) return null;
      setTriggersHydrated(false);
      setTriggerLoadError(err instanceof Error ? err.message : String(err));
      return null;
    }
  }, [graphId]);
  const validTriggerIDs = useMemo(
    () => triggersHydrated ? graphTriggers.map((trigger) => trigger.id) : undefined,
    [graphTriggers, triggersHydrated]
  );

  useEffect(() => {
    setDrafts(readLocalGraphDrafts());
  }, []);

  useEffect(() => {
    setTitleSlot(document.getElementById("graph-title-slot"));
  }, []);

  useEffect(() => {
    setGraphTriggers([]);
    setTriggersHydrated(false);
    setTriggerLoadError("");
    setSelectedTriggerId(null);
    void refreshGraphTriggers();
    return () => {
      triggerRequestRef.current += 1;
    };
  }, [refreshGraphTriggers]);

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
    const signature = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges, virtualLoops, validTriggerIDs);
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
  }, [definition, graphId, graphVersion, validTriggerIDs, virtualEdges, virtualLoops, virtualNodeIds]);

  useEffect(() => {
    activeDraftIdRef.current = activeDraftId;
  }, [activeDraftId]);

  useEffect(() => {
    const clampWidth = () => {
      setInspectorWidth((current) => {
        const next = clampInspectorWidth(current, workspaceRef.current?.clientWidth);
        if (next !== current) writeStoredInspectorWidth(next);
        return next;
      });
    };
    window.addEventListener("resize", clampWidth);
    return () => window.removeEventListener("resize", clampWidth);
  }, []);

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
  const displayVirtualLoops = useMemo(
    () => mergeVirtualGraphLoops(virtualLoops, detectVirtualGraphLoops(definition)),
    [definition, virtualLoops]
  );
  const selectedNode = useMemo(
    () => definition?.nodes.find((node) => node.id === selectedNodeId) ?? null,
    [definition, selectedNodeId]
  );
  const visibleVirtualNodes = useMemo(() => virtualNodeIds.map(virtualNodeSpec), [virtualNodeIds]);
  const triggerCanvasNodes = useMemo(
    () => projectTriggerCanvasNodes(definition, graphId, graphTriggers, virtualNodeIds),
    [definition, graphId, graphTriggers, virtualNodeIds]
  );
  const selectedTrigger = useMemo(
    () => graphTriggers.find((trigger) => trigger.id === selectedTriggerId) ?? null,
    [graphTriggers, selectedTriggerId]
  );
  const triggerStatePaths = useMemo(() => triggerStatePathSuggestions(definition), [definition]);
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
    () => buildGraphLintIssues({ definition, initialStateText, initialRequirements, analysisError: initialRequirementsError, registry }),
    [definition, initialRequirements, initialRequirementsError, initialStateText, registry]
  );
  const selectedVirtualEdge = useMemo(
    () => displayVirtualEdges.find((edge) => edge.id === selectedEdgeId) ?? null,
    [displayVirtualEdges, selectedEdgeId]
  );
  const selectedVirtualLoop = useMemo(
    () => displayVirtualLoops.find((loop) => loop.id === selectedLoopId) ?? null,
    [displayVirtualLoops, selectedLoopId]
  );
  const inspectorMode = selectedEdge || selectedVirtualEdge ? "edge" : selectedVirtualLoop ? "loop" : selectedVirtualNode ? "virtual" : selectedNode ? "node" : "graph";
  const triggerInspectorOpen = Boolean(selectedTrigger);
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
    const currentSignature = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges, virtualLoops, validTriggerIDs);
    return currentSignature !== lastSavedSignatureRef.current;
  }, [definition, graphId, graphVersion, validTriggerIDs, virtualEdges, virtualLoops, virtualNodeIds]);

  useEffect(() => {
    if (selectedTriggerId && triggersHydrated && !selectedTrigger) setSelectedTriggerId(null);
  }, [selectedTrigger, selectedTriggerId, triggersHydrated]);

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
    const next = createGraphDefinition(nextName, defaultGraphNodeType, registry?.state_modules);
    onGraphId(next.name || nextName);
    onGraphVersion(next.version || "2.0");
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
    const blockingIssue = lintIssues.find((issue) => issue.severity === "error");
    if (blockingIssue) {
      setLocalStatus(`cannot save: ${blockingIssue.message}`);
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
      definition: withSavedGraphWorkspaceState(definition, virtualNodeIds, virtualEdges, virtualLoops, validTriggerIDs),
    });
    setActiveDraftId(draft.id);
    setDrafts(readLocalGraphDrafts());
    lastSavedSignatureRef.current = autoSaveSignature(definition, graphId, graphVersion, virtualNodeIds, virtualEdges, virtualLoops, validTriggerIDs);
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
      let next = createGraphDefinition(graphId || "debug_graph", nodeType, registry?.state_modules);
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

  function changeSelectedVirtualLoop(update: (loop: VirtualGraphLoop) => VirtualGraphLoop) {
    if (!selectedVirtualLoop || selectedVirtualLoop.automatic) return;
    setVirtualLoops((current) =>
      current.map((loop) => (loop.id === selectedVirtualLoop.id ? normalizeVirtualLoop(update({ ...loop })) : loop))
    );
  }

  function deleteVirtualLoop(loopID = selectedLoopId) {
    if (!loopID) return;
    const loop = displayVirtualLoops.find((item) => item.id === loopID);
    if (loop?.automatic) {
      setLocalStatus("automatic loop follows graph edges");
      return;
    }
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
        return !(nextEdge.kind === "entry" && item.kind === "entry" && item.from === nextEdge.from);

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
    setSelectedTriggerId(null);
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

  function openTriggerMenu(triggerId: string, screen: NodePosition) {
    const trigger = graphTriggers.find((item) => item.id === triggerId);
    if (!trigger) return;
    selectTrigger(triggerId);
    setContextMenu({ kind: "trigger", triggerId, enabled: trigger.enabled, screen });
  }

  function moveNode(nodeID: string, position: NodePosition) {
    updateDefinition((current) => withNodePosition(current, nodeID, position));
  }

  function moveTrigger(triggerID: string, position: NodePosition) {
    if (!definition) return;
    setDefinition(withTriggerCanvasPosition(definition, triggerID, position, graphTriggers.map((trigger) => trigger.id)));
  }

  function selectTrigger(triggerID: string | null) {
    setSelectedTriggerId(triggerID);
    if (!triggerID) return;
    setSelectedNodeId(null);
    setSelectedEdgeId(null);
    setSelectedLoopId(null);
  }

  async function createTriggerAt(type: TriggerType, position: NodePosition) {
    setContextMenu(null);
    const targetGraphID = graphId.trim();
    const sourceDefinition = definition;
    const request = triggerRequestRef.current;
    if (!targetGraphID || !sourceDefinition || !triggersHydrated) return;
    try {
      const values = triggerEditorValues(null, { graph_id: targetGraphID }, type);
      const saved = await createTrigger(buildTriggerPayload(values, null));
      if (request !== triggerRequestRef.current) return;
      const triggerIDs = [...graphTriggers.map((trigger) => trigger.id), saved.id];
      setGraphTriggers((current) => [...current.filter((trigger) => trigger.id !== saved.id), saved]);
      setDefinition(withTriggerCanvasPosition(sourceDefinition, saved.id, position, triggerIDs));
      selectTrigger(saved.id);
      setTriggerLoadError("");
      await refreshGraphTriggers();
    } catch (err) {
      if (request === triggerRequestRef.current) {
        setTriggerLoadError(err instanceof Error ? err.message : String(err));
      }
    }
  }

  async function handleTriggerSaved(saved: Trigger) {
    setGraphTriggers((current) => [...current.filter((trigger) => trigger.id !== saved.id), saved]);
    setSelectedTriggerId(saved.id);
    await refreshGraphTriggers();
  }

  async function handleTriggerDeleted() {
    setSelectedTriggerId(null);
    await refreshGraphTriggers();
  }

  function editTriggerFromMenu(triggerId: string) {
    selectTrigger(triggerId);
    setContextMenu(null);
  }

  async function toggleTriggerFromMenu(triggerId: string, enabled: boolean) {
    setContextMenu(null);
    const trigger = graphTriggers.find((item) => item.id === triggerId);
    if (!trigger) return;
    try {
      const values = triggerEditorValues(trigger, trigger.target ?? { graph_id: graphId });
      values.enabled = enabled;
      const saved = await updateTrigger(trigger.id, buildTriggerPayload(values, trigger));
      setGraphTriggers((current) => current.map((item) => (item.id === saved.id ? saved : item)));
      setTriggerLoadError("");
      await refreshGraphTriggers();
    } catch (err) {
      setTriggerLoadError(err instanceof Error ? err.message : String(err));
    }
  }

  async function deleteTriggerFromMenu(triggerId: string) {
    setContextMenu(null);
    const trigger = graphTriggers.find((item) => item.id === triggerId);
    if (!trigger || !window.confirm(`Delete trigger ${trigger.id}?`)) return;
    try {
      await deleteTrigger(trigger.id);
      if (selectedTriggerId === trigger.id) setSelectedTriggerId(null);
      setTriggerLoadError("");
      await refreshGraphTriggers();
    } catch (err) {
      setTriggerLoadError(err instanceof Error ? err.message : String(err));
    }
  }

  function handleLoopDrag(loopId: string, delta: NodePosition) {
    const loop = displayVirtualLoops.find((item) => item.id === loopId);
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
      state: cloneJSONRecord(selectedNode.state ?? {}),
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
    setDefinition(autoLayoutGraph(definition, virtualNodeIds, displayVirtualEdges, displayVirtualLoops));
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

  function startInspectorResize(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const workspaceRight = workspaceRef.current?.getBoundingClientRect().right ?? window.innerWidth;
    const workspaceWidth = workspaceRef.current?.clientWidth;
    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;
    const onMove = (moveEvent: PointerEvent) => {
      const nextWidth = clampInspectorWidth(workspaceRight - moveEvent.clientX, workspaceWidth);
      setInspectorWidth(nextWidth);
      writeStoredInspectorWidth(nextWidth);
    };
    const stopResize = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", stopResize);
      window.removeEventListener("pointercancel", stopResize);
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
    };
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", stopResize);
    window.addEventListener("pointercancel", stopResize);
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
      ref={workspaceRef}
      className="relative grid h-full min-h-0"
      style={{ gridTemplateColumns: `minmax(0,1fr) 6px ${inspectorWidth}px` }}
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

      <section ref={canvasRef} className="relative min-h-0 bg-canvas">
        <GraphCanvas
          definition={definition}
          steps={steps}
          selectedRunId={selectedRunId}
          editable
          selectedNodeId={selectedNodeId ?? undefined}
          selectedEdgeId={selectedEdgeId ?? undefined}
          selectedLoopId={selectedLoopId ?? undefined}
          selectedTriggerId={selectedTriggerId ?? undefined}
          fitViewSignal={fitViewSignal}
          focusNodeId={focusNodeId}
          focusNodeSignal={focusNodeSignal}
          viewportStorageKey={graphCanvasViewportStorageKey(graphId, graphVersion, activeDraftId, definition)}
          highlightedNodeIds={highlightedNodeIds}
          nodeTypes={paletteNodeTypes}
          onSelectNode={(nodeID) => {
            setSelectedNodeId(nodeID);
            if (nodeID) {
              setSelectedTriggerId(null);
            }
          }}
          onSelectEdge={(edgeID) => {
            setSelectedEdgeId(edgeID);
            if (edgeID) {
              setSelectedTriggerId(null);
            }
          }}
          onSelectLoop={(loopID) => {
            setSelectedLoopId(loopID);
            if (loopID) {
              setSelectedTriggerId(null);
            }
          }}
          onSelectTrigger={selectTrigger}
          onNodePositionChange={moveNode}
          onTriggerPositionChange={moveTrigger}
          onAutoLayout={applyAutoLayout}
          onConnectNodes={connectNodes}
          onCreateNodeAt={openCreateMenu}
          onNodeContextMenu={openNodeMenu}
          onEdgeContextMenu={openEdgeMenu}
          onLoopContextMenu={openLoopMenu}
          onTriggerContextMenu={openTriggerMenu}
          onLoopDrag={handleLoopDrag}
          virtualNodeIds={virtualNodeIds}
          virtualEdges={displayVirtualEdges}
          virtualLoops={displayVirtualLoops}
          triggerNodes={triggerCanvasNodes}
        />
        {triggerLoadError ? (
          <div className="absolute right-4 top-4 z-30 max-w-sm rounded border border-destructive/40 bg-panel p-2 text-xs text-destructive shadow-sm">
            Trigger unavailable: {triggerLoadError}
          </div>
        ) : null}
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

      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize inspector"
        title="Drag to resize"
        onPointerDown={startInspectorResize}
        className="relative z-30 cursor-col-resize bg-border transition-colors hover:bg-primary/50"
      >
        <span className="absolute inset-y-0 -left-2 -right-2" />
      </div>

      {triggerInspectorOpen ? (
        <TriggerInspector
          graphID={graphId}
          trigger={selectedTrigger}
          statePathSuggestions={triggerStatePaths}
          onSaved={handleTriggerSaved}
          onDeleted={handleTriggerDeleted}
        />
      ) : (
      <GraphInspectorPanel
        conditions={conditions}
        definition={definition}
        definitionText={definitionText}
        edgeConfigText={edgeConfigText}
        initialRequirements={initialRequirements}
        initialRequirementsError={initialRequirementsError}
        initialStateText={initialStateText}
        inspectorMode={inspectorMode}
        inspectorTitle={inspectorTitle}
        lintIssues={lintIssues}
        nodeConfigText={nodeConfigText}
        paletteNodeTypes={paletteNodeTypes}
        registry={registry}
        registryLoaded={Boolean(registry)}
        toolDefinitions={toolDefinitions}
        graphSettings={graphSettings}
        onUpdateGraphSettings={onUpdateGraphSettings}
        selectedEdge={selectedEdge}
        selectedNode={selectedNode}
        selectedVirtualLoop={selectedVirtualLoop}
        selectedVirtualEdge={selectedVirtualEdge}
        steps={steps}
        visibleVirtualNodes={visibleVirtualNodes}
        onApplyEdgeConfig={applyEdgeConfig}
        onApplyNodeConfig={applyNodeConfig}
        onChangeDefinitionText={onDefinitionText}
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
      )}

      {contextMenu ? (
        <CanvasContextMenu
          boundaryRef={canvasRef}
          contextMenu={contextMenu}
          canCreateTrigger={Boolean(graphId.trim() && triggersHydrated)}
          nodeGroups={registry?.node_groups ?? []}
          paletteNodeTypes={paletteNodeTypes}
          onAddNode={addNode}
          onAddVirtualNode={addVirtualNode}
          onCreateTrigger={(type, position) => void createTriggerAt(type, position)}
          onClose={() => setContextMenu(null)}
          onDeleteEdge={deleteSelectedEdge}
          onDeleteLoop={deleteVirtualLoop}
          onDeleteNode={deleteSelectedNode}
          onDeleteTrigger={(triggerId) => void deleteTriggerFromMenu(triggerId)}
          onEditTrigger={editTriggerFromMenu}
          onToggleTrigger={(triggerId, enabled) => void toggleTriggerFromMenu(triggerId, enabled)}
        />
      ) : null}
    </div>
  );
});

function readStoredInspectorWidth(): number {
  if (typeof window === "undefined") return defaultInspectorWidth;
  try {
    const raw = window.localStorage.getItem(inspectorWidthStorageKey);
    const parsed = raw ? Number(raw) : NaN;
    if (!Number.isFinite(parsed)) return clampInspectorWidth(defaultInspectorWidth);
    return clampInspectorWidth(parsed);
  } catch {
    return defaultInspectorWidth;
  }
}

function writeStoredInspectorWidth(width: number): void {
  if (typeof window === "undefined" || !Number.isFinite(width)) return;
  try {
    window.localStorage.setItem(inspectorWidthStorageKey, String(Math.round(width)));
  } catch {
    // Inspector width persistence is best effort.
  }
}

function clampInspectorWidth(width: number, containerWidth?: number): number {
  const availableWidth =
    typeof containerWidth === "number" && Number.isFinite(containerWidth)
      ? containerWidth
      : typeof window === "undefined"
        ? defaultInspectorWidth + minCanvasWidth
        : window.innerWidth;
  const maxByContainer = Math.max(minInspectorWidth, availableWidth - minCanvasWidth - 6);
  const maxWidth = Math.max(minInspectorWidth, Math.min(maxInspectorWidth, maxByContainer));
  return Math.max(minInspectorWidth, Math.min(maxWidth, Math.round(width)));
}

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
  const scriptBadgeCount = graphScriptBadgeCount(definition);

  return (
    <div data-graph-title-menu className="relative min-w-0">
      <button
        type="button"
        className="flex max-w-[360px] min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left hover:bg-accent"
        onClick={() => onOpenChange(!graphMenuOpen)}
        aria-expanded={graphMenuOpen}
        title={scriptBadgeCount > 0 ? `${displayTitle} (${scriptBadgeCount} scripts)` : displayTitle}
      >
        <span className="min-w-0 truncate text-sm font-semibold">{displayTitle}</span>
        <ScriptCountBadge count={scriptBadgeCount} />
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
              drafts.map((draft) => {
                const draftScriptBadgeCount = graphScriptBadgeCount(draft.definition);
                return (
                  <button
                    key={draft.id}
                    type="button"
                    className={`grid w-full gap-1 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-accent ${
                      draft.id === activeDraftId ? "bg-accent" : ""
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

function graphScriptBadgeCount(definition: GraphDefinition | null): number {
  if (!definition) return 0;
  const metadata = isRecord(definition.metadata) ? definition.metadata : undefined;
  return scriptGroupCount(definition) + scriptGroupCount(metadata) + scriptGroupCount(metadata?.web);
}

function scriptGroupCount(value: unknown): number {
  if (!isRecord(value)) return 0;
  let count = 0;
  count += scriptValueCount(value.pre);
  count += scriptValueCount(value.post);
  count += scriptValueCount(value.before);
  count += scriptValueCount(value.after);
  count += scriptValueCount(value.pre_script);
  count += scriptValueCount(value.post_script);
  count += scriptValueCount(value.pre_scripts);
  count += scriptValueCount(value.post_scripts);
  count += scriptValueCount(value.preScript);
  count += scriptValueCount(value.postScript);
  count += scriptValueCount(value.preScripts);
  count += scriptValueCount(value.postScripts);
  count += scriptContainerCount(value.scripts);
  count += scriptContainerCount(value.hooks);
  return count;
}

function scriptContainerCount(value: unknown): number {
  if (!isRecord(value)) return 0;
  return (
    scriptValueCount(value.pre) +
    scriptValueCount(value.post) +
    scriptValueCount(value.before) +
    scriptValueCount(value.after) +
    scriptValueCount(value.pre_script) +
    scriptValueCount(value.post_script)
  );
}

function scriptValueCount(value: unknown): number {
  if (typeof value === "string") return value.trim() ? 1 : 0;
  if (Array.isArray(value)) return value.reduce((total, item) => total + scriptValueCount(item), 0);
  if (isRecord(value)) return Object.keys(value).length > 0 ? 1 : 0;
  return value ? 1 : 0;
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
    return !(nextEdge.kind === "entry" && edge.kind === "entry" && edge.from === nextEdge.from);

  });
  return [...next, nextEdge];
}

function withSavedGraphWorkspaceState(
  definition: GraphDefinition,
  virtualNodeIds: string[],
  virtualEdges: VirtualGraphEdge[],
  virtualLoops: VirtualGraphLoop[],
  validTriggerIDs?: string[]
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
  const next = { ...definition, metadata };
  return validTriggerIDs ? withCleanTriggerCanvasPositions(next, validTriggerIDs) : next;
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
  virtualLoops: VirtualGraphLoop[],
  validTriggerIDs?: string[]
): string {
  return JSON.stringify({
    graphId,
    graphVersion,
    definition: withSavedGraphWorkspaceState(definition, virtualNodeIds, virtualEdges, virtualLoops, validTriggerIDs),
  });
}

function isEditableKeyboardTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tagName = target.tagName.toLowerCase();
  return tagName === "input" || tagName === "textarea" || tagName === "select";
}
