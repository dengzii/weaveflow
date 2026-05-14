import { useEffect, useState } from "react";
import { api, buildUrl } from "../api";
import type { ArtifactDetail, NodeArtifactRef } from "./types";
import { JsonTree } from "./JsonTree";

export function ArtifactToggleView({
  artifact,
  cacheDir,
  runId,
  sourceId,
}: {
  artifact: NodeArtifactRef;
  cacheDir: string;
  runId: string;
  sourceId: string;
}) {
  const [open, setOpen] = useState(false);
  const [fetchState, setFetchState] = useState<"idle" | "loading" | "done" | "error">("idle");
  const [detail, setDetail] = useState<ArtifactDetail | null>(null);
  const [errorMsg, setErrorMsg] = useState("");

  const isImage = artifact.mimeType.startsWith("image/");
  const artifactPath = `/api/run/${encodeURIComponent(runId)}/artifact/${encodeURIComponent(artifact.id)}`;
  const query = sourceId && sourceId !== "live" ? { source: sourceId } : {};
  const detailUrl = buildUrl(artifactPath, cacheDir, query);
  const downloadUrl = buildUrl(artifactPath, cacheDir, {
    ...query,
    download: "1",
  });

  useEffect(() => {
    if (!open || isImage || detail) return;
    let cancelled = false;
    setFetchState("loading");
    setErrorMsg("");
    api<ArtifactDetail>(detailUrl)
      .then((data) => {
        if (!cancelled) {
          setDetail(data);
          setFetchState("done");
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setErrorMsg((err as Error).message ?? "Error");
          setFetchState("error");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [open, isImage, detail, detailUrl]);

  function renderDetail() {
    if (!detail) return null;
    const { encoding, payload, truncated } = detail;
    if (encoding === "json") {
      return (
        <div className="nodrag nowheel max-h-[112px] overflow-auto font-mono text-[10px] leading-[1.6]">
          <JsonTree data={payload} truncated={truncated} />
        </div>
      );
    }
    if (encoding === "text") {
      return (
        <pre className="nodrag nowheel max-h-[112px] overflow-auto font-mono text-[10px] leading-[1.5] text-slate-300 whitespace-pre-wrap break-words">
          {String(payload ?? "")}
          {truncated ? "\n…<truncated>" : ""}
        </pre>
      );
    }
    // base64 binary
    return (
      <div className="flex items-center gap-2">
        <span className="text-[10px] text-slate-500">{detail.bytes} bytes</span>
        <a href={downloadUrl} target="_blank" rel="noopener noreferrer" className="text-[10px] text-sky-400 hover:text-sky-300">↓ Download</a>
      </div>
    );
  }

  return (
    <div>
      <button
        type="button"
        className="nodrag nowheel flex w-full items-center justify-between gap-2 py-0.5 text-left"
        onClick={(e) => {
          e.stopPropagation();
          setOpen((current) => {
            const next = !current;
            if (next && fetchState === "error") {
              setFetchState("idle");
              setDetail(null);
              setErrorMsg("");
            }
            return next;
          });
        }}
      >
        <span className="font-mono text-[10px] text-violet-400">⬡ {artifact.type || "artifact"}</span>
        <span className="text-[10px] text-slate-500">{open ? "▾" : "▸"}</span>
      </button>
      {open ? (
        isImage ? (
          <div className="mt-1 overflow-hidden rounded bg-slate-800/50">
            <img src={downloadUrl} alt={artifact.id} className="max-h-[120px] w-full object-contain" />
          </div>
        ) : fetchState === "loading" ? (
          <div className="mt-0.5 text-[10px] text-slate-400">Loading…</div>
        ) : fetchState === "error" ? (
          <div className="mt-0.5 text-[10px] text-rose-400">{errorMsg}</div>
        ) : fetchState === "done" ? (
          <div className="mt-1">{renderDetail()}</div>
        ) : null
      ) : null}
    </div>
  );
}
