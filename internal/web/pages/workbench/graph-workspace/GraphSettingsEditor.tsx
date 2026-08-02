import { useEffect, useState } from "react";
import { Braces, Plus, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import type { RuntimeSettings, RuntimeSettingsUpdate } from "../../../types";
import { Field } from "./shared";
import {
  MODEL_API_KEY_MASK,
  environmentRowsFromSettings,
  modelAPIKeyDisplayValue,
  modelsFromSettings,
  nextModelID,
  normalizeEnvironmentSettings,
  normalizeModelSettings,
} from "./graphSettingsEditorModel";
import type { EditableEnvironmentVariable, EditableGraphModel } from "./graphSettingsEditorModel";

export function RuntimeSettingsEditor({
  settings,
  onUpdateRuntimeSettings,
}: {
  settings: RuntimeSettings | null;
  onUpdateRuntimeSettings: (settings: RuntimeSettingsUpdate) => Promise<RuntimeSettings>;
}) {
  const [models, setModels] = useState<EditableGraphModel[]>([]);
  const [memoryEnabled, setMemoryEnabled] = useState(false);
  const [memoryDirectory, setMemoryDirectory] = useState("");
  const [environmentRows, setEnvironmentRows] = useState<EditableEnvironmentVariable[]>([]);
  const [environmentPresetKey, setEnvironmentPresetKey] = useState("");
  const [newEnvironmentKey, setNewEnvironmentKey] = useState("");
  const [newEnvironmentValue, setNewEnvironmentValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState("");

  useEffect(() => {
    setModels(modelsFromSettings(settings));
    setMemoryEnabled(settings?.memory.enabled ?? false);
    setMemoryDirectory(settings?.memory.directory ?? "");
    setEnvironmentRows(environmentRowsFromSettings(settings));
    setEnvironmentPresetKey("");
    setNewEnvironmentKey("");
    setNewEnvironmentValue("");
    setStatus("");
  }, [settings]);

  function updateModel(index: number, update: Partial<EditableGraphModel>) {
    setModels((current) => current.map((model, modelIndex) => (modelIndex === index ? { ...model, ...update } : model)));
  }

  function addModel() {
    setModels((current) => [
      ...current,
      {
        id: nextModelID(current),
        enabled: true,
        provider: "openai",
        model: "",
        base_url: "",
        api_key: "",
        api_key_configured: false,
      },
    ]);
  }

  function updateEnvironment(index: number, update: Partial<EditableEnvironmentVariable>) {
    setEnvironmentRows((current) => current.map((row, rowIndex) => (rowIndex === index ? { ...row, ...update } : row)));
  }

  function removeEnvironment(index: number) {
    setEnvironmentRows((current) => current.filter((_, rowIndex) => rowIndex !== index));
  }

  function addEnvironmentPreset() {
    const preset = settings?.environment_presets?.find((item) => item.key === environmentPresetKey);
    if (!preset || environmentRows.some((row) => row.key.trim() === preset.key)) return;
    setEnvironmentRows((current) => [...current, { key: preset.key, value: preset.default_value }]);
    setEnvironmentPresetKey("");
    setStatus("");
  }

  function addEnvironment() {
    const key = newEnvironmentKey.trim();
    if (!key) {
      setStatus("Environment key is required.");
      return;
    }
    if (environmentRows.some((row) => row.key.trim() === key)) {
      setStatus(`Duplicate environment key: ${key}`);
      return;
    }
    setEnvironmentRows((current) => [...current, { key, value: newEnvironmentValue }]);
    setNewEnvironmentKey("");
    setNewEnvironmentValue("");
    setStatus("");
  }

  function removeModel(index: number) {
    setModels((current) => current.filter((_, modelIndex) => modelIndex !== index));
  }

  async function save() {
    let environment: Record<string, string>;
    let modelUpdates: RuntimeSettingsUpdate["models"];
    try {
      environment = normalizeEnvironmentSettings(environmentRows);
      modelUpdates = normalizeModelSettings(models);
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
      return;
    }

    setSaving(true);
    setStatus("");
    try {
      await onUpdateRuntimeSettings({
        environment,
        models: modelUpdates,
        memory: {
          enabled: memoryEnabled,
          directory: memoryDirectory.trim(),
        },
      });
      setModels((current) => current.map((model) => ({ ...model, api_key: modelAPIKeyDisplayValue(model) })));
      setStatus("Settings saved.");
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  const availableEnvironmentPresets = (settings?.environment_presets ?? []).filter(
    (preset) => !environmentRows.some((row) => row.key.trim() === preset.key)
  );

  return (
    <div className="grid gap-3">
      <div className="grid gap-2 rounded-md border border-border bg-muted/30 p-2">
        <div className="flex min-h-8 items-center gap-2">
          <span className="text-sm font-medium">Models</span>
          <Button type="button" variant="outline" size="sm" className="ml-auto" onClick={addModel}>
            <Plus className="h-4 w-4" />
            Add model
          </Button>
        </div>

        {models.length === 0 ? (
          <div className="rounded-md border border-dashed border-border bg-background/60 p-3 text-xs text-muted-foreground">
            No models configured.
          </div>
        ) : (
          <div className="grid gap-2">
            {models.map((model, index) => (
              <div key={`${model.id || "model"}-${index}`} className="grid gap-2 rounded-md border border-border bg-background p-2">
                <div className="flex min-h-8 items-center gap-2">
                  <input
                    type="checkbox"
                    checked={model.enabled}
                    onChange={(event) => updateModel(index, { enabled: event.target.checked })}
                    className="h-4 w-4"
                    aria-label="Enable model"
                  />
                  <Input
                    value={model.id}
                    onChange={(event) => updateModel(index, { id: event.target.value })}
                    placeholder={index === 0 ? "default" : "model-id"}
                    className="h-8 min-w-0 flex-1 font-mono text-xs"
                  />
                  <Button type="button" variant="ghost" size="icon" className="h-8 w-8" onClick={() => removeModel(index)} aria-label="Remove model">
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>

                <div className="grid gap-2 sm:grid-cols-2">
                  <Field label="Provider">
                    <Select value={model.provider} onChange={(event) => updateModel(index, { provider: event.target.value })}>
                      <option value="openai">OpenAI</option>
                    </Select>
                  </Field>
                  <Field label="Model name">
                    <Input value={model.model} onChange={(event) => updateModel(index, { model: event.target.value })} placeholder="gpt-5" />
                  </Field>
                  <Field label="Base URL">
                    <Input value={model.base_url} onChange={(event) => updateModel(index, { base_url: event.target.value })} placeholder="https://api.openai.com/v1" />
                  </Field>
                  <Field label="API key">
                    <Input type={model.api_key.trim() === MODEL_API_KEY_MASK ? "text" : "password"} value={model.api_key} onChange={(event) => updateModel(index, { api_key: event.target.value })} />
                  </Field>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="grid gap-2 rounded-md border border-border bg-muted/30 p-2">
        <label className="flex min-h-8 items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={memoryEnabled}
            onChange={(event) => setMemoryEnabled(event.target.checked)}
            className="h-4 w-4"
          />
          <span>Memory</span>
        </label>
        <Field label="Directory">
          <Input value={memoryDirectory} onChange={(event) => setMemoryDirectory(event.target.value)} />
        </Field>
      </div>

      <div className="grid gap-2 rounded-md border border-border bg-muted/30 p-2">
        <div className="flex min-h-8 items-center gap-2">
          <span className="text-sm font-medium">Environment</span>
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <Select
            value={environmentPresetKey}
            onChange={(event) => setEnvironmentPresetKey(event.target.value)}
            disabled={availableEnvironmentPresets.length === 0}
          >
            <option value="">{availableEnvironmentPresets.length === 0 ? "No presets available" : "Select preset"}</option>
            {availableEnvironmentPresets.map((preset) => (
              <option key={preset.key} value={preset.key}>
                {preset.key}
              </option>
            ))}
          </Select>
          <Button type="button" variant="outline" size="sm" onClick={addEnvironmentPreset} disabled={!environmentPresetKey}>
            <Plus className="h-4 w-4" />
            Add preset
          </Button>
        </div>

        {environmentRows.length === 0 ? (
          <div className="rounded-md border border-dashed border-border bg-background/60 p-3 text-xs text-muted-foreground">
            No environment variables configured.
          </div>
        ) : (
          <div className="grid gap-2">
            {environmentRows.map((row, index) => (
              <div key={`${row.key || "environment"}-${index}`} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] gap-2">
                <Input value={row.key} onChange={(event) => updateEnvironment(index, { key: event.target.value })} placeholder="KEY" className="font-mono text-xs" />
                <Input value={row.value} onChange={(event) => updateEnvironment(index, { value: event.target.value })} placeholder="value" className="font-mono text-xs" />
                <Button type="button" variant="ghost" size="icon" className="h-9 w-8" onClick={() => removeEnvironment(index)} aria-label="Remove environment variable">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
          </div>
        )}

        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
          <Input value={newEnvironmentKey} onChange={(event) => setNewEnvironmentKey(event.target.value)} placeholder="KEY" className="font-mono text-xs" />
          <Input value={newEnvironmentValue} onChange={(event) => setNewEnvironmentValue(event.target.value)} placeholder="value" className="font-mono text-xs" />
          <Button type="button" variant="outline" size="sm" onClick={addEnvironment} disabled={!newEnvironmentKey.trim()}>
            <Plus className="h-4 w-4" />
            Add variable
          </Button>
        </div>
      </div>

      {status ? <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">{status}</div> : null}
      <Button variant="outline" size="sm" onClick={() => void save()} disabled={saving}>
        <Braces className="h-4 w-4" />
        {saving ? "Saving..." : "Save settings"}
      </Button>
    </div>
  );
}
