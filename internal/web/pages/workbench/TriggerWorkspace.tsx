import { useCallback, useEffect, useMemo, useState } from "react";
import { Clock3, History, Pencil, Plus, RefreshCw, Trash2, Webhook, X } from "lucide-react";
import {
  createTrigger,
  deleteTrigger,
  getGraphInfo,
  listGraphs,
  listTriggers,
  listTriggerRecords,
  updateTrigger,
} from "../../api";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import type {
  CachedGraphSummary,
  GraphInfo,
  Trigger,
  TriggerTarget,
  TriggerRecord,
  TriggerType,
  WebhookStateMapping,
} from "../../types";
import { PanelHeader, StatusText, type StatusTone } from "./shared";

interface TargetOption {
  key: string;
  label: string;
  target: TriggerTarget;
}

const emptyMapping = (): WebhookStateMapping => ({ parameter: "", state_path: "" });

export function TriggerWorkspace() {
  const [items, setItems] = useState<Trigger[]>([]);
  const [records, setRecords] = useState<TriggerRecord[]>([]);
  const [view, setView] = useState<"triggers" | "records">("triggers");
  const [recordsBusy, setRecordsBusy] = useState(false);
  const [currentGraph, setCurrentGraph] = useState<GraphInfo | null>(null);
  const [cachedGraphs, setCachedGraphs] = useState<CachedGraphSummary[]>([]);
  const [editing, setEditing] = useState<Trigger | null>(null);
  const [type, setType] = useState<TriggerType>("webhook");
  const [name, setName] = useState("");
  const [id, setID] = useState("");
  const [target, setTarget] = useState<TriggerTarget>({ graph_id: "" });
  const [secret, setSecret] = useState("");
  const [signatureHeader, setSignatureHeader] = useState("");
  const [mappings, setMappings] = useState<WebhookStateMapping[]>([]);
  const [cron, setCron] = useState("*/5 * * * *");
  const [timezone, setTimezone] = useState("UTC");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const targetOptions = useMemo(
    () => buildTargetOptions(currentGraph, cachedGraphs, target),
    [cachedGraphs, currentGraph, target]
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
    void Promise.allSettled([getGraphInfo(), listGraphs()]).then(([current, cached]) => {
      if (current.status === "fulfilled") setCurrentGraph(current.value);
      if (cached.status === "fulfilled" && Array.isArray(cached.value)) setCachedGraphs(cached.value);
    });
  }, [refresh, refreshRecords]);

  useEffect(() => {
    if (view !== "records") return;
    const interval = window.setInterval(() => void refreshRecords(false), 5_000);
    return () => window.clearInterval(interval);
  }, [refreshRecords, view]);

  useEffect(() => {
    if (!targetKey(target) && targetOptions.length > 0) {
      setTarget(targetOptions[0].target);
    }
  }, [target, targetOptions]);

  function resetForm() {
    setEditing(null);
    setType("webhook");
    setName("");
    setID("");
    setTarget(defaultTarget(currentGraph, cachedGraphs));
    setSecret("");
    setSignatureHeader("");
    setMappings([]);
    setCron("*/5 * * * *");
    setTimezone("UTC");
    setError("");
  }

  function edit(item: Trigger) {
    setEditing(item);
    setType(item.type);
    setName(item.name || "");
    setID(item.id);
    setTarget(item.target || defaultTarget(currentGraph, cachedGraphs));
    setSecret("");
    setSignatureHeader(item.webhook?.signature_header || "");
    setMappings((item.webhook?.state_mappings || []).map((mapping) => ({ ...mapping })));
    setCron(item.schedule?.cron || "*/5 * * * *");
    setTimezone(item.schedule?.timezone || "UTC");
    setError("");
  }

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const input: Record<string, unknown> = {
        id: editing ? undefined : id.trim() || undefined,
        name: name.trim() || undefined,
        type,
        enabled: editing?.enabled ?? true,
        concurrency: editing?.concurrency,
        target: targetKey(target) ? target : undefined,
      };
      if (type === "webhook") {
        input.webhook = {
          secret: secret || undefined,
          signature_header: signatureHeader.trim() || undefined,
          state_mappings: mappings
            .filter((mapping) => mapping.parameter.trim() || mapping.state_path.trim())
            .map((mapping) => ({
              parameter: mapping.parameter.trim(),
              state_path: mapping.state_path.trim(),
            })),
        };
      } else {
        input.schedule = {
          cron: cron.trim(),
          timezone: timezone.trim() || undefined,
          input: editing?.schedule?.input,
        };
      }
      if (editing) {
        await updateTrigger(editing.id, input);
      } else {
        await createTrigger(input);
      }
      resetForm();
      await refresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
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

  function updateMapping(index: number, field: keyof WebhookStateMapping, value: string) {
    setMappings((current) =>
      current.map((mapping, mappingIndex) =>
        mappingIndex === index ? { ...mapping, [field]: value } : mapping
      )
    );
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-1 overflow-auto bg-background lg:grid-cols-[440px_minmax(0,1fr)] lg:overflow-hidden">
      <section className="border-b border-border bg-panel p-4 lg:min-h-0 lg:overflow-auto lg:border-b-0 lg:border-r">
        <div className="flex items-center">
          <PanelHeader icon={editing ? Pencil : Webhook} title={editing ? "Edit Trigger" : "New Trigger"} inline />
          {editing ? (
            <Button className="ml-auto" variant="ghost" size="sm" onClick={resetForm} disabled={busy}>
              <X className="h-4 w-4" />
              Cancel
            </Button>
          ) : null}
        </div>
        <div className="mt-4 grid gap-3">
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Type</span>
            <Select value={type} onChange={(event) => setType(event.target.value as TriggerType)} disabled={Boolean(editing)}>
              <option value="webhook">Webhook</option>
              <option value="schedule">Schedule</option>
            </Select>
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">ID {editing ? "" : "(optional)"}</span>
            <Input value={id} onChange={(event) => setID(event.target.value)} placeholder="deploy-hook" disabled={Boolean(editing)} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Name</span>
            <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Deploy webhook" />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Graph</span>
            <Select
              value={targetKey(target)}
              onChange={(event) => {
                const selected = targetOptions.find((option) => option.key === event.target.value);
                if (selected) setTarget(selected.target);
              }}
              disabled={targetOptions.length === 0}
            >
              {targetOptions.length === 0 ? <option value="">No graph available</option> : null}
              {targetOptions.map((option) => (
                <option key={option.key} value={option.key}>{option.label}</option>
              ))}
            </Select>
          </label>
          {type === "webhook" ? (
            <>
              <label className="grid gap-1 text-sm">
                <span className="text-xs font-medium text-muted-foreground">Signing secret</span>
                <Input
                  type="password"
                  value={secret}
                  onChange={(event) => setSecret(event.target.value)}
                  placeholder={editing ? "Unchanged" : "Optional"}
                />
              </label>
              <label className="grid gap-1 text-sm">
                <span className="text-xs font-medium text-muted-foreground">Signature header</span>
                <Input
                  value={signatureHeader}
                  onChange={(event) => setSignatureHeader(event.target.value)}
                  placeholder="X-Webhook-Signature"
                />
              </label>
              <div className="grid gap-2 rounded-md border border-border p-3">
                <div className="flex items-center gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="text-xs font-medium">State mappings</div>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => setMappings((current) => [...current, emptyMapping()])}>
                    <Plus className="h-4 w-4" />
                    Add
                  </Button>
                </div>
                {mappings.length === 0 ? (
                  <div className="rounded border border-dashed border-border p-3 text-xs text-muted-foreground">No additional state mappings.</div>
                ) : null}
                {mappings.map((mapping, index) => (
                  <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] gap-2">
                    <Input
                      value={mapping.parameter}
                      onChange={(event) => updateMapping(index, "parameter", event.target.value)}
                      placeholder="user.id"
                      aria-label={`Webhook parameter ${index + 1}`}
                      title="Webhook parameter: dotted path or $"
                    />
                    <Input
                      value={mapping.state_path}
                      onChange={(event) => updateMapping(index, "state_path", event.target.value)}
                      placeholder="shared.user.id"
                      aria-label={`State path ${index + 1}`}
                      title="State path under shared or scopes"
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => setMappings((current) => current.filter((_, mappingIndex) => mappingIndex !== index))}
                      title="Remove mapping"
                      aria-label={`Remove mapping ${index + 1}`}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
              </div>
            </>
          ) : (
            <>
              <label className="grid gap-1 text-sm">
                <span className="text-xs font-medium text-muted-foreground">Cron</span>
                <Input value={cron} onChange={(event) => setCron(event.target.value)} />
              </label>
              <label className="grid gap-1 text-sm">
                <span className="text-xs font-medium text-muted-foreground">Timezone</span>
                <Input value={timezone} onChange={(event) => setTimezone(event.target.value)} />
              </label>
            </>
          )}
          {error ? <div className="rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">{error}</div> : null}
          <Button onClick={() => void submit()} disabled={busy || !targetKey(target)}>
            {busy ? (editing ? "Saving..." : "Creating...") : (editing ? "Save changes" : "Create trigger")}
          </Button>
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
                {item.type === "webhook" ? <Webhook className="h-4 w-4 text-muted-foreground" /> : <Clock3 className="h-4 w-4 text-muted-foreground" />}
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{item.name || item.id}</div>
                  <div className="truncate font-mono text-xs text-muted-foreground">{item.id}</div>
                </div>
                <StatusText tone={item.enabled ? "live" : "neutral"}>{item.enabled ? "Enabled" : "Disabled"}</StatusText>
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
                    <code className="truncate">POST /triggers/{item.id}</code>
                    <code className="truncate">GET /triggers/{item.id}/webhook</code>
                  </div>
                ) : (
                  <span>{item.schedule?.cron} ({item.schedule?.timezone || "UTC"})</span>
                )}
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  <span>Graph: {targetLabel(item.target)}</span>
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
                  {record.trigger_type === "webhook" ? <Webhook className="mt-0.5 h-4 w-4 text-muted-foreground" /> : <Clock3 className="mt-0.5 h-4 w-4 text-muted-foreground" />}
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

function buildTargetOptions(
  current: GraphInfo | null,
  cached: CachedGraphSummary[],
  preserved: TriggerTarget
): TargetOption[] {
  const result: TargetOption[] = [];
  const keys = new Set<string>();
  const add = (label: string, target: TriggerTarget) => {
    const key = targetKey(target);
    if (!key || keys.has(key)) return;
    keys.add(key);
    result.push({ key, label, target });
  };
  if (current) {
    add(`${current.id} (current)`, { graph_id: current.id });
  }
  for (const graph of Array.isArray(cached) ? cached : []) {
    if (!graph.latest_session) continue;
    add(graph.id, { graph_id: graph.id });
  }
  if (targetKey(preserved)) {
    add(preserved.graph_id, preserved);
  }
  return result;
}

function defaultTarget(current: GraphInfo | null, cached: CachedGraphSummary[]): TriggerTarget {
  return buildTargetOptions(current, cached, { graph_id: "" })[0]?.target || { graph_id: "" };
}

function targetKey(target?: TriggerTarget): string {
  return target?.graph_id?.trim() || "";
}

function targetLabel(target?: TriggerTarget): string {
  return target?.graph_id || "server default";
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
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
