import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { getGraphDetail } from "../../api";
import {
  GraphCanvas,
  hasStoredGraphCanvasViewport,
  type VirtualGraphEdge,
  type VirtualGraphLoop,
} from "../../components/GraphCanvas";
import {
  createGraphDefinition,
  createGraphID,
  graphEdgeId,
  updateGraphNode,
  withNodePosition,
  type NodePosition,
} from "../../lib/graphEditor";
import {
  hydrateServerGraph,
  isHydratedLocalGraph,
  type LocalGraph,
} from "../../lib/localGraphs";
import { stringifyJSON } from "../../lib/utils";
import { detectVirtualGraphLoops, mergeVirtualGraphLoops } from "../../lib/loopPresentation";
import type {
  GraphDefinition,
  GraphDetail,
  GraphEdgeSpec,
  GraphNodeSpec,
  InitialStateRequirements,
  NodeTypeSchema,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  StepRecord,
  ToolDefinition,
  Trigger,
  TriggerType,
} from "../../types";
import { CanvasContextMenu } from "./graph-workspace/CanvasContextMenu";
import { CanvasSearch } from "./graph-workspace/CanvasSearch";
import { defaultVirtualNodeIds, fallbackNodeTypes } from "./graph-workspace/constants";
import { autoLayoutGraph } from "./graph-workspace/layout";
import { buildGraphLintIssues, type GraphLintIssue } from "./graph-workspace/lint";
import { ToastStack, type ToastRecord } from "./graph-workspace/ToastStack";
import { GraphInspectorPanel } from "./graph-workspace/GraphInspectorPanel";
import { GraphTitleMenu } from "./graph-workspace/GraphTitleMenu";
import {
  GraphTransferDialog,
  type GraphTransferMode,
} from "./graph-workspace/GraphTransferDialog";
import {
  resolveGraphImportConflicts,
  type ParsedGraphImport,
} from "./graph-workspace/graphTransferModel";
import { TriggerInspector } from "./graph-workspace/TriggerInspector";
import { useCanvasSearch } from "./graph-workspace/useCanvasSearch";
import type { GraphTriggerController, TriggerDraftSetup } from "./graph-workspace/useGraphTriggers";
import { useGraphWorkspaceSelection } from "./graph-workspace/useGraphWorkspaceSelection";
import { useLocalGraphs } from "./graph-workspace/useLocalGraphs";
import { useResizableInspector } from "./graph-workspace/useResizableInspector";
import {
  projectTriggerCanvasNodes,
  withTriggerCanvasPosition,
} from "./graph-workspace/triggerCanvas";
import {
  graphCanvasViewportStorageKey,
  mergeVirtualEdges,
  savedGraphWorkspaceState,
  uniqueStrings,
  virtualEdgesFromDefinition,
  withSavedGraphWorkspaceState,
} from "./graph-workspace/graphWorkspaceModel";
import {
  connectGraphWorkspaceNodes,
  deleteGraphWorkspaceEdge,
  updateGraphWorkspaceEdge,
  updateGraphWorkspaceVirtualEdge,
  type GraphWorkspaceEdgeMutation,
} from "./graph-workspace/graphWorkspaceEdgeModel";
import {
  addGraphWorkspaceNode,
  addGraphWorkspaceVirtualNode,
  deleteGraphWorkspaceNode,
  duplicateGraphWorkspaceNode,
  renameGraphWorkspaceNode,
  type GraphWorkspaceNodeMutation,
} from "./graph-workspace/graphWorkspaceNodeModel";
import {
  deleteGraphWorkspaceLoop,
  moveGraphWorkspaceLoop,
  updateGraphWorkspaceLoop,
  type GraphWorkspaceLoopMutation,
} from "./graph-workspace/graphWorkspaceLoopModel";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./graph-workspace/types";
import { triggerStatePathSuggestions, type TriggerEditorValues } from "./triggerEditor";
import {
  parseJSONObject,
  realNodeTypes,
  virtualNodeSpec,
} from "./graph-workspace/utils";

interface GraphWorkspaceProps {
  definition: GraphDefinition | null;
  definitionText: string;
  initialStateText: string;
  initialRequirements: InitialStateRequirements | null;
  directInitialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  steps: StepRecord[];
  selectedRunId: string;
  registry: RegistryInfo | null;
  toolDefinitions: ToolDefinition[];
  runtimeSettings: RuntimeSettings;
  graphTriggers: GraphTriggerController;
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
  onReplaceRuntimeSettings: (settings: RuntimeSettings) => void;
  graphId: string;
  graphVersion: string;
  serverGraphsLoaded: boolean;
  graphSwitchDisabled: boolean;
  toasts: ToastRecord[];
  onGraphId: (value: string) => void;
  onGraphVersion: (value: string) => void;
  onDefinitionText: (value: string) => void;
  onInitialStateText: (value: string) => void;
  onDismissToast: (id: string) => void;
  onGraphSwitch: () => boolean;
  onGraphDetailLoaded: (detail: GraphDetail) => void;
}

export const GraphWorkspace = memo(function GraphWorkspace({
  definition,
  definitionText,
  initialStateText,
  initialRequirements,
  directInitialRequirements,
  initialRequirementsError,
  steps,
  selectedRunId,
  registry,
  toolDefinitions,
  runtimeSettings,
  graphTriggers: graphTriggerController,
  onChangeRuntimeSettings,
  onReplaceRuntimeSettings,
  graphId,
  graphVersion,
  serverGraphsLoaded,
  graphSwitchDisabled,
  toasts,
  onGraphId,
  onGraphVersion,
  onDefinitionText,
  onInitialStateText,
  onDismissToast,
  onGraphSwitch,
  onGraphDetailLoaded,
}: GraphWorkspaceProps) {
  const [nodeConfigText, setNodeConfigText] = useState("{}");
  const [edgeConfigText, setEdgeConfigText] = useState("{}");
  const [, setLocalStatus] = useState("local ready");
  const [contextMenu, setContextMenu] = useState<CanvasContextMenuState | null>(null);
  const [graphMenuOpen, setGraphMenuOpen] = useState(false);
  const [graphTransferMode, setGraphTransferMode] = useState<GraphTransferMode | null>(null);
  const [titleSlot, setTitleSlot] = useState<HTMLElement | null>(null);
  const [virtualNodeIds, setVirtualNodeIds] = useState<string[]>(defaultVirtualNodeIds);
  const [virtualEdges, setVirtualEdges] = useState<VirtualGraphEdge[]>([]);
  const [virtualLoops, setVirtualLoops] = useState<VirtualGraphLoop[]>([]);
  const [fitViewSignal, setFitViewSignal] = useState(0);
  const autoLoadedGraphRef = useRef(false);
  const canvasRef = useRef<HTMLElement | null>(null);
  const {
    workspaceRef,
    width: inspectorWidth,
    startResize: startInspectorResize,
  } = useResizableInspector();
  const {
    triggers: graphTriggers,
    hydrated: triggersHydrated,
    loadError: triggerLoadError,
    selectedDraft: selectedTriggerDraft,
    selectedTrigger,
    selectedTriggerID: selectedTriggerId,
    validTriggerIDs,
    knownTriggerIDs,
    setSelectedTriggerID: setSelectedTriggerId,
    stageImport: stageTriggerImport,
    createForGraph: createGraphTrigger,
    updateDraft: updateTriggerDraft,
    updateEnabled: updateTriggerEnabled,
    remove: removeGraphTrigger,
  } = graphTriggerController;
  const {
    selectedNodeID: selectedNodeId,
    selectedEdgeID: selectedEdgeId,
    selectedLoopID: selectedLoopId,
    selectNode: setSelectedNodeId,
    selectEdge: setSelectedEdgeId,
    selectLoop: setSelectedLoopId,
    selectTrigger,
    clearSelection,
  } = useGraphWorkspaceSelection(setSelectedTriggerId);

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
    const handleKey = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      if ((event.ctrlKey || event.metaKey) && key === "f") {
        event.preventDefault();
        canvasSearch.setOpen(true);
        return;
      }
      if (isEditableKeyboardTarget(event.target)) return;
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

  const nodeTypes = registry?.node_types;
  const paletteNodeTypes = useMemo(
    () => realNodeTypes(nodeTypes?.length ? nodeTypes : fallbackNodeTypes),
    [nodeTypes]
  );
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
    () => projectTriggerCanvasNodes(definition, graphId, graphTriggers, virtualNodeIds, registry?.chat_channels ?? []),
    [definition, graphId, graphTriggers, registry?.chat_channels, virtualNodeIds]
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
  const exportDefinition = useMemo(
    () => definition
      ? withSavedGraphWorkspaceState(
          definition,
          virtualNodeIds,
          virtualEdges,
          virtualLoops,
          validTriggerIDs
        )
      : null,
    [definition, validTriggerIDs, virtualEdges, virtualLoops, virtualNodeIds]
  );
  const blockingSaveMessage = lintIssues.find((issue) => issue.severity === "error")?.message;
  const {
    graphs,
    cacheHydrated,
    activeCacheID,
    activateGraph,
    resetActiveGraph,
    deleteActiveGraph,
  } = useLocalGraphs({
    snapshot: {
      definition,
      graphID: graphId,
      graphVersion,
      runtimeSettings,
      virtualNodeIDs: virtualNodeIds,
      virtualEdges,
      virtualLoops,
      validTriggerIDs,
    },
    serverGraphsLoaded,
    blockingSaveMessage,
    onStatus: setLocalStatus,
  });
  useEffect(() => {
    if (autoLoadedGraphRef.current || !cacheHydrated) return;
    autoLoadedGraphRef.current = true;
    const cachedGraph = definition
      ? graphs.find((item) =>
          item.definition &&
          item.graphId === graphId &&
          item.graphVersion === graphVersion &&
          stringifyJSON(item.definition) === stringifyJSON(definition)
        )
      : undefined;
    if (cachedGraph) void loadCachedGraphWithoutGuard(cachedGraph);
  }, [cacheHydrated, definition, graphId, graphVersion, graphs]);
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
  const canvasSearch = useCanvasSearch(searchableNodes, setSelectedNodeId);
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

  function applyEdgeMutation(mutation: GraphWorkspaceEdgeMutation) {
    if (mutation.definition) setDefinition(mutation.definition);
    setVirtualEdges(mutation.virtualEdges);
    if (mutation.selectedEdgeID !== undefined) setSelectedEdgeId(mutation.selectedEdgeID);
    setLocalStatus(mutation.message);
  }

  function applyNodeMutation(mutation: GraphWorkspaceNodeMutation) {
    if (mutation.definition) setDefinition(mutation.definition);
    setVirtualNodeIds(mutation.virtualNodeIDs);
    setVirtualEdges(mutation.virtualEdges);
    setVirtualLoops(mutation.virtualLoops);
    if (mutation.selectedNodeID !== undefined) setSelectedNodeId(mutation.selectedNodeID);
    setLocalStatus(mutation.message);
  }

  function applyLoopMutation(mutation: GraphWorkspaceLoopMutation) {
    if (mutation.definition) setDefinition(mutation.definition);
    setVirtualLoops(mutation.virtualLoops);
    if (mutation.selectedLoopID !== undefined) setSelectedLoopId(mutation.selectedLoopID);
    setLocalStatus(mutation.message);
  }

  function createGraph() {
    if (!onGraphSwitch()) {
      setLocalStatus("run active");
      return;
    }
    const nextName = createGraphID();
    const next = createGraphDefinition(nextName, undefined, registry?.state_modules);
    onGraphId(next.name || nextName);
    onGraphVersion(next.version || "2.0");
    onDefinitionText(stringifyJSON(next));
    resetActiveGraph();
    clearSelection();
    setVirtualNodeIds(defaultVirtualNodeIds);
    setVirtualEdges([]);
    setVirtualLoops([]);
    setFitViewSignal((value) => value + 1);
    setLocalStatus("new graph");
    setGraphMenuOpen(false);
  }

  function loadCachedGraph(graph: LocalGraph) {
    if (!onGraphSwitch()) {
      setLocalStatus("run active");
      return;
    }
    void loadCachedGraphWithoutGuard(graph);
  }

  async function loadCachedGraphWithoutGuard(graph: LocalGraph) {
    let resolvedGraph = graph;
    if (!isHydratedLocalGraph(resolvedGraph)) {
      setLocalStatus(`loading ${graph.title}`);
      try {
        const detail = await getGraphDetail(graph.graphId);
        resolvedGraph = hydrateServerGraph(graph, detail);
        onGraphDetailLoaded(detail);
      } catch (error) {
        setLocalStatus(error instanceof Error ? error.message : String(error));
        return;
      }
    }
    if (!isHydratedLocalGraph(resolvedGraph)) return;
    const activation = activateGraph(resolvedGraph);
    const savedState = activation.workspaceState;
    onGraphId(resolvedGraph.graphId);
    onGraphVersion(resolvedGraph.graphVersion);
    onDefinitionText(stringifyJSON(resolvedGraph.definition));
    onReplaceRuntimeSettings(activation.runtimeSettings);
    setVirtualNodeIds(savedState.virtualNodeIDs);
    setVirtualEdges(savedState.virtualEdges);
    setVirtualLoops(savedState.virtualLoops);
    clearSelection();
    setLocalStatus(`loaded ${resolvedGraph.title}`);
    setGraphMenuOpen(false);
    const viewportKey = graphCanvasViewportStorageKey(
      resolvedGraph.graphId,
      resolvedGraph.graphVersion,
      resolvedGraph.id,
      resolvedGraph.definition
    );
    if (!hasStoredGraphCanvasViewport(viewportKey)) {
      setFitViewSignal((value) => value + 1);
    }
  }

  function deleteCurrentGraph() {
    deleteActiveGraph();
    setGraphMenuOpen(false);
  }

  function openGraphTransfer(mode: GraphTransferMode) {
    setGraphMenuOpen(false);
    setGraphTransferMode(mode);
  }

  function importGraph(graph: ParsedGraphImport): boolean {
    if (!onGraphSwitch()) {
      setLocalStatus("run active");
      return false;
    }
    const resolvedGraph = resolveGraphImportConflicts(
      graph,
      [graphId, ...graphs.map((item) => item.graphId)],
      [definition?.name ?? "", ...graphs.map((item) => item.definition?.name ?? item.title)],
      [...knownTriggerIDs, ...graphTriggers.map((trigger) => trigger.id)]
    );
    const nextGraphID = resolvedGraph.graphID;
    const nextGraphVersion = resolvedGraph.graphVersion || resolvedGraph.definition.version || "2.0";
    const workspaceState = savedGraphWorkspaceState(resolvedGraph.definition);
    if (resolvedGraph.triggers) stageTriggerImport(nextGraphID, resolvedGraph.triggers);
    onGraphId(nextGraphID);
    onGraphVersion(nextGraphVersion);
    onDefinitionText(stringifyJSON(resolvedGraph.definition));
    if (resolvedGraph.settings) onReplaceRuntimeSettings(resolvedGraph.settings);
    resetActiveGraph();
    setVirtualNodeIds(workspaceState.virtualNodeIDs);
    setVirtualEdges(workspaceState.virtualEdges);
    setVirtualLoops(workspaceState.virtualLoops);
    clearSelection();
    setLocalStatus(`imported ${resolvedGraph.definition.name || nextGraphID}`);
    setFitViewSignal((value) => value + 1);
    return true;
  }

  function addNode(nodeType: NodeTypeSchema, position?: NodePosition) {
    applyNodeMutation(
      addGraphWorkspaceNode(
        { definition, virtualNodeIDs: virtualNodeIds, virtualEdges, virtualLoops },
        nodeType,
        graphId,
        registry?.state_modules,
        position
      )
    );
    setContextMenu(null);
  }

  function addVirtualNode(kind: VirtualNodeKind, position?: NodePosition) {
    applyNodeMutation(
      addGraphWorkspaceVirtualNode(
        { definition, virtualNodeIDs: virtualNodeIds, virtualEdges, virtualLoops },
        kind,
        position
      )
    );
    setContextMenu(null);
  }

  function changeSelectedVirtualLoop(update: (loop: VirtualGraphLoop) => VirtualGraphLoop) {
    if (!selectedVirtualLoop) return;
    applyLoopMutation(
      updateGraphWorkspaceLoop(
        { definition, virtualLoops },
        selectedVirtualLoop,
        update
      )
    );
  }

  function deleteVirtualLoop(loopID = selectedLoopId) {
    if (!loopID) return;
    applyLoopMutation(
      deleteGraphWorkspaceLoop(
        { definition, virtualLoops },
        displayVirtualLoops,
        loopID,
        selectedLoopId
      )
    );
    setContextMenu(null);
  }

  function changeGraphField<Key extends keyof GraphDefinition>(key: Key, value: GraphDefinition[Key]) {
    updateDefinition((current) => ({ ...current, [key]: value }));
  }

  function changeSelectedNode(update: (node: GraphNodeSpec) => GraphNodeSpec) {
    if (!selectedNode) return;
    updateDefinition((current) => updateGraphNode(current, selectedNode.id, update));
  }

  function changeSelectedNodeId(value: string) {
    if (!selectedNode) return;
    applyNodeMutation(
      renameGraphWorkspaceNode(
        { definition, virtualNodeIDs: virtualNodeIds, virtualEdges, virtualLoops },
        selectedNode.id,
        value
      )
    );
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
    applyNodeMutation(
      deleteGraphWorkspaceNode(
        {
          definition,
          virtualNodeIDs: virtualNodeIds,
          virtualEdges,
          virtualLoops,
          displayVirtualEdges,
        },
        nodeID
      )
    );
    setContextMenu(null);
  }

  function changeSelectedEdge(update: (edge: GraphEdgeSpec) => GraphEdgeSpec) {
    if (!selectedEdgeId) return;
    applyEdgeMutation(
      updateGraphWorkspaceEdge({ definition, virtualEdges }, selectedEdgeId, update)
    );
  }

  function changeSelectedVirtualEdge(update: (edge: VirtualGraphEdge) => VirtualGraphEdge) {
    if (!selectedVirtualEdge) return;
    applyEdgeMutation(
      updateGraphWorkspaceVirtualEdge(
        { definition, virtualEdges },
        selectedVirtualEdge,
        update
      )
    );
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
    applyEdgeMutation(
      deleteGraphWorkspaceEdge(
        { definition, virtualEdges, displayVirtualEdges },
        edgeId
      )
    );
    setContextMenu(null);
  }

  function connectNodes(source: string, target: string) {
    applyEdgeMutation(
      connectGraphWorkspaceNodes(
        { definition, virtualEdges, displayVirtualEdges },
        source,
        target
      )
    );
  }

  function openCreateMenu(position: NodePosition, screen: NodePosition) {
    clearSelection();
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

  async function createTriggerAt(type: TriggerType, position: NodePosition) {
    setContextMenu(null);
    const sourceDefinition = definition;
    if (!sourceDefinition) return;
    const created = await createGraphTrigger(type);
    if (!created) return;
    setDefinition(withTriggerCanvasPosition(sourceDefinition, created.trigger.id, position, created.triggerIDs));
    selectTrigger(created.trigger.id);
  }

  const handleTriggerChanged = useCallback((values: TriggerEditorValues, setup: TriggerDraftSetup) => {
    if (selectedTriggerId && updateTriggerDraft(selectedTriggerId, values, setup)) {
      selectTrigger(selectedTriggerId);
    }
  }, [selectTrigger, selectedTriggerId, updateTriggerDraft]);

  function editTriggerFromMenu(triggerID: string) {
    selectTrigger(triggerID);
    setContextMenu(null);
  }

  async function toggleTriggerFromMenu(triggerID: string, enabled: boolean) {
    setContextMenu(null);
    await updateTriggerEnabled(triggerID, enabled);
  }

  async function deleteTriggerFromMenu(triggerID: string) {
    setContextMenu(null);
    const trigger = graphTriggers.find((item) => item.id === triggerID);
    if (!trigger || !window.confirm(`Delete trigger ${trigger.id}?`)) return;
    await removeGraphTrigger(trigger.id);
  }

  function handleLoopDrag(loopId: string, delta: NodePosition) {
    applyLoopMutation(
      moveGraphWorkspaceLoop(
        { definition, virtualLoops },
        displayVirtualLoops,
        loopId,
        delta
      )
    );
  }

  function duplicateSelectedNode() {
    if (!selectedNode) return;
    applyNodeMutation(
      duplicateGraphWorkspaceNode(
        { definition, virtualNodeIDs: virtualNodeIds, virtualEdges, virtualLoops },
        selectedNode.id
      )
    );
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
    if (issue.nodeID) {
      canvasSearch.focusNode(issue.nodeID);
      return;
    }
    if (issue.edgeID) {
      setSelectedEdgeId(issue.edgeID);
      return;
    }
    clearSelection();
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
              activeCacheID={activeCacheID}
              definition={definition}
              graphs={graphs}
              graphID={graphId}
              open={graphMenuOpen}
              graphSwitchDisabled={graphSwitchDisabled}
              onCreateGraph={createGraph}
              onDeleteGraph={deleteCurrentGraph}
              onExportGraph={() => openGraphTransfer("export")}
              onImportGraph={() => openGraphTransfer("import")}
              onLoadGraph={loadCachedGraph}
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
          focusNodeId={canvasSearch.focusNodeID}
          focusNodeSignal={canvasSearch.focusNodeSignal}
          viewportStorageKey={graphCanvasViewportStorageKey(graphId, graphVersion, activeCacheID, definition)}
          highlightedNodeIds={canvasSearch.highlightedNodeIDs}
          nodeTypes={paletteNodeTypes}
          onSelectNode={setSelectedNodeId}
          onSelectEdge={setSelectedEdgeId}
          onSelectLoop={setSelectedLoopId}
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
        <CanvasSearch search={canvasSearch} />
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
          values={selectedTriggerDraft!.values}
          persisted={selectedTriggerDraft!.persisted}
          chatSetupChannelID={selectedTriggerDraft!.chatSetupChannelID}
          chatSetupSessionID={selectedTriggerDraft!.chatSetupSessionID}
          statePathSuggestions={triggerStatePaths}
          chatChannels={registry?.chat_channels ?? []}
          onChange={handleTriggerChanged}
        />
      ) : (
      <GraphInspectorPanel
        conditions={conditions}
        definition={definition}
        definitionText={definitionText}
        edgeConfigText={edgeConfigText}
        initialRequirements={initialRequirements}
        directInitialRequirements={directInitialRequirements}
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
        runtimeSettings={runtimeSettings}
        onChangeRuntimeSettings={onChangeRuntimeSettings}
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

      {graphTransferMode && typeof document !== "undefined"
        ? createPortal(
            <GraphTransferDialog
              mode={graphTransferMode}
              definition={exportDefinition}
              graphID={graphId}
              graphVersion={graphVersion}
              runtimeSettings={runtimeSettings}
              triggers={graphTriggers}
              onClose={() => setGraphTransferMode(null)}
              onImport={importGraph}
            />,
            document.body
          )
        : null}
    </div>
  );
});

function isEditableKeyboardTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tagName = target.tagName.toLowerCase();
  return tagName === "input" || tagName === "textarea" || tagName === "select";
}
