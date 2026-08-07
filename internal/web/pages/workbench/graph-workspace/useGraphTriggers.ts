import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelChatChannelSetup,
  listTriggers,
  replaceTriggers,
} from "../../../api";
import type { Trigger, TriggerType } from "../../../types";
import {
  buildTriggerPayload,
  triggerDraftFromEditorValues,
  triggerEditorValues,
  type TriggerEditorValues,
} from "../triggerEditor";
import { nextTriggerName, uniqueTriggerIDs } from "./graphTriggerModel";

export interface CreatedGraphTrigger {
  trigger: Trigger;
  triggerIDs: string[];
}

export interface GraphTriggerDraft {
  trigger: Trigger;
  values: TriggerEditorValues;
  persisted: boolean;
  imported: boolean;
  chatSetupChannelID: string;
  chatSetupSessionID: string;
}

export interface TriggerDraftSetup {
  channelID: string;
  sessionID: string;
}

export function useGraphTriggers(graphID: string) {
  const normalizedGraphID = graphID.trim();
  const [drafts, setDrafts] = useState<GraphTriggerDraft[]>([]);
  const [hydrated, setHydrated] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [knownTriggerIDs, setKnownTriggerIDs] = useState<string[]>([]);
  const [selectedTriggerID, setSelectedTriggerID] = useState<string | null>(null);
  const [savedSignature, setSavedSignature] = useState("");
  const graphIDRef = useRef(normalizedGraphID);
  const draftsRef = useRef<GraphTriggerDraft[]>([]);
  const serverTriggersRef = useRef(new Map<string, Trigger>());
  const pendingImportRef = useRef<{ graphID: string; triggers: Trigger[] } | null>(null);
  const requestGenerationRef = useRef(0);
  graphIDRef.current = normalizedGraphID;

  const replaceDrafts = useCallback((next: GraphTriggerDraft[]) => {
    draftsRef.current = next;
    setDrafts(next);
  }, []);

  const isCurrentRequest = useCallback((requestGeneration: number, targetGraphID: string) => (
    requestGeneration === requestGenerationRef.current && targetGraphID === graphIDRef.current
  ), []);

  const loadGraphTriggers = useCallback(async (requestGeneration: number, targetGraphID: string) => {
    if (!targetGraphID) {
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      serverTriggersRef.current = new Map();
      replaceDrafts([]);
      setSavedSignature(triggerDraftSignature([]));
      setHydrated(true);
      setLoadError("");
      return [];
    }
    try {
      const items = await listTriggers(targetGraphID);
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      setKnownTriggerIDs(uniqueTriggerIDs(items));
      const nextDrafts = items.map(serverTriggerDraft);
      serverTriggersRef.current = new Map(items.map((trigger) => [trigger.id, trigger]));
      replaceDrafts(nextDrafts);
      setSavedSignature(triggerDraftSignature(nextDrafts));
      setHydrated(true);
      setLoadError("");
      return items;
    } catch (error) {
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      setHydrated(false);
      setLoadError(errorMessage(error));
      return null;
    }
  }, [isCurrentRequest, replaceDrafts]);

  const refresh = useCallback(async () => {
    const targetGraphID = graphIDRef.current;
    const requestGeneration = ++requestGenerationRef.current;
    return loadGraphTriggers(requestGeneration, targetGraphID);
  }, [loadGraphTriggers]);

  useEffect(() => {
    replaceDrafts([]);
    serverTriggersRef.current = new Map();
    setSavedSignature("");
    setHydrated(false);
    setLoadError("");
    setSelectedTriggerID(null);
    const pendingImport = pendingImportRef.current;
    if (pendingImport?.graphID === normalizedGraphID) {
      pendingImportRef.current = null;
      const importedDrafts = pendingImport.triggers.map(importedTriggerDraft);
      serverTriggersRef.current = new Map();
      replaceDrafts(importedDrafts);
      setSavedSignature(triggerDraftSignature([]));
      setHydrated(true);
      return () => {
        requestGenerationRef.current += 1;
      };
    }
    void refresh();
    return () => {
      requestGenerationRef.current += 1;
    };
  }, [normalizedGraphID, refresh, replaceDrafts]);

  const triggers = useMemo(() => drafts.map((draft) => draft.trigger), [drafts]);
  const selectedDraft = useMemo(
    () => drafts.find((draft) => draft.trigger.id === selectedTriggerID) ?? null,
    [drafts, selectedTriggerID]
  );
  const selectedTrigger = selectedDraft?.trigger ?? null;
  const validTriggerIDs = useMemo(
    () => hydrated ? uniqueTriggerIDs(triggers) : undefined,
    [hydrated, triggers]
  );
  const signature = useMemo(() => triggerDraftSignature(drafts), [drafts]);
  const isUnsaved = Boolean(hydrated && signature !== savedSignature);

  useEffect(() => {
    if (selectedTriggerID && hydrated && !selectedTrigger) setSelectedTriggerID(null);
  }, [hydrated, selectedTrigger, selectedTriggerID]);

  const createForGraph = useCallback(async (type: TriggerType): Promise<CreatedGraphTrigger | null> => {
    const targetGraphID = graphIDRef.current;
    if (!targetGraphID || !hydrated) return null;
    const values = triggerEditorValues(null, { graph_id: targetGraphID }, type);
    values.id = createTriggerID();
    values.name = nextTriggerName(draftsRef.current.map((draft) => draft.trigger), type);
    const trigger = triggerDraftFromEditorValues(values, null);
    const nextDrafts = [...draftsRef.current, {
      trigger,
      values,
      persisted: false,
      imported: false,
      chatSetupChannelID: "",
      chatSetupSessionID: "",
    }];
    replaceDrafts(nextDrafts);
    setSelectedTriggerID(trigger.id);
    setLoadError("");
    return { trigger, triggerIDs: uniqueTriggerIDs(nextDrafts.map((draft) => draft.trigger)) };
  }, [hydrated, replaceDrafts]);

  const updateDraft = useCallback((
    triggerID: string,
    values: TriggerEditorValues,
    setup: TriggerDraftSetup
  ) => {
    const draft = draftsRef.current.find((item) => item.trigger.id === triggerID);
    if (!draft) return false;
    if (draft.chatSetupSessionID && draft.chatSetupSessionID !== setup.sessionID) {
      void cancelChatChannelSetup(draft.chatSetupChannelID, draft.chatSetupSessionID).catch(() => undefined);
    }
    replaceDrafts(draftsRef.current.map((item) => item.trigger.id === triggerID ? {
      ...item,
      trigger: triggerDraftFromEditorValues(values, item.trigger),
      values,
      chatSetupChannelID: setup.channelID,
      chatSetupSessionID: setup.sessionID,
    } : item));
    setLoadError("");
    return true;
  }, [replaceDrafts]);

  const updateEnabled = useCallback((triggerID: string, enabled: boolean) => {
    const draft = draftsRef.current.find((item) => item.trigger.id === triggerID);
    if (!draft) return null;
    const values = { ...draft.values, enabled };
    updateDraft(triggerID, values, {
      channelID: draft.chatSetupChannelID,
      sessionID: draft.chatSetupSessionID,
    });
    return triggerDraftFromEditorValues(values, draft.trigger);
  }, [updateDraft]);

  const remove = useCallback(async (triggerID: string) => {
    const draft = draftsRef.current.find((item) => item.trigger.id === triggerID);
    if (!draft) return false;
    if (draft.chatSetupSessionID) {
      await cancelChatChannelSetup(draft.chatSetupChannelID, draft.chatSetupSessionID).catch(() => undefined);
    }
    replaceDrafts(draftsRef.current.filter((item) => item.trigger.id !== triggerID));
    setSelectedTriggerID((current) => current === triggerID ? null : current);
    setLoadError("");
    return true;
  }, [replaceDrafts]);

  const analysisPayloads = useCallback(() => {
    const serverTriggers = serverTriggersRef.current;
    return draftsRef.current.map((draft) => buildTriggerPayload(
      draft.values,
      serverTriggers.get(draft.trigger.id) ?? (draft.imported ? draft.trigger : null),
      draft.chatSetupSessionID
    ));
  }, []);

  const validate = useCallback(() => {
    analysisPayloads();
  }, [analysisPayloads]);

  const stageImport = useCallback((targetGraphID: string, triggers: Trigger[]) => {
    pendingImportRef.current = {
      graphID: targetGraphID.trim(),
      triggers: triggers.map((trigger) => ({
        ...trigger,
        target: { graph_id: targetGraphID.trim() },
      })),
    };
  }, []);

  const save = useCallback(async () => {
    const targetGraphID = graphIDRef.current;
    if (!targetGraphID || !hydrated) return [];
    const requestGeneration = requestGenerationRef.current;
    const currentDrafts = draftsRef.current;
    const payloads = analysisPayloads();

    try {
      const saved = await replaceTriggers(targetGraphID, payloads);
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return [];

      const savedByID = new Map(saved.map((trigger) => [trigger.id, trigger]));
      const nextDrafts = currentDrafts.map((draft) => serverTriggerDraft(
        savedByID.get(draft.trigger.id) ?? draft.trigger
      ));
      serverTriggersRef.current = savedByID;
      replaceDrafts(nextDrafts);
      setKnownTriggerIDs((current) => uniqueTriggerIDs(
        nextDrafts.map((draft) => draft.trigger),
        current
      ));
      setSavedSignature(triggerDraftSignature(nextDrafts));
      setLoadError("");
      return nextDrafts.map((draft) => draft.trigger);
    } catch (error) {
      if (isCurrentRequest(requestGeneration, targetGraphID)) {
        setLoadError(errorMessage(error));
      }
      throw error;
    }
  }, [analysisPayloads, hydrated, isCurrentRequest, replaceDrafts]);

  return {
    triggers,
    drafts,
    hydrated,
    loadError,
    knownTriggerIDs,
    isUnsaved,
    selectedDraft,
    selectedTrigger,
    selectedTriggerID,
    validTriggerIDs,
    setSelectedTriggerID,
    refresh,
    createForGraph,
    updateDraft,
    updateEnabled,
    remove,
    validate,
    analysisPayloads,
    stageImport,
    save,
  };
}

export type GraphTriggerController = ReturnType<typeof useGraphTriggers>;

function serverTriggerDraft(trigger: Trigger): GraphTriggerDraft {
  return {
    trigger,
    values: triggerEditorValues(trigger, trigger.target ?? { graph_id: "" }),
    persisted: true,
    imported: false,
    chatSetupChannelID: "",
    chatSetupSessionID: "",
  };
}

function importedTriggerDraft(trigger: Trigger): GraphTriggerDraft {
  return {
    trigger,
    values: triggerEditorValues(trigger, trigger.target ?? { graph_id: "" }),
    persisted: false,
    imported: true,
    chatSetupChannelID: "",
    chatSetupSessionID: "",
  };
}

function triggerDraftSignature(drafts: readonly GraphTriggerDraft[]): string {
  return JSON.stringify(
    [...drafts]
      .sort((left, right) => left.trigger.id.localeCompare(right.trigger.id))
      .map((draft) => ({
        chat_setup_channel_id: draft.chatSetupChannelID,
        chat_setup_session_id: draft.chatSetupSessionID,
        values: draft.values,
      }))
  );
}

function createTriggerID(): string {
  return globalThis.crypto?.randomUUID?.() ?? `trigger-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
