import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ToastStack } from "./ToastStack";

describe("ToastStack", () => {
  test("renders notifications as titled canvas windows", () => {
    const markup = renderToStaticMarkup(createElement(ToastStack, {
      toasts: [
        { id: "error", tone: "error", message: "Save failed" },
        { id: "warning", tone: "warn", message: "Input may be incomplete" },
        { id: "notice", tone: "info", message: "Graph saved" },
      ],
      persistentNotices: [
        { id: "trigger", tone: "error", title: "Trigger unavailable", message: "Connection failed" },
      ],
      onDismiss: () => undefined,
    }));

    expect(markup).toContain("Graph notifications");
    expect(markup).toContain("Error notification");
    expect(markup).toContain("Warning notification");
    expect(markup).toContain("Notice notification");
    expect(markup).toContain("Trigger unavailable notification");
    expect(markup).toContain("bg-panel/95");
    expect(markup).toContain("Dismiss error");
    expect(markup).not.toContain("Dismiss trigger unavailable");
  });
});
