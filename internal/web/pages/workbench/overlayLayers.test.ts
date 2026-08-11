import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ChatChannelSetupDialog } from "./ChatChannelSetupDialog";
import { RegistryDialog } from "./RegistryDialog";
import { UserInputPromptDialog } from "./UserInputPromptDialog";
import {
  WorkbenchOverlayPortal,
  workbenchOverlayLayerClass,
} from "./shared";

describe("workbench overlay layers", () => {
  test("portals global overlays to document.body", () => {
    const documentDescriptor = Object.getOwnPropertyDescriptor(globalThis, "document");
    const body = { nodeType: 1 } as HTMLElement;
    Object.defineProperty(globalThis, "document", {
      configurable: true,
      value: { body },
    });

    try {
      const portal = WorkbenchOverlayPortal({ children: createElement("div") });
      expect((portal as unknown as { containerInfo: unknown }).containerInfo).toBe(body);
    } finally {
      if (documentDescriptor) {
        Object.defineProperty(globalThis, "document", documentDescriptor);
      } else {
        Reflect.deleteProperty(globalThis, "document");
      }
    }
  });

  test("keeps popovers below dialogs", () => {
    expect(workbenchOverlayLayerClass.popover).toBe("z-[100]");
    expect(workbenchOverlayLayerClass.dialog).toBe("z-[200]");
  });

  test("uses the global dialog layer for full-screen workbench dialogs", () => {
    const promptMarkup = renderToStaticMarkup(createElement(UserInputPromptDialog, {
      prompt: {
        runID: "run-1",
        checkpointID: "checkpoint-1",
        nodeID: "input-1",
        statePath: "$.answer",
        message: "Continue?",
      },
      value: "",
      busy: false,
      onChange: () => undefined,
      onCancel: () => undefined,
      onSubmit: () => undefined,
    }));
    const registryMarkup = renderToStaticMarkup(createElement(RegistryDialog, {
      open: true,
      registry: null,
      toolDefinitions: [],
      onClose: () => undefined,
    }));
    const chatSetupMarkup = renderToStaticMarkup(createElement(ChatChannelSetupDialog, {
      channel: {
        id: "weixin",
        title: "Weixin",
        config_schema: {},
      },
      onClose: () => undefined,
      onConfirmed: () => undefined,
    }));

    for (const markup of [promptMarkup, registryMarkup, chatSetupMarkup]) {
      expect(markup).toContain(`fixed inset-0 ${workbenchOverlayLayerClass.dialog}`);
      expect(markup).not.toContain("fixed inset-0 z-50");
    }
  });
});
