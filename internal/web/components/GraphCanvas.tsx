import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  useStoreApi,
  type Connection,
  type Edge,
  type Node,
  type Viewport,
} from "@xyflow/react";
import type { GraphDefinition, NodeTypeSchema, StepRecord, TriggerCanvasNode } from "../types";
import { END_NODE_REF, START_NODE_REF, type NodePosition } from "../lib/graphEditor";
import type { VirtualGraphLoop } from "../lib/loopPresentation";
import { subscribeRuntimeEvents } from "../lib/runtimeEvents";
import { GraphCanvasControls } from "./GraphCanvasControls";
import { GraphLoopNode, GraphNode, GraphTriggerNode } from "./GraphCanvasNodes";
import { buildGraphCanvasElements, type VirtualGraphEdge } from "./graphCanvasElements";
import {
  applyRuntime,
  applyRuntimeSnapshot,
  eventAttempt,
  resetRuntimeNodes,
  runtimeFromSteps,
  runtimeStatusFromEvent,
  timeRank,
  updateRuntimeNode,
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

export function GraphCanvas({
  definition,
  steps,
  selectedRunId,
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
  virtualNodeIds = [START_NODE_REF, END_NODE_REF],
  virtualEdges = [],
  virtualLoops = [],
  triggerNodes = [],
  onAutoLayout,
  onSelectNode,
  onSelectEdge,
  onSelectLoop,
  onSelectTrigger,
  onNodePositionChange,
  onTriggerPositionChange,
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
  selectedRunId?: string;
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
  virtualNodeIds?: string[];
  virtualEdges?: VirtualGraphEdge[];
  virtualLoops?: VirtualGraphLoop[];
  triggerNodes?: TriggerCanvasNode[];
  onAutoLayout?: () => void;
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onSelectLoop?: (groupId: string | null) => void;
  onSelectTrigger?: (triggerId: string | null) => void;
  onNodePositionChange?: (nodeId: string, position: NodePosition) => void;
  onTriggerPositionChange?: (triggerId: string, position: NodePosition) => void;
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
        selectedRunId={selectedRunId}
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
        virtualNodeIds={virtualNodeIds}
        virtualEdges={virtualEdges}
        virtualLoops={virtualLoops}
        triggerNodes={triggerNodes}
        onAutoLayout={onAutoLayout}
        onSelectNode={onSelectNode}
        onSelectEdge={onSelectEdge}
        onSelectLoop={onSelectLoop}
        onSelectTrigger={onSelectTrigger}
        onNodePositionChange={onNodePositionChange}
        onTriggerPositionChange={onTriggerPositionChange}
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
  selectedRunId,
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
  virtualNodeIds,
  virtualEdges,
  virtualLoops,
  triggerNodes,
  onAutoLayout,
  onSelectNode,
  onSelectEdge,
  onSelectLoop,
  onSelectTrigger,
  onNodePositionChange,
  onTriggerPositionChange,
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
  selectedRunId?: string;
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
  virtualNodeIds: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
  triggerNodes: TriggerCanvasNode[];
  onAutoLayout?: () => void;
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onSelectLoop?: (groupId: string | null) => void;
  onSelectTrigger?: (triggerId: string | null) => void;
  onNodePositionChange?: (nodeId: string, position: NodePosition) => void;
  onTriggerPositionChange?: (triggerId: string, position: NodePosition) => void;
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

  const stepRuntime = useMemo(() => runtimeFromSteps(steps, selectedRunId), [selectedRunId, steps]);
  const highlightedNodeSet = useMemo(() => new Set(highlightedNodeIds), [highlightedNodeIds]);

  useEffect(() => {
    const nextRunId = selectedRunId ?? "";
    if (runtimeRunIdRef.current === nextRunId) return;
    runtimeRunIdRef.current = nextRunId;
    runtimeRef.current = new Map();
    setNodes((current) => resetRuntimeNodes(current));
  }, [selectedRunId, setNodes]);

  useEffect(() => {
    if (stepRuntime.size === 0) return;
    const next = new Map(runtimeRef.current);
    for (const [nodeId, runtime] of stepRuntime) {
      applyRuntime(next, nodeId, runtime.status, runtime.attempt, runtime.at);
    }
    runtimeRef.current = next;
    setNodes((current) => applyRuntimeSnapshot(current, next));
  }, [setNodes, stepRuntime]);

  useEffect(() => subscribeRuntimeEvents((event) => {
    if (selectedRunId && event.run_id && event.run_id !== selectedRunId) return;
    let switchedRun = false;
    if (event.run_id && runtimeRunIdRef.current !== event.run_id) {
      runtimeRunIdRef.current = event.run_id;
      runtimeRef.current = new Map();
      switchedRun = true;
    }
    if (!event.node_id) {
      if (switchedRun) setNodes((current) => resetRuntimeNodes(current));
      return;
    }
    const nodeId = event.node_id;
    const status = runtimeStatusFromEvent(event.type);
    if (!status) return;

    const next = new Map(runtimeRef.current);
    const changed = applyRuntime(next, nodeId, status, eventAttempt(event.payload), timeRank(event.timestamp));
    if (!changed && !switchedRun) return;
    runtimeRef.current = next;
    const runtime = next.get(nodeId);
    setNodes((current) => {
      const base = switchedRun ? resetRuntimeNodes(current) : current;
      return updateRuntimeNode(base, nodeId, runtime);
    });
  }), [selectedRunId, setNodes]);

  useEffect(() => {
    const elements = buildGraphCanvasElements({
      definition,
      editable,
      interactive: isInteractive,
      highlightedNodeIDs: highlightedNodeSet,
      nodeTypes,
      runtime: runtimeRef.current,
      selectedEdgeID: selectedEdgeId,
      selectedLoopID: selectedLoopId,
      selectedNodeID: selectedNodeId,
      selectedTriggerID: selectedTriggerId,
      triggerNodes,
      virtualEdges,
      virtualLoops,
      virtualNodeIDs: virtualNodeIds,
    });
    setNodes(elements.nodes);
    setEdges(elements.edges);
  }, [definition, editable, highlightedNodeSet, isInteractive, nodeTypes, selectedEdgeId, selectedLoopId, selectedNodeId, selectedTriggerId, setEdges, setNodes, triggerNodes, virtualEdges, virtualLoops, virtualNodeIds]);

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

  useEffect(() => {
    const key = normalizeViewportStorageKey(viewportStorageKey);
    if (!viewportInitialized || !key || restoredViewportKeyRef.current === key) {
      return;
    }
    restoredViewportKeyRef.current = key;
    const stored = readStoredCanvasViewport(key);
    if (!stored) return;
    suppressViewportPersistUntilRef.current = Date.now() + 600;
    window.requestAnimationFrame(() => {
      applyViewport(stored, { persist: false });
    });
  }, [applyViewport, viewportInitialized, viewportStorageKey]);

  useEffect(() => {
    if (!fitViewSignal || fitViewSignal === handledFitViewSignal.current || nodes.length === 0 || !viewportInitialized) {
      return;
    }
    const signal = fitViewSignal;
    window.setTimeout(() => {
      window.requestAnimationFrame(() => {
        window.requestAnimationFrame(() => {
          const applied = fitNodesToViewport(nodesRef.current, flowWrapperRef.current, (viewport) => {
            applyViewport(viewport);
          });
          if (applied) {
            handledFitViewSignal.current = signal;
          }
        });
      });
    }, 120);
  }, [applyViewport, fitViewSignal, nodes.length, viewportInitialized]);

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
        nodeTypes={{ debugNode: GraphNode, debugLoop: GraphLoopNode, debugTrigger: GraphTriggerNode }}
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
        onNodeDragStop={(_, node) => {
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
          if (node.data.virtualKind === "trigger") {
            onTriggerPositionChange?.(String(node.data.triggerID || ""), node.position);
            return;
          }
          onNodePositionChange?.(node.id, node.position);
        }}
        minZoom={minGraphCanvasZoom}
        maxZoom={maxGraphCanvasZoom}
        nodesDraggable={isInteractive}
        nodesConnectable={isInteractive}
        elementsSelectable={interactive}
        edgesReconnectable={false}
        proOptions={{ hideAttribution: true }}
        className="debug-flow"
      >
        <MiniMap pannable zoomable position="bottom-right" className="!rounded-md !border !border-border !bg-panel" />
        <GraphCanvasControls
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
