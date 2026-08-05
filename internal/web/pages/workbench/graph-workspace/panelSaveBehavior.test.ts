import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { RuntimeSettings, Trigger } from "../../../types";
import { TriggerEditorForm } from "../TriggerEditorForm";
import { triggerEditorValues } from "../triggerEditor";
import { RuntimeSettingsEditor } from "./GraphSettingsEditor";

describe("inspector save behavior", () => {
  test("keeps runtime settings local without a panel save button", () => {
    const markup = renderToStaticMarkup(createElement(RuntimeSettingsEditor, {
      settings: runtimeSettings(),
      onChangeRuntimeSettings: () => runtimeSettings(),
    }));

    expect(markup).not.toContain("Apply settings");
    expect(markup).not.toContain("Save settings");
  });

  test("keeps trigger editing local without a panel save button", () => {
    const item = trigger();
    const markup = renderToStaticMarkup(createElement(TriggerEditorForm, {
      trigger: item,
      values: triggerEditorValues(item, item.target!),
      persisted: true,
      chatSetupChannelID: "",
      chatSetupSessionID: "",
      targetOptions: [{ key: "graph-a", label: "graph-a", target: { graph_id: "graph-a" } }],
      targetLocked: true,
      showIdentityFields: false,
      showTargetField: false,
      onChange: () => undefined,
    }));

    expect(markup).not.toContain("Save changes");
    expect(markup).not.toContain("Create trigger");
    expect(markup).not.toContain("Delete trigger");
    const generalHeader = markup.indexOf(">General</span>");
    expect(generalHeader).toBeGreaterThan(0);
    expect(markup.slice(Math.max(0, generalHeader - 500), generalHeader)).toContain('aria-expanded="false"');
  });
});

function runtimeSettings(): RuntimeSettings {
  return {
    environment: {},
    models: [],
    memory: { enabled: false },
  };
}

function trigger(): Trigger {
  return {
    id: "incoming",
    name: "Webhook",
    type: "webhook",
    enabled: true,
    target: { graph_id: "graph-a" },
    webhook: {},
    created_at: "2026-08-05T00:00:00Z",
    updated_at: "2026-08-05T00:00:00Z",
  };
}
