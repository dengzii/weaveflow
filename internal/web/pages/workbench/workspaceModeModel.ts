export type WorkspaceMode = "auto" | "edit" | "debug";
export type ActiveWorkspaceMode = "edit" | "debug";

export function resolveWorkspaceMode(
  mode: WorkspaceMode,
  runStatusVisible: boolean
): ActiveWorkspaceMode {
  if (mode === "auto") return runStatusVisible ? "debug" : "edit";
  return mode;
}
