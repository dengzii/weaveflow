import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  BaseEdge,
  EdgeLabelRenderer,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  getBezierPath,
  useEdgesState,
  useNodesState,
  useReactFlow,
  useStoreApi,
  type Connection,
  type Edge,
  type EdgeProps,
  type Node,
  type Viewport,
} from "@xyflow/react";
import type { GraphDefinition, NodeTypeSchema, RunStatus, RuntimeEvent, StepRecord, TriggerCanvasNode } from "../types";
import { END_NODE_REF, START_NODE_REF, type NodePosition } from "../lib/graphEditor";
import type { VirtualGraphLoop } from "../lib/loopPresentation";
import { subscribeRuntimeEvents } from "../lib/runtimeEvents";
import { GraphCanvasControls } from "./GraphCanvasControls";
import { GraphLoopNode, GraphNode, GraphTriggerNode } from "./GraphCanvasNodes";
import {
  buildGraphCanvasElements,
  type VirtualGraphEdge,
} from "./graphCanvasElements";
import {
  applyRuntime,
  applyRuntimeEvent,
  applyRuntimeSnapshot,
  resetRuntimeNodes,
  runtimeFromExecution,
  timeRank,
  updateRuntimeNode,
  updateRuntimeNodeProjection,
  virtualNodeKind,
  type FlowNodeData,
  type RuntimeNodeState,
} from "./graphCanvasModel";
import {
  fitNodesToViewport,
  maxGraphCanvasZoom,
  minGraphCanvasZoom,
  normalizeViewportStorageKey,
  readStoredCanvasViewport,
  sameViewport,
  syncRendererZoomState,
  writeStoredCanvasViewport,
} from "./graphCanvasViewport";

export { graphNodeDimensions } from "./graphCanvasModel";
export { hasStoredGraphCanvasViewport } from "./graphCanvasViewport";

export type { VirtualGraphLoop } from "../lib/loopPresentation";
export type { VirtualGraphEdge } from "./graphCanvasElements";

export interface GraphCanvasPositionChanges {
  nodePositions: Map<string, NodePosition>;
  triggerPositions: Map<string, NodePosition>;
}

const graphCanvasNodeTypes = {
  debugNode: GraphNode,
  debugLoop: GraphLoopNode,
  debugTrigger: GraphTriggerNode,
};
const graphCanvasEdgeTypes = {
  condition: FlowEdge,
  flow: FlowEdge,
};
const emptyConfigurationErrors = new Map<string, readonly string[]>();
const emptyRuntime = new Map<string, RuntimeNodeState>();
const emptyRuntimeEvents: RuntimeEvent[] = [];

export function GraphCanvas({
  definition,
  steps,
  events = emptyRuntimeEvents,
  selectedRunId,
  runtimeVisible = true,
  runStatus,
  runTriggerId,
  currentNodeIds = [],
  runUpdatedAt,
  editable = false,
  selectedNodeId,
  selectedEdgeId,
  selectedLoopId,
  selectedTriggerId,
  fitViewSignal = 0,
  focusNodeId,
  focusNodeSignal = 0,
  viewportStorageKey,
  highlightedNodeIds = [],
  nodeTypes = [],
  configurationErrors = emptyConfigurationErrors,
  virtualNodeIds = [START_NODE_REF, END_NODE_REF],
  virtualEdges = [],
  virtualLoops = [],
  triggerNodes = [],
  onAutoLayout,
  onSelectNode,
  onSelectEdge,
  onSelectLoop,
  onSelectTrigger,
  onPositionChanges,
  onConnectNodes,
  onCreateNodeAt,
  onNodeContextMenu,
  onEdgeContextMenu,
  onLoopContextMenu,
  onTriggerContextMenu,
  onLoopDrag,
}: {
  definition: GraphDefinition | null;
  steps: StepRecord[];
  events?: RuntimeEvent[];
  selectedRunId?: string;
  runtimeVisible?: boolean;
  runStatus?: RunStatus;
  runTriggerId?: string;
  currentNodeIds?: string[];
  runUpdatedAt?: string;
  editable?: boolean;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  selectedLoopId?: string;
  selectedTriggerId?: string;
  fitViewSignal?: number;
  focusNodeId?: string;
  focusNodeSignal?: number;
  viewportStorageKey?: string;
  highlightedNodeIds?: string[];
  nodeTypes?: NodeTypeSchema[];
  configurationErrors?: ReadonlyMap<string, readonly string[]>;
  virtualNodeIds?: string[];
  virtualEdges?: VirtualGraphEdge[];
  virtualLoops?: VirtualGraphLoop[];
  triggerNodes?: TriggerCanvasNode[];
  onAutoLayout?: () => void;
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onSelectLoop?: (groupId: string | null) => void;
  onSelectTrigger?: (triggerId: string | null) => void;
  onPositionChanges?: (changes: GraphCanvasPositionChanges) => void;
  onConnectNodes?: (source: string, target: string) => void;
  onCreateNodeAt?: (position: NodePosition, screenPosition: NodePosition) => void;
  onNodeContextMenu?: (nodeId: string, screenPosition: NodePosition) => void;
  onEdgeContextMenu?: (edgeId: string, screenPosition: NodePosition) => void;
  onLoopContextMenu?: (groupId: string, screenPosition: NodePosition) => void;
  onTriggerContextMenu?: (triggerId: string, screenPosition: NodePosition) => void;
  onLoopDrag?: (groupId: string, delta: NodePosition) => void;
}) {
  return (
    <ReactFlowProvider>
      <GraphCanvasInner
        definition={definition}
        steps={steps}
        events={events}
        selectedRunId={selectedRunId}
        runtimeVisible={runtimeVisible}
        runStatus={runStatus}
        runTriggerId={runTriggerId}
        currentNodeIds={currentNodeIds}
        runUpdatedAt={runUpdatedAt}
        editable={editable}
        selectedNodeId={selectedNodeId}
        selectedEdgeId={selectedEdgeId}
        selectedLoopId={selectedLoopId}
        selectedTriggerId={selectedTriggerId}
        fitViewSignal={fitViewSignal}
        focusNodeId={focusNodeId}
        focusNodeSignal={focusNodeSignal}
        viewportStorageKey={viewportStorageKey}
        highlightedNodeIds={highlightedNodeIds}
        nodeTypes={nodeTypes}
        configurationErrors={configurationErrors}
        virtualNodeIds={virtualNodeIds}
        virtualEdges={virtualEdges}
        virtualLoops={virtualLoops}
        triggerNodes={triggerNodes}
        onAutoLayout={onAutoLayout}
        onSelectNode={onSelectNode}
        onSelectEdge={onSelectEdge}
        onSelectLoop={onSelectLoop}
        onSelectTrigger={onSelectTrigger}
        onPositionChanges={onPositionChanges}
        onConnectNodes={onConnectNodes}
        onCreateNodeAt={onCreateNodeAt}
        onNodeContextMenu={onNodeContextMenu}
        onEdgeContextMenu={onEdgeContextMenu}
        onLoopContextMenu={onLoopContextMenu}
        onTriggerContextMenu={onTriggerContextMenu}
        onLoopDrag={onLoopDrag}
      />
    </ReactFlowProvider>
  );
}

function GraphCanvasInner({
  definition,
  steps,
  events,
  selectedRunId,
  runtimeVisible,
  runStatus,
  runTriggerId,
  currentNodeIds,
  runUpdatedAt,
  editable,
  selectedNodeId,
  selectedEdgeId,
  selectedLoopId,
  selectedTriggerId,
  fitViewSignal,
  focusNodeId,
  focusNodeSignal,
  viewportStorageKey,
  highlightedNodeIds,
  nodeTypes,
  configurationErrors,
  virtualNodeIds,
  virtualEdges,
  virtualLoops,
  triggerNodes,
  onAutoLayout,
  onSelectNode,
  onSelectEdge,
  onSelectLoop,
  onSelectTrigger,
  onPositionChanges,
  onConnectNodes,
  onCreateNodeAt,
  onNodeContextMenu,
  onEdgeContextMenu,
  onLoopContextMenu,
  onTriggerContextMenu,
  onLoopDrag,
}: {
  definition: GraphDefinition | null;
  steps: StepRecord[];
  events: RuntimeEvent[];
  selectedRunId?: string;
  runtimeVisible: boolean;
  runStatus?: RunStatus;
  runTriggerId?: string;
  currentNodeIds: string[];
  runUpdatedAt?: string;
  editable: boolean;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  selectedLoopId?: string;
  selectedTriggerId?: string;
  fitViewSignal: number;
  focusNodeId?: string;
  focusNodeSignal: number;
  viewportStorageKey?: string;
  highlightedNodeIds: string[];
  nodeTypes: NodeTypeSchema[];
  configurationErrors: ReadonlyMap<string, readonly string[]>;
  virtualNodeIds: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
  triggerNodes: TriggerCanvasNode[];
  onAutoLayout?: () => void;
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onSelectLoop?: (groupId: string | null) => void;
  onSelectTrigger?: (triggerId: string | null) => void;
  onPositionChanges?: (changes: GraphCanvasPositionChanges) => void;
  onConnectNodes?: (source: string, target: string) => void;
  onCreateNodeAt?: (position: NodePosition, screenPosition: NodePosition) => void;
  onNodeContextMenu?: (nodeId: string, screenPosition: NodePosition) => void;
  onEdgeContextMenu?: (edgeId: string, screenPosition: NodePosition) => void;
  onLoopContextMenu?: (groupId: string, screenPosition: NodePosition) => void;
  onTriggerContextMenu?: (triggerId: string, screenPosition: NodePosition) => void;
  onLoopDrag?: (groupId: string, delta: NodePosition) => void;
}) {
  const { screenToFlowPosition, viewportInitialized } = useReactFlow();
  const store = useStoreApi();
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<FlowNodeData>>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [interactive, setInteractive] = useState(editable);
  const handledFitViewSignal = useRef(0);
  const flowWrapperRef = useRef<HTMLDivElement | null>(null);
  const restoredViewportKeyRef = useRef("");
  const suppressViewportPersistUntilRef = useRef(0);
  const retainedDraggedNodeIDsRef = useRef<Set<string> | null>(null);
  const nodesRef = useRef<Node<FlowNodeData>[]>([]);
  const edgesRef = useRef<Edge[]>([]);
  const runtimeRef = useRef<Map<string, RuntimeNodeState>>(new Map());
  const runtimeRunIdRef = useRef("");
  const loopDragRef = useRef<{
    groupId: string;
    startPosition: NodePosition;
    memberPositions: Map<string, NodePosition>;
  } | null>(null);
  const isInteractive = editable && interactive;

  useEffect(() => {
    setInteractive(editable);
  }, [editable]);

  const runtimeSeed = useMemo(
    () => runtimeFromExecution(steps, events, selectedRunId),
    [events, selectedRunId, steps]
  );
  const highlightedNodeSet = useMemo(() => new Set(highlightedNodeIds), [highlightedNodeIds]);
  const currentNodeSet = useMemo(() => new Set(currentNodeIds), [currentNodeIds]);
  const currentNodeStartedAt = timeRank(runUpdatedAt);

  useLayoutEffect(() => {
    const nextRunId = selectedRunId ?? "";
    if (runtimeRunIdRef.current === nextRunId) return;
    runtimeRunIdRef.current = nextRunId;
    runtimeRef.current = new Map();
    if (runtimeVisible) setNodes((current) => resetRuntimeNodes(current));
  }, [runtimeVisible, selectedRunId, setNodes]);

  useLayoutEffect(() => {
    if (runtimeSeed.size === 0) return;
    const next = new Map(runtimeRef.current);
    for (const [nodeId, runtime] of runtimeSeed) {
      applyRuntime(next, nodeId, runtime);
    }
    runtimeRef.current = next;
    if (runtimeVisible) setNodes((current) => applyRuntimeSnapshot(current, next));
  }, [runtimeSeed, runtimeVisible, setNodes]);

  useEffect(() => subscribeRuntimeEvents((event) => {
    if (selectedRunId && event.run_id && event.run_id !== selectedRunId) return;
    let switchedRun = false;
    if (event.run_id && runtimeRunIdRef.current !== event.run_id) {
      runtimeRunIdRef.current = event.run_id;
      runtimeRef.current = new Map();
      switchedRun = true;
    }
    if (!event.node_id) {
      if (switchedRun && runtimeVisible) setNodes((current) => resetRuntimeNodes(current));
      return;
    }
    const nodeId = event.node_id;
    const next = new Map(runtimeRef.current);
    const changed = applyRuntimeEvent(next, event);
    if (!changed && !switchedRun) return;
    runtimeRef.current = next;
    if (!runtimeVisible) return;
    const runtime = next.get(nodeId);
    setNodes((current) => {
      const base = switchedRun ? resetRuntimeNodes(current) : current;
      return updateRuntimeNode(base, nodeId, runtime);
    });
  }), [runtimeVisible, selectedRunId, setNodes]);

  const buildCanvasElements = useCallback(
    (runtimeNow = Date.now()) => buildGraphCanvasElements({
      definition,
      editable,
      interactive: isInteractive,
      highlightedNodeIDs: highlightedNodeSet,
      nodeTypes,
      configurationErrors,
      runtime: runtimeVisible ? runtimeRef.current : emptyRuntime,
      runtimeVisible,
      runtimeNow,
      currentNodeIDs: currentNodeSet,
      currentNodeStartedAt,
      runStatus: runtimeVisible ? runStatus : undefined,
      runTriggerID: runtimeVisible ? runTriggerId : undefined,
      selectedEdgeID: selectedEdgeId,
      selectedLoopID: selectedLoopId,
      selectedNodeID: selectedNodeId,
      selectedTriggerID: selectedTriggerId,
      triggerNodes,
      virtualEdges,
      virtualLoops,
      virtualNodeIDs: virtualNodeIds,
    }),
    [
      definition,
      editable,
      highlightedNodeSet,
      isInteractive,
      nodeTypes,
      configurationErrors,
      runtimeVisible,
      currentNodeSet,
      currentNodeStartedAt,
      runStatus,
      runTriggerId,
      selectedEdgeId,
      selectedLoopId,
      selectedNodeId,
      selectedTriggerId,
      triggerNodes,
      virtualEdges,
      virtualLoops,
      virtualNodeIds,
    ]
  );

  const refreshRuntimeNodes = useCallback(() => {
    if (!runtimeVisible) return;
    const runtimeElements = buildCanvasElements(Date.now());
    const runtimeByID = new Map(runtimeElements.nodes.map((node) => [node.id, node]));
    setNodes((current) => updateRuntimeNodeProjection(current, runtimeByID));
  }, [buildCanvasElements, runtimeVisible, setNodes]);

  useEffect(() => {
    if (!runtimeVisible || runStatus !== "running") return;
    const timer = window.setInterval(refreshRuntimeNodes, 1_000);
    return () => window.clearInterval(timer);
  }, [refreshRuntimeNodes, runStatus, runtimeVisible]);

  useLayoutEffect(() => {
    const elements = buildCanvasElements();
    const retainedNodeIDs = retainedDraggedNodeIDsRef.current;
    retainedDraggedNodeIDsRef.current = null;
    setNodes((current) => {
      if (runtimeVisible && !definition && current.length > 0 && elements.nodes.length === 0) return current;
      const currentByID = new Map(current.map((node) => [node.id, node]));
      return retainedNodeIDs
        ? elements.nodes.map((node) => preserveNodeMeasurement(
            { ...node, selected: retainedNodeIDs.has(node.id) },
            currentByID.get(node.id)
          ))
        : elements.nodes.map((node) => preserveNodeMeasurement(node, currentByID.get(node.id)));
    });
    setEdges((current) => runtimeVisible && !definition && current.length > 0 && elements.edges.length === 0
      ? current
      : elements.edges);
  }, [buildCanvasElements, setEdges, setNodes]);

  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);

  useEffect(() => {
    edgesRef.current = edges;
  }, [edges]);

  const persistViewport = useCallback(
    (viewport: Viewport) => {
      const key = normalizeViewportStorageKey(viewportStorageKey);
      if (key && restoredViewportKeyRef.current !== key) return;
      if (Date.now() < suppressViewportPersistUntilRef.current) return;
      writeStoredCanvasViewport(viewportStorageKey, viewport);
    },
    [viewportStorageKey]
  );

  const applyViewport = useCallback(
    (nextViewport: Viewport, options: { persist?: boolean } = {}) => {
      const state = store.getState();
      if (state.panZoom) {
        void state.panZoom.setViewport(nextViewport);
        store.setState({ transform: [nextViewport.x, nextViewport.y, nextViewport.zoom] });
        syncRendererZoomState(flowWrapperRef.current, nextViewport);
        if (options.persist !== false) persistViewport(nextViewport);
        return;
      }
      const currentTransform = state.transform;
      const current = { x: currentTransform[0], y: currentTransform[1], zoom: currentTransform[2] };
      if (sameViewport(current, nextViewport)) return;
      store.setState({ transform: [nextViewport.x, nextViewport.y, nextViewport.zoom] });
      if (options.persist !== false) persistViewport(nextViewport);
    },
    [persistViewport, store]
  );

  useLayoutEffect(() => {
    const key = normalizeViewportStorageKey(viewportStorageKey);
    if (!viewportInitialized || !key || restoredViewportKeyRef.current === key) {
      return;
    }
    restoredViewportKeyRef.current = key;
    const stored = readStoredCanvasViewport(key);
    if (!stored) return;
    suppressViewportPersistUntilRef.current = Date.now() + 600;
    applyViewport(stored, { persist: false });
  }, [applyViewport, viewportInitialized, viewportStorageKey]);

  useLayoutEffect(() => {
    if (!fitViewSignal || fitViewSignal === handledFitViewSignal.current || !viewportInitialized) {
      return;
    }
    const elements = buildCanvasElements();
    const applied = fitNodesToViewport(elements.nodes, flowWrapperRef.current, (viewport) => {
      applyViewport(viewport);
    });
    if (applied) handledFitViewSignal.current = fitViewSignal;
  }, [applyViewport, buildCanvasElements, fitViewSignal, viewportInitialized]);

  useEffect(() => {
    if (!focusNodeId || !focusNodeSignal || !viewportInitialized) return;
    window.requestAnimationFrame(() => {
      const target = nodesRef.current.find((node) => node.id === focusNodeId);
      if (!target) return;
      fitNodesToViewport([target], flowWrapperRef.current, applyViewport, 0.65);
    });
  }, [applyViewport, focusNodeId, focusNodeSignal, viewportInitialized]);

  function handleConnect(connection: Connection) {
    if (!isInteractive || !connection.source || !connection.target) return;
    if (triggerNodes.some((item) => item.canvas_id === connection.source || item.canvas_id === connection.target)) return;
    const sourceIsLoop = virtualLoops.some((loop) => loop.id === connection.source);
    const targetIsLoop = virtualLoops.some((loop) => loop.id === connection.target);
    if (sourceIsLoop || targetIsLoop) return;
    const sourceKind = virtualNodeKind(connection.source);
    const targetKind = virtualNodeKind(connection.target);
    if (sourceKind === "end") return;
    if (targetKind === "start") return;
    if (sourceKind === "start" && targetKind === "end") return;
    onConnectNodes?.(connection.source, connection.target);
  }

  function screenPoint(event: { clientX: number; clientY: number }): NodePosition {
    return { x: event.clientX, y: event.clientY };
  }

  return (
    <div ref={flowWrapperRef} className="h-full w-full">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={graphCanvasNodeTypes}
        edgeTypes={graphCanvasEdgeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={handleConnect}
        onNodeClick={(event, node) => {
          if (node.data.virtualKind === "trigger") {
            onSelectNode?.(null);
            onSelectEdge?.(null);
            onSelectLoop?.(null);
            onSelectTrigger?.(String(node.data.triggerID || ""));
            return;
          }
          if (node.data.virtualKind === "loop") {
            if ((event.target as Element).closest?.("[data-loop-title]")) {
              onSelectLoop?.(node.id);
              onSelectNode?.(null);
              onSelectEdge?.(null);
            }
            return;
          }
          onSelectNode?.(node.id);
          onSelectEdge?.(null);
          onSelectLoop?.(null);
          onSelectTrigger?.(null);
        }}
        onNodeContextMenu={(event, node) => {
          if (!isInteractive) return;
          event.preventDefault();
          event.stopPropagation();
          if (node.data.virtualKind === "trigger") {
            const triggerId = String(node.data.triggerID || "");
            onSelectNode?.(null);
            onSelectEdge?.(null);
            onSelectLoop?.(null);
            onSelectTrigger?.(triggerId);
            onTriggerContextMenu?.(triggerId, screenPoint(event));
            return;
          }
          if (node.data.virtualKind === "loop") {
            const target = event.target as Element;
            if (target.closest("[data-loop-title]")) {
              onSelectLoop?.(node.id);
              onSelectNode?.(null);
              onSelectEdge?.(null);
              const loop = virtualLoops.find((item) => item.id === node.id);
              if (!loop?.automatic) onLoopContextMenu?.(node.id, screenPoint(event));
            } else {
              const position = screenToFlowPosition({ x: event.clientX, y: event.clientY });
              onCreateNodeAt?.(position, screenPoint(event));
            }
            return;
          }
          onSelectNode?.(node.id);
          onSelectEdge?.(null);
          onSelectLoop?.(null);
          onSelectTrigger?.(null);
          onNodeContextMenu?.(node.id, screenPoint(event));
        }}
        onEdgeClick={(_, edge) => {
          if (edge.data?.triggerEdge) return;
          onSelectEdge?.(flowEdgeSelectionId(edge));
          onSelectNode?.(null);
          onSelectLoop?.(null);
          onSelectTrigger?.(null);
        }}
        onEdgeContextMenu={(event, edge) => {
          if (edge.data?.triggerEdge) return;
          if (!isInteractive) return;
          event.preventDefault();
          event.stopPropagation();
          const selectionId = flowEdgeSelectionId(edge);
          onSelectEdge?.(selectionId);
          onSelectNode?.(null);
          onSelectLoop?.(null);
          onSelectTrigger?.(null);
          onEdgeContextMenu?.(selectionId, screenPoint(event));
        }}
        onPaneClick={() => {
          onSelectNode?.(null);
          onSelectEdge?.(null);
          onSelectLoop?.(null);
          onSelectTrigger?.(null);
        }}
        onPaneContextMenu={(event) => {
          if (!isInteractive) return;
          event.preventDefault();
          const position = screenToFlowPosition({ x: event.clientX, y: event.clientY });
          onCreateNodeAt?.(position, screenPoint(event));
        }}
        onMoveEnd={(_, viewport) => {
          persistViewport(viewport);
        }}
        onNodeDragStart={(_, node) => {
          if (node.data.virtualKind === "loop") {
            const group = virtualLoops.find((g) => g.id === node.id);
            const memberPositions = new Map<string, NodePosition>();
            if (group) {
              for (const n of nodesRef.current) {
                if (group.nodeIds.includes(n.id)) {
                  memberPositions.set(n.id, { ...n.position });
                }
              }
            }
            loopDragRef.current = {
              groupId: node.id,
              startPosition: { ...node.position },
              memberPositions,
            };
          }
        }}
        onNodeDrag={(_, node) => {
          const drag = loopDragRef.current;
          if (!drag || node.data.virtualKind !== "loop" || drag.groupId !== node.id) return;
          const dx = node.position.x - drag.startPosition.x;
          const dy = node.position.y - drag.startPosition.y;
          setNodes((current) =>
            current.map((n) => {
              const orig = drag.memberPositions.get(n.id);
              if (!orig) return n;
              return { ...n, position: { x: orig.x + dx, y: orig.y + dy } };
            })
          );
        }}
        onNodeDragStop={(_, node, draggedNodes) => {
          const drag = loopDragRef.current;
          if (drag && node.data.virtualKind === "loop" && drag.groupId === node.id) {
            const dx = node.position.x - drag.startPosition.x;
            const dy = node.position.y - drag.startPosition.y;
            loopDragRef.current = null;
            if (Math.abs(dx) > 0.5 || Math.abs(dy) > 0.5) {
              onLoopDrag?.(node.id, { x: dx, y: dy });
            }
            return;
          }
          const movedNodes = draggedNodes.length > 0 ? draggedNodes : [node];
          const changes = positionChangesForNodes(movedNodes);
          if (onPositionChanges && (changes.nodePositions.size > 0 || changes.triggerPositions.size > 0)) {
            retainedDraggedNodeIDsRef.current = new Set(movedNodes.map((movedNode) => movedNode.id));
            onPositionChanges(changes);
          }
        }}
        minZoom={minGraphCanvasZoom}
        maxZoom={maxGraphCanvasZoom}
        panOnDrag={editable ? [1] : [0, 1]}
        selectionOnDrag={isInteractive}
        nodesDraggable={isInteractive}
        nodesConnectable={isInteractive}
        elementsSelectable={editable ? interactive : true}
        edgesReconnectable={false}
        proOptions={{ hideAttribution: true }}
        className={`debug-flow ${isInteractive ? "debug-flow-editable" : "debug-flow-locked"}${editable ? "" : " debug-flow-left-pan"}`}
      >
        <MiniMap<Node<FlowNodeData>>
          pannable
          zoomable
          position="bottom-right"
          ariaLabel="Graph overview"
          className="debug-flow-minimap"
          style={{ width: 220, height: 150 }}
          bgColor="var(--panel)"
          maskColor="var(--flow-minimap-mask)"
          maskStrokeColor="var(--flow-edge-selected)"
          maskStrokeWidth={1.5}
          nodeColor={miniMapNodeColor}
          nodeStrokeColor={miniMapNodeStrokeColor}
          nodeStrokeWidth={2}
          nodeBorderRadius={4}
          offsetScale={8}
        />
        <GraphCanvasControls
          showEditControls={editable}
          interactive={interactive}
          canAutoLayout={Boolean(definition)}
          hasSelection={Boolean(selectedNodeId || selectedEdgeId || selectedLoopId || selectedTriggerId)}
          onAutoLayout={onAutoLayout}
          onFitView={() => fitNodesToViewport(nodesRef.current, flowWrapperRef.current, applyViewport)}
          onFitSelection={() => fitNodesToViewport(selectedNodesForFit(), flowWrapperRef.current, applyViewport, 0.65)}
          onToggleInteractive={() => setInteractive((value) => !value)}
          onZoomIn={() => zoomViewport(1.2)}
          onZoomOut={() => zoomViewport(1 / 1.2)}
        />
        <Background gap={22} size={1.1} color="var(--flow-background-dot)" />
      </ReactFlow>
    </div>
  );

  function zoomViewport(factor: number) {
    const rect = flowWrapperRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0 || rect.height <= 0) return;
    const [x, y, zoom] = store.getState().transform;
    const nextZoom = Math.max(minGraphCanvasZoom, Math.min(maxGraphCanvasZoom, zoom * factor));
    if (Math.abs(nextZoom - zoom) < 0.0001) return;
    const centerX = (rect.width / 2 - x) / zoom;
    const centerY = (rect.height / 2 - y) / zoom;
    applyViewport({
      x: rect.width / 2 - centerX * nextZoom,
      y: rect.height / 2 - centerY * nextZoom,
      zoom: nextZoom,
    });
  }

  function selectedNodesForFit(): Node<FlowNodeData>[] {
    if (selectedLoopId) {
      const groupNode = nodesRef.current.find((node) => node.id === selectedLoopId);
      if (!groupNode) return [];
      const memberIds = new Set(virtualLoops.find((group) => group.id === selectedLoopId)?.nodeIds ?? []);
      const members = nodesRef.current.filter((node) => memberIds.has(node.id));
      return [groupNode, ...members];
    }
    if (selectedNodeId) {
      return nodesRef.current.filter((node) => node.id === selectedNodeId);
    }
    if (selectedTriggerId) {
      return nodesRef.current.filter((node) => node.data.triggerID === selectedTriggerId);
    }
    if (selectedEdgeId) {
      const edge = edgesRef.current.find((item) => item.id === selectedEdgeId);
      if (!edge) return [];
      const ids = new Set([edge.source, edge.target]);
      return nodesRef.current.filter((node) => ids.has(node.id));
    }
    return [];
  }
}

function flowEdgeSelectionId(edge: Edge): string {
  const selectionId = edge.data && typeof edge.data === "object" && "selectionId" in edge.data
    ? edge.data.selectionId
    : undefined;
  return typeof selectionId === "string" ? selectionId : edge.id;
}

type FlowEdgeData = {
  selectionId?: string;
  edgeOffset?: number;
};

type GraphFlowEdge = Edge<FlowEdgeData, "condition" | "flow">;

function FlowEdge({
  id,
  sourceX,
  sourceY,
  sourcePosition,
  targetX,
  targetY,
  targetPosition,
  data,
  label,
  style,
  markerStart,
  markerEnd,
  interactionWidth,
  selected,
}: EdgeProps<GraphFlowEdge>) {
  const edgeOffset = data?.edgeOffset ?? 0;
  const [edgePath, labelX, labelY] = offsetBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    offset: edgeOffset,
  });

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        style={style}
        markerStart={markerStart}
        markerEnd={markerEnd}
        interactionWidth={interactionWidth}
      />
      {label ? (
        <EdgeLabelRenderer>
          <div
            className={`debug-condition-edge-label nodrag nopan${selected ? " debug-condition-edge-label-selected" : ""}`}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
            aria-label={String(label)}
          >
            <span>{label}</span>
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  );
}

function offsetBezierPath({
  sourceX,
  sourceY,
  sourcePosition,
  targetX,
  targetY,
  targetPosition,
  offset,
}: {
  sourceX: number;
  sourceY: number;
  sourcePosition: Position;
  targetX: number;
  targetY: number;
  targetPosition: Position;
  offset: number;
}): [string, number, number] {
  if (!offset) {
    const [path, labelX, labelY] = getBezierPath({
      sourceX,
      sourceY,
      sourcePosition,
      targetX,
      targetY,
      targetPosition,
    });
    return [path, labelX, labelY];
  }
  const deltaX = targetX - sourceX;
  const deltaY = targetY - sourceY;
  const length = Math.hypot(deltaX, deltaY);
  if (length < 0.001) {
    return [`M${sourceX},${sourceY} C${sourceX + offset},${sourceY - offset} ${targetX + offset},${targetY - offset} ${targetX},${targetY}`, sourceX + offset, sourceY - offset];
  }
  const direction = sourceX < targetX || (sourceX === targetX && sourceY <= targetY) ? 1 : -1;
  const normalX = -(deltaY * direction) / length;
  const normalY = (deltaX * direction) / length;
  const labelX = (sourceX + targetX) / 2 + normalX * offset;
  const labelY = (sourceY + targetY) / 2 + normalY * offset;
  const tangentScale = Math.min(length / 6, 60);
  const tangentX = deltaX / length * tangentScale;
  const tangentY = deltaY / length * tangentScale;
  const [sourceControlX, sourceControlY] = edgeControlPoint(
    sourcePosition,
    sourceX,
    sourceY,
    targetX,
    targetY
  );
  const [targetControlX, targetControlY] = edgeControlPoint(
    targetPosition,
    targetX,
    targetY,
    sourceX,
    sourceY
  );
  return [
    `M${sourceX},${sourceY} C${sourceControlX},${sourceControlY} ${labelX - tangentX},${labelY - tangentY} ${labelX},${labelY} C${labelX + tangentX},${labelY + tangentY} ${targetControlX},${targetControlY} ${targetX},${targetY}`,
    labelX,
    labelY,
  ];
}

function edgeControlPoint(
  position: Position,
  x: number,
  y: number,
  otherX: number,
  otherY: number
): [number, number] {
  switch (position) {
    case Position.Left:
      return [x - edgeControlOffset(x - otherX), y];
    case Position.Right:
      return [x + edgeControlOffset(otherX - x), y];
    case Position.Top:
      return [x, y - edgeControlOffset(y - otherY)];
    case Position.Bottom:
      return [x, y + edgeControlOffset(otherY - y)];
  }
}

function edgeControlOffset(distance: number): number {
  return distance >= 0 ? distance * 0.5 : 6.25 * Math.sqrt(-distance);
}

function preserveNodeMeasurement(
  node: Node<FlowNodeData>,
  previous?: Node<FlowNodeData>
): Node<FlowNodeData> {
  if (!previous) return node;
  return {
    ...node,
    measured: previous.measured,
    width: previous.width,
    height: previous.height,
  };
}

function positionChangesForNodes(nodes: Node<FlowNodeData>[]): GraphCanvasPositionChanges {
  const nodePositions = new Map<string, NodePosition>();
  const triggerPositions = new Map<string, NodePosition>();
  for (const node of nodes) {
    if (node.data.virtualKind === "loop") continue;
    if (node.data.virtualKind === "trigger") {
      const triggerID = String(node.data.triggerID || "");
      if (triggerID) triggerPositions.set(triggerID, node.position);
      continue;
    }
    nodePositions.set(node.id, node.position);
  }
  return { nodePositions, triggerPositions };
}

function miniMapNodeColor(node: Node<FlowNodeData>): string {
  if (node.selected) return "var(--flow-edge-selected)";
  if (node.data.virtualKind === "loop") {
    return "color-mix(in srgb, var(--flow-edge-selected) 9%, transparent)";
  }
  if (node.data.virtualKind === "trigger") return "#8b5cf6";
  if (node.data.virtualKind === "start") return "var(--flow-edge-entry)";
  if (node.data.virtualKind === "end") return "var(--flow-edge-finish)";
  switch (node.data.status) {
    case "running":
      return "var(--status-live-text)";
    case "succeeded":
      return "var(--status-ok-text)";
    case "failed":
      return "var(--status-danger-text)";
    case "paused":
      return "var(--status-warn-text)";
    default:
      return "var(--muted-foreground)";
  }
}

function miniMapNodeStrokeColor(node: Node<FlowNodeData>): string {
  if (node.selected || node.data.virtualKind === "loop") return "var(--flow-edge-selected)";
  return "var(--panel)";
}
