import type { GraphSettings, GraphSettingsUpdate } from "../../../types";

export const MODEL_API_KEY_MASK = "********";

export interface EditableGraphModel {
  id: string;
  enabled: boolean;
  provider: string;
  model: string;
  base_url: string;
  api_key: string;
  api_key_configured: boolean;
}

export interface EditableEnvironmentVariable {
  key: string;
  value: string;
}

export function modelsFromSettings(settings: GraphSettings | null): EditableGraphModel[] {
  const configured = Array.isArray(settings?.models) ? settings.models : settings?.model ? [settings.model] : [];
  return configured.map((model, index) => ({
    id: model.id || (index === 0 ? "default" : `model-${index + 1}`),
    enabled: model.enabled,
    provider: model.provider || "openai",
    model: model.model ?? "",
    base_url: model.base_url ?? "",
    api_key: model.api_key_configured ? MODEL_API_KEY_MASK : "",
    api_key_configured: model.api_key_configured,
  }));
}

export function nextModelID(models: EditableGraphModel[]): string {
  if (models.length === 0) return "default";

  const existing = new Set(models.map((model) => model.id.trim()).filter(Boolean));
  let index = models.length + 1;
  let modelID = `model-${index}`;
  while (existing.has(modelID)) {
    index += 1;
    modelID = `model-${index}`;
  }
  return modelID;
}

export function modelAPIKeyDisplayValue(model: EditableGraphModel): string {
  const apiKey = model.api_key.trim();
  if (apiKey && apiKey !== MODEL_API_KEY_MASK) return MODEL_API_KEY_MASK;
  return model.api_key_configured ? MODEL_API_KEY_MASK : "";
}

export function normalizeModelSettings(models: EditableGraphModel[]): GraphSettingsUpdate["models"] {
  const seen = new Set<string>();
  return models.map((model, index) => {
    const modelID = model.id.trim();
    if (!modelID) {
      throw new Error(`Model ${index + 1} id is required.`);
    }
    if (seen.has(modelID)) {
      throw new Error(`Duplicate model id: ${modelID}`);
    }
    seen.add(modelID);
    const apiKey = model.api_key.trim();
    return {
      id: modelID,
      enabled: model.enabled,
      provider: model.provider || "openai",
      model: model.model.trim(),
      base_url: model.base_url.trim(),
      api_key: apiKey && apiKey !== MODEL_API_KEY_MASK ? apiKey : undefined,
    };
  });
}

export function environmentRowsFromSettings(settings: GraphSettings | null): EditableEnvironmentVariable[] {
  return Object.entries(editableEnvironment(settings))
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({ key, value }));
}

export function normalizeEnvironmentSettings(rows: EditableEnvironmentVariable[]): Record<string, string> {
  const environment: Record<string, string> = {};
  const seen = new Set<string>();
  for (const [index, row] of rows.entries()) {
    const key = row.key.trim();
    if (!key) {
      throw new Error(`Environment ${index + 1} key is required.`);
    }
    if (seen.has(key)) {
      throw new Error(`Duplicate environment key: ${key}`);
    }
    seen.add(key);
    environment[key] = row.value;
  }
  return environment;
}

function editableEnvironment(settings: GraphSettings | null): Record<string, string> {
  const input = settings?.environment ?? {};
  const environment: Record<string, string> = {};
  for (const [key, value] of Object.entries(input)) {
    if (key === "OPENAI_MODEL" || key === "OPENAI_BASE_URL") continue;
    environment[key] = value;
  }
  return environment;
}
