import { describe, expect, test } from "bun:test";
import type { GraphSettings } from "../../../types";
import {
  MODEL_API_KEY_MASK,
  environmentRowsFromSettings,
  modelsFromSettings,
  nextModelID,
  normalizeEnvironmentSettings,
  normalizeModelSettings,
} from "./graphSettingsEditorModel";

describe("graph settings editor model", () => {
  test("builds editable models without exposing configured API keys", () => {
    const models = modelsFromSettings(graphSettings());

    expect(models).toEqual([
      {
        id: "default",
        enabled: true,
        provider: "openai",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        api_key: MODEL_API_KEY_MASK,
        api_key_configured: true,
      },
    ]);
  });

  test("normalizes models while omitting the API key mask", () => {
    expect(
      normalizeModelSettings([
        {
          id: " default ",
          enabled: true,
          provider: "openai",
          model: " gpt-5 ",
          base_url: " https://api.example.test/v1 ",
          api_key: MODEL_API_KEY_MASK,
          api_key_configured: true,
        },
      ])
    ).toEqual([
      {
        id: "default",
        enabled: true,
        provider: "openai",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        api_key: undefined,
      },
    ]);
  });

  test("rejects duplicate model and environment identifiers", () => {
    const model = {
      id: "default",
      enabled: true,
      provider: "openai",
      model: "gpt-5",
      base_url: "",
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
});

function graphSettings(): GraphSettings {
  return {
    environment: {
      Z_VALUE: "z",
      OPENAI_MODEL: "gpt-5",
      A_VALUE: "a",
      OPENAI_BASE_URL: "https://api.example.test/v1",
    },
    model: {
      id: "default",
      enabled: true,
      provider: "openai",
      model: "gpt-5",
      base_url: "https://api.example.test/v1",
      api_key_configured: true,
    },
    models: [
      {
        id: "default",
        enabled: true,
        provider: "openai",
        model: "gpt-5",
        base_url: "https://api.example.test/v1",
        api_key_configured: true,
      },
    ],
    memory: { enabled: false },
  };
}
