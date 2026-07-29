import { useEffect, useId, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { createTrigger, deleteTrigger, updateTrigger } from "../../api";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import type { Trigger, TriggerTarget, WebhookStateMapping } from "../../types";
import {
  buildTriggerPayload,
  triggerEditorValues,
  triggerTargetKey,
  type TriggerEditorValues,
  type TriggerInitialStateEntry,
  type TriggerTargetOption,
} from "./triggerEditor";

const emptyMapping = (): WebhookStateMapping => ({ parameter: "", state_path: "" });
const emptyInitialStateEntry = (): TriggerInitialStateEntry => ({ path: "", value: "" });

export function TriggerEditorForm({
  trigger,
  fallbackTarget,
  targetOptions,
  targetLocked = false,
  statePathSuggestions = [],
  showIdentityFields = true,
  showTargetField = true,
  allowDelete = false,
  onSaved,
  onDeleted,
}: {
  trigger: Trigger | null;
  fallbackTarget: TriggerTarget;
  targetOptions: TriggerTargetOption[];
  targetLocked?: boolean;
  statePathSuggestions?: string[];
  showIdentityFields?: boolean;
  showTargetField?: boolean;
  allowDelete?: boolean;
  onSaved: (trigger: Trigger) => void | Promise<void>;
  onDeleted?: (trigger: Trigger) => void | Promise<void>;
}) {
  const statePathListID = useId();
  const [values, setValues] = useState<TriggerEditorValues>(() => triggerEditorValues(trigger, fallbackTarget));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setValues(triggerEditorValues(trigger, fallbackTarget));
    setError("");
  }, [fallbackTarget.graph_id, trigger?.id, trigger?.updated_at]);

  function change<Key extends keyof TriggerEditorValues>(key: Key, value: TriggerEditorValues[Key]) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function updateMapping(index: number, field: keyof WebhookStateMapping, value: string) {
    change(
      "mappings",
      values.mappings.map((mapping, mappingIndex) =>
        mappingIndex === index ? { ...mapping, [field]: value } : mapping
      )
    );
  }

  function updateInitialStateEntry(index: number, field: keyof TriggerInitialStateEntry, value: string) {
    change(
      "initialStateEntries",
      values.initialStateEntries.map((entry, entryIndex) =>
        entryIndex === index ? { ...entry, [field]: value } : entry
      )
    );
  }

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const payload = buildTriggerPayload(values, trigger);
      const saved = trigger
        ? await updateTrigger(trigger.id, payload)
        : await createTrigger(payload);
      await onSaved(saved);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!trigger || !window.confirm(`Delete trigger ${trigger.id}?`)) return;
    setBusy(true);
    setError("");
    try {
      await deleteTrigger(trigger.id);
      await onDeleted?.(trigger);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid gap-3">
      {showIdentityFields ? (
        <>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Type</span>
            <Select value={values.type} onChange={(event) => change("type", event.target.value as TriggerEditorValues["type"])} disabled={Boolean(trigger)}>
              <option value="webhook">Webhook</option>
              <option value="schedule">Schedule</option>
            </Select>
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">ID {trigger ? "" : "(optional)"}</span>
            <Input value={values.id} onChange={(event) => change("id", event.target.value)} placeholder="deploy-hook" disabled={Boolean(trigger)} />
          </label>
        </>
      ) : null}
      <label className="grid gap-1 text-sm">
        <span className="text-xs font-medium text-muted-foreground">Name</span>
        <Input value={values.name} onChange={(event) => change("name", event.target.value)} placeholder="Deploy webhook" />
      </label>
      {showTargetField ? (
        <label className="grid gap-1 text-sm">
          <span className="text-xs font-medium text-muted-foreground">Graph</span>
          <Select
            value={triggerTargetKey(values.target)}
            onChange={(event) => {
              const selected = targetOptions.find((option) => option.key === event.target.value);
              if (selected) change("target", selected.target);
            }}
            disabled={targetLocked || targetOptions.length === 0}
          >
            {targetOptions.length === 0 ? <option value="">No graph available</option> : null}
            {targetOptions.map((option) => <option key={option.key} value={option.key}>{option.label}</option>)}
          </Select>
        </label>
      ) : null}
      <div className="grid grid-cols-2 gap-2">
        <div className="grid gap-1 text-sm">
          <span className="text-xs font-medium text-muted-foreground">Status</span>
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <input type="checkbox" checked={values.enabled} onChange={(event) => change("enabled", event.target.checked)} />
            Enabled
          </label>
        </div>
        <label className="grid gap-1 text-sm">
          <span className="text-xs font-medium text-muted-foreground">Concurrency</span>
          <Select value={values.concurrency} onChange={(event) => change("concurrency", event.target.value as TriggerEditorValues["concurrency"])}>
            <option value="parallel">Parallel</option>
            <option value="skip">Skip while running</option>
          </Select>
        </label>
      </div>
      <div className="grid gap-2 rounded-md border border-border p-3">
        <div className="flex items-center gap-2">
          <div className="min-w-0 flex-1 text-xs font-medium">Initial state</div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => change("initialStateEntries", [...values.initialStateEntries, emptyInitialStateEntry()])}
          >
            <Plus className="h-4 w-4" /> Add
          </Button>
        </div>
        {values.initialStateEntries.length === 0 ? (
          <div className="rounded border border-dashed border-border p-3 text-xs text-muted-foreground">No initial state values.</div>
        ) : null}
        {values.initialStateEntries.map((entry, index) => (
          <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] gap-2">
            <Input
              list={statePathSuggestions.length > 0 ? statePathListID : undefined}
              value={entry.path}
              onChange={(event) => updateInitialStateEntry(index, "path", event.target.value)}
              placeholder={statePathSuggestions[0] ?? "shared.path"}
              aria-label={`Initial state path ${index + 1}`}
            />
            <Input
              value={entry.value}
              onChange={(event) => updateInitialStateEntry(index, "value", event.target.value)}
              placeholder="value"
              aria-label={`Initial state value ${index + 1}`}
            />
            <Button
              variant="ghost"
              size="icon"
              onClick={() => change("initialStateEntries", values.initialStateEntries.filter((_, entryIndex) => entryIndex !== index))}
              title="Remove initial state value"
              aria-label={`Remove initial state value ${index + 1}`}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        ))}
        <div className="text-[11px] text-muted-foreground">
          Paths support shared and scopes. Boolean, number, and null values are typed; other values are stored as text.
        </div>
      </div>
      {values.type === "webhook" ? (
        <>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">API key</span>
            <Input type="password" value={values.apiKey} onChange={(event) => change("apiKey", event.target.value)} placeholder={trigger ? "Unchanged" : "Optional"} />
          </label>
          <div className="grid gap-2 rounded-md border border-border p-3">
            <div className="flex items-center gap-2">
              <div className="min-w-0 flex-1 text-xs font-medium">State mappings</div>
              <Button variant="outline" size="sm" onClick={() => change("mappings", [...values.mappings, emptyMapping()])}>
                <Plus className="h-4 w-4" /> Add
              </Button>
            </div>
            {values.mappings.length === 0 ? <div className="rounded border border-dashed border-border p-3 text-xs text-muted-foreground">No additional state mappings.</div> : null}
            {values.mappings.map((mapping, index) => (
              <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] gap-2">
                <Input value={mapping.parameter} onChange={(event) => updateMapping(index, "parameter", event.target.value)} placeholder="user.id" aria-label={`Webhook parameter ${index + 1}`} />
                <Input
                  list={statePathSuggestions.length > 0 ? statePathListID : undefined}
                  value={mapping.state_path}
                  onChange={(event) => updateMapping(index, "state_path", event.target.value)}
                  placeholder={statePathSuggestions[0] ?? "shared.user.id"}
                  aria-label={`State path ${index + 1}`}
                />
                <Button variant="ghost" size="icon" onClick={() => change("mappings", values.mappings.filter((_, mappingIndex) => mappingIndex !== index))} title="Remove mapping" aria-label={`Remove mapping ${index + 1}`}>
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
            <Input value={values.cron} onChange={(event) => change("cron", event.target.value)} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Timezone</span>
            <Input value={values.timezone} onChange={(event) => change("timezone", event.target.value)} />
          </label>
        </>
      )}
      {statePathSuggestions.length > 0 ? (
        <datalist id={statePathListID}>
          {statePathSuggestions.map((path) => <option key={path} value={path} />)}
        </datalist>
      ) : null}
      {error ? <div className="rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">{error}</div> : null}
      <div className="flex gap-2">
        <Button className="flex-1" onClick={() => void submit()} disabled={busy || !triggerTargetKey(values.target)}>
          {busy ? (trigger ? "Saving..." : "Creating...") : (trigger ? "Save changes" : "Create trigger")}
        </Button>
        {allowDelete && trigger ? (
          <Button variant="destructive" size="icon" onClick={() => void remove()} disabled={busy} title="Delete trigger" aria-label="Delete trigger">
            <Trash2 className="h-4 w-4" />
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
