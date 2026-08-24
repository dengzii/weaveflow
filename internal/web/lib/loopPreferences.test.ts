import { describe, expect, test } from "bun:test";
import {
  readStoredAutoDetectLoops,
  writeStoredAutoDetectLoops,
  type LoopPreferenceStorage,
} from "./loopPreferences";

function storage(initial: Record<string, string> = {}): LoopPreferenceStorage {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      values.set(key, value);
    },
  };
}

describe("loop preferences", () => {
  test("defaults automatic loop detection to disabled", () => {
    expect(readStoredAutoDetectLoops(storage())).toBe(false);
    expect(readStoredAutoDetectLoops(storage({ "weaveflow:web:auto-detect-loops:v1": "invalid" }))).toBe(false);
  });

  test("persists the automatic loop detection choice", () => {
    const preferences = storage();
    writeStoredAutoDetectLoops(false, preferences);
    expect(readStoredAutoDetectLoops(preferences)).toBe(false);
    writeStoredAutoDetectLoops(true, preferences);
    expect(readStoredAutoDetectLoops(preferences)).toBe(true);
  });
});
