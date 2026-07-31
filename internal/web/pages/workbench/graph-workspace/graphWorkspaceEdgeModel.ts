import type { VirtualGraphEdge } from "../../../components/graphCanvasElements";
import {
  END_NODE_REF,
  addGraphEdge,
  findGraphEdgeIndex,
  graphEdgeId,
  removeGraphEdge,
  updateGraphEdge,
} from "../../../lib/graphEditor";
import type { GraphDefinition, GraphEdgeSpec } from "../../../types";
import { upsertVirtualEdge } from "./graphWorkspaceModel";
import { findLastEdgeId, lastVirtualEdge, virtualEdgeId, virtualNodeKind } from "./utils";

export interface GraphWorkspaceEdgeState {
  definition: GraphDefinition | null;
  virtualEdges: VirtualGraphEdge[];
}

export interface GraphWorkspaceEdgeMutation extends GraphWorkspaceEdgeState {
  selectedEdgeID?: string | null;
  message: string;
}

interface GraphWorkspaceEdgeContext extends GraphWorkspaceEdgeState {
  displayVirtualEdges: VirtualGraphEdge[];
}

export function updateGraphWorkspaceEdge(
  state: GraphWorkspaceEdgeState,
  selectedEdgeID: string,
  update: (edge: GraphEdgeSpec) => GraphEdgeSpec
): GraphWorkspaceEdgeMutation {
  const { definition } = state;
  if (!definition) return { ...state, message: "invalid graph json" };
  const previousIndex = (definition.edges ?? []).findIndex(
    (edge, index) => graphEdgeId(edge, index) === selectedEdgeID
  );
  let nextDefinition = updateGraphEdge(definition, selectedEdgeID, update);
  if (nextDefinition === definition) {
    return { ...state, selectedEdgeID, message: "edge already exists" };
  }
  const nextEdge = previousIndex >= 0 ? nextDefinition.edges?.[previousIndex] : undefined;
  if (nextEdge?.to === END_NODE_REF && nextDefinition.finish_point === nextEdge.from) {
    nextDefinition = { ...nextDefinition, finish_point: undefined };
  }
  return {
    definition: nextDefinition,
    virtualEdges: state.virtualEdges,
    selectedEdgeID: nextEdge ? graphEdgeId(nextEdge, previousIndex) : null,
    message: "edge updated",
  };
}

export function updateGraphWorkspaceVirtualEdge(
  state: GraphWorkspaceEdgeState,
  selectedEdge: VirtualGraphEdge,
  update: (edge: VirtualGraphEdge) => VirtualGraphEdge
): GraphWorkspaceEdgeMutation {
  const { definition, virtualEdges } = state;
  if (!definition) return { ...state, message: "invalid graph json" };
  const updated = update({ ...selectedEdge });
  const sourceKind = virtualNodeKind(updated.from);
  const targetKind = virtualNodeKind(updated.to);
  if (updated.kind === "entry" && (sourceKind !== "start" || targetKind)) {
    return { ...state, selectedEdgeID: selectedEdge.id, message: "invalid entry edge" };
  }
  if (updated.kind === "finish" && (sourceKind || targetKind !== "end")) {
    return { ...state, selectedEdgeID: selectedEdge.id, message: "invalid finish edge" };
  }

  if (updated.kind === "finish") {
    const graphEdge: GraphEdgeSpec = {
      from: updated.from,
      to: END_NODE_REF,
      condition: updated.condition,
    };
    const existingIndex = (definition.edges ?? []).findIndex(
      (edge) => edge.from === graphEdge.from && edge.to === graphEdge.to
    );
    const nextEdges = [...(definition.edges ?? [])];
    const nextIndex = existingIndex >= 0 ? existingIndex : nextEdges.length;
    nextEdges[nextIndex] = graphEdge;
    return {
      definition: {
        ...definition,
        finish_point: definition.finish_point === selectedEdge.from ? undefined : definition.finish_point,
        edges: nextEdges,
      },
      virtualEdges: virtualEdges.filter((edge) => edge.id !== selectedEdge.id),
      selectedEdgeID: graphEdgeId(graphEdge, nextIndex),
      message: "edge updated",
    };
  }

  const nextEdge = {
    ...updated,
    id: virtualEdgeId(updated.from, updated.to, updated.kind),
  };
  if (definition.entry_point === updated.to && selectedEdge.to !== updated.to) {
    return { ...state, selectedEdgeID: selectedEdge.id, message: "edge already exists" };
  }
  return {
    definition: { ...definition, entry_point: nextEdge.to },
    virtualEdges: upsertVirtualEdge(virtualEdges, selectedEdge, nextEdge),
    selectedEdgeID: nextEdge.id,
    message: "edge updated",
  };
}

export function deleteGraphWorkspaceEdge(
  state: GraphWorkspaceEdgeContext,
  edgeID: string
): GraphWorkspaceEdgeMutation {
  const virtualEdge = state.displayVirtualEdges.find((edge) => edge.id === edgeID);
  if (!virtualEdge) {
    if (!state.definition) return { ...state, message: "invalid graph json" };
    return {
      definition: removeGraphEdge(state.definition, edgeID),
      virtualEdges: state.virtualEdges,
      selectedEdgeID: null,
      message: "edge deleted",
    };
  }

  const remainingEdges = state.virtualEdges.filter((edge) => edge.id !== edgeID);
  let nextDefinition = state.definition;
  if (nextDefinition && virtualEdge.kind === "entry") {
    nextDefinition = { ...nextDefinition, entry_point: lastVirtualEdge(remainingEdges, "entry")?.to };
  }
  if (
    nextDefinition &&
    virtualEdge.kind === "finish" &&
    nextDefinition.finish_point === virtualEdge.from
  ) {
    nextDefinition = { ...nextDefinition, finish_point: lastVirtualEdge(remainingEdges, "finish")?.from };
  }
  return {
    definition: nextDefinition,
    virtualEdges: remainingEdges,
    selectedEdgeID: null,
    message: "edge deleted",
  };
}

export function removeGraphWorkspaceVirtualEdgesForNode(
  state: GraphWorkspaceEdgeContext,
  nodeID: string
): GraphWorkspaceEdgeState {
  const removedEdges = state.displayVirtualEdges.filter(
    (edge) => edge.from === nodeID || edge.to === nodeID
  );
  if (removedEdges.length === 0) return state;
  const remainingEdges = state.virtualEdges.filter(
    (edge) => edge.from !== nodeID && edge.to !== nodeID
  );
  let nextDefinition = state.definition;
  if (
    nextDefinition &&
    removedEdges.some(
      (edge) => edge.kind === "entry" && nextDefinition?.entry_point === edge.to
    )
  ) {
    nextDefinition = { ...nextDefinition, entry_point: lastVirtualEdge(remainingEdges, "entry")?.to };
  }
  if (
    nextDefinition &&
    removedEdges.some(
      (edge) => edge.kind === "finish" && nextDefinition?.finish_point === edge.from
    )
  ) {
    nextDefinition = { ...nextDefinition, finish_point: lastVirtualEdge(remainingEdges, "finish")?.from };
  }
  return { definition: nextDefinition, virtualEdges: remainingEdges };
}

export function connectGraphWorkspaceNodes(
  state: GraphWorkspaceEdgeContext,
  source: string,
  target: string
): GraphWorkspaceEdgeMutation {
  const { definition } = state;
  if (!definition) return { ...state, message: "invalid graph json" };
  const sourceKind = virtualNodeKind(source);
  const targetKind = virtualNodeKind(target);
  if (sourceKind === "end" || targetKind === "start" || (sourceKind === "start" && targetKind === "end")) {
    return { ...state, message: "invalid virtual edge" };
  }

  if (sourceKind === "start") {
    if (definition.entry_point === target) {
      const existingEdge = state.displayVirtualEdges.find(
        (edge) => edge.kind === "entry" && edge.from === source && edge.to === target
      );
      return {
        definition,
        virtualEdges: state.virtualEdges,
        selectedEdgeID: existingEdge?.id ?? null,
        message: "edge already exists",
      };
    }
    const nextVirtualEdge = withAddedVirtualEdge(state.virtualEdges, {
      from: source,
      to: target,
      kind: "entry",
    });
    return {
      definition: { ...definition, entry_point: target },
      virtualEdges: nextVirtualEdge.edges,
      selectedEdgeID: nextVirtualEdge.edgeID,
      message: "entry connected",
    };
  }

  const graphTarget = targetKind === "end" ? END_NODE_REF : target;
  const existingEdgeIndex = findGraphEdgeIndex(definition, source, graphTarget);
  if (existingEdgeIndex >= 0) {
    const existingEdge = definition.edges?.[existingEdgeIndex];
    return {
      definition,
      virtualEdges: state.virtualEdges,
      selectedEdgeID: existingEdge ? graphEdgeId(existingEdge, existingEdgeIndex) : null,
      message: "edge already exists",
    };
  }

  const nextDefinition = addGraphEdge(definition, source, graphTarget);
  return {
    definition: targetKind === "end" && nextDefinition.finish_point === source
      ? { ...nextDefinition, finish_point: undefined }
      : nextDefinition,
    virtualEdges: state.virtualEdges,
    selectedEdgeID: findLastEdgeId(nextDefinition, source, graphTarget),
    message: "edge connected",
  };
}

function withAddedVirtualEdge(
  edges: VirtualGraphEdge[],
  edge: Omit<VirtualGraphEdge, "id">
): { edges: VirtualGraphEdge[]; edgeID: string } {
  const nextEdge = { ...edge, id: virtualEdgeId(edge.from, edge.to, edge.kind) };
  return {
    edges: [
      ...edges.filter((item) => {
        if (item.id === nextEdge.id) return false;
        return !(nextEdge.kind === "entry" && item.kind === "entry" && item.from === nextEdge.from);
      }),
      nextEdge,
    ],
    edgeID: nextEdge.id,
  };
}
