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
import type { RunDetail } from "../replay/types";
import {
  applyProjectionToEdges,
  applyProjectionToNodes,
  buildBaseFlow,
  buildProjection,
  parseSourceGraph,
  type FlowNodeData,
} from "./graph";

const elk = new ELK();

export function ReplayGraphCanvas({
  detail,
  replayIndex,
  layoutVersion,
}: {
  detail: RunDetail | null;
  replayIndex: number;
  layoutVersion: number;
}) {
  return (
    <ReactFlowProvider>
      <ReplayGraphCanvasInner
        detail={detail}
        replayIndex={replayIndex}
        layoutVersion={layoutVersion}
      />
    </ReactFlowProvider>
  );
}

function ReplayGraphCanvasInner({
  detail,
  replayIndex,
  layoutVersion,
}: {
  detail: RunDetail | null;
  replayIndex: number;
  layoutVersion: number;
}) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<FlowNodeData>>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [graphError, setGraphError] = useState("");
  const flowRef = useRef<ReactFlowInstance<Node<FlowNodeData>, Edge> | null>(null);
  const graphKeyRef = useRef("");

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

      const projection = buildProjection(detail, sourceGraph, replayIndex);
      const baseFlow = buildBaseFlow(sourceGraph, projection);
      const layout = await computeElkLayout(baseFlow.nodes, baseFlow.edges);
      if (!active) return;

      graphKeyRef.current = detail.run.run_id;
      setGraphError("");
      setNodes(layout.nodes);
      setEdges(layout.edges);
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
  }, [detail, layoutVersion, setEdges, setNodes]);

  useEffect(() => {
    if (!detail || graphKeyRef.current !== detail.run.run_id) return;
    const sourceGraph = parseSourceGraph(detail.source.graph);
    if (!sourceGraph || sourceGraph.nodes.length === 0) return;
    const projection = buildProjection(detail, sourceGraph, replayIndex);
    setNodes((current) => applyProjectionToNodes(current, sourceGraph, projection));
    setEdges((current) => applyProjectionToEdges(current, projection));
  }, [detail, replayIndex, setEdges, setNodes]);

  return (
    <div className="absolute inset-0">
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(56,189,248,0.14),transparent_28%),radial-gradient(circle_at_top_right,rgba(251,191,36,0.10),transparent_22%),linear-gradient(180deg,#0b1120,#111a2e_58%,#162235)]" />
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
        defaultEdgeOptions={{ type: "bezier" }}
        proOptions={{ hideAttribution: true }}
        className="replay-v2-flow relative z-10"
      >
        <MiniMap
          pannable
          zoomable
          position="bottom-right"
          className="!rounded-xl !border !border-slate-500/60 !bg-slate-900/92 !shadow-xl"
          nodeStrokeColor={(node) => String(node.style?.borderColor ?? "#94a3b8")}
          nodeColor={(node) => {
            const background = node.style?.background;
            return typeof background === "string" ? background : "#e2e8f0";
          }}
        />
        <Controls position="bottom-left" className="!shadow-xl" />
        <Background gap={20} size={1.1} color="rgba(148, 163, 184, 0.18)" />
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
      width: 260,
      height: 156,
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
