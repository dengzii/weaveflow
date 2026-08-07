import type {
  GraphDefinition,
  GraphInitialStateAnalysis,
  InitialStateRequirement,
  InitialStateRequirements,
  RuntimeSettingsUpdate,
} from "../../types";

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

export function graphAnalysisSignature(definition: GraphDefinition, triggers: readonly unknown[] = []): string {
  const { metadata: _metadata, ...executableDefinition } = definition;
  return JSON.stringify(canonicalJSONValue({ definition: executableDefinition, triggers }));
}

export function effectiveInitialStateRequirements(analysis: GraphInitialStateAnalysis): InitialStateRequirements {
  const direct = analysis.direct;
  if (analysis.triggers.length === 0) return direct;

  const candidates = [...direct.required, ...direct.unresolved];
  const providedPaths = new Set(candidates
    .filter((requirement) => analysis.triggers.every((item) => (
      item.requirements.provided_by_entry.some((provided) => provided.path === requirement.path)
    )))
    .map((requirement) => requirement.path));
  if (providedPaths.size === 0) return direct;

  const moved = candidates
    .filter((requirement, index) => (
      providedPaths.has(requirement.path) && candidates.findIndex((item) => item.path === requirement.path) === index
    ))
    .map((requirement): InitialStateRequirement => ({
      ...requirement,
      sources: analysis.triggers.map((item) => `trigger:${item.trigger_id}`),
      message: "Provided by every configured Trigger entry.",
    }));
  const providedByEntry = new Map(direct.provided_by_entry.map((requirement) => [requirement.path, requirement]));
  for (const requirement of moved) providedByEntry.set(requirement.path, requirement);

  return {
    ...direct,
    required: direct.required.filter((requirement) => !providedPaths.has(requirement.path)),
    provided_by_entry: [...providedByEntry.values()].sort((left, right) => left.path.localeCompare(right.path)),
    unresolved: direct.unresolved.filter((requirement) => !providedPaths.has(requirement.path)),
  };
}

export function missingTriggerStateRequirements(analysis: GraphInitialStateAnalysis): Array<{
  triggerID: string;
  paths: string[];
}> {
  return analysis.triggers.flatMap((item) => {
    const paths = Array.from(new Set([
      ...item.requirements.required,
      ...item.requirements.unresolved,
    ].map((requirement) => requirement.path))).sort();
    return paths.length > 0 ? [{ triggerID: item.trigger_id, paths }] : [];
  });
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
