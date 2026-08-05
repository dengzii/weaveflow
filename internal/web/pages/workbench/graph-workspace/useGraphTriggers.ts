import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelChatChannelSetup,
  createTrigger,
  deleteTrigger,
  listTriggers,
  updateTrigger,
} from "../../../api";
import type { Trigger, TriggerType } from "../../../types";
import {
  buildTriggerPayload,
  triggerDraftFromEditorValues,
  triggerEditorValues,
  type TriggerEditorValues,
} from "../triggerEditor";
import { nextTriggerName, triggersForGraph, uniqueTriggerIDs } from "./graphTriggerModel";

export interface CreatedGraphTrigger {
  trigger: Trigger;
  triggerIDs: string[];
}

export interface GraphTriggerDraft {
  trigger: Trigger;
  values: TriggerEditorValues;
  persisted: boolean;
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
  const [selectedTriggerID, setSelectedTriggerID] = useState<string | null>(null);
  const [savedSignature, setSavedSignature] = useState("");
  const graphIDRef = useRef(normalizedGraphID);
  const draftsRef = useRef<GraphTriggerDraft[]>([]);
  const serverTriggersRef = useRef(new Map<string, Trigger>());
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
      const items = triggersForGraph(await listTriggers(), targetGraphID);
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
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

  const validate = useCallback(() => {
    for (const draft of draftsRef.current) {
      buildTriggerPayload(
        draft.values,
        serverTriggersRef.current.get(draft.trigger.id) ?? null,
        draft.chatSetupSessionID
      );
    }
  }, []);

  const save = useCallback(async () => {
    const targetGraphID = graphIDRef.current;
    if (!targetGraphID || !hydrated) return [];
    const requestGeneration = requestGenerationRef.current;
    const currentDrafts = draftsRef.current;
    const serverTriggers = serverTriggersRef.current;
    const payloads = currentDrafts.map((draft) => ({
      draft,
      payload: buildTriggerPayload(
        draft.values,
        serverTriggers.get(draft.trigger.id) ?? null,
        draft.chatSetupSessionID
      ),
    }));
    const savedTriggers = new Map<string, Trigger>();

    try {
      for (const { draft, payload } of payloads) {
        const serverTrigger = serverTriggers.get(draft.trigger.id);
        if (serverTrigger && triggerValuesSignature(draft.values) === triggerValuesSignature(triggerEditorValues(serverTrigger, serverTrigger.target ?? { graph_id: targetGraphID })) && !draft.chatSetupSessionID) {
          savedTriggers.set(serverTrigger.id, serverTrigger);
          continue;
        }
        const saved = serverTrigger
          ? await updateTrigger(draft.trigger.id, payload)
          : await createTrigger(payload);
        serverTriggers.set(saved.id, saved);
        savedTriggers.set(saved.id, saved);
      }

      const currentIDs = new Set(currentDrafts.map((draft) => draft.trigger.id));
      for (const serverTrigger of Array.from(serverTriggers.values())) {
        if (currentIDs.has(serverTrigger.id)) continue;
        await deleteTrigger(serverTrigger.id);
        serverTriggers.delete(serverTrigger.id);
      }
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return [];

      const nextDrafts = currentDrafts.map((draft) => serverTriggerDraft(
        savedTriggers.get(draft.trigger.id) ?? serverTriggers.get(draft.trigger.id) ?? draft.trigger
      ));
      replaceDrafts(nextDrafts);
      setSavedSignature(triggerDraftSignature(nextDrafts));
      setLoadError("");
      return nextDrafts.map((draft) => draft.trigger);
    } catch (error) {
      if (isCurrentRequest(requestGeneration, targetGraphID)) {
        if (savedTriggers.size > 0) {
          replaceDrafts(draftsRef.current.map((draft) => {
            const saved = savedTriggers.get(draft.trigger.id);
            return saved ? serverTriggerDraft(saved) : draft;
          }));
        }
        setLoadError(errorMessage(error));
      }
      throw error;
    }
  }, [hydrated, isCurrentRequest, replaceDrafts]);

  return {
    triggers,
    drafts,
    hydrated,
    loadError,
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
    save,
  };
}

export type GraphTriggerController = ReturnType<typeof useGraphTriggers>;

function serverTriggerDraft(trigger: Trigger): GraphTriggerDraft {
  return {
    trigger,
    values: triggerEditorValues(trigger, trigger.target ?? { graph_id: "" }),
    persisted: true,
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

function triggerValuesSignature(values: TriggerEditorValues): string {
  return JSON.stringify(values);
}

function createTriggerID(): string {
  return globalThis.crypto?.randomUUID?.() ?? `trigger-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
