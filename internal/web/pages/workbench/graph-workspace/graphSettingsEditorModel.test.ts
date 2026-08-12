import { describe, expect, test } from "bun:test";
import type { RuntimeSettings } from "../../../types";
import {
  applyRuntimeSettingsUpdate,
  environmentRowsFromSettings,
  modelsFromSettings,
  nextModelID,
  normalizeEnvironmentSettings,
  normalizeModelSettings,
  runtimeSettingsUpload,
} from "./graphSettingsEditorModel";

describe("graph settings editor model", () => {
  test("builds editable models without exposing configured API keys", () => {
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
        api_key: "",
        api_key_configured: true,
      },
    ]);
  });

  test("normalizes models while omitting an unchanged configured API key", () => {
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
          api_key: "",
          api_key_configured: true,
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
        api_key: undefined,
      },
    ]);
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
      api_key: "",
      api_key_configured: false,
    };

    expect(() => normalizeModelSettings([model, model])).toThrow("Duplicate model id: default");
    expect(() =>
      normalizeEnvironmentSettings([
        { key: "WORKDIR", value: "one" },
        { key: " WORKDIR ", value: "two" },
      ])
    ).toThrow("Duplicate environment key: WORKDIR");
  });

  test("keeps environment rows sorted and excludes model transport fields", () => {
    expect(environmentRowsFromSettings(graphSettings())).toEqual([
      { key: "A_VALUE", value: "a" },
      { key: "Z_VALUE", value: "z" },
    ]);
    expect(nextModelID([])).toBe("default");
    expect(nextModelID(modelsFromSettings(graphSettings()))).toBe("model-2");
  });

  test("keeps a locally entered API key until the graph upload", () => {
    const next = applyRuntimeSettingsUpdate(graphSettings(), {
      models: [{
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        api_key: "local-secret",
      }],
    });

    expect(next.models[0].api_key).toBe("local-secret");
    expect(runtimeSettingsUpload(next).models?.[0].api_key).toBe("local-secret");
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
    models: [
      {
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        api_key_configured: true,
      },
    ],
  };
}
