import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import {
  deleteLocalGraphDraft,
  readLocalGraphDrafts,
  saveLocalGraphDraft,
  writeLastLocalGraphDraftId,
  type LocalGraphDraft,
  type SaveLocalGraphInput,
} from "../../../lib/localGraphs";
import { formatTime } from "../../../lib/utils";
import type { GraphDefinition } from "../../../types";
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
  virtualNodeIDs: string[];
  virtualEdges: VirtualGraphEdge[];
  virtualLoops: VirtualGraphLoop[];
  validTriggerIDs?: string[];
}

export function localGraphWorkspaceSignature(snapshot: LocalGraphWorkspaceSnapshot): string {
  return snapshot.definition
    ? autoSaveSignature(
        snapshot.definition,
        snapshot.graphID,
        snapshot.graphVersion,
        snapshot.virtualNodeIDs,
        snapshot.virtualEdges,
        snapshot.virtualLoops,
        snapshot.validTriggerIDs
      )
    : "";
}

export function localGraphDraftSaveInput(
  snapshot: LocalGraphWorkspaceSnapshot,
  activeDraftID: string
): SaveLocalGraphInput | null {
  const definition = snapshot.definition;
  if (!definition) return null;
  return {
    id: activeDraftID || undefined,
    title: definition.name || snapshot.graphID,
    graphId: snapshot.graphID,
    graphVersion: snapshot.graphVersion,
    definition: withSavedGraphWorkspaceState(
      definition,
      snapshot.virtualNodeIDs,
      snapshot.virtualEdges,
      snapshot.virtualLoops,
      snapshot.validTriggerIDs
    ),
  };
}

export function localGraphDraftActivation(draft: LocalGraphDraft) {
  const workspaceState = savedGraphWorkspaceState(draft.definition);
  return {
    workspaceState,
    signature: autoSaveSignature(
      draft.definition,
      draft.graphId,
      draft.graphVersion,
      workspaceState.virtualNodeIDs,
      workspaceState.virtualEdges,
      workspaceState.virtualLoops
    ),
  };
}

export function useLocalGraphDrafts({
  snapshot,
  blockingSaveMessage,
  onStatus,
}: {
  snapshot: LocalGraphWorkspaceSnapshot;
  blockingSaveMessage?: string;
  onStatus: (message: string) => void;
}) {
  const {
    definition,
    graphID,
    graphVersion,
    validTriggerIDs,
    virtualEdges,
    virtualLoops,
    virtualNodeIDs,
  } = snapshot;
  const [drafts, setDrafts] = useState<LocalGraphDraft[]>(readLocalGraphDrafts);
  const [activeDraftID, setActiveDraftID] = useState("");
  const autoSaveHydratedRef = useRef(false);
  const autoSaveTimerRef = useRef<number | null>(null);
  const activeDraftIDRef = useRef("");
  const lastSavedSignatureRef = useRef("");
  activeDraftIDRef.current = activeDraftID;

  const signature = useMemo(() => localGraphWorkspaceSignature(snapshot), [
    definition,
    graphID,
    graphVersion,
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
    const input = localGraphDraftSaveInput(snapshot, activeDraftIDRef.current);
    if (!input) return null;
    const draft = saveLocalGraphDraft(input);
    activeDraftIDRef.current = draft.id;
    setActiveDraftID(draft.id);
    setDrafts(readLocalGraphDrafts());
    lastSavedSignatureRef.current = signature;
    onStatus(`${options.mode === "auto" ? "autosaved" : "saved"} ${formatTime(draft.updatedAt)}`);
    return draft;
  }, [blockingSaveMessage, clearAutoSave, definition, graphID, graphVersion, onStatus, signature, validTriggerIDs, virtualEdges, virtualLoops, virtualNodeIDs]);

  const activateDraft = useCallback((draft: LocalGraphDraft) => {
    clearAutoSave();
    autoSaveHydratedRef.current = false;
    writeLastLocalGraphDraftId(draft.id);
    activeDraftIDRef.current = draft.id;
    setActiveDraftID(draft.id);
    const { workspaceState, signature: draftSignature } = localGraphDraftActivation(draft);
    lastSavedSignatureRef.current = draftSignature;
    return workspaceState;
  }, [clearAutoSave]);

  const resetActiveDraft = useCallback(() => {
    clearAutoSave();
    activeDraftIDRef.current = "";
    setActiveDraftID("");
    lastSavedSignatureRef.current = "";
    autoSaveHydratedRef.current = true;
  }, [clearAutoSave]);

  const deleteActiveDraft = useCallback(() => {
    const draftID = activeDraftIDRef.current;
    if (!draftID) return false;
    clearAutoSave();
    setDrafts(deleteLocalGraphDraft(draftID));
    activeDraftIDRef.current = "";
    setActiveDraftID("");
    lastSavedSignatureRef.current = "";
    onStatus("draft deleted");
    return true;
  }, [clearAutoSave, onStatus]);

  useEffect(() => {
    if (!definition) return;
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
  }, [clearAutoSave, definition, onStatus, saveLocal, signature]);

  return {
    drafts,
    activeDraftID,
    isUnsaved: Boolean(definition && signature !== lastSavedSignatureRef.current),
    saveLocal,
    activateDraft,
    resetActiveDraft,
    deleteActiveDraft,
  };
}
