import { useEffect, useState } from "react";
import { api, buildUrl } from "../api";
import type { ArtifactDetail, NodeArtifactRef, StatePathValueEntry } from "./types";
import { formatConfigValue, formatConfigValueFull, objectValue } from "./utils";

const STATE_ARTIFACT_VALUE_LIMIT = 4;
const stateArtifactValuesCache = new Map<string, StatePathValueEntry[]>();

export function StateArtifactValuesSection({
  artifact,
  cacheDir,
  label,
  labelClassName,
  runId,
  sourceId,
}: {
  artifact: NodeArtifactRef;
  cacheDir: string;
  label: string;
  labelClassName: string;
  runId: string;
  sourceId: string;
}) {
  const artifactPath = `/api/run/${encodeURIComponent(runId)}/artifact/${encodeURIComponent(artifact.id)}`;
  const query = sourceId && sourceId !== "live" ? { source: sourceId } : {};
  const detailUrl = buildUrl(artifactPath, cacheDir, query);
  const cacheKey = `${runId}::${sourceId}::${artifact.id}`;
  const cachedEntries = stateArtifactValuesCache.get(cacheKey) ?? null;
  const [entries, setEntries] = useState<StatePathValueEntry[] | null>(cachedEntries);
  const [fetchState, setFetchState] = useState<"idle" | "loading" | "done" | "error">(
    cachedEntries ? "done" : "idle"
  );
  const [errorMsg, setErrorMsg] = useState("");

  useEffect(() => {
    if (cachedEntries) return;
    let cancelled = false;
    setFetchState("loading");
    setErrorMsg("");
    api<ArtifactDetail>(detailUrl)
      .then((detail) => {
        if (cancelled) return;
        const nextEntries = extractStateArtifactValues(detail);
        stateArtifactValuesCache.set(cacheKey, nextEntries);
        setEntries(nextEntries);
        setFetchState("done");
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setErrorMsg((err as Error).message ?? "Error");
        setFetchState("error");
      });
    return () => {
      cancelled = true;
    };
  }, [cacheKey, cachedEntries, detailUrl]);

  const visibleEntries = (entries ?? []).slice(0, STATE_ARTIFACT_VALUE_LIMIT);
  const hiddenCount = Math.max(0, (entries?.length ?? 0) - visibleEntries.length);

  return (
    <div className="space-y-1 rounded-md border border-slate-700/50 bg-slate-900/35 px-2 py-1.5">
      <div className={`text-[8px] font-semibold uppercase tracking-[0.12em] ${labelClassName}`}>
        {label}
      </div>
      {fetchState === "loading" ? (
        <div className="text-[10px] text-slate-500">Loading values...</div>
      ) : null}
      {fetchState === "error" ? (
        <div className="text-[10px] text-rose-400">{errorMsg}</div>
      ) : null}
      {fetchState === "done" && visibleEntries.length === 0 ? (
        <div className="text-[10px] text-slate-500">No state values</div>
      ) : null}
      {visibleEntries.length > 0 ? (
        <div className="space-y-1">
          {visibleEntries.map((entry) => (
            <StatePathValueRow key={entry.path} entry={entry} />
          ))}
          {hiddenCount > 0 ? (
            <div className="text-[9px] text-slate-500">+{hiddenCount} more paths</div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function StatePathValueRow({ entry }: { entry: StatePathValueEntry }) {
  const [open, setOpen] = useState(false);
  const preview = formatConfigValue(entry.value);
  const full = formatConfigValueFull(entry.value);
  const isLong = full.length > 96 || preview !== full;

  if (!isLong) {
    return (
      <div className="grid grid-cols-[minmax(0,1fr)] gap-y-0.5 text-[10px]">
        <div className="font-mono text-[9px] text-slate-400">{entry.path}</div>
        <div className="break-words font-mono text-slate-200">{preview}</div>
      </div>
    );
  }

  return (
    <div className="text-[10px]">
      <button
        type="button"
        className="w-full text-left"
        onClick={(e) => {
          e.stopPropagation();
          setOpen((current) => !current);
        }}
      >
        <div className="font-mono text-[9px] text-slate-400">{entry.path}</div>
        <div className="mt-0.5 flex items-start justify-between gap-2">
          <div
            className={
              open
                ? "min-w-0 whitespace-pre-wrap break-words font-mono text-slate-200"
                : "line-clamp-2 min-w-0 break-words font-mono text-slate-200"
            }
          >
            {open ? full : preview}
          </div>
          <span className="shrink-0 text-[8px] text-slate-500">{open ? "Hide" : "More"}</span>
        </div>
      </button>
    </div>
  );
}

function extractStateArtifactValues(detail: ArtifactDetail): StatePathValueEntry[] {
  if (detail.encoding !== "json") return [];
  const payload = objectValue(detail.payload);
  const snapshot = objectValue(payload?.snapshot);
  if (!snapshot) return [];
  return snapshotPathValues(snapshot);
}

function snapshotPathValues(snapshot: Record<string, unknown>): StatePathValueEntry[] {
  const entries: StatePathValueEntry[] = [];

  pushNamespacePathValues(entries, "runtime", objectValue(snapshot.runtime));
  pushNamespacePathValues(entries, "conversation", objectValue(snapshot.conversation));
  pushNamespacePathValues(entries, "shared", objectValue(snapshot.shared));

  const scopes = objectValue(snapshot.scopes);
  if (scopes) {
    for (const [scopeName, scopeValue] of Object.entries(scopes)) {
      const path = `scopes.${scopeName}`;
      const scopeObject = objectValue(scopeValue);
      if (scopeObject) {
        pushNamespacePathValues(entries, path, scopeObject);
      } else {
        entries.push({ path, value: scopeValue });
      }
    }
  }

  for (const [namespace, value] of Object.entries(snapshot)) {
    if (
      namespace === "version" ||
      namespace === "runtime" ||
      namespace === "conversation" ||
      namespace === "shared" ||
      namespace === "scopes"
    ) {
      continue;
    }
    const namespaceObject = objectValue(value);
    if (namespaceObject) {
      pushNamespacePathValues(entries, namespace, namespaceObject);
      continue;
    }
    entries.push({ path: namespace, value });
  }

  return entries;
}

function pushNamespacePathValues(
  entries: StatePathValueEntry[],
  prefix: string,
  values: Record<string, unknown> | null
) {
  if (!values) return;
  for (const [key, value] of Object.entries(values)) {
    entries.push({ path: `${prefix}.${key}`, value });
  }
}
