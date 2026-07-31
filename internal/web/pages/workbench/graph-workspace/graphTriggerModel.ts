import type { Trigger } from "../../../types";

export function triggersForGraph(triggers: readonly Trigger[], graphID: string): Trigger[] {
  const targetGraphID = graphID.trim();
  if (!targetGraphID) return [];
  return triggers.filter((trigger) => trigger.target?.graph_id?.trim() === targetGraphID);
}

export function upsertTrigger(triggers: readonly Trigger[], saved: Trigger): Trigger[] {
  const index = triggers.findIndex((trigger) => trigger.id === saved.id);
  if (index < 0) return [...triggers, saved];
  return triggers.map((trigger, triggerIndex) => triggerIndex === index ? saved : trigger);
}

export function uniqueTriggerIDs(triggers: readonly Trigger[], additionalIDs: readonly string[] = []): string[] {
  return Array.from(new Set(
    [...triggers.map((trigger) => trigger.id), ...additionalIDs]
      .map((triggerID) => triggerID.trim())
      .filter(Boolean)
  ));
}
