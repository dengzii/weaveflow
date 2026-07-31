import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import {
  addNodeToGraph,
  createGraphDefinition,
  graphNodePositions,
  removeGraphNode,
  renameGraphNode,
  withNodePosition,
  type NodePosition,
} from "../../../lib/graphEditor";
import { cloneJSONValue } from "../../../lib/utils";
import type {
  GraphDefinition,
  GraphNodeSpec,
  NodeTypeSchema,
  StateModuleDefinition,
} from "../../../types";
import { removeGraphWorkspaceVirtualEdgesForNode } from "./graphWorkspaceEdgeModel";
import { cloneJSONRecord, uniqueNodeID } from "./graphWorkspaceModel";
import {
  isVirtualNodeId,
  isVirtualNodeType,
  nextVirtualNodeId,
  virtualNodeLabel,
} from "./utils";
import type { VirtualNodeKind } from "./types";

export interface GraphWorkspaceNodeState {
  definition: GraphDefinition | null;
  virtualNodeIDs: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
}

export interface GraphWorkspaceNodeMutation extends GraphWorkspaceNodeState {
  selectedNodeID?: string | null;
  message: string;
}

interface GraphWorkspaceNodeContext extends GraphWorkspaceNodeState {
  displayVirtualEdges: VirtualGraphEdge[];
}

export function addGraphWorkspaceNode(
  state: GraphWorkspaceNodeState,
  nodeType: NodeTypeSchema,
  graphID: string,
  stateModules: StateModuleDefinition[] = [],
  position?: NodePosition
): GraphWorkspaceNodeMutation {
  if (isVirtualNodeType(nodeType.type)) {
    return addGraphWorkspaceVirtualNode(state, nodeType.type, position);
  }

  let nextDefinition = state.definition;
  if (!nextDefinition) {
    nextDefinition = createGraphDefinition(graphID || "debug_graph", nodeType, stateModules);
    const node = nextDefinition.nodes[0];
    if (position && node) nextDefinition = withNodePosition(nextDefinition, node.id, position);
  } else {
    nextDefinition = addNodeToGraph(nextDefinition, nodeType, position);
  }
  return {
    ...state,
    definition: nextDefinition,
    selectedNodeID: nextDefinition.nodes.at(-1)?.id ?? null,
    message: position ? "node created" : "node added",
  };
}

export function addGraphWorkspaceVirtualNode(
  state: GraphWorkspaceNodeState,
  kind: VirtualNodeKind,
  position?: NodePosition
): GraphWorkspaceNodeMutation {
  const nodeID = nextVirtualNodeId(kind, state.virtualNodeIDs);
  return {
    ...state,
    definition: state.definition && position
      ? withNodePosition(state.definition, nodeID, position)
      : state.definition,
    virtualNodeIDs: state.virtualNodeIDs.includes(nodeID)
      ? state.virtualNodeIDs
      : [...state.virtualNodeIDs, nodeID],
    selectedNodeID: nodeID,
    message: `${virtualNodeLabel(nodeID)} ready`,
  };
}

export function renameGraphWorkspaceNode(
  state: GraphWorkspaceNodeState,
  nodeID: string,
  value: string
): GraphWorkspaceNodeMutation {
  if (!state.definition) return { ...state, message: "invalid graph json" };
  const nextID = value.trim();
  if (!nextID) return { ...state, selectedNodeID: nodeID, message: "node id required" };
  if (state.definition.nodes.some((node) => node.id === nextID && node.id !== nodeID)) {
    return { ...state, selectedNodeID: nodeID, message: "node id already exists" };
  }
  if (!state.definition.nodes.some((node) => node.id === nodeID)) {
    return { ...state, selectedNodeID: null, message: "node not found" };
  }
  if (nextID === nodeID) {
    return { ...state, selectedNodeID: nodeID, message: "node id unchanged" };
  }
  return {
    ...state,
    definition: renameGraphNode(state.definition, nodeID, nextID),
    virtualLoops: state.virtualLoops.map((loop) => ({
      ...loop,
      nodeIds: loop.nodeIds.map((memberID) => (memberID === nodeID ? nextID : memberID)),
    })),
    selectedNodeID: nextID,
    message: "node renamed",
  };
}

export function duplicateGraphWorkspaceNode(
  state: GraphWorkspaceNodeState,
  nodeID: string
): GraphWorkspaceNodeMutation {
  const { definition } = state;
  const selectedNode = definition?.nodes.find((node) => node.id === nodeID);
  if (!definition) return { ...state, message: "invalid graph json" };
  if (!selectedNode || isVirtualNodeId(selectedNode.id)) {
    return { ...state, selectedNodeID: nodeID, message: "node not found" };
  }

  const nextID = uniqueNodeID(`${selectedNode.id}_copy`, definition.nodes);
  const sourcePosition = graphNodePositions(definition).get(selectedNode.id) ?? { x: 0, y: 0 };
  const nodeCopy: GraphNodeSpec = {
    ...selectedNode,
    id: nextID,
    name: selectedNode.name ? `${selectedNode.name} copy` : nextID,
    config: cloneJSONRecord(selectedNode.config ?? {}),
    state: cloneJSONValue(selectedNode.state ?? {}),
  };
  return {
    ...state,
    definition: withNodePosition(
      { ...definition, nodes: [...definition.nodes, nodeCopy] },
      nextID,
      { x: sourcePosition.x + 40, y: sourcePosition.y + 40 }
    ),
    selectedNodeID: nextID,
    message: "node duplicated",
  };
}

export function deleteGraphWorkspaceNode(
  state: GraphWorkspaceNodeContext,
  nodeID: string
): GraphWorkspaceNodeMutation {
  const edgeState = removeGraphWorkspaceVirtualEdgesForNode(state, nodeID);
  if (isVirtualNodeId(nodeID)) {
    return {
      ...state,
      definition: edgeState.definition,
      virtualNodeIDs: state.virtualNodeIDs.filter((id) => id !== nodeID),
      virtualEdges: edgeState.virtualEdges,
      selectedNodeID: null,
      message: `${virtualNodeLabel(nodeID)} hidden`,
    };
  }
  if (!edgeState.definition) return { ...state, message: "invalid graph json" };
  return {
    definition: removeGraphNode(edgeState.definition, nodeID),
    virtualNodeIDs: state.virtualNodeIDs,
    virtualEdges: edgeState.virtualEdges,
    virtualLoops: state.virtualLoops.map((loop) => ({
      ...loop,
      nodeIds: loop.nodeIds.filter((memberID) => memberID !== nodeID),
    })),
    selectedNodeID: null,
    message: "node deleted",
  };
}
