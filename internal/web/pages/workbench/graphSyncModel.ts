import type { GraphDefinition } from "../../types";

export interface SyncedGraphState {
  signature: string;
  official: boolean;
}

export function graphUploadSignature(
  definition: GraphDefinition,
  graphID: string,
  graphVersion: string
): string {
  return JSON.stringify({
    graph_id: graphID.trim(),
    graph_version: graphVersion.trim(),
    definition: canonicalJSONValue(definition),
  });
}

export function graphUploadRequired(signature: string, synced: SyncedGraphState | null): boolean {
  return !synced || synced.signature !== signature;
}

export function graphPublishRequired(signature: string, synced: SyncedGraphState | null): boolean {
  return graphUploadRequired(signature, synced) || !synced?.official;
}

function canonicalJSONValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => item === undefined ? null : canonicalJSONValue(item));
  }
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(
    Object.entries(value)
      .filter(([, item]) => item !== undefined)
      .sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0)
      .map(([key, item]) => [key, canonicalJSONValue(item)])
  );
}
