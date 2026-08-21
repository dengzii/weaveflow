import type {
  ApiErrorPayload,
  ApiResponse,
  ArtifactDetail,
  ArtifactRef,
  CachedGraphSummary,
  ChatChannelSetupResult,
  CheckpointDetail,
  ForkResult,
  GraphDefinition,
  GraphDetail,
  GraphInitialStateAnalysis,
  GraphLoadResult,
  RegistryInfo,
  RunInspection,
  RunComparison,
  RunRecord,
  RunResult,
  RuntimeEventPage,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  ServerInfo,
  ToolsInfo,
  Trigger,
} from "./types";
import {
  validateGraphDetail,
  validateGraphListPage,
  validateGraphLoadResult,
  validateForkResult,
  validateRegistryInfo,
  validateRunInspection,
  validateRunComparison,
  validateRunListPage,
  validateRunRecord,
  validateRunResult,
  validateRuntimeEventPage,
  validateToolsInfo,
} from "./apiValidation";
import { managementHeaders, resolveBackendUrl } from "./lib/backend";

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
  const response = await fetch(resolveBackendUrl(path), {
    ...init,
    headers: managementHeaders(init?.headers),
  });
  return readResponse<T>(response);
}

export async function getServerInfo(): Promise<ServerInfo> {
  return apiFetch<ServerInfo>("/healthz");
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
    const page = validateGraphListPage(
      await apiFetch<unknown>(`/graphs?${query.toString()}`),
      "GET /graphs response"
    );
    items.push(...page.items);
    cursor = page.next_cursor;
  } while (cursor);
  return items;
}

export async function getGraphDetail(graphID: string): Promise<GraphDetail> {
  const path = graphPath(graphID);
  return validateGraphDetail(await apiFetch<unknown>(path), `GET ${path} response`);
}

export async function deleteGraph(graphID: string): Promise<void> {
  await apiFetch<unknown>(graphPath(graphID), { method: "DELETE" });
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

export async function commitGraph(
  graphID: string,
  definition: GraphDefinition,
  settings: RuntimeSettingsUpdate,
  triggers: Record<string, unknown>[],
  mode: "create" | "overwrite",
  expectedGraphSessionID?: string,
  graphVersion?: string
): Promise<GraphLoadResult> {
  const path = `${graphPath(graphID)}/sessions`;
  const request = {
    graph_version: graphVersion || undefined,
    definition,
    settings,
    triggers,
    mode,
    expected_graph_session_id: expectedGraphSessionID || undefined,
  };
  const result = await apiFetch<unknown>(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ ...request, request_id: graphCommitRequestID(graphID, request) }),
  });
  return validateGraphLoadResult(result, `POST ${path} response`);
}

function graphCommitRequestID(graphID: string, request: Record<string, unknown>): string {
  const value = JSON.stringify([graphID.trim(), request]);
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return `graph-${(hash >>> 0).toString(16).padStart(8, "0")}`;
}

export async function getRegistry(): Promise<RegistryInfo> {
  return validateRegistryInfo(await apiFetch<unknown>("/registry"), "GET /registry response");
}

export async function getTools(): Promise<ToolsInfo> {
  return validateToolsInfo(await apiFetch<unknown>("/runtime/tools"), "GET /runtime/tools response");
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
    const page = validateRunListPage(
      await apiFetch<unknown>(`${graphPath(graphID)}/runs?${query.toString()}`),
      `GET ${graphPath(graphID)}/runs response`
    );
    items = [...page.items, ...items];
    cursor = page.next_cursor;
  } while (cursor);
  return items;
}

export async function startRun(graphID: string, sessionID: string, initialState: unknown): Promise<RunRecord> {
  const path = `${graphPath(graphID)}/sessions/${encodeURIComponent(sessionID)}/runs`;
  return validateRunRecord(await apiFetch<unknown>(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ initial_state: initialState }),
  }), `POST ${path} response`);
}

export async function resumeRun(graphID: string, runID: string, input: unknown): Promise<RunResult> {
  const path = `${runPath(graphID, runID)}/resume`;
  return validateRunResult(await apiFetch<unknown>(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input }),
  }), `POST ${path} response`);
}

export async function getRunInspection(
  graphID: string,
  runID: string,
  eventCursor?: string
): Promise<RunInspection> {
  const query = new URLSearchParams({ event_limit: "500" });
  if (eventCursor) query.set("event_cursor", eventCursor);
  const path = `${runPath(graphID, runID)}/inspection?${query.toString()}`;
  return validateRunInspection(await apiFetch<unknown>(path), `GET ${path} response`);
}

export async function forkRun(
  graphID: string,
  runID: string,
  checkpointID: string,
  requestKey: string,
  input: unknown = {}
): Promise<ForkResult> {
  const path = `${runPath(graphID, runID)}/forks`;
  return validateForkResult(await apiFetch<unknown>(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ checkpoint_id: checkpointID, request_key: requestKey, input }),
  }), `POST ${path} response`);
}

export async function compareRuns(
  graphID: string,
  runID: string,
  otherRunID: string
): Promise<RunComparison> {
  const path = `${runPath(graphID, runID)}/compare/${encodeURIComponent(otherRunID)}`;
  return validateRunComparison(await apiFetch<unknown>(path), `GET ${path} response`);
}

export async function pauseRun(graphID: string, runID: string): Promise<RunRecord> {
  const path = `${runPath(graphID, runID)}/pause`;
  return validateRunRecord(await apiFetch<unknown>(path, { method: "POST" }), `POST ${path} response`);
}

export async function cancelRun(graphID: string, runID: string): Promise<RunRecord> {
  const path = `${runPath(graphID, runID)}/cancel`;
  return validateRunRecord(await apiFetch<unknown>(path, { method: "POST" }), `POST ${path} response`);
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
  const path = `${runPath(graphID, runID)}/events?${query.toString()}`;
  return validateRuntimeEventPage(await apiFetch<unknown>(path), `GET ${path} response`);
}
