import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  Handle,
  MiniMap,
  Panel,
  Position,
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
import { Focus, Lock, Maximize2, Network, Repeat2, Unlock, ZoomIn, ZoomOut } from "lucide-react";
import type { GraphConditionSpec, GraphDefinition, GraphNodeSpec, NodeTypeSchema, RuntimeEvent, StepRecord } from "../types";
import { END_NODE_REF, START_NODE_REF, graphEdgeId, graphNodePositions, matchesDynamicStatePortName, resolveDefaultStatePath, type NodePosition } from "../lib/graphEditor";
import {
  analyzeVirtualGraphLoop,
  conditionDisplayLabel,
  edgeSegmentsForLoopDisplay,
  graphEdgesForLoopDisplay,
  loopContinueHandleId,
  loopEndHandleId,
  loopEndInnerHandleId,
  loopStartHandleId,
  loopStartInnerHandleId,
  type VirtualGraphLoop,
} from "../lib/loopPresentation";
import { subscribeRuntimeEvents } from "../lib/runtimeEvents";

export type { VirtualGraphLoop } from "../lib/loopPresentation";

interface FlowNodeData extends Record<string, unknown> {
  label: string;
  type: string;
  status: string;
  editable: boolean;
  attempt?: number;
  highlighted?: boolean;
  bindingSummary?: string;
  missingBindings?: boolean;
  virtualKind?: "start" | "end" | "loop";
  width?: number;
  height?: number;
}

export interface VirtualGraphEdge {
  id: string;
  from: string;
  to: string;
  kind: "entry" | "finish";
  condition?: GraphConditionSpec;
}

const nodeWidth = 190;
const nodeHeight = 76;
const loopPaddingX = 62;
const loopPaddingTop = 54;
const loopPaddingBottom = 20;
const minLoopWidth = 250;
const minLoopHeight = 150;
const minZoom = 0.2;
const maxZoom = 2;
const viewportStoragePrefix = "weaveflow.workbench.graphCanvas.viewport.";

interface StoredCanvasViewport {
  x: number;
  y: number;
  zoom: number;
}

export function GraphCanvas({
  definition,
  steps,
  selectedRunId,
  editable = false,
  selectedNodeId,
  selectedEdgeId,
  selectedLoopId,
  fitViewSignal = 0,
  focusNodeId,
  focusNodeSignal = 0,
  viewportStorageKey,
  highlightedNodeIds = [],
  nodeTypes = [],
  virtualNodeIds = [START_NODE_REF, END_NODE_REF],
  virtualEdges = [],
  virtualLoops = [],
  onAutoLayout,
  onSelectNode,
  onSelectEdge,
  onSelectLoop,
  onNodePositionChange,
  onConnectNodes,
  onCreateNodeAt,
  onNodeContextMenu,
  onEdgeContextMenu,
  onLoopContextMenu,
  onLoopDrag,
}: {
  definition: GraphDefinition | null;
  steps: StepRecord[];
  selectedRunId?: string;
  editable?: boolean;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  selectedLoopId?: string;
  fitViewSignal?: number;
  focusNodeId?: string;
  focusNodeSignal?: number;
  viewportStorageKey?: string;
  highlightedNodeIds?: string[];
  nodeTypes?: NodeTypeSchema[];
  virtualNodeIds?: string[];
  virtualEdges?: VirtualGraphEdge[];
  virtualLoops?: VirtualGraphLoop[];
  onAutoLayout?: () => void;
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onSelectLoop?: (groupId: string | null) => void;
  onNodePositionChange?: (nodeId: string, position: NodePosition) => void;
  onConnectNodes?: (source: string, target: string) => void;
  onCreateNodeAt?: (position: NodePosition, screenPosition: NodePosition) => void;
  onNodeContextMenu?: (nodeId: string, screenPosition: NodePosition) => void;
  onEdgeContextMenu?: (edgeId: string, screenPosition: NodePosition) => void;
  onLoopContextMenu?: (groupId: string, screenPosition: NodePosition) => void;
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
        fitViewSignal={fitViewSignal}
        focusNodeId={focusNodeId}
        focusNodeSignal={focusNodeSignal}
        viewportStorageKey={viewportStorageKey}
        highlightedNodeIds={highlightedNodeIds}
        nodeTypes={nodeTypes}
        virtualNodeIds={virtualNodeIds}
        virtualEdges={virtualEdges}
        virtualLoops={virtualLoops}
        onAutoLayout={onAutoLayout}
        onSelectNode={onSelectNode}
        onSelectEdge={onSelectEdge}
        onSelectLoop={onSelectLoop}
        onNodePositionChange={onNodePositionChange}
        onConnectNodes={onConnectNodes}
        onCreateNodeAt={onCreateNodeAt}
        onNodeContextMenu={onNodeContextMenu}
        onEdgeContextMenu={onEdgeContextMenu}
        onLoopContextMenu={onLoopContextMenu}
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
  fitViewSignal,
  focusNodeId,
  focusNodeSignal,
  viewportStorageKey,
  highlightedNodeIds,
  nodeTypes,
  virtualNodeIds,
  virtualEdges,
  virtualLoops,
  onAutoLayout,
  onSelectNode,
  onSelectEdge,
  onSelectLoop,
  onNodePositionChange,
  onConnectNodes,
  onCreateNodeAt,
  onNodeContextMenu,
  onEdgeContextMenu,
  onLoopContextMenu,
  onLoopDrag,
}: {
  definition: GraphDefinition | null;
  steps: StepRecord[];
  selectedRunId?: string;
  editable: boolean;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  selectedLoopId?: string;
  fitViewSignal: number;
  focusNodeId?: string;
  focusNodeSignal: number;
  viewportStorageKey?: string;
  highlightedNodeIds: string[];
  nodeTypes: NodeTypeSchema[];
  virtualNodeIds: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
  onAutoLayout?: () => void;
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onSelectLoop?: (groupId: string | null) => void;
  onNodePositionChange?: (nodeId: string, position: NodePosition) => void;
  onConnectNodes?: (source: string, target: string) => void;
  onCreateNodeAt?: (position: NodePosition, screenPosition: NodePosition) => void;
  onNodeContextMenu?: (nodeId: string, screenPosition: NodePosition) => void;
  onEdgeContextMenu?: (edgeId: string, screenPosition: NodePosition) => void;
  onLoopContextMenu?: (groupId: string, screenPosition: NodePosition) => void;
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
    if (!definition) {
      setNodes([]);
      setEdges([]);
      return;
    }

    const visibleVirtualNodeIds = new Set(virtualNodeIds);
    const startVirtualNodeIds = virtualNodeIds.filter(isVirtualStartNodeId);
    const endVirtualNodeIds = virtualNodeIds.filter(isVirtualEndNodeId);
    const displayNodes: GraphNodeSpec[] = [
      ...startVirtualNodeIds.map(virtualNodeSpec),
      ...definition.nodes,
      ...endVirtualNodeIds.map(virtualNodeSpec),
    ];
    const positions = layoutNodes(definition, visibleVirtualNodeIds);
    const loopLayouts = virtualLoopLayouts(definition, virtualLoops, positions);
    setNodes(
      [
        ...loopLayouts.map((layout) => ({
          id: layout.loop.id,
          type: "debugLoop",
          className: "debug-loop-node",
          position: layout.position,
          draggable: editable,
          dragHandle: ".debug-loop-title",
          selectable: true,
          selected: layout.loop.id === selectedLoopId,
          zIndex: 0,
          data: {
            label: layout.loop.name || "Loop",
            type: "loop",
            status: "idle",
            editable: isInteractive,
            virtualKind: "loop" as const,
            width: layout.width,
            height: layout.height,
          },
          style: {
            width: layout.width,
            height: layout.height,
          },
        })),
        ...displayNodes.map((node) => {
          const virtualKind = virtualNodeKind(node.id);
          const nodeType = nodeTypes.find((item) => item.type === node.type);
          const statePorts = nodeType?.state_ports ?? [];
          const staticPortNames = new Set(statePorts.map((port) => port.name));
          const dynamicPortNames = Object.keys(node.state ?? {}).filter((name) => !staticPortNames.has(name) && matchesDynamicStatePortName(name, nodeType?.dynamic_state_ports));
          const boundPortCount = statePorts.filter((port) => Boolean(node.state?.[port.name]?.path.trim() || resolveDefaultStatePath(port.default_path, node.id))).length
            + dynamicPortNames.filter((name) => Boolean(node.state?.[name]?.path.trim())).length;
          const totalPortCount = statePorts.length + dynamicPortNames.length;
          const missingBindings = statePorts.some((port) => port.required && !node.state?.[port.name]?.path.trim() && !resolveDefaultStatePath(port.default_path, node.id));
          const dynamicMinimum = nodeType?.dynamic_state_ports?.min_ports ?? 0;
          const missingDynamicBindings = dynamicPortNames.length < dynamicMinimum;
          return {
            id: node.id,
            type: "debugNode",
            position: positions.get(node.id) ?? { x: 0, y: 0 },
            draggable: editable,
            selectable: true,
            selected: node.id === selectedNodeId,
            zIndex: 2,
            data: {
              label: node.name || node.id,
              type: node.type || "node",
              status: virtualKind ? "idle" : runtimeRef.current.get(node.id)?.status || "idle",
              attempt: virtualKind ? 0 : runtimeRef.current.get(node.id)?.attempt || 0,
              editable: isInteractive,
              highlighted: highlightedNodeSet.has(node.id),
              bindingSummary: virtualKind || totalPortCount === 0 ? undefined : `${boundPortCount}/${totalPortCount} state`,
              missingBindings: missingBindings || missingDynamicBindings,
              virtualKind,
            },
          };
        }),
      ]
    );

    const displayVirtualEdges = virtualEdges.flatMap((edge) =>
      edgeSegmentsForLoopDisplay(
        definition,
        { from: edge.from, to: edge.to, condition: edge.condition },
        edge.id,
        virtualLoops
      )
    );
    const displayGraphEdges = graphEdgesForLoopDisplay(definition, virtualLoops);
    setEdges(
      [...displayVirtualEdges, ...displayGraphEdges].map(({
        edge,
        id,
        selectionId = id,
        source,
        target,
        sourceHandle,
        targetHandle,
        showLabel = true,
        contained = false,
      }) => {
        const selected = selectionId === selectedEdgeId;
        const condition = Boolean(edge.condition);
        return {
          id,
          data: { selectionId },
          source,
          target,
          sourceHandle,
          targetHandle,
          type: contained ? "default" : undefined,
          label: showLabel && edge.condition ? conditionDisplayLabel(edge.condition) : undefined,
          labelStyle: showLabel && condition ? {
            fill: "var(--foreground)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            fontWeight: 600,
          } : undefined,
          labelBgStyle: showLabel && condition ? {
            fill: "var(--panel)",
            fillOpacity: 0.96,
            stroke: "var(--border)",
            strokeWidth: 1,
          } : undefined,
          labelBgPadding: showLabel && condition ? [7, 4] as [number, number] : undefined,
          labelBgBorderRadius: showLabel && condition ? 5 : undefined,
          animated: showLabel && condition,
          selected,
          reconnectable: false,
          interactionWidth: 24,
          zIndex: 1,
          style: edgeStyle(selected, condition),
        };
      })
    );
  }, [definition, editable, highlightedNodeSet, isInteractive, nodeTypes, selectedEdgeId, selectedLoopId, selectedNodeId, setEdges, setNodes, virtualEdges, virtualLoops, virtualNodeIds]);

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
        nodeTypes={{ debugNode: DebugNode, debugLoop: DebugLoop }}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={handleConnect}
        onNodeClick={(event, node) => {
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
        }}
        onNodeContextMenu={(event, node) => {
          if (!isInteractive) return;
          event.preventDefault();
          event.stopPropagation();
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
          onNodeContextMenu?.(node.id, screenPoint(event));
        }}
        onEdgeClick={(_, edge) => {
          onSelectEdge?.(flowEdgeSelectionId(edge));
          onSelectNode?.(null);
          onSelectLoop?.(null);
        }}
        onEdgeContextMenu={(event, edge) => {
          if (!isInteractive) return;
          event.preventDefault();
          event.stopPropagation();
          const selectionId = flowEdgeSelectionId(edge);
          onSelectEdge?.(selectionId);
          onSelectNode?.(null);
          onSelectLoop?.(null);
          onEdgeContextMenu?.(selectionId, screenPoint(event));
        }}
        onPaneClick={() => {
          onSelectNode?.(null);
          onSelectEdge?.(null);
          onSelectLoop?.(null);
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
          onNodePositionChange?.(node.id, node.position);
        }}
        minZoom={minZoom}
        maxZoom={maxZoom}
        nodesDraggable={isInteractive}
        nodesConnectable={isInteractive}
        elementsSelectable={interactive}
        edgesReconnectable={false}
        proOptions={{ hideAttribution: true }}
        className="debug-flow"
      >
        <MiniMap pannable zoomable position="bottom-right" className="!rounded-md !border !border-border !bg-panel" />
        <CanvasControls
          interactive={interactive}
          canAutoLayout={Boolean(definition)}
          hasSelection={Boolean(selectedNodeId || selectedEdgeId || selectedLoopId)}
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
    const nextZoom = Math.max(minZoom, Math.min(maxZoom, zoom * factor));
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
    if (selectedEdgeId) {
      const edge = edgesRef.current.find((item) => item.id === selectedEdgeId);
      if (!edge) return [];
      const ids = new Set([edge.source, edge.target]);
      return nodesRef.current.filter((node) => ids.has(node.id));
    }
    return [];
  }
}

function CanvasControls({
  interactive,
  canAutoLayout,
  hasSelection,
  onAutoLayout,
  onFitView,
  onFitSelection,
  onToggleInteractive,
  onZoomIn,
  onZoomOut,
}: {
  interactive: boolean;
  canAutoLayout: boolean;
  hasSelection: boolean;
  onAutoLayout?: () => void;
  onFitView: () => void;
  onFitSelection: () => void;
  onToggleInteractive: () => void;
  onZoomIn: () => void;
  onZoomOut: () => void;
}) {
  return (
    <Panel position="top-right" className="react-flow__controls vertical" aria-label="Canvas controls">
      <button type="button" className="react-flow__controls-button" title="Zoom in" aria-label="Zoom in" onClick={onZoomIn}>
        <ZoomIn className="h-3.5 w-3.5" />
      </button>
      <button type="button" className="react-flow__controls-button" title="Zoom out" aria-label="Zoom out" onClick={onZoomOut}>
        <ZoomOut className="h-3.5 w-3.5" />
      </button>
      <button type="button" className="react-flow__controls-button" title="Fit view" aria-label="Fit view" onClick={onFitView}>
        <Maximize2 className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        className="react-flow__controls-button"
        title="Auto layout"
        aria-label="Auto layout"
        onClick={onAutoLayout}
        disabled={!canAutoLayout || !onAutoLayout}
      >
        <Network className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        className="react-flow__controls-button"
        title="Fit selection"
        aria-label="Fit selection"
        onClick={onFitSelection}
        disabled={!hasSelection}
      >
        <Focus className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        className="react-flow__controls-button"
        title={interactive ? "Lock canvas" : "Unlock canvas"}
        aria-label={interactive ? "Lock canvas" : "Unlock canvas"}
        onClick={onToggleInteractive}
      >
        {interactive ? <Unlock className="h-3.5 w-3.5" /> : <Lock className="h-3.5 w-3.5" />}
      </button>
    </Panel>
  );
}

function edgeColor(selected: boolean, condition: boolean): string {
  if (selected) return "var(--flow-edge-selected)";
  return condition ? "#8b5cf6" : "var(--muted-foreground)";
}

function edgeStyle(selected: boolean, condition: boolean) {
  return {
    strokeWidth: selected ? 2.6 : 1.4,
    stroke: edgeColor(selected, condition),
  };
}

function flowEdgeSelectionId(edge: Edge): string {
  const selectionId = edge.data && typeof edge.data === "object" && "selectionId" in edge.data
    ? edge.data.selectionId
    : undefined;
  return typeof selectionId === "string" ? selectionId : edge.id;
}

function fitNodesToViewport(
  nodes: Node<FlowNodeData>[],
  viewportElement: HTMLDivElement | null,
  applyViewport: (viewport: Viewport) => void,
  padding = 0.2
): boolean {
  const rect = viewportElement?.getBoundingClientRect();
  if (!rect || rect.width <= 0 || rect.height <= 0 || nodes.length === 0) return false;

  const bounds = nodes.reduce(
    (current, node) => {
      const dimensions = flowNodeDimensions(node);
      return {
        minX: Math.min(current.minX, node.position.x),
        minY: Math.min(current.minY, node.position.y),
        maxX: Math.max(current.maxX, node.position.x + dimensions.width),
        maxY: Math.max(current.maxY, node.position.y + dimensions.height),
      };
    },
    { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity }
  );
  if (!Number.isFinite(bounds.minX) || !Number.isFinite(bounds.minY)) return false;

  const width = Math.max(bounds.maxX - bounds.minX, nodeWidth);
  const height = Math.max(bounds.maxY - bounds.minY, nodeHeight);
  const zoom = Math.max(
    0.2,
    Math.min(2, Math.min(rect.width / (width * (1 + padding * 2)), rect.height / (height * (1 + padding * 2))))
  );
  const centerX = bounds.minX + width / 2;
  const centerY = bounds.minY + height / 2;
  const viewport = {
    x: rect.width / 2 - centerX * zoom,
    y: rect.height / 2 - centerY * zoom,
    zoom,
  };
  applyViewport(viewport);
  return true;
}

function flowNodeDimensions(node: Node<FlowNodeData>): { width: number; height: number } {
  const dataWidth = typeof node.data.width === "number" ? node.data.width : 0;
  const dataHeight = typeof node.data.height === "number" ? node.data.height : 0;
  return {
    width: dataWidth || node.measured?.width || node.width || nodeWidth,
    height: dataHeight || node.measured?.height || node.height || nodeHeight,
  };
}

function sameViewport(a: Viewport, b: Viewport): boolean {
  return Math.abs(a.x - b.x) < 0.01 && Math.abs(a.y - b.y) < 0.01 && Math.abs(a.zoom - b.zoom) < 0.0001;
}

function normalizeViewportStorageKey(key?: string): string {
  const trimmed = key?.trim();
  return trimmed ? `${viewportStoragePrefix}${trimmed}` : "";
}

export function hasStoredGraphCanvasViewport(key?: string): boolean {
  return Boolean(readStoredCanvasViewport(normalizeViewportStorageKey(key)));
}

function readStoredCanvasViewport(key: string): Viewport | null {
  if (typeof window === "undefined" || !key) return null;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as StoredCanvasViewport;
    if (!isFiniteNumber(parsed.x) || !isFiniteNumber(parsed.y) || !isFiniteNumber(parsed.zoom)) {
      return null;
    }
    return {
      x: parsed.x,
      y: parsed.y,
      zoom: Math.max(minZoom, Math.min(maxZoom, parsed.zoom)),
    };
  } catch {
    return null;
  }
}

function writeStoredCanvasViewport(key: string | undefined, viewport: Viewport): void {
  const storageKey = normalizeViewportStorageKey(key);
  if (typeof window === "undefined" || !storageKey) return;
  if (!isFiniteNumber(viewport.x) || !isFiniteNumber(viewport.y) || !isFiniteNumber(viewport.zoom)) return;
  try {
    window.localStorage.setItem(
      storageKey,
      JSON.stringify({
        x: viewport.x,
        y: viewport.y,
        zoom: Math.max(minZoom, Math.min(maxZoom, viewport.zoom)),
      })
    );
  } catch {
    // Ignore storage errors; viewport persistence is best effort.
  }
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

interface D3ZoomTransform {
  x: number;
  y: number;
  k: number;
}

interface D3ZoomElement extends Element {
  __zoom?: D3ZoomTransform;
}

interface RuntimeNodeState {
  status: string;
  attempt: number;
  at: number;
}

function runtimeFromSteps(steps: StepRecord[], runId?: string): Map<string, RuntimeNodeState> {
  const runtime = new Map<string, RuntimeNodeState>();
  for (const step of steps) {
    if (!step.node_id) continue;
    if (runId && step.run_id && step.run_id !== runId) continue;
    applyRuntime(
      runtime,
      step.node_id,
      normalizeRuntimeStatus(step.status),
      Number.isFinite(step.attempt) ? step.attempt : 0,
      timeRank(step.updated_at || step.finished_at || step.started_at)
    );
  }
  return runtime;
}

function applyRuntime(
  runtime: Map<string, RuntimeNodeState>,
  nodeId: string,
  status: string,
  attempt: number,
  at: number
): boolean {
  const current = runtime.get(nodeId);
  const nextAttempt = Math.max(current?.attempt ?? 0, attempt);
  if (current && current.at > at) {
    if (nextAttempt === current.attempt) return false;
    runtime.set(nodeId, { ...current, attempt: nextAttempt });
    return true;
  }

  const next = { status, attempt: nextAttempt, at };
  if (current && current.status === next.status && current.attempt === next.attempt && current.at === next.at) {
    return false;
  }
  runtime.set(nodeId, next);
  return true;
}

function applyRuntimeSnapshot(
  nodes: Node<FlowNodeData>[],
  runtime: Map<string, RuntimeNodeState>
): Node<FlowNodeData>[] {
  let changed = false;
  const next = nodes.map((node) => {
    if (node.data.virtualKind) return node;
    const update = runtime.get(node.id);
    if (!update) return node;
    const updated = updateRuntimeNodeData(node, update);
    if (updated !== node) changed = true;
    return updated;
  });
  return changed ? next : nodes;
}

function updateRuntimeNode(
  nodes: Node<FlowNodeData>[],
  nodeId: string,
  runtime?: RuntimeNodeState
): Node<FlowNodeData>[] {
  if (!runtime) return nodes;
  let changed = false;
  const next = nodes.map((node) => {
    if (node.id !== nodeId || node.data.virtualKind) return node;
    const updated = updateRuntimeNodeData(node, runtime);
    if (updated !== node) changed = true;
    return updated;
  });
  return changed ? next : nodes;
}

function updateRuntimeNodeData(node: Node<FlowNodeData>, runtime: RuntimeNodeState): Node<FlowNodeData> {
  const attempt = runtime.attempt || 0;
  if (node.data.status === runtime.status && node.data.attempt === attempt) return node;
  return {
    ...node,
    data: {
      ...node.data,
      status: runtime.status,
      attempt,
    },
  };
}

function resetRuntimeNodes(nodes: Node<FlowNodeData>[]): Node<FlowNodeData>[] {
  let changed = false;
  const next = nodes.map((node) => {
    if (node.data.virtualKind) return node;
    if ((node.data.status || "idle") === "idle" && !node.data.attempt) return node;
    changed = true;
    return {
      ...node,
      data: {
        ...node.data,
        status: "idle",
        attempt: 0,
      },
    };
  });
  return changed ? next : nodes;
}

function syncRendererZoomState(viewportElement: HTMLElement | null, viewport: Viewport) {
  const root = viewportElement ?? document.body;
  const renderers = [
    ...root.querySelectorAll<D3ZoomElement>(".react-flow__renderer"),
    ...root.ownerDocument.querySelectorAll<D3ZoomElement>(".react-flow__renderer"),
  ];

  for (const renderer of new Set(renderers)) {
    const current = renderer.__zoom;
    if (!current) continue;

    // React Flow renders from store.transform, while d3-zoom keeps its own
    // __zoom state for the next drag gesture. Keep both aligned after custom
    // viewport changes so panning starts from the visible viewport.
    const ZoomTransform = current.constructor as new (k: number, x: number, y: number) => D3ZoomTransform;
    renderer.__zoom = new ZoomTransform(viewport.zoom, viewport.x, viewport.y);
  }
}

function normalizeRuntimeStatus(status: string): string {
  const lower = status.toLowerCase();
  if (!lower) return "idle";
  if (lower.includes("fail") || lower.includes("error")) return "failed";
  if (lower.includes("cancel")) return "canceled";
  if (lower.includes("pause")) return "paused";
  if (lower.includes("finish") || lower.includes("complete") || lower.includes("success")) return "succeeded";
  if (lower.includes("run") || lower.includes("start") || lower.includes("pend") || lower.includes("retry")) return "running";
  return lower;
}

function runtimeStatusFromEvent(type: string): string {
  switch (type) {
    case "nodes.started":
    case "nodes.retry":
      return "running";
    case "nodes.finished":
      return "succeeded";
    case "nodes.failed":
      return "failed";
    default:
      return "";
  }
}

function eventAttempt(payload: unknown): number {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return 0;
  const value = (payload as Record<string, unknown>).attempt;
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function timeRank(value?: string): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function DebugNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const status = String(data.status || "idle");
  const editable = Boolean(data.editable);
  const virtualKind = data.virtualKind;
  const attempt = typeof data.attempt === "number" && data.attempt > 0 ? data.attempt : 0;
  const highlighted = Boolean(data.highlighted);
  const className = `debug-node debug-node-${status}${virtualKind ? ` debug-node-virtual debug-node-virtual-${virtualKind}` : ""}${selected ? " debug-node-selected" : ""}${highlighted ? " debug-node-highlighted" : ""}`;
  if (virtualKind === "start" || virtualKind === "end") {
    return (
      <div className={className}>
        {virtualKind === "end" ? <Handle type="target" position={Position.Left} isConnectable={editable} /> : null}
        <div className="debug-node-virtual-label">{data.label}</div>
        {virtualKind === "start" ? <Handle type="source" position={Position.Right} isConnectable={editable} /> : null}
      </div>
    );
  }

  const typeLabel = humanizeNodeType(data.type);
  const showType = !data.bindingSummary || shouldShowNodeType(data.label, typeLabel);
  return (
    <div className={className}>
      <Handle type="target" position={Position.Left} isConnectable={editable} />
      <div className="debug-node-header">
        <span
          className="debug-node-status-dot"
          role="img"
          aria-label={`Execution status: ${status}`}
          title={`Execution status: ${status}`}
        />
        <div className="min-w-0 flex-1 truncate text-sm font-semibold">{data.label}</div>
      </div>
      <div className="debug-node-meta">
        <span className="debug-node-meta-main">
          {showType ? <span className="debug-node-type" title={typeLabel}>{typeLabel}</span> : null}
        </span>
        {attempt ? <span className="debug-node-attempt">#{attempt}</span> : null}
      </div>
      <Handle type="source" position={Position.Right} isConnectable={editable} />
    </div>
  );
}

function humanizeNodeType(value: string): string {
  return value
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .toLowerCase();
}

function shouldShowNodeType(label: string, typeLabel: string): boolean {
  const normalizedLabel = humanizeNodeType(label).replace(/\s+node$/, "");
  return Boolean(typeLabel) && !normalizedLabel.includes(typeLabel);
}

function DebugLoop({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const width = typeof data.width === "number" ? data.width : minLoopWidth;
  const height = typeof data.height === "number" ? data.height : minLoopHeight;
  return (
    <div className={`debug-loop${selected ? " debug-loop-selected" : ""}`} style={{ width, height }}>
      <Handle
        id={loopStartHandleId}
        type="target"
        position={Position.Left}
        className="debug-loop-handle debug-loop-handle-start"
        isConnectable={false}
        title="Loop start"
        aria-label="Loop start"
      />
      <Handle
        id={loopStartInnerHandleId}
        type="source"
        position={Position.Right}
        className="debug-loop-handle debug-loop-handle-start debug-loop-handle-pair"
        isConnectable={false}
        aria-hidden="true"
      />
      <Handle
        id={loopContinueHandleId}
        type="target"
        position={Position.Left}
        className="debug-loop-handle debug-loop-handle-continue"
        isConnectable={false}
        title="Continue loop"
        aria-label="Continue loop"
      />
      <Handle
        id={loopEndInnerHandleId}
        type="target"
        position={Position.Left}
        className="debug-loop-handle debug-loop-handle-end debug-loop-handle-pair"
        isConnectable={false}
        aria-hidden="true"
      />
      <Handle
        id={loopEndHandleId}
        type="source"
        position={Position.Right}
        className="debug-loop-handle debug-loop-handle-end"
        isConnectable={false}
        title="End loop"
        aria-label="End loop"
      />
      <span className="debug-loop-port-label debug-loop-port-label-continue">continue</span>
      <div className="debug-loop-title" data-loop-title>
        <span className="flex min-w-0 items-center gap-1.5 truncate">
          <Repeat2 className="h-3.5 w-3.5 shrink-0" />
          <span className="truncate">{data.label}</span>
        </span>
      </div>
    </div>
  );
}

interface VirtualLoopLayout {
  loop: VirtualGraphLoop;
  nodeIds: string[];
  nodeIdSet: Set<string>;
  position: NodePosition;
  width: number;
  height: number;
}

function virtualLoopLayouts(
  definition: GraphDefinition,
  loops: VirtualGraphLoop[],
  positions: Map<string, NodePosition>
): VirtualLoopLayout[] {
  const nodeIds = new Set(definition.nodes.map((node) => node.id));
  const savedPositions = graphNodePositions(definition);
  return loops.map((loop) => {
    const analysis = analyzeVirtualGraphLoop(definition, loop);
    const validNodeIds = analysis.nodeIds.filter((nodeID) => nodeIds.has(nodeID));
    const bounds = loopBounds(loop.id, validNodeIds, positions, savedPositions);
    return {
      loop,
      nodeIds: validNodeIds,
      nodeIdSet: new Set(validNodeIds),
      ...bounds,
    };
  });
}

function loopBounds(
  groupId: string,
  nodeIds: string[],
  positions: Map<string, NodePosition>,
  savedPositions: Map<string, NodePosition>
) {
  if (nodeIds.length === 0) {
    return {
      position: savedPositions.get(groupId) ?? { x: 0, y: 0 },
      width: minLoopWidth,
      height: minLoopHeight,
    };
  }

  const bounds = nodeIds.reduce(
    (current, nodeID) => {
      const position = positions.get(nodeID) ?? { x: 0, y: 0 };
      return {
        minX: Math.min(current.minX, position.x),
        minY: Math.min(current.minY, position.y),
        maxX: Math.max(current.maxX, position.x + nodeWidth),
        maxY: Math.max(current.maxY, position.y + nodeHeight),
      };
    },
    { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity }
  );

  if (!Number.isFinite(bounds.minX) || !Number.isFinite(bounds.minY)) {
    return {
      position: savedPositions.get(groupId) ?? { x: 0, y: 0 },
      width: minLoopWidth,
      height: minLoopHeight,
    };
  }

  return {
    position: {
      x: bounds.minX - loopPaddingX,
      y: bounds.minY - loopPaddingTop,
    },
    width: Math.max(minLoopWidth, bounds.maxX - bounds.minX + loopPaddingX * 2),
    height: Math.max(minLoopHeight, bounds.maxY - bounds.minY + loopPaddingTop + loopPaddingBottom),
  };
}

function layoutNodes(definition: GraphDefinition, virtualNodeIds: Set<string>) {
  const levels = new Map<string, number>();
  const outgoing = new Map<string, string[]>();
  const entry = definition.entry_point;
  const finish = definition.finish_point;
  const layoutEntry = entry || definition.nodes[0]?.id;
  const startVirtualNodeIds = [...virtualNodeIds].filter(isVirtualStartNodeId);
  const endVirtualNodeIds = [...virtualNodeIds].filter(isVirtualEndNodeId);
  const endVirtualNodeIdSet = new Set(endVirtualNodeIds);
  for (const edge of definition.edges ?? []) {
    outgoing.set(edge.from, [...(outgoing.get(edge.from) ?? []), edge.to]);
  }
  if (entry) {
    for (const startID of startVirtualNodeIds) {
      outgoing.set(startID, [entry]);
    }
  }
  if (finish) {
    outgoing.set(finish, [...(outgoing.get(finish) ?? []), ...endVirtualNodeIds]);
  }

  const queue: Array<{ id: string; level: number }> = [];
  if (startVirtualNodeIds.length > 0) {
    for (const startID of startVirtualNodeIds) {
      queue.push({ id: startID, level: -1 });
    }
  } else if (layoutEntry) {
    queue.push({ id: layoutEntry, level: 0 });
  }

  while (queue.length > 0) {
    const item = queue.shift()!;
    const current = levels.get(item.id);
    if (current !== undefined && current <= item.level) continue;
    levels.set(item.id, item.level);
    for (const next of outgoing.get(item.id) ?? []) {
      queue.push({ id: next, level: item.level + 1 });
    }
  }

  for (const node of definition.nodes) {
    if (!levels.has(node.id)) levels.set(node.id, 0);
  }
  for (const startID of startVirtualNodeIds) {
    levels.set(startID, Math.min(levels.get(startID) ?? -1, -1));
  }
  const maxLevel = Math.max(
    0,
    ...[...levels.entries()].filter(([id]) => !endVirtualNodeIdSet.has(id)).map(([, level]) => level)
  );
  for (const endID of endVirtualNodeIds) {
    levels.set(endID, Math.max(levels.get(endID) ?? maxLevel + 1, maxLevel + 1));
  }

  const buckets = new Map<number, string[]>();
  for (const id of [
    ...startVirtualNodeIds,
    ...definition.nodes.map((node) => node.id),
    ...endVirtualNodeIds,
  ]) {
    const level = levels.get(id) ?? 0;
    buckets.set(level, [...(buckets.get(level) ?? []), id]);
  }

  const positions = new Map<string, { x: number; y: number }>();
  const savedPositions = graphNodePositions(definition);
  for (const [level, ids] of buckets) {
    ids.forEach((id, index) => {
      positions.set(
        id,
        savedPositions.get(id) ?? {
          x: level * 260,
          y: index * 130,
        }
      );
    });
  }
  return positions;
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

function virtualNodeKind(nodeID: string): "start" | "end" | undefined {
  if (isVirtualStartNodeId(nodeID)) return "start";
  if (isVirtualEndNodeId(nodeID)) return "end";
  return undefined;
}

function virtualNodeLabel(nodeID: string): string {
  const kind = virtualNodeKind(nodeID);
  const index = virtualNodeIndex(nodeID);
  const label = kind === "start" ? "Start" : "End";
  return index > 1 ? `${label} ${index}` : label;
}

function virtualNodeIndex(nodeID: string): number {
  const kind = virtualNodeKind(nodeID);
  const base = kind === "start" ? START_NODE_REF : kind === "end" ? END_NODE_REF : "";
  const prefix = `${base}:`;
  if (!base || nodeID === base || !nodeID.startsWith(prefix)) return 1;
  const index = Number(nodeID.slice(prefix.length));
  return Number.isInteger(index) && index > 1 ? index : 1;
}

function isVirtualStartNodeId(nodeID: string): boolean {
  return nodeID === START_NODE_REF || nodeID.startsWith(`${START_NODE_REF}:`);
}

function isVirtualEndNodeId(nodeID: string): boolean {
  return nodeID === END_NODE_REF || nodeID.startsWith(`${END_NODE_REF}:`);
}

export const graphNodeDimensions = { width: nodeWidth, height: nodeHeight };
