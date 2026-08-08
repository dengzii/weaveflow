import { useEffect, useRef, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input, SensitiveInput } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import type { RuntimeSettings, RuntimeSettingsUpdate } from "../../../types";
import { Field } from "./shared";
import {
  environmentRowsFromSettings,
  modelsFromSettings,
  nextModelID,
  normalizeEnvironmentSettings,
  normalizeModelSettings,
} from "./graphSettingsEditorModel";
import type { EditableEnvironmentVariable, EditableGraphModel } from "./graphSettingsEditorModel";

export function RuntimeSettingsEditor({
  settings,
  onChangeRuntimeSettings,
}: {
  settings: RuntimeSettings | null;
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
}) {
  const [models, setModels] = useState<EditableGraphModel[]>([]);
  const [memoryEnabled, setMemoryEnabled] = useState(false);
  const [memoryDirectory, setMemoryDirectory] = useState("");
  const [environmentRows, setEnvironmentRows] = useState<EditableEnvironmentVariable[]>([]);
  const [environmentPresetKey, setEnvironmentPresetKey] = useState("");
  const [newEnvironmentKey, setNewEnvironmentKey] = useState("");
  const [newEnvironmentValue, setNewEnvironmentValue] = useState("");
  const [status, setStatus] = useState("");
  const locallyAppliedSettingsRef = useRef<RuntimeSettings | null>(null);

  useEffect(() => {
    if (settings === locallyAppliedSettingsRef.current) {
      locallyAppliedSettingsRef.current = null;
      return;
    }
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
    const nextModels = models.map((model, modelIndex) => (modelIndex === index ? { ...model, ...update } : model));
    setModels(nextModels);
    publish(nextModels, memoryEnabled, memoryDirectory, environmentRows);
  }

  function addModel() {
    const nextModels = [
      ...models,
      {
        id: nextModelID(models),
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "",
        base_url: "",
        extra_body: "",
        api_key: "",
        api_key_configured: false,
      },
    ];
    setModels(nextModels);
    publish(nextModels, memoryEnabled, memoryDirectory, environmentRows);
  }

  function updateEnvironment(index: number, update: Partial<EditableEnvironmentVariable>) {
    const nextRows = environmentRows.map((row, rowIndex) => (rowIndex === index ? { ...row, ...update } : row));
    setEnvironmentRows(nextRows);
    publish(models, memoryEnabled, memoryDirectory, nextRows);
  }

  function removeEnvironment(index: number) {
    const nextRows = environmentRows.filter((_, rowIndex) => rowIndex !== index);
    setEnvironmentRows(nextRows);
    publish(models, memoryEnabled, memoryDirectory, nextRows);
  }

  function addEnvironmentPreset() {
    const preset = settings?.environment_presets?.find((item) => item.key === environmentPresetKey);
    if (!preset || environmentRows.some((row) => row.key.trim() === preset.key)) return;
    const nextRows = [...environmentRows, { key: preset.key, value: preset.default_value }];
    setEnvironmentRows(nextRows);
    setEnvironmentPresetKey("");
    publish(models, memoryEnabled, memoryDirectory, nextRows);
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
    const nextRows = [...environmentRows, { key, value: newEnvironmentValue }];
    setEnvironmentRows(nextRows);
    setNewEnvironmentKey("");
    setNewEnvironmentValue("");
    publish(models, memoryEnabled, memoryDirectory, nextRows);
  }

  function removeModel(index: number) {
    const nextModels = models.filter((_, modelIndex) => modelIndex !== index);
    setModels(nextModels);
    publish(nextModels, memoryEnabled, memoryDirectory, environmentRows);
  }

  function publish(
    nextModels: EditableGraphModel[],
    nextMemoryEnabled: boolean,
    nextMemoryDirectory: string,
    nextEnvironmentRows: EditableEnvironmentVariable[]
  ) {
    let environment: Record<string, string>;
    let modelUpdates: RuntimeSettingsUpdate["models"];
    try {
      environment = normalizeEnvironmentSettings(nextEnvironmentRows);
      modelUpdates = normalizeModelSettings(nextModels);
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
      return;
    }

    setStatus("");
    try {
      const next = onChangeRuntimeSettings({
        environment,
        models: modelUpdates,
        memory: {
          enabled: nextMemoryEnabled,
          directory: nextMemoryDirectory.trim(),
        },
      });
      locallyAppliedSettingsRef.current = next;
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
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
                      <option value="azure">Azure OpenAI</option>
                      <option value="deepseek">DeepSeek</option>
                      <option value="gemini">Gemini</option>
                      <option value="vllm">vLLM</option>
                      <option value="mistral">Mistral</option>
                      <option value="xai">xAI</option>
                      <option value="openrouter">OpenRouter</option>
                    </Select>
                  </Field>
                  <Field label="API format">
                    <Select value={model.api_format} onChange={(event) => updateModel(index, { api_format: event.target.value })}>
                      <option value="chat_completions">Chat Completions</option>
                      <option value="responses">Responses</option>
                    </Select>
                  </Field>
                  <Field label="Model name">
                    <Input value={model.model} onChange={(event) => updateModel(index, { model: event.target.value })} placeholder="gpt-5" />
                  </Field>
                  <Field label="Base URL">
                    <Input value={model.base_url} onChange={(event) => updateModel(index, { base_url: event.target.value })} placeholder="https://api.openai.com/v1" />
                  </Field>
                  <Field label="API key">
                    <SensitiveInput
                      value={model.api_key}
                      configured={model.api_key_configured}
                      onValueChange={(value) => updateModel(index, { api_key: value })}
                    />
                  </Field>
                  <Field label="Extra body (JSON)" className="sm:col-span-2">
                    <Textarea
                      value={model.extra_body}
                      onChange={(event) => updateModel(index, { extra_body: event.target.value })}
                      placeholder={'{\n  "top_k": 40\n}'}
                      className="min-h-24 font-mono text-xs"
                    />
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
            onChange={(event) => {
              const enabled = event.target.checked;
              setMemoryEnabled(enabled);
              publish(models, enabled, memoryDirectory, environmentRows);
            }}
            className="h-4 w-4"
          />
          <span>Memory</span>
        </label>
        <Field label="Directory">
          <Input
            value={memoryDirectory}
            onChange={(event) => {
              const directory = event.target.value;
              setMemoryDirectory(directory);
              publish(models, memoryEnabled, directory, environmentRows);
            }}
          />
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
                {isSensitiveEnvironmentName(row.key) ? (
                  <SensitiveInput
                    value={row.value}
                    onValueChange={(value) => updateEnvironment(index, { value })}
                    placeholder="value"
                    className="font-mono text-xs"
                  />
                ) : (
                  <Input value={row.value} onChange={(event) => updateEnvironment(index, { value: event.target.value })} placeholder="value" className="font-mono text-xs" />
                )}
                <Button type="button" variant="ghost" size="icon" className="h-9 w-8" onClick={() => removeEnvironment(index)} aria-label="Remove environment variable">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
          </div>
        )}

        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
          <Input value={newEnvironmentKey} onChange={(event) => setNewEnvironmentKey(event.target.value)} placeholder="KEY" className="font-mono text-xs" />
          {isSensitiveEnvironmentName(newEnvironmentKey) ? (
            <SensitiveInput
              value={newEnvironmentValue}
              onValueChange={setNewEnvironmentValue}
              placeholder="value"
              className="font-mono text-xs"
            />
          ) : (
            <Input value={newEnvironmentValue} onChange={(event) => setNewEnvironmentValue(event.target.value)} placeholder="value" className="font-mono text-xs" />
          )}
          <Button type="button" variant="outline" size="sm" onClick={addEnvironment} disabled={!newEnvironmentKey.trim()}>
            <Plus className="h-4 w-4" />
            Add variable
          </Button>
        </div>
      </div>

      {status ? <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">{status}</div> : null}
    </div>
  );
}

function isSensitiveEnvironmentName(value: string): boolean {
  const normalized = value.trim().toUpperCase();
  return normalized.includes("KEY")
    || normalized.includes("TOKEN")
    || normalized.includes("SECRET")
    || normalized.includes("PASSWORD");
}
