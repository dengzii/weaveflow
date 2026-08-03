import type { Trigger, TriggerType } from "../../../types";
import { triggerTypeName } from "../triggerEditor";

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

export function nextTriggerName(triggers: readonly Trigger[], type: TriggerType): string {
  const baseName = triggerTypeName(type);
  let maximumSequence = 0;
  for (const trigger of triggers) {
    if (trigger.type !== type) continue;
    const name = trigger.name?.trim() || triggerTypeName(trigger.type);
    if (name === baseName) {
      maximumSequence = Math.max(maximumSequence, 1);
      continue;
    }
    if (!name.startsWith(`${baseName} `)) continue;
    const suffix = name.slice(baseName.length + 1);
    if (!/^\d+$/.test(suffix)) continue;
    const sequence = Number(suffix);
    if (Number.isSafeInteger(sequence) && sequence >= 2) {
      maximumSequence = Math.max(maximumSequence, sequence);
    }
  }
  return maximumSequence === 0 ? baseName : `${baseName} ${maximumSequence + 1}`;
}
