import type { Edge, Node } from "@xyflow/react";
import type {
  GraphConditionSpec,
  GraphDefinition,
  GraphNodeSpec,
  NodeTypeSchema,
  TriggerCanvasNode,
} from "../types";
import {
  matchesDynamicStatePortName,
  resolveDefaultStatePath,
} from "../lib/graphEditor";
import {
  conditionDisplayLabel,
  edgeSegmentsForLoopDisplay,
  graphEdgesForLoopDisplay,
  type VirtualGraphLoop,
} from "../lib/loopPresentation";
import {
  isVirtualEndNodeID,
  isVirtualStartNodeID,
  layoutNodes,
  triggerTargetHandleID,
  virtualLoopLayouts,
  virtualNodeKind,
  virtualNodeSpec,
  type FlowNodeData,
  type RuntimeNodeState,
} from "./graphCanvasModel";

export interface VirtualGraphEdge {
  id: string;
  from: string;
  to: string;
  kind: "entry" | "finish";
  condition?: GraphConditionSpec;
}

export interface GraphCanvasElements {
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
}

export interface GraphCanvasElementOptions {
  definition: GraphDefinition | null;
  editable: boolean;
  interactive: boolean;
  highlightedNodeIDs: ReadonlySet<string>;
  nodeTypes: NodeTypeSchema[];
  runtime: ReadonlyMap<string, RuntimeNodeState>;
  selectedEdgeID?: string;
  selectedLoopID?: string;
  selectedNodeID?: string;
  selectedTriggerID?: string;
  triggerNodes: TriggerCanvasNode[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
  virtualNodeIDs: string[];
}

export function buildGraphCanvasElements({
  definition,
  editable,
  interactive,
  highlightedNodeIDs,
  nodeTypes,
  runtime,
  selectedEdgeID,
  selectedLoopID,
  selectedNodeID,
  selectedTriggerID,
  triggerNodes,
  virtualEdges,
  virtualLoops,
  virtualNodeIDs,
}: GraphCanvasElementOptions): GraphCanvasElements {
  if (!definition) return { nodes: [], edges: [] };

  const visibleVirtualNodeIDs = new Set(virtualNodeIDs);
  const startVirtualNodeIDs = virtualNodeIDs.filter(isVirtualStartNodeID);
  const endVirtualNodeIDs = virtualNodeIDs.filter(isVirtualEndNodeID);
  const displayNodes: GraphNodeSpec[] = [
    ...startVirtualNodeIDs.map(virtualNodeSpec),
    ...definition.nodes,
    ...endVirtualNodeIDs.map(virtualNodeSpec),
  ];
  const positions = layoutNodes(definition, visibleVirtualNodeIDs);
  const loopLayouts = virtualLoopLayouts(definition, virtualLoops, positions);
  const nodes: Node<FlowNodeData>[] = [
    ...loopLayouts.map((layout): Node<FlowNodeData> => ({
      id: layout.loop.id,
      type: "debugLoop",
      className: "debug-loop-node",
      position: layout.position,
      draggable: editable,
      dragHandle: ".debug-loop-title",
      selectable: true,
      selected: layout.loop.id === selectedLoopID,
      zIndex: 0,
      data: {
        label: layout.loop.name || "Loop",
        type: "loop",
        status: "idle",
        editable: interactive,
        virtualKind: "loop",
        width: layout.width,
        height: layout.height,
      },
      style: {
        width: layout.width,
        height: layout.height,
      },
    })),
    ...triggerNodes.map((item): Node<FlowNodeData> => ({
      id: item.canvas_id,
      type: "debugTrigger",
      position: item.position,
      draggable: editable,
      selectable: true,
      selected: item.trigger.id === selectedTriggerID,
      zIndex: 2,
      data: {
        label: item.trigger.name || item.trigger.id,
        type: item.trigger.type,
        status: item.trigger.enabled ? "enabled" : "disabled",
        editable: false,
        virtualKind: "trigger",
        triggerID: item.trigger.id,
        triggerType: item.trigger.type,
        triggerEnabled: item.trigger.enabled,
        triggerValid: item.valid,
      },
    })),
    ...displayNodes.map((node): Node<FlowNodeData> => {
      const virtualKind = virtualNodeKind(node.id);
      const nodeType = nodeTypes.find((item) => item.type === node.type);
      const statePorts = nodeType?.state_ports ?? [];
      const staticPortNames = new Set(statePorts.map((port) => port.name));
      const dynamicPortNames = Object.keys(node.state ?? {}).filter(
        (name) => !staticPortNames.has(name)
          && matchesDynamicStatePortName(name, nodeType?.dynamic_state_ports)
      );
      const boundPortCount = statePorts.filter((port) => Boolean(
        node.state?.[port.name]?.path.trim()
          || resolveDefaultStatePath(port.default_path, node.id)
      )).length + dynamicPortNames.filter((name) => Boolean(node.state?.[name]?.path.trim())).length;
      const totalPortCount = statePorts.length + dynamicPortNames.length;
      const missingBindings = statePorts.some((port) =>
        port.required
          && !node.state?.[port.name]?.path.trim()
          && !resolveDefaultStatePath(port.default_path, node.id)
      );
      const dynamicMinimum = nodeType?.dynamic_state_ports?.min_ports ?? 0;
      const missingDynamicBindings = dynamicPortNames.length < dynamicMinimum;
      return {
        id: node.id,
        type: "debugNode",
        position: positions.get(node.id) ?? { x: 0, y: 0 },
        draggable: editable,
        selectable: true,
        selected: node.id === selectedNodeID,
        zIndex: 2,
        data: {
          label: node.name || node.id,
          type: node.type || "node",
          status: virtualKind ? "idle" : runtime.get(node.id)?.status || "idle",
          attempt: virtualKind ? 0 : runtime.get(node.id)?.attempt || 0,
          editable: interactive,
          highlighted: highlightedNodeIDs.has(node.id),
          bindingSummary: virtualKind || totalPortCount === 0
            ? undefined
            : `${boundPortCount}/${totalPortCount} state`,
          missingBindings: missingBindings || missingDynamicBindings,
          virtualKind,
        },
      };
    }),
  ];

  const displayVirtualEdges = virtualEdges.flatMap((edge) =>
    edgeSegmentsForLoopDisplay(
      definition,
      { from: edge.from, to: edge.to, condition: edge.condition },
      edge.id,
      virtualLoops
    )
  );
  const displayGraphEdges = graphEdgesForLoopDisplay(definition, virtualLoops);
  const flowEdges: Edge[] = [...displayVirtualEdges, ...displayGraphEdges].map(({
    edge,
    id,
    selectionId = id,
    source,
    target,
    sourceHandle,
    targetHandle,
    showLabel = true,
    contained = false,
  }): Edge => {
    const selected = selectionId === selectedEdgeID;
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
      labelBgPadding: showLabel && condition ? [7, 4] : undefined,
      labelBgBorderRadius: showLabel && condition ? 5 : undefined,
      animated: showLabel && condition,
      selected,
      reconnectable: false,
      interactionWidth: 24,
      zIndex: 1,
      style: edgeStyle(selected, condition),
    };
  });
  const triggerTarget = startVirtualNodeIDs[0] ?? definition.entry_point;
  const triggerEdges: Edge[] = triggerTarget
    ? triggerNodes.map((item) => ({
        id: `trigger-edge:${item.canvas_id}`,
        source: item.canvas_id,
        target: triggerTarget,
        selectable: false,
        reconnectable: false,
        focusable: false,
        interactionWidth: 0,
        zIndex: 1,
        data: { triggerEdge: true },
        targetHandle: startVirtualNodeIDs.length > 0 ? triggerTargetHandleID : undefined,
        style: {
          stroke: "var(--muted-foreground)",
          strokeDasharray: "6 5",
          strokeWidth: 1.5,
          opacity: item.trigger.enabled ? 0.8 : 0.4,
        },
      }))
    : [];
  return { nodes, edges: [...flowEdges, ...triggerEdges] };
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
