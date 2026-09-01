import type { Edge, Node } from "@xyflow/react";
import type {
  GraphConditionSpec,
  GraphDefinition,
  GraphNodeSpec,
  NodeTypeSchema,
  RunStatus,
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
  type LoopDisplayEdge,
  type VirtualGraphLoop,
} from "../lib/loopPresentation";
import {
  flowNodeAriaLabel,
  isVirtualEndNodeID,
  isVirtualStartNodeID,
  layoutNodes,
  runtimeDurations,
  triggerNodeRuntimeStatus,
  triggerTargetHandleID,
  virtualLoopLayouts,
  virtualNodeKind,
  virtualNodeRuntimeStatus,
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
  configurationErrors: ReadonlyMap<string, readonly string[]>;
  editable: boolean;
  interactive: boolean;
  highlightedNodeIDs: ReadonlySet<string>;
  nodeTypes: NodeTypeSchema[];
  runtime: ReadonlyMap<string, RuntimeNodeState>;
  runtimeVisible: boolean;
  runtimeNow?: number;
  currentNodeIDs?: ReadonlySet<string>;
  currentNodeStartedAt?: number;
  runStatus?: RunStatus;
  runTriggerID?: string;
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
  configurationErrors,
  editable,
  interactive,
  highlightedNodeIDs,
  nodeTypes,
  runtime,
  runtimeVisible,
  runtimeNow = Date.now(),
  currentNodeIDs = new Set(),
  currentNodeStartedAt = 0,
  runStatus,
  runTriggerID,
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
        runtimeVisible,
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
        label: item.label,
        type: item.trigger.type,
        status: runtimeVisible && item.trigger.id === runTriggerID
          ? triggerNodeRuntimeStatus(runStatus)
          : "idle",
        editable: false,
        runtimeVisible,
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
      const runtimeState = runtimeVisible ? runtime.get(node.id) : undefined;
      const currentFromRun = runtimeVisible && currentNodeIDs.has(node.id);
      const currentProjectionStatus = currentFromRun && runStatus === "running"
        ? "running"
        : currentFromRun && runStatus === "paused"
          ? "paused"
          : "idle";
      const status = runtimeState?.status || currentProjectionStatus;
      const durations = runtimeVisible
        ? runtimeDurations(runtimeState || (currentFromRun
          ? { status: "running", executionCount: 0, at: currentNodeStartedAt || runtimeNow }
          : undefined), runtimeNow)
        : { totalMs: 0, currentMs: 0 };
      const current = runtimeVisible && (status === "running" || (currentFromRun && !runtimeState && status === "paused"));
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
      const data: FlowNodeData = {
        label: node.name || node.id,
        type: node.type || "node",
        typeLabel: nodeType?.title || node.type || "Node",
        status: virtualKind && runtimeVisible
          ? virtualNodeRuntimeStatus(virtualKind, runStatus)
          : status,
        executionCount: virtualKind ? 0 : runtimeState?.executionCount || 0,
        runTimeMs: virtualKind || !runtimeVisible || (status === "idle" && durations.totalMs <= 0 && !runtimeState?.executionCount)
          ? undefined
          : durations.totalMs,
        currentRunTimeMs: virtualKind || !runtimeVisible || status !== "running"
          ? undefined
          : durations.currentMs,
        current: virtualKind ? undefined : current || undefined,
        editable: interactive,
        runtimeVisible,
        highlighted: highlightedNodeIDs.has(node.id),
        bindingSummary: virtualKind || totalPortCount === 0
          ? undefined
          : `${boundPortCount}/${totalPortCount} state`,
        stateBindingPreview: virtualKind ? undefined : stateBindingPreview(node, nodeType),
        missingBindings: missingBindings || missingDynamicBindings,
        configurationSummary: virtualKind ? undefined : importantConfigurationSummary(node, nodeType),
        configurationPreview: virtualKind ? undefined : configurationPreview(node),
        configurationErrors: virtualKind ? undefined : configurationErrors.get(node.id),
        errorSummary: virtualKind || !runtimeVisible ? undefined : runtimeState?.errorMessage,
        virtualKind,
      };
      return {
        id: node.id,
        type: "debugNode",
        ariaLabel: flowNodeAriaLabel(data),
        position: positions.get(node.id) ?? { x: 0, y: 0 },
        draggable: editable,
        selectable: true,
        selected: node.id === selectedNodeID,
        zIndex: 2,
        data,
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
  const displayEdges = [...displayVirtualEdges, ...displayGraphEdges];
  const edgeOffsets = parallelEdgeOffsets(displayEdges);
  const flowEdges: Edge[] = displayEdges.map(({
    edge,
    id,
    selectionId = id,
    source,
    target,
    sourceHandle,
    targetHandle,
    showLabel = true,
    contained = false,
  }, displayIndex): Edge => {
    const selected = selectionId === selectedEdgeID;
    const condition = Boolean(edge.condition);
    const failure = Boolean(edge.failure);
    const label = edge.failure
      ? failureEdgeLabel(edge.failure)
      : edge.condition
        ? conditionDisplayLabel(edge.condition)
        : undefined;
    return {
      id,
      data: {
        selectionId,
        edgeOffset: edgeOffsets[displayIndex] ?? 0,
      },
      source,
      target,
      sourceHandle,
      targetHandle,
      type: contained ? "default" : condition ? "condition" : failure ? undefined : "flow",
      label: showLabel ? label : undefined,
      labelStyle: showLabel && (condition || failure) ? {
        fill: "var(--foreground)",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        fontWeight: 600,
      } : undefined,
      labelBgStyle: showLabel && (condition || failure) ? {
        fill: "var(--panel)",
        fillOpacity: 0.96,
        stroke: "var(--border)",
        strokeWidth: 1,
      } : undefined,
      labelBgPadding: showLabel && (condition || failure) ? [7, 4] : undefined,
      labelBgBorderRadius: showLabel && (condition || failure) ? 5 : undefined,
      animated: false,
      selected,
      reconnectable: false,
      interactionWidth: 24,
      zIndex: 1,
      style: edgeStyle(selected, condition, failure),
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

const parallelEdgeSpacing = 48;

function parallelEdgeOffsets(edges: readonly LoopDisplayEdge[]): number[] {
  const groups = new Map<string, number[]>();
  edges.forEach((item, index) => {
    if (item.contained) return;
    const key = [item.source, item.target].sort().join("\u0000");
    groups.set(key, [...(groups.get(key) ?? []), index]);
  });

  const offsets = edges.map(() => 0);
  for (const indexes of groups.values()) {
    const center = (indexes.length - 1) / 2;
    indexes.forEach((edgeIndex, index) => {
      offsets[edgeIndex] = (index - center) * parallelEdgeSpacing;
    });
  }
  return offsets;
}

function stateBindingPreview(node: GraphNodeSpec, nodeType?: NodeTypeSchema) {
  const preview: Array<{ name: string; value: string }> = [];
  const staticPortNames = new Set<string>();
  for (const port of nodeType?.state_ports ?? []) {
    staticPortNames.add(port.name);
    const path = node.state?.[port.name]?.path.trim()
      || resolveDefaultStatePath(port.default_path, node.id);
    if (path) preview.push({ name: port.name, value: path });
  }
  for (const name of Object.keys(node.state ?? {}).sort()) {
    if (staticPortNames.has(name)) continue;
    const path = node.state?.[name]?.path.trim();
    if (path) preview.push({ name, value: path });
  }
  return preview;
}

function configurationPreview(node: GraphNodeSpec) {
  return Object.entries(node.config ?? {})
    .filter(([, value]) => value !== undefined)
    .map(([name, value]) => ({ name, value: previewValue(value) }));
}

function previewValue(value: unknown): string {
  if (typeof value === "string") return value || '""';
  if (value === null) return "null";
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value) ?? String(value);
}

function importantConfigurationSummary(node: GraphNodeSpec, nodeType?: NodeTypeSchema): string | undefined {
  const schemaProperties = recordValue(nodeType?.config_schema?.properties);
  const config = recordValue(node.config);
  const parts: string[] = [];
  if (schemaProperties && Object.prototype.hasOwnProperty.call(schemaProperties, "model_id")) {
    const modelID = typeof config?.model_id === "string" ? config.model_id.trim() : "";
    parts.push(`model: ${modelID || "default"}`);
  }
  if (schemaProperties && Object.prototype.hasOwnProperty.call(schemaProperties, "tool_ids")) {
    const toolCount = Array.isArray(config?.tool_ids) ? config.tool_ids.length : 0;
    parts.push(`tools: ${toolCount}`);
  }
  return parts.length > 0 ? parts.join(" · ") : undefined;
}

function recordValue(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function failureEdgeLabel(failure: NonNullable<GraphDefinition["edges"]>[number]["failure"]): string {
  const stages = failure?.stages?.join("|");
  const classes = failure?.error_classes?.join("|");
  return ["failure", stages, classes].filter(Boolean).join(" · ");
}

function edgeColor(selected: boolean, condition: boolean, failure: boolean): string {
  if (selected) return "var(--flow-edge-selected)";
  if (failure) return "var(--flow-edge-failure)";
  return condition ? "#8b5cf6" : "var(--muted-foreground)";
}

function edgeStyle(selected: boolean, condition: boolean, failure: boolean) {
  return {
    strokeWidth: selected ? 2.6 : 1.4,
    stroke: edgeColor(selected, condition, failure),
    strokeDasharray: failure ? "7 4" : undefined,
  };
}
