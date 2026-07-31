import { useCallback, useState } from "react";

export type GraphWorkspaceSelectionKind = "node" | "edge" | "loop";

export interface GraphWorkspaceSelection {
  kind: GraphWorkspaceSelectionKind;
  id: string;
}

export function nextGraphWorkspaceSelection(
  current: GraphWorkspaceSelection | null,
  kind: GraphWorkspaceSelectionKind,
  id: string | null
): GraphWorkspaceSelection | null {
  if (id) return { kind, id };
  return current?.kind === kind ? null : current;
}

export function useGraphWorkspaceSelection(
  setSelectedTriggerID: (triggerID: string | null) => void
) {
  const [selection, setSelection] = useState<GraphWorkspaceSelection | null>(null);

  const selectLocal = useCallback((kind: GraphWorkspaceSelectionKind, id: string | null) => {
    setSelection((current) => nextGraphWorkspaceSelection(current, kind, id));
    if (id) setSelectedTriggerID(null);
  }, [setSelectedTriggerID]);

  const selectNode = useCallback((nodeID: string | null) => {
    selectLocal("node", nodeID);
  }, [selectLocal]);

  const selectEdge = useCallback((edgeID: string | null) => {
    selectLocal("edge", edgeID);
  }, [selectLocal]);

  const selectLoop = useCallback((loopID: string | null) => {
    selectLocal("loop", loopID);
  }, [selectLocal]);

  const selectTrigger = useCallback((triggerID: string | null) => {
    setSelectedTriggerID(triggerID);
    if (triggerID) setSelection(null);
  }, [setSelectedTriggerID]);

  const clearSelection = useCallback(() => {
    setSelection(null);
    setSelectedTriggerID(null);
  }, [setSelectedTriggerID]);

  return {
    selectedNodeID: selection?.kind === "node" ? selection.id : null,
    selectedEdgeID: selection?.kind === "edge" ? selection.id : null,
    selectedLoopID: selection?.kind === "loop" ? selection.id : null,
    selectNode,
    selectEdge,
    selectLoop,
    selectTrigger,
    clearSelection,
  };
}
