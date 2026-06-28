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
import { Lock, Maximize2, Unlock, ZoomIn, ZoomOut } from "lucide-react";
import type { GraphDefinition, GraphNodeSpec, RuntimeEvent, StepRecord } from "../types";
import { END_NODE_REF, START_NODE_REF, graphEdgeId, graphNodePositions, type NodePosition } from "../lib/graphEditor";

interface FlowNodeData extends Record<string, unknown> {
  label: string;
  type: string;
  status: string;
  editable: boolean;
  virtualKind?: "start" | "end";
}

export interface VirtualGraphEdge {
  id: string;
  from: string;
  to: string;
  kind: "entry" | "finish";
}

const nodeWidth = 190;
const nodeHeight = 76;
const minZoom = 0.2;
const maxZoom = 2;

export function GraphCanvas({
  definition,
  steps,
  events,
  editable = false,
  selectedNodeId,
  selectedEdgeId,
  fitViewSignal = 0,
  virtualNodeIds = [START_NODE_REF, END_NODE_REF],
  virtualEdges = [],
  onSelectNode,
  onSelectEdge,
  onNodePositionChange,
  onConnectNodes,
  onCreateNodeAt,
  onNodeContextMenu,
  onEdgeContextMenu,
}: {
  definition: GraphDefinition | null;
  steps: StepRecord[];
  events: RuntimeEvent[];
  editable?: boolean;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  fitViewSignal?: number;
  virtualNodeIds?: string[];
  virtualEdges?: VirtualGraphEdge[];
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onNodePositionChange?: (nodeId: string, position: NodePosition) => void;
  onConnectNodes?: (source: string, target: string) => void;
  onCreateNodeAt?: (position: NodePosition, screenPosition: NodePosition) => void;
  onNodeContextMenu?: (nodeId: string, screenPosition: NodePosition) => void;
  onEdgeContextMenu?: (edgeId: string, screenPosition: NodePosition) => void;
}) {
  return (
    <ReactFlowProvider>
      <GraphCanvasInner
        definition={definition}
        steps={steps}
        events={events}
        editable={editable}
        selectedNodeId={selectedNodeId}
        selectedEdgeId={selectedEdgeId}
        fitViewSignal={fitViewSignal}
        virtualNodeIds={virtualNodeIds}
        virtualEdges={virtualEdges}
        onSelectNode={onSelectNode}
        onSelectEdge={onSelectEdge}
        onNodePositionChange={onNodePositionChange}
        onConnectNodes={onConnectNodes}
        onCreateNodeAt={onCreateNodeAt}
        onNodeContextMenu={onNodeContextMenu}
        onEdgeContextMenu={onEdgeContextMenu}
      />
    </ReactFlowProvider>
  );
}

function GraphCanvasInner({
  definition,
  steps,
  events,
  editable,
  selectedNodeId,
  selectedEdgeId,
  fitViewSignal,
  virtualNodeIds,
  virtualEdges,
  onSelectNode,
  onSelectEdge,
  onNodePositionChange,
  onConnectNodes,
  onCreateNodeAt,
  onNodeContextMenu,
  onEdgeContextMenu,
}: {
  definition: GraphDefinition | null;
  steps: StepRecord[];
  events: RuntimeEvent[];
  editable: boolean;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  fitViewSignal: number;
  virtualNodeIds: string[];
  virtualEdges: VirtualGraphEdge[];
  onSelectNode?: (nodeId: string | null) => void;
  onSelectEdge?: (edgeId: string | null) => void;
  onNodePositionChange?: (nodeId: string, position: NodePosition) => void;
  onConnectNodes?: (source: string, target: string) => void;
  onCreateNodeAt?: (position: NodePosition, screenPosition: NodePosition) => void;
  onNodeContextMenu?: (nodeId: string, screenPosition: NodePosition) => void;
  onEdgeContextMenu?: (edgeId: string, screenPosition: NodePosition) => void;
}) {
  const { screenToFlowPosition, viewportInitialized } = useReactFlow();
  const store = useStoreApi();
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<FlowNodeData>>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [interactive, setInteractive] = useState(editable);
  const handledFitViewSignal = useRef(0);
  const flowWrapperRef = useRef<HTMLDivElement | null>(null);
  const nodesRef = useRef<Node<FlowNodeData>[]>([]);
  const isInteractive = editable && interactive;

  useEffect(() => {
    setInteractive(editable);
  }, [editable]);

  const nodeStatus = useMemo(() => {
    const status = new Map<string, string>();
    for (const step of steps) {
      status.set(step.node_id, step.status);
    }
    for (const event of events) {
      if (!event.node_id) continue;
      if (event.type === "nodes.started") status.set(event.node_id, "running");
      if (event.type === "nodes.finished") status.set(event.node_id, "succeeded");
      if (event.type === "nodes.failed") status.set(event.node_id, "failed");
    }
    return status;
  }, [events, steps]);

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
    setNodes(
      displayNodes.map((node) => {
        const virtualKind = virtualNodeKind(node.id);
        return {
          id: node.id,
          type: "debugNode",
          position: positions.get(node.id) ?? { x: 0, y: 0 },
          draggable: editable,
          selectable: true,
          selected: node.id === selectedNodeId,
          data: {
            label: node.name || node.id,
            type: node.type || "node",
            status: virtualKind ? "idle" : nodeStatus.get(node.id) || "idle",
            editable: isInteractive,
            virtualKind,
          },
        };
      })
    );

    setEdges([
      ...virtualEdges.map((edge) => {
        const selected = edge.id === selectedEdgeId;
        return {
          id: edge.id,
          source: edge.from,
          target: edge.to,
          label: edge.kind,
          animated: true,
          selected,
          reconnectable: false,
          style: {
            stroke: edge.kind === "entry" ? "var(--flow-edge-entry)" : "var(--flow-edge-finish)",
            strokeDasharray: "5 4",
            strokeWidth: selected ? 2.6 : 1.6,
          },
        };
      }),
      ...(definition.edges ?? []).map((edge, index) => ({
        id: graphEdgeId(edge, index),
        source: edge.from,
        target: edge.to,
        label: edge.condition?.type,
        animated: Boolean(edge.condition),
        selected: graphEdgeId(edge, index) === selectedEdgeId,
        reconnectable: false,
        style: {
          strokeWidth: graphEdgeId(edge, index) === selectedEdgeId ? 2.6 : 1.4,
          stroke: graphEdgeId(edge, index) === selectedEdgeId ? "var(--flow-edge-selected)" : undefined,
        },
      })),
    ]);
  }, [definition, editable, isInteractive, nodeStatus, selectedEdgeId, selectedNodeId, setEdges, setNodes, virtualEdges, virtualNodeIds]);

  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);

  const applyViewport = useCallback(
    (nextViewport: Viewport) => {
      const state = store.getState();
      if (state.panZoom) {
        void state.panZoom.setViewport(nextViewport);
        store.setState({ transform: [nextViewport.x, nextViewport.y, nextViewport.zoom] });
        syncRendererZoomState(flowWrapperRef.current, nextViewport);
        return;
      }
      const currentTransform = state.transform;
      const current = { x: currentTransform[0], y: currentTransform[1], zoom: currentTransform[2] };
      if (sameViewport(current, nextViewport)) return;
      store.setState({ transform: [nextViewport.x, nextViewport.y, nextViewport.zoom] });
    },
    [store]
  );

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

  function handleConnect(connection: Connection) {
    if (!isInteractive || !connection.source || !connection.target) return;
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
        nodeTypes={{ debugNode: DebugNode }}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={handleConnect}
        onNodeClick={(_, node) => {
          onSelectNode?.(node.id);
          onSelectEdge?.(null);
        }}
        onNodeContextMenu={(event, node) => {
          if (!isInteractive) return;
          event.preventDefault();
          event.stopPropagation();
          onSelectNode?.(node.id);
          onSelectEdge?.(null);
          onNodeContextMenu?.(node.id, screenPoint(event));
        }}
        onEdgeClick={(_, edge) => {
          onSelectEdge?.(edge.id);
          onSelectNode?.(null);
        }}
        onEdgeContextMenu={(event, edge) => {
          if (!isInteractive) return;
          event.preventDefault();
          event.stopPropagation();
          onSelectEdge?.(edge.id);
          onSelectNode?.(null);
          onEdgeContextMenu?.(edge.id, screenPoint(event));
        }}
        onPaneClick={() => {
          onSelectNode?.(null);
          onSelectEdge?.(null);
        }}
        onPaneContextMenu={(event) => {
          if (!isInteractive) return;
          event.preventDefault();
          const position = screenToFlowPosition({ x: event.clientX, y: event.clientY });
          onCreateNodeAt?.(position, screenPoint(event));
        }}
        onNodeDragStop={(_, node) => {
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
          onFitView={() => fitNodesToViewport(nodesRef.current, flowWrapperRef.current, applyViewport)}
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
}

function CanvasControls({
  interactive,
  onFitView,
  onToggleInteractive,
  onZoomIn,
  onZoomOut,
}: {
  interactive: boolean;
  onFitView: () => void;
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
        title={interactive ? "Lock canvas" : "Unlock canvas"}
        aria-label={interactive ? "Lock canvas" : "Unlock canvas"}
        onClick={onToggleInteractive}
      >
        {interactive ? <Unlock className="h-3.5 w-3.5" /> : <Lock className="h-3.5 w-3.5" />}
      </button>
    </Panel>
  );
}

function fitNodesToViewport(
  nodes: Node<FlowNodeData>[],
  viewportElement: HTMLDivElement | null,
  applyViewport: (viewport: Viewport) => void
): boolean {
  const rect = viewportElement?.getBoundingClientRect();
  if (!rect || rect.width <= 0 || rect.height <= 0 || nodes.length === 0) return false;

  const bounds = nodes.reduce(
    (current, node) => ({
      minX: Math.min(current.minX, node.position.x),
      minY: Math.min(current.minY, node.position.y),
      maxX: Math.max(current.maxX, node.position.x + nodeWidth),
      maxY: Math.max(current.maxY, node.position.y + nodeHeight),
    }),
    { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity }
  );
  if (!Number.isFinite(bounds.minX) || !Number.isFinite(bounds.minY)) return false;

  const width = Math.max(bounds.maxX - bounds.minX, nodeWidth);
  const height = Math.max(bounds.maxY - bounds.minY, nodeHeight);
  const padding = 0.2;
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

function sameViewport(a: Viewport, b: Viewport): boolean {
  return Math.abs(a.x - b.x) < 0.01 && Math.abs(a.y - b.y) < 0.01 && Math.abs(a.zoom - b.zoom) < 0.0001;
}

interface D3ZoomTransform {
  x: number;
  y: number;
  k: number;
}

interface D3ZoomElement extends Element {
  __zoom?: D3ZoomTransform;
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

function DebugNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const status = String(data.status || "idle");
  const editable = Boolean(data.editable);
  const virtualKind = data.virtualKind;
  return (
    <div className={`debug-node debug-node-${status}${virtualKind ? " debug-node-virtual" : ""}${selected ? " debug-node-selected" : ""}`}>
      {editable && virtualKind !== "start" ? <Handle type="target" position={Position.Left} /> : null}
      <div className="truncate text-sm font-semibold">{data.label}</div>
      <div className="mt-1 flex items-center justify-between gap-3 text-xs text-muted-foreground">
        <span className="truncate">{data.type}</span>
        <span>{status}</span>
      </div>
      {editable && virtualKind !== "end" ? <Handle type="source" position={Position.Right} /> : null}
    </div>
  );
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
