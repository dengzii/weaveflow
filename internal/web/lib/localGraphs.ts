import type { CachedGraphSummary, GraphDefinition, RuntimeSettings } from "../types";

export interface LocalGraph {
  id: string;
  title: string;
  graphId: string;
  graphVersion: string;
  definition: GraphDefinition;
  runtimeSettings: RuntimeSettings;
  createdAt: string;
  updatedAt: string;
}

export interface SaveLocalGraphInput {
  id?: string;
  title?: string;
  graphId: string;
  graphVersion: string;
  definition: GraphDefinition;
  runtimeSettings: RuntimeSettings;
}

let cachedGraphs: LocalGraph[] = [];

export function readLocalGraphs(): LocalGraph[] {
  return [...cachedGraphs].sort(sortGraphs);
}

export function cacheServerGraphs(graphs: CachedGraphSummary[]): LocalGraph[] {
  cachedGraphs = graphs.map((graph) => ({
    id: serverGraphCacheID(graph),
    title: graph.definition.name || graph.id,
    graphId: graph.id,
    graphVersion: graph.graph_version,
    definition: graph.definition,
    runtimeSettings: graph.settings,
    createdAt: graph.updated_at,
    updatedAt: graph.updated_at,
  })).sort(sortGraphs);
  return readLocalGraphs();
}

export function saveLocalGraph(input: SaveLocalGraphInput): LocalGraph {
  const now = new Date().toISOString();
  const graphs = readLocalGraphs();
  const existing = input.id ? graphs.find((graph) => graph.id === input.id) : undefined;
  const graph: LocalGraph = {
    id: existing?.id ?? createLocalGraphID(),
    title: input.title?.trim() || input.definition.name || input.graphId || "Untitled graph",
    graphId: input.graphId.trim() || input.definition.name || "debug_graph",
    graphVersion: input.graphVersion.trim() || input.definition.version || "2.0",
    definition: input.definition,
    runtimeSettings: input.runtimeSettings,
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

function serverGraphCacheID(graph: CachedGraphSummary) {
  return `server:${graph.id}:${graph.latest_session}`;
}
