import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createTrigger, deleteTrigger, listTriggers, updateTrigger } from "../../../api";
import type { Trigger, TriggerType } from "../../../types";
import { buildTriggerPayload, triggerEditorValues } from "../triggerEditor";
import { triggersForGraph, uniqueTriggerIDs, upsertTrigger } from "./graphTriggerModel";

export interface CreatedGraphTrigger {
  trigger: Trigger;
  triggerIDs: string[];
}

export function useGraphTriggers(graphID: string) {
  const normalizedGraphID = graphID.trim();
  const [triggers, setTriggers] = useState<Trigger[]>([]);
  const [hydrated, setHydrated] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [selectedTriggerID, setSelectedTriggerID] = useState<string | null>(null);
  const graphIDRef = useRef(normalizedGraphID);
  const triggersRef = useRef<Trigger[]>([]);
  const requestGenerationRef = useRef(0);
  graphIDRef.current = normalizedGraphID;

  const replaceTriggers = useCallback((next: Trigger[]) => {
    triggersRef.current = next;
    setTriggers(next);
  }, []);

  const isCurrentRequest = useCallback((requestGeneration: number, targetGraphID: string) => (
    requestGeneration === requestGenerationRef.current && targetGraphID === graphIDRef.current
  ), []);

  const loadGraphTriggers = useCallback(async (requestGeneration: number, targetGraphID: string) => {
    if (!targetGraphID) {
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      replaceTriggers([]);
      setHydrated(true);
      setLoadError("");
      return [];
    }
    try {
      const items = await listTriggers();
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      const matched = triggersForGraph(items, targetGraphID);
      replaceTriggers(matched);
      setHydrated(true);
      setLoadError("");
      return matched;
    } catch (error) {
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      setHydrated(false);
      setLoadError(errorMessage(error));
      return null;
    }
  }, [isCurrentRequest, replaceTriggers]);

  const refresh = useCallback(async () => {
    const targetGraphID = graphIDRef.current;
    const requestGeneration = ++requestGenerationRef.current;
    return loadGraphTriggers(requestGeneration, targetGraphID);
  }, [loadGraphTriggers]);

  useEffect(() => {
    replaceTriggers([]);
    setHydrated(false);
    setLoadError("");
    setSelectedTriggerID(null);
    void refresh();
    return () => {
      requestGenerationRef.current += 1;
    };
  }, [normalizedGraphID, refresh, replaceTriggers]);

  const selectedTrigger = useMemo(
    () => triggers.find((trigger) => trigger.id === selectedTriggerID) ?? null,
    [selectedTriggerID, triggers]
  );
  const validTriggerIDs = useMemo(
    () => hydrated ? uniqueTriggerIDs(triggers) : undefined,
    [hydrated, triggers]
  );

  useEffect(() => {
    if (selectedTriggerID && hydrated && !selectedTrigger) setSelectedTriggerID(null);
  }, [hydrated, selectedTrigger, selectedTriggerID]);

  const createForGraph = useCallback(async (type: TriggerType): Promise<CreatedGraphTrigger | null> => {
    const targetGraphID = graphIDRef.current;
    if (!targetGraphID || !hydrated) return null;
    const requestGeneration = ++requestGenerationRef.current;
    try {
      const values = triggerEditorValues(null, { graph_id: targetGraphID }, type);
      const saved = await createTrigger(buildTriggerPayload(values, null));
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      const triggerIDs = uniqueTriggerIDs(triggersRef.current, [saved.id]);
      replaceTriggers(upsertTrigger(triggersRef.current, saved));
      setSelectedTriggerID(saved.id);
      setLoadError("");
      await loadGraphTriggers(requestGeneration, targetGraphID);
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      return { trigger: saved, triggerIDs };
    } catch (error) {
      if (isCurrentRequest(requestGeneration, targetGraphID)) setLoadError(errorMessage(error));
      return null;
    }
  }, [hydrated, isCurrentRequest, loadGraphTriggers, replaceTriggers]);

  const recordSaved = useCallback(async (saved: Trigger) => {
    const targetGraphID = graphIDRef.current;
    if (saved.target?.graph_id?.trim() !== targetGraphID) return false;
    const requestGeneration = ++requestGenerationRef.current;
    replaceTriggers(upsertTrigger(triggersRef.current, saved));
    setSelectedTriggerID(saved.id);
    setLoadError("");
    await loadGraphTriggers(requestGeneration, targetGraphID);
    return isCurrentRequest(requestGeneration, targetGraphID);
  }, [isCurrentRequest, loadGraphTriggers, replaceTriggers]);

  const recordDeleted = useCallback(async (deleted: Trigger) => {
    const targetGraphID = graphIDRef.current;
    if (deleted.target?.graph_id?.trim() !== targetGraphID) return false;
    const requestGeneration = ++requestGenerationRef.current;
    replaceTriggers(triggersRef.current.filter((trigger) => trigger.id !== deleted.id));
    setSelectedTriggerID((current) => current === deleted.id ? null : current);
    setLoadError("");
    await loadGraphTriggers(requestGeneration, targetGraphID);
    return isCurrentRequest(requestGeneration, targetGraphID);
  }, [isCurrentRequest, loadGraphTriggers, replaceTriggers]);

  const updateEnabled = useCallback(async (triggerID: string, enabled: boolean) => {
    const targetGraphID = graphIDRef.current;
    const trigger = triggersRef.current.find((item) => item.id === triggerID);
    if (!trigger || !targetGraphID) return null;
    const requestGeneration = ++requestGenerationRef.current;
    try {
      const values = triggerEditorValues(trigger, trigger.target ?? { graph_id: targetGraphID });
      values.enabled = enabled;
      const saved = await updateTrigger(trigger.id, buildTriggerPayload(values, trigger));
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return null;
      replaceTriggers(upsertTrigger(triggersRef.current, saved));
      setLoadError("");
      await loadGraphTriggers(requestGeneration, targetGraphID);
      return isCurrentRequest(requestGeneration, targetGraphID) ? saved : null;
    } catch (error) {
      if (isCurrentRequest(requestGeneration, targetGraphID)) setLoadError(errorMessage(error));
      return null;
    }
  }, [isCurrentRequest, loadGraphTriggers, replaceTriggers]);

  const remove = useCallback(async (triggerID: string) => {
    const targetGraphID = graphIDRef.current;
    const trigger = triggersRef.current.find((item) => item.id === triggerID);
    if (!trigger || !targetGraphID) return false;
    const requestGeneration = ++requestGenerationRef.current;
    try {
      await deleteTrigger(trigger.id);
      if (!isCurrentRequest(requestGeneration, targetGraphID)) return false;
      replaceTriggers(triggersRef.current.filter((item) => item.id !== trigger.id));
      setSelectedTriggerID((current) => current === trigger.id ? null : current);
      setLoadError("");
      await loadGraphTriggers(requestGeneration, targetGraphID);
      return isCurrentRequest(requestGeneration, targetGraphID);
    } catch (error) {
      if (isCurrentRequest(requestGeneration, targetGraphID)) setLoadError(errorMessage(error));
      return false;
    }
  }, [isCurrentRequest, loadGraphTriggers, replaceTriggers]);

  return {
    triggers,
    hydrated,
    loadError,
    selectedTrigger,
    selectedTriggerID,
    validTriggerIDs,
    setSelectedTriggerID,
    refresh,
    createForGraph,
    recordSaved,
    recordDeleted,
    updateEnabled,
    remove,
  };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
