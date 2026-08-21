import { describe, expect, test } from "bun:test";
import { resolveWorkspaceMode } from "./workspaceModeModel";

describe("workspace mode", () => {
  test("defaults explicit modes without runtime state", () => {
    expect(resolveWorkspaceMode("edit", false)).toBe("edit");
    expect(resolveWorkspaceMode("edit", true)).toBe("edit");
    expect(resolveWorkspaceMode("debug", false)).toBe("debug");
    expect(resolveWorkspaceMode("debug", true)).toBe("debug");
  });

  test("auto mode follows run panel visibility", () => {
    expect(resolveWorkspaceMode("auto", false)).toBe("edit");
    expect(resolveWorkspaceMode("auto", true)).toBe("debug");
  });
});
