import { afterEach, describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { setStoredTriggerToken } from "../../lib/backend";
import type { Trigger } from "../../types";
import { ChatPanel } from "./ChatPanel";

const trigger: Trigger = {
  id: "chat-primary",
  name: "Primary chat",
  type: "chat",
  enabled: true,
  credential_configured: true,
  chat: { channel: "http" },
  created_at: "2026-09-06T00:00:00Z",
  updated_at: "2026-09-06T00:00:00Z",
};

afterEach(() => {
  Reflect.deleteProperty(globalThis, "window");
});

describe("ChatPanel layout", () => {
  test("uses compact labels and keeps the conversation controls shrinkable", () => {
    installWindowStorage();
    expect(setStoredTriggerToken("trigger-token")).toBe("trigger-token");

    const markup = renderToStaticMarkup(createElement(ChatPanel, {
      graphID: "graph-a",
      triggers: [trigger],
      runtimeEvents: [],
      onClose: () => undefined,
    }));

    expect(markup).toContain("Primary chat");
    expect(markup).not.toContain("HTTP / SSE Trigger");
    expect(markup).toContain("Conversation ID");
    expect(markup).toContain("Trigger token");
    expect(markup).toContain("Enter the token configured on this Trigger");
    expect(markup).toContain('aria-label="New conversation"');
    expect(markup).toContain('placeholder="发送消息…"');
    expect(markup).toContain("Enter 发送 · Shift+Enter 换行");
    expect(markup).toContain("w-[min(360px,calc(100vw-4rem))]");
    expect(markup).toContain("overflow-x-hidden");
    expect(markup).not.toContain("Token 已在");
  });
});

function installWindowStorage() {
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      localStorage: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
        removeItem: (key: string) => values.delete(key),
      },
    },
  });
}
