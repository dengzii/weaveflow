import { useEffect, useMemo, useState } from "react";
import type { CacheFileDetail, CacheFileEntry } from "../types";
import { formatBytes, formatTime, prettyJSON } from "../utils";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";
import { cn } from "../../../lib/utils";

function formatFileContent(detail: CacheFileDetail | null): string {
  if (!detail) return "";
  if (detail.is_text) {
    const contentType = detail.content_type || "";
    if (contentType.includes("json")) {
      try {
        return prettyJSON(JSON.parse(detail.content || "null"));
      } catch {
        return detail.content || "";
      }
    }
    return detail.content || "";
  }
  if (!detail.content) {
    return "Binary file";
  }
  return `Binary file (${detail.content_type || "application/octet-stream"})\n\nBase64 preview:\n${detail.content}`;
}

export function CacheFilesPanel({
  files,
  detail,
  selectedPath,
  onSelect,
}: {
  files: CacheFileEntry[];
  detail: CacheFileDetail | null;
  selectedPath: string;
  onSelect: (path: string) => void;
}) {
  const [filter, setFilter] = useState("");

  useEffect(() => {
    setFilter("");
  }, [files]);

  const filtered = useMemo(() => {
    const keyword = filter.trim().toLowerCase();
    if (!keyword) return files;
    return files.filter((file) => file.path.toLowerCase().includes(keyword));
  }, [files, filter]);

  const content = formatFileContent(detail);

  return (
    <Card className="min-h-0">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="text-base">Cache Files</CardTitle>
          <span className="text-xs bg-muted text-muted-foreground px-2 py-0.5 rounded-full">
            {files.length}
          </span>
        </div>
        <input
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder="Filter files..."
          className="h-8 w-full rounded-md border border-input bg-background px-2.5 text-xs outline-none"
        />
      </CardHeader>
      <CardContent className="grid min-h-0 flex-1 grid-cols-[320px_minmax(0,1fr)] gap-3">
        <div className="min-h-0 overflow-y-auto rounded-md border border-border/60">
          {filtered.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">No files</div>
          ) : (
            filtered.map((file) => (
              <button
                key={file.path}
                type="button"
                onClick={() => onSelect(file.path)}
                className={cn(
                  "flex w-full items-start justify-between gap-3 border-b border-border/40 px-3 py-2.5 text-left text-xs transition-colors last:border-b-0",
                  selectedPath === file.path
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-muted/40"
                )}
              >
                <div className="min-w-0">
                  <div className="truncate font-medium">{file.name}</div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">
                    {file.path}
                  </div>
                </div>
                <div className="shrink-0 text-right text-[11px] text-muted-foreground">
                  <div>{formatBytes(file.size)}</div>
                  <div>{file.is_text ? "text" : "binary"}</div>
                </div>
              </button>
            ))
          )}
        </div>

        <div className="min-h-0 overflow-hidden rounded-md border border-border/60 bg-muted/20">
          {!detail ? (
            <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
              Select a file to preview
            </div>
          ) : (
            <div className="flex h-full flex-col">
              <div className="border-b border-border/60 px-3 py-2">
                <div className="truncate text-sm font-medium">{detail.path}</div>
                <div className="mt-1 flex flex-wrap gap-3 text-[11px] text-muted-foreground">
                  <span>{detail.content_type || "-"}</span>
                  <span>{formatBytes(detail.size)}</span>
                  <span>{formatTime(detail.modified_at)}</span>
                  {detail.truncated ? <span>preview truncated</span> : null}
                </div>
              </div>
              <pre className="min-h-0 flex-1 overflow-auto p-3 text-xs leading-5">
                {content}
              </pre>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
