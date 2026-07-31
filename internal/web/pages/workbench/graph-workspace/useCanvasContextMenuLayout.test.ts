import { describe, expect, test } from "bun:test";
import { rootContextMenuLayout, submenuContextMenuLayout } from "./useCanvasContextMenuLayout";

const viewport = { width: 1000, height: 800 };
const boundary = { left: 100, top: 50, right: 900, bottom: 750 };

describe("canvas context menu layout", () => {
  test("keeps a root menu inside its canvas and flips at lower edges", () => {
    expect(rootContextMenuLayout({ x: 120, y: 60 }, { width: 200, height: 300 }, viewport, boundary)).toEqual({
      left: 120,
      top: 60,
      maxWidth: 784,
      maxHeight: 684,
    });
    expect(rootContextMenuLayout({ x: 850, y: 700 }, { width: 200, height: 300 }, viewport, boundary)).toMatchObject({
      left: 650,
      top: 400,
    });
  });

  test("opens a submenu to the left and clamps it vertically when needed", () => {
    expect(submenuContextMenuLayout(
      { left: 800, top: 700, right: 850, bottom: 730 },
      { width: 200, height: 200 },
      viewport,
      boundary
    )).toEqual({ left: 600, top: 542, maxWidth: 784, maxHeight: 684 });
  });
});
