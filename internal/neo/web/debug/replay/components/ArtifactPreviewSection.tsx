import { useEffect, useState } from "react";
import { api, buildUrl } from "../api";
import { JsonTree } from "../graph";

type ArtifactDetail = {
  bytes: number;
  encoding: "text" | "json" | "base64";
  payload: unknown;
  truncated?: boolean;
};

export function ArtifactPreviewSection({
  cacheDir,
  runId,
  sourceId,
  artifactId,
  mimeType,
}: {
  cacheDir: string;
  runId: string;
  sourceId: string;
  artifactId: string;
  mimeType: string;
}) {
  const [detail, setDetail] = useState<ArtifactDetail | null>(null);
  const [fetchError, setFetchError] = useState("");
  const [loading, setLoading] = useState(false);

  const isImage = mimeType.startsWith("image/");
  const artifactPath = `/api/run/${encodeURIComponent(runId)}/artifact/${encodeURIComponent(artifactId)}`;
  const query: Record<string, string> =
    sourceId && sourceId !== "live" ? { source: sourceId } : {};
  const detailUrl = buildUrl(artifactPath, cacheDir, query);
  const downloadUrl = buildUrl(artifactPath, cacheDir, {
    ...query,
    download: "1",
  });

  useEffect(() => {
    if (isImage || !runId || !artifactId) return;
    let cancelled = false;
    setLoading(true);
    setDetail(null);
    setFetchError("");

    api<ArtifactDetail>(detailUrl)
      .then((data) => {
        if (!cancelled) setDetail(data);
      })
      .catch((err: unknown) => {
        if (!cancelled) setFetchError((err as Error).message ?? String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [artifactId, detailUrl, isImage, runId]);

  if (isImage) {
    return (
      <div className="overflow-hidden rounded-lg border border-border bg-card">
        <img src={downloadUrl} alt={artifactId} className="max-h-[240px] w-full object-contain" />
      </div>
    );
  }

  if (loading) return <div className="text-[11px] text-muted-foreground">Loading…</div>;
  if (fetchError) return <div className="text-[11px] text-rose-400">{fetchError}</div>;

  if (detail) {
    const { encoding, payload, truncated } = detail;
    if (encoding === "json") {
      return (
        <div className="max-h-[200px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-[1.65]">
          <JsonTree data={payload} truncated={truncated} />
        </div>
      );
    }

    if (encoding === "text") {
      return (
        <pre className="max-h-[200px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-5 text-foreground whitespace-pre-wrap break-words">
          {String(payload ?? "")}
          {truncated ? "\n…<truncated>" : ""}
        </pre>
      );
    }

    return (
      <div className="flex items-center gap-3">
        <span className="text-[11px] text-muted-foreground">{detail.bytes} bytes</span>
        <a
          href={downloadUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-[11px] text-sky-400 hover:text-sky-300"
        >
          ↓ Download
        </a>
      </div>
    );
  }

  return (
    <a
      href={downloadUrl}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-muted/60 px-3 py-2 text-[11px] text-sky-400 transition-colors hover:bg-muted hover:text-sky-300"
    >
      ↓ Download {mimeType || "artifact"}
    </a>
  );
}
