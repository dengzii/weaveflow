import type { GraphDefinition, RuntimeSettingsUpdate } from "../../types";

export function graphSaveIdentity(graphID: string, graphVersion: string): string {
  return JSON.stringify([graphID.trim(), graphVersion.trim()]);
}

export function graphSaveSignature(
  definition: GraphDefinition,
  settings: RuntimeSettingsUpdate,
  graphID: string,
  graphVersion: string
): string {
  return JSON.stringify(canonicalJSONValue({
    definition,
    graph_id: graphID.trim(),
    graph_version: graphVersion.trim(),
    settings,
  }));
}

export function isGraphSavePending(currentSignature: string, savedSignature?: string): boolean {
  return Boolean(currentSignature && currentSignature !== savedSignature);
}

export function graphAnalysisSignature(definition: GraphDefinition): string {
  const { metadata: _metadata, ...executableDefinition } = definition;
  return JSON.stringify(canonicalJSONValue(executableDefinition));
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
