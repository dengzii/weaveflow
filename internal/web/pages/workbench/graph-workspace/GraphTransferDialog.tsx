import { useEffect, useRef, useState, type ComponentType } from "react";
import {
  Braces,
  Download,
  FileJson2,
  FileUp,
  GitBranch,
  LayoutDashboard,
  LoaderCircle,
  LockKeyhole,
  Settings,
  Zap,
  X,
} from "lucide-react";
import { Button } from "../../../components/ui/button";
import { getGraphDetail } from "../../../api";
import type { GraphDefinition, GraphDetail, RuntimeSettings, Trigger } from "../../../types";
import { WorkbenchDialogOverlay } from "../shared";
import {
  buildGraphExportBundle,
  graphExportFilename,
  parseGraphImport,
  type GraphImportStrategy,
  type ParsedGraphImport,
} from "./graphTransferModel";

export type GraphTransferMode = "import" | "export";

export interface GraphImportOptions {
  strategy: GraphImportStrategy;
}

const maxImportFileBytes = 8 * 1024 * 1024;

export function GraphTransferDialog({
  mode,
  definition,
  graphID,
  graphVersion,
  runtimeSettings,
  triggers,
  existingGraphIDs,
  onClose,
  onImport,
}: {
  mode: GraphTransferMode | null;
  definition: GraphDefinition | null;
  graphID: string;
  graphVersion: string;
  runtimeSettings: RuntimeSettings;
  triggers: Trigger[];
  existingGraphIDs: string[];
  onClose: () => void;
  onImport: (graph: ParsedGraphImport, options: GraphImportOptions) => Promise<boolean>;
}) {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [includeConfig, setIncludeConfig] = useState(true);
  const [includeSettings, setIncludeSettings] = useState(true);
  const [includeTriggers, setIncludeTriggers] = useState(true);
  const [includeUI, setIncludeUI] = useState(true);
  const [fileName, setFileName] = useState("");
  const [parsedImport, setParsedImport] = useState<ParsedGraphImport | null>(null);
  const [overwriteExisting, setOverwriteExisting] = useState(false);
  const [readingFile, setReadingFile] = useState(false);
  const [importing, setImporting] = useState(false);
  const [overwriteTarget, setOverwriteTarget] = useState<GraphDetail | null>(null);
  const [targetLoading, setTargetLoading] = useState(false);
  const [targetReloadRevision, setTargetReloadRevision] = useState(0);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!mode) return;
    setIncludeConfig(true);
    setIncludeSettings(true);
    setIncludeTriggers(true);
    setIncludeUI(true);
    setFileName("");
    setParsedImport(null);
    setOverwriteExisting(false);
    setReadingFile(false);
    setImporting(false);
    setOverwriteTarget(null);
    setTargetLoading(false);
    setTargetReloadRevision(0);
    setError("");
  }, [mode]);

  useEffect(() => {
    if (!mode) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [mode, onClose]);

  useEffect(() => {
    if (!overwriteExisting || !parsedImport?.graphID.trim()) {
      setOverwriteTarget(null);
      setTargetLoading(false);
      return;
    }
    const controller = new AbortController();
    setTargetLoading(true);
    setError("");
    void getGraphDetail(parsedImport.graphID)
      .then((detail) => {
        if (!controller.signal.aborted) setOverwriteTarget(detail);
      })
      .catch((loadError) => {
        if (!controller.signal.aborted) setError(loadError instanceof Error ? loadError.message : String(loadError));
      })
      .finally(() => {
        if (!controller.signal.aborted) setTargetLoading(false);
      });
    return () => controller.abort();
  }, [overwriteExisting, parsedImport, targetReloadRevision]);

  if (!mode) return null;

  async function readImportFile(file: File | undefined) {
    if (!file) return;
    setFileName(file.name);
    setParsedImport(null);
    setOverwriteExisting(false);
    setError("");
    if (file.size > maxImportFileBytes) {
      setError("Graph file exceeds the 8 MB limit.");
      return;
    }
    setReadingFile(true);
    try {
      setParsedImport(parseGraphImport(await file.text()));
    } catch (readError) {
      setError(readError instanceof Error ? readError.message : String(readError));
    } finally {
      setReadingFile(false);
    }
  }

  async function importGraph() {
    if (!parsedImport) return;
    setImporting(true);
    setError("");
    try {
      if (await onImport(parsedImport, { strategy: overwriteExisting ? "overwrite" : "copy" })) onClose();
    } catch (importError) {
      setError(importError instanceof Error ? importError.message : String(importError));
    } finally {
      setImporting(false);
    }
  }

  function exportGraph() {
    if (!definition) return;
    const bundle = buildGraphExportBundle({
      definition,
      graphID,
      graphVersion,
      runtimeSettings,
      triggers,
      includeConfig,
      includeSettings,
      includeTriggers,
      includeUI,
    });
    const blob = new Blob([`${JSON.stringify(bundle, null, 2)}\n`], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = graphExportFilename(graphID, definition);
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
    onClose();
  }

  const title = mode === "import" ? "Import graph" : "Export graph";
  const importedGraphID = parsedImport?.graphID.trim() ?? "";
  const existingGraphIDSet = new Set(existingGraphIDs.map((value) => value.trim()).filter(Boolean));
  const hasGraphIDConflict = Boolean(importedGraphID && existingGraphIDSet.has(importedGraphID));
  return (
    <WorkbenchDialogOverlay onDismiss={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="w-[min(600px,100%)] overflow-hidden rounded-md border border-border bg-panel shadow-xl"
      >
        <div className="flex h-14 items-center gap-3 border-b border-border px-4">
          {mode === "import" ? (
            <FileUp className="h-4 w-4 text-muted-foreground" />
          ) : (
            <Download className="h-4 w-4 text-muted-foreground" />
          )}
          <div className="min-w-0 flex-1 truncate text-sm font-semibold">{title}</div>
          <Button variant="ghost" size="icon" onClick={onClose} title="Close" aria-label={`Close ${title}`}>
            <X className="h-4 w-4" />
          </Button>
        </div>

        {mode === "import" ? (
          <div className="grid gap-4 p-4">
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json,.json"
              className="hidden"
              onChange={(event) => {
                void readImportFile(event.target.files?.[0]);
                event.target.value = "";
              }}
            />
            <button
              type="button"
              className="grid min-h-36 place-items-center gap-2 rounded-md border border-dashed border-border bg-muted/20 p-5 text-center hover:bg-muted/40"
              onClick={() => fileInputRef.current?.click()}
            >
              {readingFile ? (
                <LoaderCircle className="h-7 w-7 animate-spin text-muted-foreground" />
              ) : (
                <FileJson2 className="h-7 w-7 text-muted-foreground" />
              )}
              <span className="text-sm font-medium">{fileName || "Choose a JSON file"}</span>
            </button>

            {parsedImport ? (
              <div className="grid gap-2 border-t border-border pt-3 text-sm">
                <TransferDetail label="Graph" value={parsedImport.graphID || parsedImport.definition.name || "Untitled graph"} />
                <TransferDetail label="Version" value={parsedImport.graphVersion || "Not set"} />
                <TransferDetail label="Contents" value={contentLabel(parsedImport.contents)} />
                <TransferDetail label="Nodes" value={String(parsedImport.definition.nodes.length)} />
                {hasGraphIDConflict ? (
                  <label className="mt-2 flex items-start gap-3 rounded-md border border-border bg-muted/20 p-3">
                    <input
                      type="checkbox"
                      className="mt-0.5 h-4 w-4 accent-primary"
                      checked={overwriteExisting}
                      onChange={(event) => setOverwriteExisting(event.target.checked)}
                    />
                    <span className="grid gap-1">
                      <span className="font-medium">Overwrite existing graph</span>
                      <span className="text-xs text-muted-foreground">
                        Preserve any Settings or Triggers not declared by the file, and reject if the target Head changes.
                      </span>
                    </span>
                  </label>
                ) : null}
                {overwriteExisting ? (
                  <div className="mt-1 grid gap-2 rounded-md border border-border bg-background p-3">
                    <TransferDetail label="Target" value={overwriteTarget?.graph.id || importedGraphID} />
                    <TransferDetail
                      label="Head session"
                      value={targetLoading ? "Loading…" : overwriteTarget?.graph.graph_session_id || "Unavailable"}
                    />
                    <TransferDetail
                      label="Settings"
                      value={parsedImport.contents.includes("settings") ? "Replace from file" : "Preserve target"}
                    />
                    <TransferDetail
                      label="Triggers"
                      value={parsedImport.contents.includes("triggers") ? "Replace from file" : "Preserve target"}
                    />
                  </div>
                ) : null}
                {parsedImport.contents.includes("triggers") ? (
                  <div className="mt-1 text-xs text-muted-foreground">
                    Imported Triggers remain disabled; managed credentials, pending values, Bot IDs, and plaintext API keys are not imported.
                  </div>
                ) : null}
              </div>
            ) : null}
            {error ? <div className="text-sm text-destructive">{error}</div> : null}
            {overwriteExisting && error.includes("graph head conflict") ? (
              <Button variant="outline" onClick={() => setTargetReloadRevision((revision) => revision + 1)}>
                Reload target Head
              </Button>
            ) : null}
          </div>
        ) : (
          <div className="grid gap-4 p-5">
            <fieldset className="grid gap-2">
              <legend className="mb-1 text-xs font-semibold uppercase text-muted-foreground">Contents</legend>
              <div className="overflow-hidden rounded-md border border-border bg-background">
                <ContentOption
                  icon={GitBranch}
                  label="Graph"
                  description="Topology and state bindings"
                  checked
                  disabled
                  required
                />
                <ContentOption
                  icon={Braces}
                  label="Config"
                  description="Node and edge configuration"
                  checked={includeConfig}
                  onChange={setIncludeConfig}
                />
                <ContentOption
                  icon={Settings}
                  label="Settings"
                  description="Models, environment, and memory"
                  checked={includeSettings}
                  onChange={setIncludeSettings}
                />
                <ContentOption
                  icon={Zap}
                  label="Triggers"
                  description="Webhook, schedule, and chat configuration"
                  checked={includeTriggers}
                  onChange={setIncludeTriggers}
                />
                <ContentOption
                  icon={LayoutDashboard}
                  label="UI information"
                  description="Canvas layout and editor metadata"
                  checked={includeUI}
                  onChange={setIncludeUI}
                />
              </div>
            </fieldset>

          </div>
        )}

        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          {mode === "import" ? (
            <Button onClick={() => void importGraph()} disabled={!parsedImport || readingFile || importing || targetLoading || (overwriteExisting && !overwriteTarget)}>
              {importing ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <FileUp className="h-4 w-4" />}
              {importing ? "Importing…" : "Import graph"}
            </Button>
          ) : (
            <Button onClick={exportGraph} disabled={!definition}>
              <Download className="h-4 w-4" />
              Export JSON
            </Button>
          )}
        </div>
      </div>
    </WorkbenchDialogOverlay>
  );
}

function ContentOption({
  icon: Icon,
  label,
  description,
  checked,
  disabled = false,
  required = false,
  onChange,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  description: string;
  checked: boolean;
  disabled?: boolean;
  required?: boolean;
  onChange?: (checked: boolean) => void;
}) {
  return (
    <label
      className={`grid min-h-16 grid-cols-[20px_32px_minmax(0,1fr)_20px] items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0 ${
        disabled ? "cursor-default" : "cursor-pointer hover:bg-accent/60"
      } ${
        checked ? "bg-primary/[0.04]" : "bg-background"
      }`}
    >
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange?.(event.target.checked)}
        className="h-4 w-4 accent-primary"
      />
      <Icon className={`h-4 w-4 ${checked ? "text-foreground" : "text-muted-foreground"}`} />
      <span className="grid min-w-0 gap-0.5">
        <span className="text-sm font-medium text-foreground">{label}</span>
        <span className="truncate text-xs text-muted-foreground">{description}</span>
      </span>
      {required ? <LockKeyhole className="h-3.5 w-3.5 text-muted-foreground" aria-label="Required" /> : <span />}
    </label>
  );
}

function TransferDetail({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate text-right font-medium" title={value}>{value}</span>
    </div>
  );
}

function contentLabel(contents: ParsedGraphImport["contents"]): string {
  const labels = {
    graph: "Graph",
    config: "Config",
    settings: "Settings",
    triggers: "Triggers",
    ui: "UI",
  };
  return contents.map((content) => labels[content]).join(" + ");
}
