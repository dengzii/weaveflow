import type { RuntimeSettings, RuntimeSettingsUpdate } from "../../../types";

export interface EditableGraphModel {
  id: string;
  enabled: boolean;
  provider: string;
  api_format: string;
  model: string;
  base_url: string;
  extra_body: string;
  credential_configured: boolean;
  credential_input: string;
  credential_value: string;
  credential_clear: boolean;
  pricing_currency: string;
  input_per_million: string;
  cached_input_per_million: string;
  output_per_million: string;
}

export interface EditableEnvironmentVariable {
  key: string;
  value: string;
  secret: boolean;
  secret_source: "env" | "file" | "managed";
  secret_ref: string;
}

export function modelsFromSettings(settings: RuntimeSettings | null): EditableGraphModel[] {
  const configured = Array.isArray(settings?.models) ? settings.models : [];
  return configured.map((model, index) => ({
    id: model.id || (index === 0 ? "default" : `model-${index + 1}`),
    enabled: model.enabled,
    provider: model.provider || "openai",
    api_format: model.api_format || "chat_completions",
    model: model.model ?? "",
    base_url: model.base_url ?? "",
    extra_body: model.extra_body && Object.keys(model.extra_body).length > 0
      ? JSON.stringify(model.extra_body, null, 2)
      : "",
    credential_configured: Boolean(model.credential_configured),
    credential_input: "",
    credential_value: model.credential_value ?? "",
    credential_clear: Boolean(model.credential_clear),
    pricing_currency: model.pricing?.currency ?? "USD",
    input_per_million: pricingRate(model.pricing?.input_per_million),
    cached_input_per_million: pricingRate(model.pricing?.cached_input_per_million),
    output_per_million: pricingRate(model.pricing?.output_per_million),
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
    const credentialValue = model.credential_value?.trim() ?? "";
    const credentialClear = Boolean(model.credential_clear);
    return {
      id: modelID,
      enabled: model.enabled,
      provider: model.provider.trim().toLowerCase() || "openai",
      api_format: model.api_format.trim().toLowerCase() || "chat_completions",
      model: model.model.trim(),
      base_url: model.base_url.trim(),
      extra_body: parseModelExtraBody(model.extra_body, index),
      pricing: normalizeModelPricing(model, index),
      credential_value: credentialValue || undefined,
      credential_clear: credentialClear || undefined,
    };
  });
}

export function environmentRowsFromSettings(settings: RuntimeSettings | null): EditableEnvironmentVariable[] {
  const values = Object.entries(editableEnvironment(settings)).map(([key, value]) => ({
    key,
    value,
    secret: false,
    secret_source: "env" as const,
    secret_ref: "",
  }));
  const secrets = Object.entries(settings?.environment_secrets ?? {}).map(([key, ref]) => ({
    key,
    value: "",
    secret: true,
    secret_source: ref.source,
    secret_ref: ref.ref,
  }));
  return [...values, ...secrets].sort((left, right) => left.key.localeCompare(right.key));
}

export function normalizeEnvironmentSettings(rows: EditableEnvironmentVariable[]): {
  environment: Record<string, string>;
  environmentSecrets: RuntimeSettingsUpdate["environment_secrets"];
} {
  const environment: Record<string, string> = {};
  const environmentSecrets: NonNullable<RuntimeSettingsUpdate["environment_secrets"]> = {};
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
    if (row.secret) {
      const ref = row.secret_ref.trim();
      if (!ref) throw new Error(`Environment ${key} secret ref is required.`);
      environmentSecrets[key] = { source: row.secret_source, ref };
    } else {
      environment[key] = row.value;
    }
  }
  return { environment, environmentSecrets };
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
    const credentialValue = model.credential_value?.trim() ?? "";
    const credentialClear = Boolean(model.credential_clear);
    return {
      id,
      enabled: model.enabled ?? previous?.enabled ?? true,
      provider: model.provider?.trim() || previous?.provider || "openai",
      api_format: model.api_format?.trim() || previous?.api_format || "chat_completions",
      model: model.model !== undefined ? model.model.trim() : previous?.model ?? "",
      base_url: model.base_url !== undefined ? model.base_url.trim() : previous?.base_url ?? "",
      extra_body: model.extra_body ?? previous?.extra_body,
      pricing: model.pricing ?? previous?.pricing,
      credential_configured: credentialClear
        ? false
        : Boolean(credentialValue || previous?.credential_configured),
      credential_value: credentialValue || undefined,
      credential_clear: credentialClear || undefined,
    };
  });
  return {
    environment: update.environment ?? current.environment,
    environment_secrets: update.environment_secrets ?? current.environment_secrets ?? {},
    environment_presets: current.environment_presets,
    models,
    tool_permissions: update.tool_permissions ?? current.tool_permissions ?? [],
    tool_approvals: update.tool_approvals ?? current.tool_approvals ?? {},
  };
}

export function runtimeSettingsUpload(settings: RuntimeSettings): RuntimeSettingsUpdate {
  requireRuntimeSettings(settings, "Cannot upload graph");
  return {
    environment: settings.environment,
    environment_secrets: settings.environment_secrets,
    models: settings.models.map((model) => ({
      id: model.id,
      enabled: model.enabled,
      provider: model.provider,
      api_format: model.api_format || "chat_completions",
      model: model.model ?? "",
      base_url: model.base_url ?? "",
      extra_body: model.extra_body,
      pricing: model.pricing,
      credential_value: model.credential_value?.trim() || undefined,
      credential_clear: model.credential_clear || undefined,
    })),
    tool_permissions: settings.tool_permissions ?? [],
    tool_approvals: settings.tool_approvals ?? {},
  };
}

function pricingRate(value: number | undefined): string {
  return value === undefined || value === 0 ? "" : String(value);
}

function normalizeModelPricing(model: EditableGraphModel, index: number) {
  const input = parsePricingRate(model.input_per_million ?? "", index, "input");
  const cachedInput = parsePricingRate(model.cached_input_per_million ?? "", index, "cached input");
  const output = parsePricingRate(model.output_per_million ?? "", index, "output");
  if (input === 0 && cachedInput === 0 && output === 0) return undefined;
  return {
    currency: model.pricing_currency.trim().toUpperCase() || "USD",
    input_per_million: input,
    cached_input_per_million: cachedInput,
    output_per_million: output,
  };
}

function parsePricingRate(value: string, index: number, label: string): number {
  const normalized = value.trim();
  if (!normalized) return 0;
  const rate = Number(normalized);
  if (!Number.isFinite(rate) || rate < 0) {
    throw new Error(`Model ${index + 1} ${label} price must be a non-negative number.`);
  }
  return rate;
}

function parseModelExtraBody(value: string, index: number): Record<string, unknown> | undefined {
  const input = value.trim();
  if (!input) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(input);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new Error(`Model ${index + 1} extra body must be valid JSON: ${message}`);
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new Error(`Model ${index + 1} extra body must be a JSON object.`);
  }
  return parsed as Record<string, unknown>;
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
    typeof settings.environment_secrets !== "object" ||
    settings.environment_secrets === null ||
    Array.isArray(settings.environment_secrets) ||
    !Array.isArray(settings.models)
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
