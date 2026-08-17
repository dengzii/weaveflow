import type { CachedGraphSummary, GraphDefinition, GraphDetail, GraphLoadResult, RuntimeSettings } from "../types";

export const defaultGraphVersion = "1.0";

export interface LocalGraph {
  id: string;
  title: string;
  graphId: string;
  graphVersion: string;
  definition?: GraphDefinition;
  runtimeSettings?: RuntimeSettings;
  nodeCount: number;
  serverGraph: boolean;
  latestSession?: string;
  createdAt: string;
  updatedAt: string;
}

export type HydratedLocalGraph = LocalGraph & {
  definition: GraphDefinition;
  runtimeSettings: RuntimeSettings;
};

export interface SaveLocalGraphInput {
  id?: string;
  title?: string;
  graphId: string;
  graphVersion: string;
  definition: GraphDefinition;
  runtimeSettings: RuntimeSettings;
}

let cachedGraphs: LocalGraph[] = [];
const selectedGraphIDStorageKey = "weaveflow:web:selected-graph-id:v1";

interface GraphSelectionStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export function readRememberedGraphID(storage: GraphSelectionStorage | null = browserStorage()): string {
  if (!storage) return "";
  try {
    return storage.getItem(selectedGraphIDStorageKey)?.trim() ?? "";
  } catch {
    return "";
  }
}

export function rememberGraphID(graphID: string, storage: GraphSelectionStorage | null = browserStorage()): void {
  const normalized = graphID.trim();
  if (!storage || !normalized) return;
  try {
    storage.setItem(selectedGraphIDStorageKey, normalized);
  } catch {}
}

export function preferredServerGraph(
  graphs: CachedGraphSummary[],
  rememberedGraphID = readRememberedGraphID()
): CachedGraphSummary | undefined {
  return graphs.find((graph) => graph.id === rememberedGraphID) ?? graphs[0];
}

export function readLocalGraphs(): LocalGraph[] {
  return [...cachedGraphs].sort(sortGraphs);
}

export function cacheServerGraphs(graphs: CachedGraphSummary[]): LocalGraph[] {
  const currentByID = new Map(cachedGraphs.map((graph) => [graph.id, graph]));
  cachedGraphs = graphs.map((graph) => {
    const id = serverGraphCacheID(graph.id, graph.latest_session);
    const current = currentByID.get(id);
    return {
      id,
      title: graph.name || graph.id,
      graphId: graph.id,
      graphVersion: graph.graph_version,
      definition: current?.definition,
      runtimeSettings: current?.runtimeSettings,
      nodeCount: graph.node_count,
      serverGraph: true,
      latestSession: graph.latest_session,
      createdAt: graph.updated_at,
      updatedAt: graph.updated_at,
    };
  }).sort(sortGraphs);
  return readLocalGraphs();
}

export function hydrateServerGraph(graph: LocalGraph, detail: GraphDetail): HydratedLocalGraph {
  if (graph.graphId !== detail.graph.id) {
    throw new Error(`graph detail ${detail.graph.id} does not match ${graph.graphId}`);
  }
  const hydrated: HydratedLocalGraph = {
    ...graph,
    title: detail.definition.name || detail.graph.id,
    graphVersion: detail.graph.version,
    definition: detail.definition,
    runtimeSettings: detail.settings,
    nodeCount: detail.definition.nodes.length,
    latestSession: detail.latest_session.id,
  };
  cachedGraphs = cachedGraphs.map((item) => item.id === graph.id ? hydrated : item).sort(sortGraphs);
  return hydrated;
}

export function hydrateServerGraphResult(graph: LocalGraph, result: GraphLoadResult): HydratedLocalGraph {
  if (graph.graphId !== result.graph.id) {
    throw new Error(`graph commit ${result.graph.id} does not match ${graph.graphId}`);
  }
  const hydrated: HydratedLocalGraph = {
    ...graph,
    title: result.definition.name || result.graph.id,
    graphVersion: result.graph.version,
    definition: result.definition,
    runtimeSettings: result.settings,
    nodeCount: result.definition.nodes.length,
    latestSession: result.graph.graph_session_id,
  };
  cachedGraphs = cachedGraphs.map((item) => item.id === graph.id ? hydrated : item).sort(sortGraphs);
  return hydrated;
}

export function isHydratedLocalGraph(graph: LocalGraph): graph is HydratedLocalGraph {
  return Boolean(graph.definition && graph.runtimeSettings);
}

export function saveLocalGraph(input: SaveLocalGraphInput): LocalGraph {
  const now = new Date().toISOString();
  const graphs = readLocalGraphs();
  const existing = input.id ? graphs.find((graph) => graph.id === input.id) : undefined;
  const graph: LocalGraph = {
    id: existing?.id ?? createLocalGraphID(),
    title: input.title?.trim() || input.definition.name || input.graphId || "Untitled graph",
    graphId: input.graphId.trim() || input.definition.name || "debug_graph",
    graphVersion: input.graphVersion.trim() || defaultGraphVersion,
    definition: input.definition,
    runtimeSettings: input.runtimeSettings,
    nodeCount: input.definition.nodes.length,
    serverGraph: existing?.serverGraph ?? false,
    latestSession: existing?.latestSession,
    createdAt: existing?.createdAt ?? now,
    updatedAt: now,
  };
  writeLocalGraphs([graph, ...graphs.filter((item) => item.id !== graph.id)].sort(sortGraphs));
  return graph;
}

export function deleteLocalGraph(id: string): LocalGraph[] {
  const graphs = readLocalGraphs().filter((graph) => graph.id !== id);
  writeLocalGraphs(graphs);
  return graphs;
}

function writeLocalGraphs(graphs: LocalGraph[]) {
  cachedGraphs = graphs.slice(0, 80);
}

function createLocalGraphID() {
  return `local_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

function sortGraphs(a: LocalGraph, b: LocalGraph) {
  return b.updatedAt.localeCompare(a.updatedAt);
}

function serverGraphCacheID(graphID: string, latestSession: string) {
  return `server:${graphID}:${latestSession}`;
}

function browserStorage(): GraphSelectionStorage | null {
  return typeof window === "undefined" ? null : window.localStorage;
}
