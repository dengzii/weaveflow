import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { GraphDefinition } from "../../types";
import { WorkbenchShell } from "./WorkbenchShell";

const definition: GraphDefinition = { nodes: [] };

const baseProps = {
  streamStatus: "connected" as const,
  streamDiagnostics: {
    lastEventID: "",
    lastErrorKind: "",
    lastError: "",
    receivedEvents: 0,
    discardedFrames: 0,
    receivedEventsPerSecond: 0,
    discardedFramesPerSecond: 0,
    selectedEventsPerSecond: 0,
    unselectedEventsPerSecond: 0,
    selectedEventRatio: 0,
    handlingDurationMS: 0,
  },
  busy: true,
  runBusy: false,
  runLaunchPending: false,
  initializing: false,
  saving: false,
  unsaved: false,
  definition,
  runControlMode: "active" as const,
  workspaceMode: "debug" as const,
  canResume: false,
  runControlsDisabled: false,
  children: null,
  runStatusPanel: null,
  runStatusVisible: false,
  hasRunStatus: true,
  onRun: () => undefined,
  onSave: () => undefined,
  onPause: () => undefined,
  onStop: () => undefined,
  onResume: () => undefined,
  onShowRegistry: () => undefined,
  onShowSettings: () => undefined,
  onReconnectEventStream: () => undefined,
  onToggleRunStatus: () => undefined,
  onWorkspaceModeChange: () => undefined,
};

describe("WorkbenchShell run controls", () => {
  test("exposes the run panel toggle keyboard shortcut", () => {
    const markup = renderToStaticMarkup(createElement(WorkbenchShell, baseProps));

    expect(markup).toContain('aria-keyshortcuts="Control+J Meta+J"');
    expect(markup).toContain('title="Show run panel"');
  });

  test("keeps pause and stop enabled when only the page is busy", () => {
    const markup = renderToStaticMarkup(createElement(WorkbenchShell, baseProps));

    expect(markup).not.toMatch(/disabled=""[^>]*title="Pause run"/);
    expect(markup).not.toMatch(/disabled=""[^>]*title="Stop run"/);
  });

  test("disables pause and stop while a run operation is in flight", () => {
    const markup = renderToStaticMarkup(
      createElement(WorkbenchShell, {
        ...baseProps,
        runBusy: true,
        runControlsDisabled: true,
      })
    );

    expect(markup).toMatch(/disabled=""[^>]*title="Pause run"/);
    expect(markup).toMatch(/disabled=""[^>]*title="Stop run"/);
  });
});
