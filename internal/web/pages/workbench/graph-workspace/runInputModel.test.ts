import { describe, expect, test } from "bun:test";
import {
  getPathValue,
  hasFilledInitialStatePath,
  hasFilledRequirementValue,
  parseInitialStateText,
  parseStatePath,
  updateInitialStatePath,
} from "./runInputModel";

describe("run input model", () => {
  test("parses normalized state paths and rejects unsafe segments", () => {
    expect(parseStatePath(" shared . request..input ")).toEqual(["shared", "request", "input"]);
    expect(parseStatePath("")).toBeNull();
    expect(parseStatePath("shared.__proto__.polluted")).toBeNull();
    expect(parseStatePath("shared.constructor.prototype")).toBeNull();
  });

  test("updates nested values while preserving existing input", () => {
    const nextValue = { prompt: "Draft", metadata: { priority: 2 } };
    const result = updateInitialStatePath(
      '{"shared":{"request":{"language":"zh"}},"custom":{"keep":true}}',
      "shared.request.input",
      nextValue
    );

    nextValue.metadata.priority = 3;
    expect(getPathValue(result, "shared.request.input")).toEqual({ prompt: "Draft", metadata: { priority: 2 } });
    expect(getPathValue(result, "shared.request.language")).toBe("zh");
    expect(getPathValue(result, "custom.keep")).toBe(true);
  });

  test("validates required values against their declared type", () => {
    expect(hasFilledRequirementValue(" request ", "string")).toBe(true);
    expect(hasFilledRequirementValue("   ", "string")).toBe(false);
    expect(hasFilledRequirementValue(false, "boolean")).toBe(true);
    expect(hasFilledRequirementValue("false", "boolean")).toBe(false);
    expect(hasFilledRequirementValue(0, "number")).toBe(true);
    expect(hasFilledRequirementValue("0", "number")).toBe(false);
    expect(hasFilledRequirementValue({}, "object")).toBe(true);
    expect(hasFilledRequirementValue([], "object")).toBe(false);
    expect(hasFilledRequirementValue([], "array")).toBe(true);
    expect(hasFilledRequirementValue({}, "array")).toBe(false);
  });

  test("recovers from malformed JSON when a form field changes", () => {
    expect(parseInitialStateText("{broken").error).not.toBeNull();

    const result = updateInitialStatePath("{broken", "shared.request.input", "recovered");
    expect(getPathValue(result, "shared.request.input")).toBe("recovered");
    expect(result.scopes).toEqual({});
    expect(result.internal).toEqual({});
    expect(result.runtime).toEqual({});
  });

  test("checks typed values at a full state path", () => {
    const initialState = { shared: { enabled: false, count: 0, config: "invalid" } };
    expect(hasFilledInitialStatePath(initialState, "shared.enabled", "boolean")).toBe(true);
    expect(hasFilledInitialStatePath(initialState, "shared.count", "number")).toBe(true);
    expect(hasFilledInitialStatePath(initialState, "shared.config", "object")).toBe(false);
    expect(hasFilledInitialStatePath(initialState, "shared.missing", "string")).toBe(false);
  });
});
