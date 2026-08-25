import { describe, expect, test } from "bun:test";
import { isToggleRunPanelShortcut } from "./utils";

describe("isToggleRunPanelShortcut", () => {
  test("accepts Ctrl+J and Command+J without extra modifiers", () => {
    expect(isToggleRunPanelShortcut(shortcutEvent({ ctrlKey: true, key: "j" }))).toBe(true);
    expect(isToggleRunPanelShortcut(shortcutEvent({ metaKey: true, key: "J" }))).toBe(true);
  });

  test("rejects unmodified or conflicting key combinations", () => {
    expect(isToggleRunPanelShortcut(shortcutEvent({ key: "j" }))).toBe(false);
    expect(isToggleRunPanelShortcut(shortcutEvent({ ctrlKey: true, key: "k" }))).toBe(false);
    expect(isToggleRunPanelShortcut(shortcutEvent({ ctrlKey: true, key: "j", shiftKey: true }))).toBe(false);
    expect(isToggleRunPanelShortcut(shortcutEvent({ altKey: true, metaKey: true, key: "j" }))).toBe(false);
  });
});

function shortcutEvent(
  overrides: Partial<Pick<KeyboardEvent, "altKey" | "ctrlKey" | "key" | "metaKey" | "shiftKey">>
): Pick<KeyboardEvent, "altKey" | "ctrlKey" | "key" | "metaKey" | "shiftKey"> {
  return {
    altKey: false,
    ctrlKey: false,
    key: "",
    metaKey: false,
    shiftKey: false,
    ...overrides,
  };
}
