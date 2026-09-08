import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownTextDialog, TextValuePreview } from "./TextValuePreview";

describe("TextValuePreview", () => {
  test("renders a clickable string preview with full-text affordance", () => {
    const markup = renderToStaticMarkup(
      createElement(TextValuePreview, {
        value: "A long answer",
        label: "Answer",
      })
    );

    expect(markup).toContain('aria-label="Open Answer text"');
    expect(markup).toContain('aria-haspopup="dialog"');
    expect(markup).toContain('title="Open Answer as Markdown"');
    expect(markup).toContain("A long answer");
    expect(markup).not.toContain('role="dialog"');
  });

  test("renders Markdown content in the full-text dialog", () => {
    const markup = renderToStaticMarkup(
      createElement(MarkdownTextDialog, {
        label: "Answer",
        text: "# Summary\n\n- **Ready**\n- `42`",
        onClose: () => undefined,
      })
    );

    expect(markup).toContain('role="dialog"');
    expect(markup).toContain('aria-modal="true"');
    expect(markup).toContain('aria-label="Close full text"');
    expect(markup).toContain("<h1>Summary</h1>");
    expect(markup).toContain("<strong>Ready</strong>");
    expect(markup).toContain("<code>42</code>");
    expect(markup).toContain("29 characters");
  });
});
