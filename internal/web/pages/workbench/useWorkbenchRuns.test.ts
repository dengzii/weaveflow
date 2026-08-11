import { describe, expect, test } from "bun:test";
import { isCurrentRunLaunch } from "./useWorkbenchRuns";

describe("workbench run launch sequencing", () => {
  test("ignores an old launch response after reset or a newer launch", async () => {
    let resolveFirst: ((runID: string) => void) | undefined;
    const firstResponse = new Promise<string>((resolveResponse) => {
      resolveFirst = resolveResponse;
    });
    const firstLaunch = { runContextVersion: 1, launchGeneration: 1 };
    const currentLaunch = { runContextVersion: 2, launchGeneration: 2 };

    resolveFirst?.("run-stale");
    const staleRunID = await firstResponse;
    expect(staleRunID).toBe("run-stale");
    expect(isCurrentRunLaunch(
      firstLaunch.runContextVersion,
      firstLaunch.launchGeneration,
      currentLaunch.runContextVersion,
      currentLaunch.launchGeneration
    )).toBe(false);
    expect(isCurrentRunLaunch(
      currentLaunch.runContextVersion,
      currentLaunch.launchGeneration,
      currentLaunch.runContextVersion,
      currentLaunch.launchGeneration
    )).toBe(true);
  });
});
