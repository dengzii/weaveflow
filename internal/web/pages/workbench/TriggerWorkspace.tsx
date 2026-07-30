import { useCallback, useEffect, useMemo, useState } from "react";
import { Clock3, History, MessageCircle, Pencil, Power, RefreshCw, Trash2, Webhook, X } from "lucide-react";
import {
  deleteTrigger,
  getGraphInfo,
  getRegistry,
  listGraphs,
  listTriggers,
  listTriggerRecords,
  updateTrigger,
} from "../../api";
import { Button } from "../../components/ui/button";
import type {
  CachedGraphSummary,
  ChatChannelDefinition,
  GraphInfo,
  Trigger,
  TriggerRecord,
} from "../../types";
import { PanelHeader, StatusText, type StatusTone } from "./shared";
import { TriggerEditorForm } from "./TriggerEditorForm";
import {
  buildTriggerPayload,
  buildTriggerTargetOptions,
  chatTriggerURL,
  defaultTriggerTarget,
  triggerEditorValues,
  triggerTargetLabel,
  webhookTriggerURLs,
} from "./triggerEditor";

export function TriggerWorkspace() {
  const [items, setItems] = useState<Trigger[]>([]);
  const [records, setRecords] = useState<TriggerRecord[]>([]);
  const [view, setView] = useState<"triggers" | "records">("triggers");
  const [recordsBusy, setRecordsBusy] = useState(false);
  const [currentGraph, setCurrentGraph] = useState<GraphInfo | null>(null);
  const [cachedGraphs, setCachedGraphs] = useState<CachedGraphSummary[]>([]);
  const [chatChannels, setChatChannels] = useState<ChatChannelDefinition[]>([]);
  const [editing, setEditing] = useState<Trigger | null>(null);
  const [error, setError] = useState("");

  const fallbackTarget = editing?.target ?? defaultTriggerTarget(currentGraph, cachedGraphs);

  const targetOptions = useMemo(
    () => buildTriggerTargetOptions(currentGraph, cachedGraphs, fallbackTarget),
    [cachedGraphs, currentGraph, fallbackTarget.graph_id]
  );

  const triggerNames = useMemo(
    () => new Map(items.map((item) => [item.id, item.name || item.id])),
    [items]
  );

  const refresh = useCallback(async () => {
    try {
      setItems(await listTriggers());
      setError("");
    } catch (err) {
      setError(errorMessage(err));
    }
  }, []);

  const refreshRecords = useCallback(async (reportError = true) => {
    setRecordsBusy(true);
    try {
      setRecords(await listTriggerRecords(undefined, 100));
    } catch (err) {
      if (reportError) setError(errorMessage(err));
    } finally {
      setRecordsBusy(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    void refreshRecords();
    void Promise.allSettled([getGraphInfo(), listGraphs(), getRegistry()]).then(([current, cached, registry]) => {
      if (current.status === "fulfilled") setCurrentGraph(current.value);
      if (cached.status === "fulfilled" && Array.isArray(cached.value)) setCachedGraphs(cached.value);
      if (registry.status === "fulfilled") setChatChannels(registry.value.chat_channels ?? []);
    });
  }, [refresh, refreshRecords]);

  useEffect(() => {
    if (view !== "records") return;
    const interval = window.setInterval(() => void refreshRecords(false), 5_000);
    return () => window.clearInterval(interval);
  }, [refreshRecords, view]);

  function resetForm() {
    setEditing(null);
    setError("");
  }

  function edit(item: Trigger) {
    setEditing(item);
    setError("");
  }

  async function remove(item: Trigger) {
    if (!window.confirm(`Delete trigger ${item.id}?`)) return;
    try {
      await deleteTrigger(item.id);
      if (editing?.id === item.id) resetForm();
      await refresh();
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  async function toggleEnabled(item: Trigger) {
    try {
      const values = triggerEditorValues(item, item.target ?? fallbackTarget);
      values.enabled = !item.enabled;
      const saved = await updateTrigger(item.id, buildTriggerPayload(values, item));
      if (editing?.id === item.id) setEditing(saved);
      await refresh();
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-1 overflow-auto bg-background lg:grid-cols-[440px_minmax(0,1fr)] lg:overflow-hidden">
      <section className="border-b border-border bg-panel p-4 lg:min-h-0 lg:overflow-auto lg:border-b-0 lg:border-r">
        <div className="flex items-center">
          <PanelHeader icon={editing ? Pencil : Webhook} title={editing ? "Edit Trigger" : "New Trigger"} inline />
          {editing ? (
            <Button className="ml-auto" variant="ghost" size="sm" onClick={resetForm}>
              <X className="h-4 w-4" />
              Cancel
            </Button>
          ) : null}
        </div>
        <div className="mt-4">
          <TriggerEditorForm
            trigger={editing}
            fallbackTarget={fallbackTarget}
            targetOptions={targetOptions}
            chatChannels={chatChannels}
            onSaved={async () => {
              resetForm();
              await refresh();
            }}
          />
          {error ? <div className="mt-3 rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">{error}</div> : null}
        </div>
      </section>
      <section className="min-h-0 p-4 lg:overflow-auto">
        <div className="flex items-center gap-2">
          <PanelHeader icon={view === "records" ? History : Clock3} title={view === "records" ? "Trigger Records" : "Configured Triggers"} inline />
          {view === "records" ? (
            <Button
              className="ml-auto"
              variant="ghost"
              size="icon"
              onClick={() => void refreshRecords()}
              disabled={recordsBusy}
              title="Refresh trigger records"
              aria-label="Refresh trigger records"
            >
              <RefreshCw className={`h-4 w-4 ${recordsBusy ? "animate-spin" : ""}`} />
            </Button>
          ) : null}
        </div>
        <div className="flex h-10 items-end gap-5 border-b border-border px-3" role="tablist" aria-label="Trigger views">
          <button
            type="button"
            role="tab"
            aria-selected={view === "triggers"}
            className={`h-10 border-b-2 px-1 text-xs font-medium ${view === "triggers" ? "border-foreground text-foreground" : "border-transparent text-muted-foreground"}`}
            onClick={() => setView("triggers")}
          >
            Triggers <span className="ml-1 text-muted-foreground">{items.length}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={view === "records"}
            className={`h-10 border-b-2 px-1 text-xs font-medium ${view === "records" ? "border-foreground text-foreground" : "border-transparent text-muted-foreground"}`}
            onClick={() => setView("records")}
          >
            Records <span className="ml-1 text-muted-foreground">{records.length}</span>
          </button>
        </div>
        {view === "triggers" ? (
        <div className="mt-4 grid gap-3">
          {items.length === 0 ? <div className="rounded border border-dashed border-border p-6 text-sm text-muted-foreground">No triggers configured.</div> : null}
          {items.map((item) => (
            <div key={item.id} className="rounded-md border border-border bg-panel p-3">
              <div className="flex items-center gap-3">
                <TriggerTypeIcon type={item.type} className="h-4 w-4 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{item.name || item.id}</div>
                  <div className="truncate font-mono text-xs text-muted-foreground">{item.id}</div>
                </div>
                <StatusText tone={item.enabled ? "live" : "neutral"}>{item.enabled ? "Enabled" : "Disabled"}</StatusText>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => void toggleEnabled(item)}
                  title={item.enabled ? "Disable trigger" : "Enable trigger"}
                  aria-label={item.enabled ? "Disable trigger" : "Enable trigger"}
                  aria-pressed={item.enabled}
                >
                  <Power className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" onClick={() => edit(item)} title="Edit trigger">
                  <Pencil className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" onClick={() => void remove(item)} title="Delete trigger">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
              <div className="mt-3 grid gap-1 border-t border-border pt-2 text-xs text-muted-foreground">
                {item.type === "webhook" ? (
                  <div className="grid gap-0.5">
                    <code className="truncate">POST {webhookTriggerURLs(item.id).post}</code>
                    <code className="truncate">GET {webhookTriggerURLs(item.id).get}</code>
                  </div>
                ) : item.type === "schedule" ? (
                  <span>{item.schedule?.cron} ({item.schedule?.timezone || "UTC"})</span>
                ) : (
                  <div className="grid gap-0.5">
                    <code className="truncate">POST {chatTriggerURL(item.id)}</code>
                    <span>Channel: {item.chat?.channel || "http"}</span>
                    <span>Reply: {item.chat?.reply_path || "shared.final.answer"}</span>
                  </div>
                )}
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  <span>Graph: {triggerTargetLabel(item.target)}</span>
                  {item.type === "webhook" ? <span>Mappings: {item.webhook?.state_mappings?.length || 0}</span> : null}
                </div>
              </div>
            </div>
          ))}
        </div>
        ) : (
          <div className="mt-4 grid gap-3">
            {records.length === 0 ? (
              <div className="rounded border border-dashed border-border p-6 text-sm text-muted-foreground">No trigger records yet.</div>
            ) : null}
            {records.map((record) => (
              <div key={record.id} className="rounded-md border border-border bg-panel p-3">
                <div className="flex items-start gap-3">
                  <TriggerTypeIcon type={record.trigger_type} className="mt-0.5 h-4 w-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium">{triggerNames.get(record.trigger_id) || record.trigger_id}</div>
                    <div className="truncate font-mono text-xs text-muted-foreground">{record.trigger_id}</div>
                  </div>
                  <span className="shrink-0 text-xs text-muted-foreground">{formatTimestamp(record.triggered_at)}</span>
                  <StatusText tone={recordStatusTone(record.status)} className="shrink-0">{record.status}</StatusText>
                </div>
                <div className="mt-3 grid gap-1 border-t border-border pt-2 text-xs text-muted-foreground">
                  <div className="flex flex-wrap gap-x-4 gap-y-1">
                    <span>Graph: {record.target.graph_id}</span>
                    <span>Type: {record.trigger_type}</span>
                  </div>
                  <div className="truncate">Run: <code>{record.run?.run_id || "Not started"}</code></div>
                  {record.error_message ? <div className="break-words text-destructive">{record.error_message}</div> : null}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function TriggerTypeIcon({ type, className }: { type: Trigger["type"]; className?: string }) {
  const Icon = type === "webhook" ? Webhook : type === "schedule" ? Clock3 : MessageCircle;
  return <Icon className={className} />;
}

function recordStatusTone(status: string): StatusTone {
  switch (status) {
    case "completed":
      return "ok";
    case "failed":
    case "canceled":
      return "danger";
    case "paused":
      return "warn";
    case "running":
      return "live";
    default:
      return "neutral";
  }
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
