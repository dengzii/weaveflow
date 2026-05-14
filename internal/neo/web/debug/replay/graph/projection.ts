import type { ReplayItem, RunDetail } from "../types";
import type {
  GraphProjection,
  NodeArtifactRef,
  NodeEventSummary,
  SourceGraph,
} from "./types";
import { SYNTHETIC_END_ID } from "./types";
import { metricValue, uniqueStrings, formatFuncArgs, objectValue, stringValue } from "./utils";

export function buildProjection(
  detail: RunDetail,
  sourceGraph: SourceGraph | null,
  replayIndex: number
): GraphProjection {
  const validNodeIds = new Set(sourceGraph?.nodes.map((item) => item.id) ?? []);
  const limited = detail.replay.slice(0, Math.max(0, replayIndex + 1));
  const visitedNodeIds = new Set<string>();
  const completedNodeIds = new Set<string>();
  const failedNodeIds = new Set<string>();
  const traversedEdgeIds = new Set<string>();
  const nodeTrail: string[] = [];
  let currentNodeId = "";

  for (const item of limited) {
    const nodeId = replayNodeId(item, validNodeIds);
    if (nodeId) {
      visitedNodeIds.add(nodeId);
      currentNodeId = nodeId;
      if (nodeTrail[nodeTrail.length - 1] !== nodeId) nodeTrail.push(nodeId);
    }

    const eventType = String(item.event.type ?? "").toLowerCase();
    if (nodeId && eventType.includes("finished")) completedNodeIds.add(nodeId);
    if (nodeId && eventType.includes("failed")) failedNodeIds.add(nodeId);
  }

  const edgeIndex = new Map<string, string>();
  for (const edge of sourceGraph?.edges ?? []) {
    edgeIndex.set(`${edge.from}-->${edge.to}`, edge.id);
  }

  // Propagate to synthetic END when the last real node is completed and connects to it
  if (
    currentNodeId &&
    completedNodeIds.has(currentNodeId) &&
    edgeIndex.has(`${currentNodeId}-->${SYNTHETIC_END_ID}`)
  ) {
    nodeTrail.push(SYNTHETIC_END_ID);
    visitedNodeIds.add(SYNTHETIC_END_ID);
    completedNodeIds.add(SYNTHETIC_END_ID);
    currentNodeId = SYNTHETIC_END_ID;
  }

  let currentEdgeId = "";
  for (let index = 1; index < nodeTrail.length; index += 1) {
    const edgeId = edgeIndex.get(`${nodeTrail[index - 1]}-->${nodeTrail[index]}`);
    if (!edgeId) continue;
    traversedEdgeIds.add(edgeId);
    currentEdgeId = edgeId;
  }

  const nodeEventSummaries = collectNodeEvents(limited, validNodeIds);

  return {
    currentNodeId,
    currentEdgeId,
    visitedNodeIds,
    completedNodeIds,
    failedNodeIds,
    traversedEdgeIds,
    nodeEventSummaries,
  };
}

function collectNodeEvents(
  events: ReplayItem[],
  validNodeIds: Set<string>
): Map<string, NodeEventSummary> {
  const summaries = new Map<string, NodeEventSummary>();
  const nodeFirstTime = new Map<string, number>();

  function ensure(nodeId: string): NodeEventSummary {
    let s = summaries.get(nodeId);
    if (!s) {
      s = {
        durationMs: -1,
        llmReasoning: "",
        llmContent: "",
        tokenUsage: {
          promptTokens: 0,
          completionTokens: 0,
          totalTokens: 0,
          reasoningTokens: 0,
          promptCachedTokens: 0,
        },
        functionCalls: [],
        toolCalls: [],
        statePatch: {
          eventCount: 0,
          changeCount: 0,
          lastPaths: [],
        },
        artifacts: [],
      };
      summaries.set(nodeId, s);
    }
    return s;
  }

  for (const item of events) {
    const nodeId = String(item.event.node_id ?? "").trim();
    if (!nodeId || !validNodeIds.has(nodeId)) continue;

    const eventType = String(item.event.type ?? "");
    const payload = (item.event.payload ?? {}) as Record<string, unknown>;
    const ts = item.event.timestamp || item.timestamp;

    // Track node duration: record first timestamp, detect finish
    if (ts) {
      const t = new Date(ts).getTime();
      if (!isNaN(t)) {
        if (!nodeFirstTime.has(nodeId)) nodeFirstTime.set(nodeId, t);
        const typeLower = eventType.toLowerCase();
        if (typeLower.includes("node") && typeLower.includes("finished")) {
          const start = nodeFirstTime.get(nodeId)!;
          ensure(nodeId).durationMs = Math.max(0, t - start);
        }
      }
    }

    if (eventType === "llm.reasoning") {
      ensure(nodeId).llmReasoning = String(payload.text ?? "");
    } else if (eventType === "llm.reasoning_chunk") {
      ensure(nodeId).llmReasoning += String(payload.text ?? "");
    } else if (eventType === "llm.content") {
      ensure(nodeId).llmContent = String(payload.text ?? "");
    } else if (eventType === "llm.content_chunk") {
      ensure(nodeId).llmContent += String(payload.text ?? "");
    } else if (eventType === "llm.usage") {
      const summary = ensure(nodeId);
      summary.tokenUsage.promptTokens += metricValue(payload, "prompt_tokens");
      summary.tokenUsage.completionTokens += metricValue(payload, "completion_tokens");
      summary.tokenUsage.totalTokens += metricValue(payload, "total_tokens");
      summary.tokenUsage.reasoningTokens += metricValue(payload, "reasoning_tokens");
      summary.tokenUsage.promptCachedTokens += metricValue(payload, "prompt_cached_tokens");
    } else if (eventType === "llm.function_call") {
      const name = String(payload.name ?? payload.function_name ?? "");
      if (!name) continue;
      const rawArgs = payload.arguments ?? payload.args ?? "";
      ensure(nodeId).functionCalls.push({ name, args: formatFuncArgs(rawArgs) });
    } else if (eventType === "tool.called" || eventType === "tool.started") {
      const name = String(
        payload.function_name ?? payload.name ?? payload.tool_name ?? payload.tool ?? ""
      );
      if (!name) continue;
      ensure(nodeId).toolCalls.push({ name, status: "pending" });
    } else if (eventType === "tool.returned") {
      const pending = [...ensure(nodeId).toolCalls].reverse().find((t) => t.status === "pending");
      if (pending) pending.status = "done";
    } else if (eventType === "tool.failed") {
      const pending = [...ensure(nodeId).toolCalls].reverse().find((t) => t.status === "pending");
      if (pending) pending.status = "failed";
    } else if (eventType === "state.changed") {
      const summary = ensure(nodeId);
      const changes = stateChangePaths(payload);
      const changeCount = stateChangeCount(payload);
      summary.statePatch.eventCount += 1;
      summary.statePatch.changeCount += changeCount;
      if (changes.length > 0) {
        summary.statePatch.lastPaths = changes;
      }
    } else if (eventType === "artifact.created") {
      const artifact = artifactFromPayload(payload);
      if (!artifact) continue;
      const summary = ensure(nodeId);
      if (artifact.type === "contract.input_view") {
        summary.contractInput = artifact;
      } else if (artifact.type === "contract.output_patch") {
        summary.contractOutputPatch = artifact;
      } else if (artifact.type === "contract.merged_state") {
        summary.contractMergedState = artifact;
      } else {
        summary.artifacts.push(artifact);
      }
    }
  }

  return summaries;
}

function artifactFromPayload(payload: Record<string, unknown>): NodeArtifactRef | null {
  const id = String(payload.artifact_id ?? payload.id ?? "").trim();
  if (!id) return null;
  return {
    id,
    type: String(payload.type ?? "").trim(),
    mimeType: String(payload.mime_type ?? "").trim(),
  };
}

function stateChangePaths(payload: Record<string, unknown>): string[] {
  const rawChanges = Array.isArray(payload.changes) ? payload.changes : [];
  const paths = rawChanges
    .map((change) => stringValue(objectValue(change)?.path))
    .filter(Boolean);
  return uniqueStrings(paths).slice(0, 6);
}

function stateChangeCount(payload: Record<string, unknown>): number {
  return Array.isArray(payload.changes) ? payload.changes.length : 0;
}

function replayNodeId(item: ReplayItem, validNodeIds: Set<string>): string {
  const eventType = String(item.event.type ?? "").toLowerCase();
  if (!eventType.includes("node")) return "";
  const nodeId = String(item.event.node_id ?? "").trim();
  if (!nodeId || !validNodeIds.has(nodeId)) return "";
  return nodeId;
}
