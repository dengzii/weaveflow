import type { GraphDefinition } from "../types";

const storageKey = "weaveflow:web:local-graphs:v1";

export interface LocalGraphDraft {
  id: string;
  title: string;
  graphId: string;
  graphVersion: string;
  definition: GraphDefinition;
  createdAt: string;
  updatedAt: string;
}

export interface SaveLocalGraphInput {
  id?: string;
  title?: string;
  graphId: string;
  graphVersion: string;
  definition: GraphDefinition;
}

export function readLocalGraphDrafts(): LocalGraphDraft[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(storageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(isLocalGraphDraft).sort(sortDrafts);
  } catch {
    return [];
  }
}

export function saveLocalGraphDraft(input: SaveLocalGraphInput): LocalGraphDraft {
  const now = new Date().toISOString();
  const drafts = readLocalGraphDrafts();
  const existing = input.id ? drafts.find((draft) => draft.id === input.id) : undefined;
  const draft: LocalGraphDraft = {
    id: existing?.id ?? createDraftID(),
    title: input.title?.trim() || input.definition.name || input.graphId || "Untitled graph",
    graphId: input.graphId.trim() || input.definition.name || "debug_graph",
    graphVersion: input.graphVersion.trim() || input.definition.version || "1.0",
    definition: input.definition,
    createdAt: existing?.createdAt ?? now,
    updatedAt: now,
  };
  writeLocalGraphDrafts([draft, ...drafts.filter((item) => item.id !== draft.id)].sort(sortDrafts));
  return draft;
}

export function deleteLocalGraphDraft(id: string): LocalGraphDraft[] {
  const drafts = readLocalGraphDrafts().filter((draft) => draft.id !== id);
  writeLocalGraphDrafts(drafts);
  return drafts;
}

export function duplicateLocalGraphDraft(id: string): LocalGraphDraft | null {
  const source = readLocalGraphDrafts().find((draft) => draft.id === id);
  if (!source) return null;
  return saveLocalGraphDraft({
    title: `${source.title} Copy`,
    graphId: `${source.graphId}_copy`,
    graphVersion: source.graphVersion,
    definition: {
      ...source.definition,
      name: source.definition.name ? `${source.definition.name}_copy` : source.definition.name,
    },
  });
}

function writeLocalGraphDrafts(drafts: LocalGraphDraft[]) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(storageKey, JSON.stringify(drafts.slice(0, 80)));
}

function createDraftID() {
  return `draft_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

function sortDrafts(a: LocalGraphDraft, b: LocalGraphDraft) {
  return b.updatedAt.localeCompare(a.updatedAt);
}

function isLocalGraphDraft(value: unknown): value is LocalGraphDraft {
  if (!value || typeof value !== "object") return false;
  const draft = value as LocalGraphDraft;
  return (
    typeof draft.id === "string" &&
    typeof draft.title === "string" &&
    typeof draft.graphId === "string" &&
    typeof draft.graphVersion === "string" &&
    Boolean(draft.definition) &&
    Array.isArray(draft.definition.nodes)
  );
}
