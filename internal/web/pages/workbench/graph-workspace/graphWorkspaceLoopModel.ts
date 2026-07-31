import type { VirtualGraphLoop } from "../../../components/GraphCanvas";
import {
  graphNodePositions,
  withNodePosition,
  type NodePosition,
} from "../../../lib/graphEditor";
import type { GraphDefinition } from "../../../types";
import { normalizeVirtualLoop } from "./graphWorkspaceModel";

export interface GraphWorkspaceLoopState {
  definition: GraphDefinition | null;
  virtualLoops: VirtualGraphLoop[];
}

export interface GraphWorkspaceLoopMutation extends GraphWorkspaceLoopState {
  selectedLoopID?: string | null;
  message: string;
}

export function updateGraphWorkspaceLoop(
  state: GraphWorkspaceLoopState,
  selectedLoop: VirtualGraphLoop,
  update: (loop: VirtualGraphLoop) => VirtualGraphLoop
): GraphWorkspaceLoopMutation {
  if (selectedLoop.automatic) {
    return {
      ...state,
      selectedLoopID: selectedLoop.id,
      message: "automatic loop follows graph edges",
    };
  }
  return {
    ...state,
    virtualLoops: state.virtualLoops.map((loop) =>
      loop.id === selectedLoop.id ? normalizeVirtualLoop(update({ ...loop })) : loop
    ),
    selectedLoopID: selectedLoop.id,
    message: "loop updated",
  };
}

export function deleteGraphWorkspaceLoop(
  state: GraphWorkspaceLoopState,
  displayLoops: VirtualGraphLoop[],
  loopID: string,
  selectedLoopID: string | null
): GraphWorkspaceLoopMutation {
  const loop = displayLoops.find((item) => item.id === loopID);
  if (loop?.automatic) {
    return {
      ...state,
      selectedLoopID: selectedLoopID === loopID ? loopID : undefined,
      message: "automatic loop follows graph edges",
    };
  }
  return {
    ...state,
    virtualLoops: state.virtualLoops.filter((item) => item.id !== loopID),
    selectedLoopID: selectedLoopID === loopID ? null : undefined,
    message: "loop deleted",
  };
}

export function moveGraphWorkspaceLoop(
  state: GraphWorkspaceLoopState,
  displayLoops: VirtualGraphLoop[],
  loopID: string,
  delta: NodePosition
): GraphWorkspaceLoopMutation {
  const loop = displayLoops.find((item) => item.id === loopID);
  if (!loop) return { ...state, message: "loop not found" };
  if (!state.definition) return { ...state, message: "invalid graph json" };

  const positions = graphNodePositions(state.definition);
  let nextDefinition = state.definition;
  for (const nodeID of loop.nodeIds) {
    const position = positions.get(nodeID);
    if (position) {
      nextDefinition = withNodePosition(nextDefinition, nodeID, {
        x: position.x + delta.x,
        y: position.y + delta.y,
      });
    }
  }
  return {
    ...state,
    definition: nextDefinition,
    message: "loop moved",
  };
}
