import { describe, expect, test } from "bun:test";
import type { RuntimeSettings } from "../../../types";
import {
  applyRuntimeSettingsUpdate,
  environmentRowsFromSettings,
  modelsFromSettings,
  modelIDValidationError,
  newEditableGraphModel,
  nextModelID,
  normalizeEnvironmentSettings,
  normalizeModelSettings,
  runtimeSettingsUpload,
} from "./graphSettingsEditorModel";

describe("graph settings editor model", () => {
  test("builds editable models from configured status", () => {
    const models = modelsFromSettings(graphSettings());

    expect(models).toEqual([
      {
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        extra_body: "",
        credential_configured: true,
        credential_input: "",
        credential_value: "",
        credential_clear: false,
        credential_dirty: false,
        pricing_currency: "USD",
        input_per_million: "",
        cached_input_per_million: "",
        output_per_million: "",
      },
    ]);
  });

  test("normalizes models without credential transport metadata", () => {
    expect(
      normalizeModelSettings([
        {
          id: " default ",
          enabled: true,
          provider: "openai",
          api_format: "responses",
          model: " gpt-5 ",
          base_url: " https://api.example.test/v1 ",
          extra_body: "{\"include\":[\"reasoning.encrypted_content\"]}",
          credential_configured: true,
          credential_input: "",
          credential_value: "",
          credential_clear: false,
          pricing_currency: "USD",
          input_per_million: "",
          cached_input_per_million: "",
          output_per_million: "",
        },
      ])
    ).toEqual([
      {
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "responses",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        extra_body: { include: ["reasoning.encrypted_content"] },
        pricing: undefined,
      },
    ]);
  });

  test("only uploads credentials after an explicit edit", () => {
    const model = modelsFromSettings(graphSettings())[0];
    const unchanged = normalizeModelSettings([{ ...model, credential_value: "stale-key" }])[0];
    expect(unchanged.credential_value).toBeUndefined();
    expect(unchanged.credential_clear).toBeUndefined();
    expect(JSON.stringify(unchanged)).not.toContain("credential_value");
    expect(JSON.stringify(unchanged)).not.toContain("credential_clear");

    const changed = normalizeModelSettings([{ ...model, credential_value: "new-key", credential_dirty: true }])[0];
    expect(changed).toMatchObject({ credential_value: "new-key" });
  });

  test("rejects duplicate model and environment identifiers", () => {
    const model = {
      id: "default",
      enabled: true,
      provider: "openai",
      api_format: "chat_completions",
      model: "gpt-5",
      base_url: "",
      extra_body: "",
      credential_configured: false,
      credential_input: "",
      credential_value: "",
      credential_clear: false,
      credential_dirty: false,
      pricing_currency: "USD",
      input_per_million: "",
      cached_input_per_million: "",
      output_per_million: "",
    };

    expect(() => normalizeModelSettings([model, model])).toThrow("Duplicate model id: default");
    expect(() =>
      normalizeEnvironmentSettings([
        { key: "WORKDIR", value: "one", secret: false, secret_source: "env", secret_ref: "" },
        { key: " WORKDIR ", value: "two", secret: false, secret_source: "env", secret_ref: "" },
      ])
    ).toThrow("Duplicate environment key: WORKDIR");
  });

  test("keeps environment rows sorted and excludes model transport fields", () => {
    expect(environmentRowsFromSettings(graphSettings())).toEqual([
      { key: "A_VALUE", value: "a", secret: false, secret_source: "env", secret_ref: "" },
      { key: "SERVICE_TOKEN", value: "", secret: true, secret_source: "env", secret_ref: "SERVICE_TOKEN" },
      { key: "Z_VALUE", value: "z", secret: false, secret_source: "env", secret_ref: "" },
    ]);
    expect(nextModelID([])).toBe("default");
    expect(nextModelID(modelsFromSettings(graphSettings()))).toBe("model-2");
  });

  test("creates model drafts and validates trimmed identifiers", () => {
    expect(newEditableGraphModel("model-2")).toMatchObject({
      id: "model-2",
      enabled: true,
      provider: "openai",
      api_format: "chat_completions",
    });
    expect(modelIDValidationError(["default"], " default ")).toBe("Model ID already exists: default");
    expect(modelIDValidationError(["default"], " ")).toBe("Model ID is required.");
    expect(modelIDValidationError(["default"], "reasoner")).toBe("");
  });

  test("keeps configured status local without uploading it", () => {
    const next = applyRuntimeSettingsUpdate(graphSettings(), {
      models: [{
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
      }],
    });


    expect(next.models[0].credential_configured).toBe(true);
    expect(runtimeSettingsUpload(next).models?.[0]).not.toHaveProperty("credential_configured");
  });

  test("uploads a managed credential value once and supports explicit clear", () => {
    const replacement = applyRuntimeSettingsUpdate(graphSettings(), {
      models: [{
        id: "default",
        credential_value: " replacement-key ",
      }],
    });

    expect(replacement.models[0].credential_configured).toBe(true);
    expect(runtimeSettingsUpload(replacement).models?.[0]).toMatchObject({
      credential_value: "replacement-key",
      credential_clear: undefined,
    });

    const cleared = applyRuntimeSettingsUpdate(graphSettings(), {
      models: [{ id: "default", credential_clear: true }],
    });
    expect(cleared.models[0].credential_configured).toBe(false);
    expect(runtimeSettingsUpload(cleared).models?.[0]).toMatchObject({
      credential_value: undefined,
      credential_clear: true,
    });
  });

  test("rejects missing runtime settings with an explicit error", () => {
    expect(() => applyRuntimeSettingsUpdate(undefined as unknown as RuntimeSettings, {}))
      .toThrow("Cannot update runtime settings: runtime settings are missing.");
    expect(() => runtimeSettingsUpload(undefined as unknown as RuntimeSettings))
      .toThrow("Cannot upload graph: runtime settings are missing.");
  });

  test("rejects malformed or non-object model extra body", () => {
    const model = modelsFromSettings(graphSettings())[0];
    expect(() => normalizeModelSettings([{ ...model, extra_body: "{" }]))
      .toThrow("extra body must be valid JSON");
    expect(() => normalizeModelSettings([{ ...model, extra_body: "[]" }]))
      .toThrow("extra body must be a JSON object");
  });
});

function graphSettings(): RuntimeSettings {
  return {
    environment: {
      Z_VALUE: "z",
      OPENAI_MODEL: "gpt-5",
      A_VALUE: "a",
      OPENAI_BASE_URL: "https://api.example.test/v1",
    },
    environment_secrets: {
      SERVICE_TOKEN: { source: "env", ref: "SERVICE_TOKEN" },
    },
    models: [
      {
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        credential_configured: true,
        credential_value: "server-should-not-expose",
      },
    ],
    tool_permissions: [],
    tool_approvals: {},
  };
}
