import type { GraphDefinition, RunInterrupt } from "../../types";
import { parseStatePath } from "./graph-workspace/runInputModel";

export interface UserInputPrompt {
  runID: string;
  checkpointID: string;
  nodeID: string;
  statePath: string;
  message: string;
}

export function userInputPromptFromInterrupt(
  interrupt: RunInterrupt | null | undefined,
  definition: GraphDefinition | null
): UserInputPrompt | null {
  if (!interrupt?.run_id || !interrupt.checkpoint_id || !interrupt.node_id || !definition) return null;
  const node = definition.nodes.find((item) => item.id === interrupt.node_id);
  if (node?.type !== "user_input") return null;
  const statePath = node.state?.pending_input?.path.trim() ?? "";
  if (!parseStatePath(statePath)) return null;
  return {
    runID: interrupt.run_id,
    checkpointID: interrupt.checkpoint_id,
    nodeID: interrupt.node_id,
    statePath,
    message: interrupt.message || "The run is waiting for user input.",
  };
}

export function pendingUserInputState(path: string, message: string): Record<string, unknown> {
  const segments = parseStatePath(path);
  if (!segments) return {};

  const root: Record<string, unknown> = {};
  let current = root;
  for (const segment of segments.slice(0, -1)) {
    const next: Record<string, unknown> = {};
    current[segment] = next;
    current = next;
  }
  current[segments[segments.length - 1]] = message;
  return root;
}
