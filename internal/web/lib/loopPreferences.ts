const autoDetectLoopsStorageKey = "weaveflow:web:auto-detect-loops:v1";

export type LoopPreferenceStorage = Pick<Storage, "getItem" | "setItem">;

export function readStoredAutoDetectLoops(
  storage: LoopPreferenceStorage | null = browserStorage()
): boolean {
  if (!storage) return false;
  try {
    return storage.getItem(autoDetectLoopsStorageKey) === "true";
  } catch {
    return false;
  }
}

export function writeStoredAutoDetectLoops(
  enabled: boolean,
  storage: LoopPreferenceStorage | null = browserStorage()
): void {
  if (!storage) return;
  try {
    storage.setItem(autoDetectLoopsStorageKey, String(enabled));
  } catch {
    return;
  }
}

function browserStorage(): LoopPreferenceStorage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}
