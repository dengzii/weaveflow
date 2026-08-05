import type { RuntimeSettings, RuntimeSettingsUpdate } from "../../../types";

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

export function modelsFromSettings(settings: RuntimeSettings | null): EditableGraphModel[] {
  const configured = Array.isArray(settings?.models) ? settings.models : [];
  return configured.map((model, index) => ({
    id: model.id || (index === 0 ? "default" : `model-${index + 1}`),
    enabled: model.enabled,
    provider: model.provider || "openai",
    model: model.model ?? "",
    base_url: model.base_url ?? "",
    api_key: "",
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

export function normalizeModelSettings(models: EditableGraphModel[]): RuntimeSettingsUpdate["models"] {
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
      api_key: apiKey || undefined,
    };
  });
}

export function environmentRowsFromSettings(settings: RuntimeSettings | null): EditableEnvironmentVariable[] {
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

export function applyRuntimeSettingsUpdate(
  current: RuntimeSettings,
  update: RuntimeSettingsUpdate
): RuntimeSettings {
  requireRuntimeSettings(current, "Cannot update runtime settings");
  const currentModels = new Map(current.models.map((model) => [model.id, model]));
  const models = (update.models ?? current.models).map((model, index) => {
    const id = model.id?.trim() || (index === 0 ? "default" : `model-${index + 1}`);
    const previous = currentModels.get(id);
    const apiKey = model.api_key?.trim() || previous?.api_key;
    return {
      id,
      enabled: model.enabled ?? previous?.enabled ?? true,
      provider: model.provider?.trim() || previous?.provider || "openai",
      model: model.model !== undefined ? model.model.trim() : previous?.model ?? "",
      base_url: model.base_url !== undefined ? model.base_url.trim() : previous?.base_url ?? "",
      api_key_configured: Boolean(apiKey || previous?.api_key_configured),
      api_key: apiKey,
    };
  });
  return {
    environment: update.environment ?? current.environment,
    environment_presets: current.environment_presets,
    models,
    memory: {
      enabled: update.memory?.enabled ?? current.memory.enabled,
      directory: update.memory?.directory !== undefined
        ? update.memory.directory.trim()
        : current.memory.directory ?? "",
    },
  };
}

export function runtimeSettingsUpload(settings: RuntimeSettings): RuntimeSettingsUpdate {
  requireRuntimeSettings(settings, "Cannot upload graph");
  return {
    environment: settings.environment,
    models: settings.models.map((model) => ({
      id: model.id,
      enabled: model.enabled,
      provider: model.provider,
      model: model.model ?? "",
      base_url: model.base_url ?? "",
      api_key: model.api_key,
    })),
    memory: {
      enabled: settings.memory.enabled,
      directory: settings.memory.directory ?? "",
    },
  };
}

function requireRuntimeSettings(
  settings: RuntimeSettings | null | undefined,
  operation: string
): asserts settings is RuntimeSettings {
  if (
    !settings ||
    typeof settings.environment !== "object" ||
    settings.environment === null ||
    Array.isArray(settings.environment) ||
    !Array.isArray(settings.models) ||
    typeof settings.memory !== "object" ||
    settings.memory === null ||
    Array.isArray(settings.memory)
  ) {
    throw new Error(`${operation}: runtime settings are missing.`);
  }
}

function editableEnvironment(settings: RuntimeSettings | null): Record<string, string> {
  const input = settings?.environment ?? {};
  const environment: Record<string, string> = {};
  for (const [key, value] of Object.entries(input)) {
    if (key === "OPENAI_MODEL" || key === "OPENAI_BASE_URL") continue;
    environment[key] = value;
  }
  return environment;
}
