import type {
  ApiResponse,
  ArtifactDetail,
  ArtifactRef,
  CachedGraphSummary,
  ChatChannelSetupResult,
  CheckpointDetail,
  CheckpointRecord,
  GraphDefinition,
  GraphInfo,
  GraphLoadResult,
  InitialStateRequirements,
  RegistryInfo,
  RunInspection,
  RunInterrupt,
  RunRecord,
  RunResult,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  RuntimeEvent,
  StepRecord,
  ToolsInfo,
  Trigger,
  TriggerInvocation,
} from "./types";
import { resolveBackendUrl } from "./lib/backend";

async function readResponse<T>(resp: Response): Promise<T> {
  const contentType = resp.headers.get("content-type") ?? "";
  const text = await resp.text();
  let payload: ApiResponse<T> | undefined;
  if (contentType.includes("application/json") && text) {
    payload = JSON.parse(text) as ApiResponse<T>;
  }
  if (!resp.ok) {
    throw new Error(payload?.error?.message || text || resp.statusText);
  }
  if (resp.status === 204) {
    return undefined as T;
  }
  if (!payload) {
    throw new Error("invalid API response: expected JSON");
  }
  if (payload && "data" in payload) {
    return payload.data as T;
  }
  return payload as T;
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(resolveBackendUrl(path), init);
  return readResponse<T>(resp);
}

function graphQuery(graphId?: string): string {
  if (!graphId) return "";
  return `?graph_id=${encodeURIComponent(graphId)}`;
}

function appendGraphQuery(path: string, graphId?: string): string {
  if (!graphId) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}graph_id=${encodeURIComponent(graphId)}`;
}

export async function getGraphInfo(): Promise<GraphInfo> {
  return apiFetch<GraphInfo>("/graph");
}

export async function getGraphDefinition(): Promise<GraphDefinition> {
  return apiFetch<GraphDefinition>("/graph/definition");
}

export async function getRuntimeSettings(): Promise<RuntimeSettings> {
  return apiFetch<RuntimeSettings>("/runtime/settings");
}

export async function updateRuntimeSettings(settings: RuntimeSettingsUpdate): Promise<RuntimeSettings> {
  return apiFetch<RuntimeSettings>("/runtime/settings", {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(settings),
  });
}

export async function getInitialStateRequirements(): Promise<InitialStateRequirements> {
  return apiFetch<InitialStateRequirements>("/graph/initial-state-requirements");
}

export async function analyzeInitialStateRequirements(
  definition: GraphDefinition,
  graphId?: string,
  graphVersion?: string
): Promise<InitialStateRequirements> {
  return apiFetch<InitialStateRequirements>("/graph/initial-state-requirements", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      graph_id: graphId || undefined,
      graph_version: graphVersion || undefined,
      definition,
    }),
  });
}

export async function setGraphDefinition(
  definition: GraphDefinition,
  graphId?: string,
  graphVersion?: string
): Promise<GraphLoadResult> {
  return apiFetch<GraphLoadResult>("/graph", {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      graph_id: graphId || undefined,
      graph_version: graphVersion || undefined,
      definition,
    }),
  });
}

export async function publishGraphDefinition(
  definition: GraphDefinition,
  graphId?: string,
  graphVersion?: string
): Promise<GraphLoadResult> {
  return apiFetch<GraphLoadResult>("/graph/publish", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      graph_id: graphId || undefined,
      graph_version: graphVersion || undefined,
      definition,
    }),
  });
}

export async function getRegistry(): Promise<RegistryInfo> {
  return apiFetch<RegistryInfo>("/registry");
}

export async function getTools(): Promise<ToolsInfo> {
  return apiFetch<ToolsInfo>("/runtime/tools");
}

export async function startChatChannelSetup(channelID: string, triggerID?: string): Promise<ChatChannelSetupResult> {
  return apiFetch<ChatChannelSetupResult>(`/chat-channels/${encodeURIComponent(channelID)}/setup-sessions`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ trigger_id: triggerID || undefined }),
  });
}

export async function pollChatChannelSetup(
  channelID: string,
  sessionID: string,
  verificationCode?: string,
  signal?: AbortSignal
): Promise<ChatChannelSetupResult> {
  return apiFetch<ChatChannelSetupResult>(
    `/chat-channels/${encodeURIComponent(channelID)}/setup-sessions/${encodeURIComponent(sessionID)}/poll`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ verification_code: verificationCode || undefined }),
      signal,
    }
  );
}

export async function cancelChatChannelSetup(channelID: string, sessionID: string): Promise<void> {
  await apiFetch<unknown>(
    `/chat-channels/${encodeURIComponent(channelID)}/setup-sessions/${encodeURIComponent(sessionID)}`,
    { method: "DELETE" }
  );
}

export async function listTriggers(): Promise<Trigger[]> {
  const items = await apiFetch<unknown>("/triggers");
  if (!Array.isArray(items)) {
    throw new Error("invalid trigger list response");
  }
  return items as Trigger[];
}

export async function listTriggerInvocations(triggerID?: string, limit = 100): Promise<TriggerInvocation[]> {
  const query = new URLSearchParams({ limit: String(limit) });
  const path = triggerID
    ? `/triggers/${encodeURIComponent(triggerID)}/invocations`
    : "/trigger-invocations";
  const items = await apiFetch<unknown>(`${path}?${query.toString()}`);
  if (!Array.isArray(items)) {
    throw new Error("invalid trigger invocation list response");
  }
  return items as TriggerInvocation[];
}

export async function createTrigger(input: Record<string, unknown>): Promise<Trigger> {
  return apiFetch<Trigger>("/triggers", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function updateTrigger(triggerID: string, input: Record<string, unknown>): Promise<Trigger> {
  return apiFetch<Trigger>(`/triggers/${encodeURIComponent(triggerID)}`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function deleteTrigger(triggerID: string): Promise<void> {
  await apiFetch<unknown>(`/triggers/${encodeURIComponent(triggerID)}`, { method: "DELETE" });
}

export async function listGraphs(): Promise<CachedGraphSummary[]> {
  const items = await apiFetch<unknown>("/graphs");
  if (!Array.isArray(items)) {
    throw new Error("invalid graph list response");
  }
  return items as CachedGraphSummary[];
}

export async function listRuns(graphId?: string): Promise<RunRecord[]> {
  return apiFetch<RunRecord[]>(`/runs${graphQuery(graphId)}`);
}

export async function startRun(initialState: unknown): Promise<RunResult> {
  return apiFetch<RunResult>("/runs", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ initial_state: initialState }),
  });
}

export async function resumeRun(runId: string, input: unknown): Promise<RunResult> {
  return apiFetch<RunResult>(`/runs/${encodeURIComponent(runId)}/resume`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input }),
  });
}

export async function resumeCheckpoint(checkpointId: string, input: unknown): Promise<RunResult> {
  return apiFetch<RunResult>(`/checkpoints/${encodeURIComponent(checkpointId)}/resume`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input }),
  });
}

export async function getRun(runId: string, graphId?: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}`, graphId));
}

export async function getRunInterrupt(runId: string, graphId?: string): Promise<RunInterrupt | null> {
  return apiFetch<RunInterrupt | null>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/interrupt`, graphId));
}

export async function getRunInspection(runId: string, graphId?: string): Promise<RunInspection> {
  const [run, steps, checkpoints, events, interrupt] = await Promise.all([
    getRun(runId, graphId),
    listSteps(runId, graphId),
    listCheckpoints(runId, graphId),
    listEvents(runId, graphId),
    getRunInterrupt(runId, graphId),
  ]);
  return { run, steps, checkpoints, events, interrupt };
}

export async function pauseRun(runId: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(`/runs/${encodeURIComponent(runId)}/pause`, { method: "POST" });
}

export async function cancelRun(runId: string, graphId?: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/cancel`, graphId), { method: "POST" });
}

export async function deleteRun(runId: string, graphId?: string): Promise<void> {
  await apiFetch<void>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}`, graphId), { method: "DELETE" });
}

export async function listSteps(runId: string, graphId?: string): Promise<StepRecord[]> {
  return apiFetch<StepRecord[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/steps`, graphId));
}

export async function listCheckpoints(runId: string, graphId?: string): Promise<CheckpointRecord[]> {
  return apiFetch<CheckpointRecord[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/checkpoints`, graphId));
}

export async function getCheckpoint(checkpointId: string, graphId?: string): Promise<CheckpointDetail> {
  return apiFetch<CheckpointDetail>(appendGraphQuery(`/checkpoints/${encodeURIComponent(checkpointId)}`, graphId));
}

export async function listArtifacts(runId: string, graphId?: string): Promise<ArtifactRef[]> {
  return apiFetch<ArtifactRef[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/artifacts`, graphId));
}

export async function getArtifact(runId: string, artifactId: string, graphId?: string): Promise<ArtifactDetail> {
  return apiFetch<ArtifactDetail>(
    appendGraphQuery(`/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}`, graphId)
  );
}

export async function listEvents(runId: string, graphId?: string): Promise<RuntimeEvent[]> {
  return apiFetch<RuntimeEvent[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/events`, graphId));
}
