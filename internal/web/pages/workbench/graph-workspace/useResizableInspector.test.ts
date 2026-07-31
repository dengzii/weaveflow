import { describe, expect, test } from "bun:test";
import { clampInspectorWidth } from "./useResizableInspector";

describe("resizable inspector model", () => {
  test("keeps the inspector and canvas within their width constraints", () => {
    expect(clampInspectorWidth(100, 1200)).toBe(320);
    expect(clampInspectorWidth(900, 1200)).toBe(720);
    expect(clampInspectorWidth(900, 900)).toBe(534);
    expect(clampInspectorWidth(480, 600)).toBe(320);
  });

  test("rounds persisted widths and handles an unavailable container", () => {
    expect(clampInspectorWidth(401.6, 1200)).toBe(402);
    expect(clampInspectorWidth(500, Number.NaN)).toBe(380);
  });
});
