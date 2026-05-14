import { useEffect, useRef, useState } from "react";
import ELK from "elkjs/lib/elk.bundled.js";
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  type Edge,
  type Node,
  type ReactFlowInstance,
} from "@xyflow/react";
import type { RunDetail } from "./types";
import {
  applyProjectionToEdges,
  applyProjectionToNodes,
  buildBaseFlow,
  buildProjection,
  parseSourceGraph,
  type FlowNodeData,
  type SourceGraph,
} from "./graph";

const elk = new ELK();

export function ReplayGraphCanvas({
  cacheDir,
  detail,
  replayIndex,
  layoutVersion,
}: {
  cacheDir: string;
  detail: RunDetail | null;
  replayIndex: number;
  layoutVersion: number;
}) {
  return (
    <ReactFlowProvider>
      <ReplayGraphCanvasInner
        cacheDir={cacheDir}
        detail={detail}
        replayIndex={replayIndex}
        layoutVersion={layoutVersion}
      />
    </ReactFlowProvider>
  );
}

function ReplayGraphCanvasInner({
  cacheDir,
  detail,
  replayIndex,
  layoutVersion,
}: {
  cacheDir: string;
  detail: RunDetail | null;
  replayIndex: number;
  layoutVersion: number;
}) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<FlowNodeData>>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [graphError, setGraphError] = useState("");
  const flowRef = useRef<ReactFlowInstance<Node<FlowNodeData>, Edge> | null>(null);
  const graphKeyRef = useRef("");
  const positionCacheRef = useRef<Map<string, Map<string, { x: number; y: number }>>>(new Map());

  useEffect(() => {
    const graphKey = graphKeyRef.current;
    if (!graphKey || nodes.length === 0) return;

    positionCacheRef.current.set(
      graphKey,
      new Map(nodes.map((node) => [node.id, { x: node.position.x, y: node.position.y }]))
    );
  }, [nodes]);

  useEffect(() => {
    let active = true;

    async function layoutGraph() {
      if (!detail) {
        graphKeyRef.current = "";
        setNodes([]);
        setEdges([]);
        setGraphError("");
        return;
      }

      const sourceGraph = parseSourceGraph(detail.source.graph);
      if (!sourceGraph || sourceGraph.nodes.length === 0) {
        graphKeyRef.current = "";
        setNodes([]);
        setEdges([]);
        setGraphError("source.graph 里没有可渲染的 nodes/edges");
        return;
      }

      const runId = detail.run.run_id;
      const sourceId = detail.source.id;
      const graphKey = replayGraphLayoutKey(sourceGraph);

      // Use full replay to compute stable node heights (so card sizes don't shift during playback)
      const maxProjection = buildProjection(detail, sourceGraph, detail.replay.length - 1);
      const baseFlow = buildBaseFlow(sourceGraph, maxProjection, runId, sourceId, cacheDir);
      const layout = await computeElkLayout(baseFlow.nodes, baseFlow.edges);
      if (!active) return;

      graphKeyRef.current = graphKey;
      setGraphError("");

      // Apply current replayIndex projection for initial styling
      const currentProjection = buildProjection(detail, sourceGraph, replayIndex);
      setNodes(
        applyProjectionToNodes(
          applyCachedPositions(layout.nodes, positionCacheRef.current.get(graphKey)),
          sourceGraph,
          currentProjection,
          runId,
          sourceId,
          cacheDir
        )
      );
      setEdges(applyProjectionToEdges(layout.edges, currentProjection));
      requestAnimationFrame(() => {
        flowRef.current?.fitView({ padding: 0.18, duration: 450 });
      });
    }

    layoutGraph().catch((error) => {
      if (!active) return;
      setNodes([]);
      setEdges([]);
      setGraphError((error as Error).message);
    });

    return () => {
      active = false;
    };
  }, [cacheDir, detail, layoutVersion, setEdges, setNodes]);

  useEffect(() => {
    if (!detail) return;
    const sourceGraph = parseSourceGraph(detail.source.graph);
    if (!sourceGraph || sourceGraph.nodes.length === 0) return;
    if (graphKeyRef.current !== replayGraphLayoutKey(sourceGraph)) return;
    const projection = buildProjection(detail, sourceGraph, replayIndex);
    const runId = detail.run.run_id;
    const sourceId = detail.source.id;
    setNodes((current) =>
      applyProjectionToNodes(current, sourceGraph, projection, runId, sourceId, cacheDir)
    );
    setEdges((current) => applyProjectionToEdges(current, projection));
  }, [cacheDir, detail, replayIndex, setEdges, setNodes]);

  return (
    <div className="absolute inset-0">
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(56,189,248,0.04),transparent_28%),radial-gradient(circle_at_top_right,rgba(251,191,36,0.03),transparent_22%),linear-gradient(180deg,#0f172a,#0f172a_60%,#0d1424)]" />
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onInit={(instance) => {
          flowRef.current = instance;
        }}
        fitView
        minZoom={0.2}
        maxZoom={2}
        nodesDraggable
        nodesConnectable={false}
        elementsSelectable
        panOnDrag
        defaultEdgeOptions={{ type: "default" }}
        proOptions={{ hideAttribution: true }}
        className="replay-flow relative z-10"
      >
        <MiniMap
          pannable
          zoomable
          position="bottom-right"
          className="!rounded-xl !border !border-slate-700/60 !bg-slate-900/90 !shadow-xl"
          nodeStrokeColor={(node) => String(node.style?.borderColor ?? "#334155")}
          nodeColor={(node) => {
            const background = node.style?.background;
            return typeof background === "string" ? background : "#1e293b";
          }}
        />
        <Controls position="top-right" className="!shadow-xl" />
        <Background gap={20} size={1.1} color="rgba(148, 163, 184, 0.08)" />
      </ReactFlow>
      {graphError ? (
        <div className="absolute inset-x-0 bottom-6 flex justify-center px-6">
          <div className="rounded-2xl border border-destructive/25 bg-white/95 px-4 py-3 text-sm text-destructive shadow-xl backdrop-blur">
            {graphError}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function replayGraphLayoutKey(sourceGraph: SourceGraph): string {
  const nodeKey = sourceGraph.nodes.map((node) => `${node.id}:${node.type}`).join("|");
  const edgeKey = sourceGraph.edges.map((edge) => `${edge.from}>${edge.to}:${edge.label}`).join("|");
  return `${sourceGraph.entry_point ?? ""}::${sourceGraph.finish_point ?? ""}::${nodeKey}::${edgeKey}`;
}

function applyCachedPositions(
  nodes: Node<FlowNodeData>[],
  cache?: Map<string, { x: number; y: number }>
): Node<FlowNodeData>[] {
  if (!cache || cache.size === 0) {
    return nodes;
  }

  return nodes.map((node) => {
    const cached = cache.get(node.id);
    if (!cached) {
      return node;
    }
    return {
      ...node,
      position: { x: cached.x, y: cached.y },
    };
  });
}

async function computeElkLayout(nodes: Node<FlowNodeData>[], edges: Edge[]) {
  const layout = await elk.layout({
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.edgeRouting": "SPLINES",
      "elk.separateConnectedComponents": "true",
      "elk.spacing.nodeNode": "96",
      "elk.spacing.edgeNode": "32",
      "elk.spacing.edgeEdge": "24",
      "elk.layered.spacing.nodeNodeBetweenLayers": "160",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.thoroughness": "12",
      "elk.padding": "[top=56,left=56,bottom=56,right=56]",
    },
    children: nodes.map((node) => ({
      id: node.id,
      width: node.data.elkWidth,
      height: node.data.elkHeight,
      layoutOptions:
        node.data.meta.type === "start"
          ? { "elk.layered.layering.layerConstraint": "FIRST" }
          : node.data.meta.type === "end"
            ? { "elk.layered.layering.layerConstraint": "LAST" }
            : undefined,
    })),
    edges: edges.map((edge) => ({
      id: edge.id,
      sources: [edge.source],
      targets: [edge.target],
    })),
  });

  const positioned = new Map(layout.children?.map((node) => [node.id, node]) ?? []);
  return {
    nodes: nodes.map((node) => {
      const positionedNode = positioned.get(node.id);
      return {
        ...node,
        position: {
          x: positionedNode?.x ?? 0,
          y: positionedNode?.y ?? 0,
        },
      };
    }),
    edges,
  };
}
