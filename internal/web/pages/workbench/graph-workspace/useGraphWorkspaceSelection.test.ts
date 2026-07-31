import { describe, expect, test } from "bun:test";
import { nextGraphWorkspaceSelection, type GraphWorkspaceSelection } from "./useGraphWorkspaceSelection";

describe("graph workspace selection", () => {
  test("keeps exactly one local canvas selection", () => {
    let selection: GraphWorkspaceSelection | null = null;
    selection = nextGraphWorkspaceSelection(selection, "node", "task");
    expect(selection).toEqual({ kind: "node", id: "task" });

    selection = nextGraphWorkspaceSelection(selection, "edge", "task->review");
    expect(selection).toEqual({ kind: "edge", id: "task->review" });
  });

  test("ignores unrelated clear callbacks from a compound canvas selection event", () => {
    let selection: GraphWorkspaceSelection | null = { kind: "loop", id: "retry" };
    selection = nextGraphWorkspaceSelection(selection, "node", null);
    selection = nextGraphWorkspaceSelection(selection, "edge", null);
    expect(selection).toEqual({ kind: "loop", id: "retry" });

    selection = nextGraphWorkspaceSelection(selection, "loop", null);
    expect(selection).toBeNull();
  });
});
