import type {
  ApiErrorPayload,
  ApiResponse,
  ArtifactDetail,
  ArtifactRef,
  CachedGraphSummary,
  ChatChannelSetupResult,
  CheckpointDetail,
  GraphDefinition,
  GraphDetail,
  GraphInitialStateAnalysis,
  GraphListPage,
  GraphLoadResult,
  RegistryInfo,
  RunInspection,
  RunListPage,
  RunRecord,
  RunResult,
  RuntimeEventPage,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  ToolsInfo,
  Trigger,
} from "./types";
import { resolveBackendUrl } from "./lib/backend";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, payload?: ApiErrorPayload, fallback = "Request failed") {
    super(payload?.message || fallback);
    this.name = "ApiError";
    this.status = status;
    this.code = payload?.code || "http_error";
  }
}

async function readResponse<T>(response: Response): Promise<T> {
  const contentType = response.headers.get("content-type") ?? "";
  const text = await response.text();
  let payload: ApiResponse<T> | undefined;
  if (contentType.includes("application/json") && text) {
    try {
      payload = JSON.parse(text) as ApiResponse<T>;
    } catch {
      if (response.ok) throw new ApiError(response.status, undefined, "Invalid JSON API response");
    }
  }
  if (!response.ok) {
    throw new ApiError(response.status, payload?.error, text || response.statusText);
  }
  if (response.status === 204) return undefined as T;
  if (!payload || !("data" in payload)) {
    throw new ApiError(response.status, undefined, "Invalid API response: expected a data envelope");
  }
  return payload.data;
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(resolveBackendUrl(path), init);
  return readResponse<T>(response);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function requireRuntimeSettings(value: unknown, source: string): RuntimeSettings {
  if (
    !isRecord(value) ||
    !isRecord(value.environment) ||
    !Array.isArray(value.models) ||
    !isRecord(value.memory)
  ) {
    throw new Error(`invalid ${source}: runtime settings are missing`);
  }
  return value as unknown as RuntimeSettings;
}

function graphPath(graphID: string): string {
  return `/graphs/${encodeURIComponent(graphID)}`;
}

function runPath(graphID: string, runID: string): string {
  return `${graphPath(graphID)}/runs/${encodeURIComponent(runID)}`;
}

export async function listGraphs(): Promise<CachedGraphSummary[]> {
  const items: CachedGraphSummary[] = [];
  let cursor = "";
  do {
    const query = new URLSearchParams({ limit: "200" });
    if (cursor) query.set("cursor", cursor);
    const page = await apiFetch<GraphListPage>(`/graphs?${query.toString()}`);
    items.push(...page.items);
    cursor = page.next_cursor;
  } while (cursor);
  return items;
}

export async function getGraphDetail(graphID: string): Promise<GraphDetail> {
  const detail = await apiFetch<GraphDetail>(graphPath(graphID));
  return {
    ...detail,
    settings: requireRuntimeSettings(detail.settings, `GET ${graphPath(graphID)} response`),
  };
}

export async function analyzeInitialStateRequirements(
  graphID: string,
  definition: GraphDefinition,
  triggers: Record<string, unknown>[],
  signal?: AbortSignal
): Promise<GraphInitialStateAnalysis> {
  return apiFetch<GraphInitialStateAnalysis>(`${graphPath(graphID)}/analysis/initial-state-requirements`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    signal,
    body: JSON.stringify({ definition, triggers }),
  });
}

export async function createGraphSession(
  graphID: string,
  definition: GraphDefinition,
  settings: RuntimeSettingsUpdate,
  graphVersion?: string
): Promise<GraphLoadResult> {
  const result = await apiFetch<GraphLoadResult>(`${graphPath(graphID)}/sessions`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      graph_version: graphVersion || undefined,
      definition,
      settings,
    }),
  });
  if (!isRecord(result.graph) || !isRecord(result.definition)) {
    throw new Error("invalid graph session response: graph result is missing");
  }
  return {
    ...result,
    settings: requireRuntimeSettings(result.settings, "graph session response"),
  };
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

export async function getChatChannelSetup(
  channelID: string,
  sessionID: string,
  signal?: AbortSignal
): Promise<ChatChannelSetupResult> {
  return apiFetch<ChatChannelSetupResult>(
    `/chat-channels/${encodeURIComponent(channelID)}/setup-sessions/${encodeURIComponent(sessionID)}`,
    { signal }
  );
}

export async function submitChatChannelSetupVerification(
  channelID: string,
  sessionID: string,
  verificationCode: string,
  signal?: AbortSignal
): Promise<ChatChannelSetupResult> {
  return apiFetch<ChatChannelSetupResult>(
    `/chat-channels/${encodeURIComponent(channelID)}/setup-sessions/${encodeURIComponent(sessionID)}/verification`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ verification_code: verificationCode }),
      signal,
    }
  );
}

export async function cancelChatChannelSetup(channelID: string, sessionID: string): Promise<void> {
  await apiFetch<void>(
    `/chat-channels/${encodeURIComponent(channelID)}/setup-sessions/${encodeURIComponent(sessionID)}`,
    { method: "DELETE" }
  );
}

export async function listTriggers(graphID: string): Promise<Trigger[]> {
  const items = await apiFetch<unknown>(`${graphPath(graphID)}/triggers`);
  if (!Array.isArray(items)) throw new Error("invalid trigger list response");
  return items as Trigger[];
}

export async function replaceTriggers(
  graphID: string,
  triggers: Record<string, unknown>[]
): Promise<Trigger[]> {
  const items = await apiFetch<unknown>(`${graphPath(graphID)}/triggers`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ triggers }),
  });
  if (!Array.isArray(items)) throw new Error("invalid trigger replacement response");
  return items as Trigger[];
}

export async function listRuns(graphID: string): Promise<RunRecord[]> {
  let items: RunRecord[] = [];
  let cursor = "";
  do {
    const query = new URLSearchParams({ limit: "500" });
    if (cursor) query.set("cursor", cursor);
    const page = await apiFetch<RunListPage>(`${graphPath(graphID)}/runs?${query.toString()}`);
    items = [...page.items, ...items];
    cursor = page.next_cursor;
  } while (cursor);
  return items;
}

export async function startRun(graphID: string, sessionID: string, initialState: unknown): Promise<RunRecord> {
  return apiFetch<RunRecord>(`${graphPath(graphID)}/sessions/${encodeURIComponent(sessionID)}/runs`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ initial_state: initialState }),
  });
}

export async function resumeRun(graphID: string, runID: string, input: unknown): Promise<RunResult> {
  return apiFetch<RunResult>(`${runPath(graphID, runID)}/resume`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input }),
  });
}

export async function getRunInspection(
  graphID: string,
  runID: string,
  eventCursor?: string
): Promise<RunInspection> {
  const query = new URLSearchParams({ event_limit: "500" });
  if (eventCursor) query.set("event_cursor", eventCursor);
  return apiFetch<RunInspection>(`${runPath(graphID, runID)}/inspection?${query.toString()}`);
}

export async function pauseRun(graphID: string, runID: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(`${runPath(graphID, runID)}/pause`, { method: "POST" });
}

export async function cancelRun(graphID: string, runID: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(`${runPath(graphID, runID)}/cancel`, { method: "POST" });
}

export async function deleteRun(graphID: string, runID: string): Promise<void> {
  await apiFetch<void>(runPath(graphID, runID), { method: "DELETE" });
}

export async function getCheckpoint(
  graphID: string,
  runID: string,
  checkpointID: string
): Promise<CheckpointDetail> {
  return apiFetch<CheckpointDetail>(
    `${runPath(graphID, runID)}/checkpoints/${encodeURIComponent(checkpointID)}`
  );
}

export async function listArtifacts(graphID: string, runID: string): Promise<ArtifactRef[]> {
  return apiFetch<ArtifactRef[]>(`${runPath(graphID, runID)}/artifacts`);
}

export async function getArtifact(graphID: string, runID: string, artifactID: string): Promise<ArtifactDetail> {
  return apiFetch<ArtifactDetail>(
    `${runPath(graphID, runID)}/artifacts/${encodeURIComponent(artifactID)}`
  );
}

export async function listEvents(
  graphID: string,
  runID: string,
  cursor?: string,
  limit = 500
): Promise<RuntimeEventPage> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (cursor) query.set("cursor", cursor);
  return apiFetch<RuntimeEventPage>(`${runPath(graphID, runID)}/events?${query.toString()}`);
}
