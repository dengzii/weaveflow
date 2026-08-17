import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import {
  deleteLocalGraph,
  isHydratedLocalGraph,
  readLocalGraphs,
  saveLocalGraph,
  type LocalGraph,
  type SaveLocalGraphInput,
} from "../../../lib/localGraphs";
import { formatTime } from "../../../lib/utils";
import type { GraphDefinition, RuntimeSettings } from "../../../types";
import {
  autoSaveSignature,
  savedGraphWorkspaceState,
  withSavedGraphWorkspaceState,
} from "./graphWorkspaceModel";

const autoSaveWindowMs = 3000;

export interface LocalGraphWorkspaceSnapshot {
  definition: GraphDefinition | null;
  graphID: string;
  graphVersion: string;
  runtimeSettings: RuntimeSettings;
  virtualNodeIDs: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
  validTriggerIDs?: string[];
}

export function localGraphWorkspaceSignature(snapshot: LocalGraphWorkspaceSnapshot): string {
  if (!snapshot.definition) return "";
  return JSON.stringify({
    workspace: autoSaveSignature(
      snapshot.definition,
      snapshot.graphID,
      snapshot.graphVersion,
      snapshot.virtualNodeIDs,
      snapshot.virtualEdges,
      snapshot.virtualLoops,
      snapshot.validTriggerIDs
    ),
    runtimeSettings: snapshot.runtimeSettings,
  });
}

export function localGraphSaveInput(
  snapshot: LocalGraphWorkspaceSnapshot,
  activeCacheID: string
): SaveLocalGraphInput | null {
  const definition = snapshot.definition;
  if (!definition) return null;
  return {
    id: activeCacheID || undefined,
    title: definition.name || snapshot.graphID,
    graphId: snapshot.graphID,
    graphVersion: snapshot.graphVersion,
    runtimeSettings: snapshot.runtimeSettings,
    definition: withSavedGraphWorkspaceState(
      definition,
      snapshot.virtualNodeIDs,
      snapshot.virtualEdges,
      snapshot.virtualLoops,
      snapshot.validTriggerIDs
    ),
  };
}

export function localGraphActivation(graph: LocalGraph) {
  if (!isHydratedLocalGraph(graph)) {
    throw new Error(`graph ${graph.graphId} detail is not loaded`);
  }
  const workspaceState = savedGraphWorkspaceState(graph.definition);
  return {
    workspaceState,
    runtimeSettings: graph.runtimeSettings,
    signature: localGraphWorkspaceSignature({
      definition: graph.definition,
      graphID: graph.graphId,
      graphVersion: graph.graphVersion,
      runtimeSettings: graph.runtimeSettings,
      virtualNodeIDs: workspaceState.virtualNodeIDs,
      virtualEdges: workspaceState.virtualEdges,
      virtualLoops: workspaceState.virtualLoops,
    }),
  };
}

export function useLocalGraphs({
  snapshot,
  serverGraphsLoaded,
  blockingSaveMessage,
  onStatus,
}: {
  snapshot: LocalGraphWorkspaceSnapshot;
  serverGraphsLoaded: boolean;
  blockingSaveMessage?: string;
  onStatus: (message: string) => void;
}) {
  const {
    definition,
    graphID,
    graphVersion,
    runtimeSettings,
    validTriggerIDs,
    virtualEdges,
    virtualLoops,
    virtualNodeIDs,
  } = snapshot;
  const [graphs, setGraphs] = useState<LocalGraph[]>(() =>
    serverGraphsLoaded ? readLocalGraphs() : []
  );
  const [cacheHydrated, setCacheHydrated] = useState(serverGraphsLoaded);
  const [activeCacheID, setActiveCacheID] = useState("");
  const autoSaveHydratedRef = useRef(false);
  const autoSaveTimerRef = useRef<number | null>(null);
  const activeCacheIDRef = useRef("");
  const lastSavedSignatureRef = useRef("");
  activeCacheIDRef.current = activeCacheID;

  const signature = useMemo(() => localGraphWorkspaceSignature(snapshot), [
    definition,
    graphID,
    graphVersion,
    runtimeSettings,
    validTriggerIDs,
    virtualEdges,
    virtualLoops,
    virtualNodeIDs,
  ]);

  const clearAutoSave = useCallback(() => {
    if (autoSaveTimerRef.current === null) return;
    window.clearTimeout(autoSaveTimerRef.current);
    autoSaveTimerRef.current = null;
  }, []);

  useEffect(() => {
    if (!serverGraphsLoaded || cacheHydrated) return;
    clearAutoSave();
    autoSaveHydratedRef.current = false;
    activeCacheIDRef.current = "";
    setActiveCacheID("");
    lastSavedSignatureRef.current = "";
    setGraphs(readLocalGraphs());
    setCacheHydrated(true);
  }, [cacheHydrated, clearAutoSave, serverGraphsLoaded]);

  const saveLocal = useCallback((options: { mode?: "manual" | "auto" } = {}) => {
    if (!definition) {
      onStatus("invalid graph json");
      return null;
    }
    if (blockingSaveMessage) {
      onStatus(`cannot save: ${blockingSaveMessage}`);
      return null;
    }

    clearAutoSave();
    const input = localGraphSaveInput(snapshot, activeCacheIDRef.current);
    if (!input) return null;
    const graph = saveLocalGraph(input);
    activeCacheIDRef.current = graph.id;
    setActiveCacheID(graph.id);
    setGraphs(readLocalGraphs());
    lastSavedSignatureRef.current = signature;
    onStatus(`${options.mode === "auto" ? "autosaved" : "saved"} ${formatTime(graph.updatedAt)}`);
    return graph;
  }, [blockingSaveMessage, clearAutoSave, definition, graphID, graphVersion, onStatus, runtimeSettings, signature, validTriggerIDs, virtualEdges, virtualLoops, virtualNodeIDs]);

  const activateGraph = useCallback((graph: LocalGraph) => {
    clearAutoSave();
    autoSaveHydratedRef.current = false;
    activeCacheIDRef.current = graph.id;
    setActiveCacheID(graph.id);
    setGraphs((current) => current.map((item) => item.id === graph.id ? graph : item));
    const activation = localGraphActivation(graph);
    lastSavedSignatureRef.current = activation.signature;
    return activation;
  }, [clearAutoSave]);

  const resetActiveGraph = useCallback(() => {
    clearAutoSave();
    activeCacheIDRef.current = "";
    setActiveCacheID("");
    lastSavedSignatureRef.current = "";
    autoSaveHydratedRef.current = true;
  }, [clearAutoSave]);

  const deleteActiveGraph = useCallback(() => {
    const cacheID = activeCacheIDRef.current;
    if (!cacheID) return false;
    clearAutoSave();
    setGraphs(deleteLocalGraph(cacheID));
    activeCacheIDRef.current = "";
    setActiveCacheID("");
    lastSavedSignatureRef.current = "";
    onStatus("graph deleted");
    return true;
  }, [clearAutoSave, onStatus]);

  useEffect(() => {
    if (!cacheHydrated || !definition) return;
    if (!autoSaveHydratedRef.current) {
      autoSaveHydratedRef.current = true;
      if (!lastSavedSignatureRef.current) lastSavedSignatureRef.current = signature;
      return;
    }
    if (signature === lastSavedSignatureRef.current) return;

    onStatus("autosave queued");
    clearAutoSave();
    autoSaveTimerRef.current = window.setTimeout(() => {
      autoSaveTimerRef.current = null;
      saveLocal({ mode: "auto" });
    }, autoSaveWindowMs);
    return clearAutoSave;
  }, [cacheHydrated, clearAutoSave, definition, onStatus, saveLocal, signature]);

  const refreshGraphs = useCallback(() => {
    setGraphs(readLocalGraphs());
  }, []);

  return {
    graphs,
    cacheHydrated,
    activeCacheID,
    isUnsaved: Boolean(cacheHydrated && definition && signature !== lastSavedSignatureRef.current),
    saveLocal,
    activateGraph,
    resetActiveGraph,
    deleteActiveGraph,
    refreshGraphs,
  };
}
