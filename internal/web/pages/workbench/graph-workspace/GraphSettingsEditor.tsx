import { type ReactNode, useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronRight, KeyRound, Plus, Trash2, X } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import type { RuntimeSettings, RuntimeSettingsUpdate } from "../../../types";
import type { ToolDefinition } from "../../../types";
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
  toolDefinitions,
}: {
  settings: RuntimeSettings | null;
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
  toolDefinitions: ToolDefinition[];
}) {
  const [models, setModels] = useState<EditableGraphModel[]>(() => modelsFromSettings(settings));
  const [environmentRows, setEnvironmentRows] = useState<EditableEnvironmentVariable[]>(() => environmentRowsFromSettings(settings));
  const [environmentPresetKey, setEnvironmentPresetKey] = useState("");
  const [newEnvironmentKey, setNewEnvironmentKey] = useState("");
  const [newEnvironmentValue, setNewEnvironmentValue] = useState("");
  const [modelsOpen, setModelsOpen] = useState(true);
  const [toolAccessOpen, setToolAccessOpen] = useState(false);
  const [environmentOpen, setEnvironmentOpen] = useState(true);
  const [status, setStatus] = useState("");
  const locallyAppliedSettingsRef = useRef<RuntimeSettings | null>(null);

  useEffect(() => {
    if (settings === locallyAppliedSettingsRef.current) {
      locallyAppliedSettingsRef.current = null;
      return;
    }
    setModels(modelsFromSettings(settings));
    setEnvironmentRows(environmentRowsFromSettings(settings));
    setEnvironmentPresetKey("");
    setNewEnvironmentKey("");
    setNewEnvironmentValue("");
    setStatus("");
  }, [settings]);

  function updateModel(index: number, update: Partial<EditableGraphModel>) {
    const nextModels = models.map((model, modelIndex) => (modelIndex === index ? { ...model, ...update } : model));
    setModels(nextModels);
    publish(nextModels, environmentRows);
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
        credential_configured: false,
        credential_input: "",
        credential_value: "",
        credential_clear: false,
        pricing_currency: "USD",
        input_per_million: "",
        cached_input_per_million: "",
        output_per_million: "",
      },
    ];
    setModels(nextModels);
    publish(nextModels, environmentRows);
  }

  function updateEnvironment(index: number, update: Partial<EditableEnvironmentVariable>) {
    const nextRows = environmentRows.map((row, rowIndex) => (rowIndex === index ? { ...row, ...update } : row));
    setEnvironmentRows(nextRows);
    publish(models, nextRows);
  }

  function updateModelInput(index: number, value: string) {
    setModels(models.map((model, modelIndex) => (
      modelIndex === index ? { ...model, credential_input: value } : model
    )));
  }

  function setModelCredential(index: number) {
    const value = models[index]?.credential_input.trim() ?? "";
    if (!value) {
      setStatus("API key is required.");
      return;
    }
    updateModel(index, {
      credential_configured: true,
      credential_input: "",
      credential_value: value,
      credential_clear: false,
    });
  }

  function clearModelCredential(index: number) {
    updateModel(index, {
      credential_configured: false,
      credential_input: "",
      credential_value: "",
      credential_clear: true,
    });
  }

  function removeEnvironment(index: number) {
    const nextRows = environmentRows.filter((_, rowIndex) => rowIndex !== index);
    setEnvironmentRows(nextRows);
    publish(models, nextRows);
  }

  function addEnvironmentPreset() {
    const preset = settings?.environment_presets?.find((item) => item.key === environmentPresetKey);
    if (!preset || environmentRows.some((row) => row.key.trim() === preset.key)) return;
    const nextRows = [...environmentRows, {
      key: preset.key,
      value: preset.secret ? "" : preset.default_value,
      secret: Boolean(preset.secret),
      secret_source: "env" as const,
      secret_ref: preset.secret ? preset.key : "",
    }];
    setEnvironmentRows(nextRows);
    setEnvironmentPresetKey("");
    publish(models, nextRows);
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
    const secret = isSensitiveEnvironmentName(key);
    const nextRows = [...environmentRows, {
      key,
      value: secret ? "" : newEnvironmentValue,
      secret,
      secret_source: "env" as const,
      secret_ref: secret ? (newEnvironmentValue.trim() || key) : "",
    }];
    setEnvironmentRows(nextRows);
    setNewEnvironmentKey("");
    setNewEnvironmentValue("");
    publish(models, nextRows);
  }

  function removeModel(index: number) {
    const nextModels = models.filter((_, modelIndex) => modelIndex !== index);
    setModels(nextModels);
    publish(nextModels, environmentRows);
  }

  function toggleAllToolPermissions() {
    const next = new Set(settings?.tool_permissions ?? []);
    if (allToolPermissionsSelected) {
      for (const permission of allToolPermissions) next.delete(permission);
    } else {
      for (const permission of allToolPermissions) next.add(permission);
    }
    onChangeRuntimeSettings({ tool_permissions: [...next].sort() });
  }

  function publish(
    nextModels: EditableGraphModel[],
    nextEnvironmentRows: EditableEnvironmentVariable[]
  ) {
    let environment: Record<string, string>;
    let environmentSecrets: RuntimeSettingsUpdate["environment_secrets"];
    let modelUpdates: RuntimeSettingsUpdate["models"];
    try {
      ({ environment, environmentSecrets } = normalizeEnvironmentSettings(nextEnvironmentRows));
      modelUpdates = normalizeModelSettings(nextModels);
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
      return;
    }

    setStatus("");
    try {
      const next = onChangeRuntimeSettings({
        environment,
        environment_secrets: environmentSecrets,
        models: modelUpdates,
        tool_permissions: settings?.tool_permissions ?? [],
        tool_approvals: settings?.tool_approvals ?? {},
      });
      locallyAppliedSettingsRef.current = next;
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
    }
  }

  const availableEnvironmentPresets = (settings?.environment_presets ?? []).filter(
    (preset) => !environmentRows.some((row) => row.key.trim() === preset.key)
  );
  const allToolPermissions = [...new Set(toolDefinitions.flatMap((tool) => tool.permissions ?? []))].sort();
  const allToolPermissionsSelected = allToolPermissions.length > 0 && allToolPermissions.every(
    (permission) => (settings?.tool_permissions ?? []).includes(permission)
  );
  return (
    <div className="grid gap-3">
      <SettingsSection
        title="Models"
        open={modelsOpen}
        onOpenChange={setModelsOpen}
        action={(
          <Button type="button" variant="outline" size="sm" onClick={addModel}>
            <Plus className="h-4 w-4" />
            Add model
          </Button>
        )}
      >
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
                  <div className="grid min-w-0 gap-1 sm:col-span-2">
                    <span className="text-xs font-medium text-muted-foreground">API key</span>
                    <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
                      <Input
                        type="password"
                        autoComplete="new-password"
                        value={model.credential_input}
                        onChange={(event) => updateModelInput(index, event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter") {
                            event.preventDefault();
                            setModelCredential(index);
                          }
                        }}
                        placeholder={modelCredentialConfigured(model) ? "Enter a replacement key" : "Enter API key"}
                        className="font-mono text-xs"
                      />
                      <Button type="button" variant="outline" size="sm" onClick={() => setModelCredential(index)} disabled={!model.credential_input.trim()}>
                        <KeyRound className="h-4 w-4" />
                        {modelCredentialConfigured(model) ? "Replace" : "Set"}
                      </Button>
                      <Button type="button" variant="ghost" size="sm" onClick={() => clearModelCredential(index)} disabled={!modelCredentialConfigured(model)}>
                        <X className="h-4 w-4" />
                        Clear
                      </Button>
                    </div>
                    <span className="text-xs text-muted-foreground">{modelCredentialStatus(model)}</span>
                  </div>
                  <Field label="Pricing currency">
                    <Input value={model.pricing_currency} onChange={(event) => updateModel(index, { pricing_currency: event.target.value })} placeholder="USD" />
                  </Field>
                  <Field label="Input / 1M">
                    <Input type="number" min="0" step="any" value={model.input_per_million} onChange={(event) => updateModel(index, { input_per_million: event.target.value })} />
                  </Field>
                  <Field label="Cached input / 1M">
                    <Input type="number" min="0" step="any" value={model.cached_input_per_million} onChange={(event) => updateModel(index, { cached_input_per_million: event.target.value })} />
                  </Field>
                  <Field label="Output / 1M">
                    <Input type="number" min="0" step="any" value={model.output_per_million} onChange={(event) => updateModel(index, { output_per_million: event.target.value })} />
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
      </SettingsSection>

      <SettingsSection
        title="Tool Access"
        open={toolAccessOpen}
        onOpenChange={setToolAccessOpen}
        action={(
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={toggleAllToolPermissions}
            disabled={allToolPermissions.length === 0}
          >
            {allToolPermissionsSelected ? "Deselect all" : "Select all"}
          </Button>
        )}
      >
        {toolDefinitions.length === 0 ? (
          <div className="rounded-md border border-dashed border-border bg-background/60 p-3 text-xs text-muted-foreground">
            No tools available.
          </div>
        ) : (
          <div className="grid gap-2">
            {toolDefinitions.map((tool) => {
              const permissions = tool.permissions ?? [];
              const approvalKey = (tool.name || tool.id).trim();
              const approval = settings?.tool_approvals?.[approvalKey.toLowerCase()];
              return (
                <div key={tool.id} className="flex min-h-10 min-w-0 items-center gap-3 rounded-md border border-border bg-background p-2">
                  <div className="min-w-0 flex-1 truncate font-mono text-xs font-medium">{approvalKey}</div>
                  <div className="flex shrink-0 items-center gap-3">
                    {tool.approval === "required" ? (
                      <Select
                        aria-label={`${approvalKey} approval`}
                        value={approval === undefined ? "pending" : approval ? "allow" : "deny"}
                        onChange={(event) => {
                          const next = { ...(settings?.tool_approvals ?? {}) };
                          if (event.target.value === "pending") delete next[approvalKey.toLowerCase()];
                          else next[approvalKey.toLowerCase()] = event.target.value === "allow";
                          onChangeRuntimeSettings({ tool_approvals: next });
                        }}
                      >
                        <option value="pending">Approval required</option>
                        <option value="allow">Allow</option>
                        <option value="deny">Deny</option>
                      </Select>
                    ) : null}
                    {permissions.map((permission) => (
                      <label key={permission} className="flex items-center gap-1.5 whitespace-nowrap text-xs">
                        <span>{permission}</span>
                        <input
                          type="checkbox"
                          className="shrink-0"
                          checked={(settings?.tool_permissions ?? []).includes(permission)}
                          onChange={(event) => {
                            const current = new Set(settings?.tool_permissions ?? []);
                            if (event.target.checked) current.add(permission);
                            else current.delete(permission);
                            onChangeRuntimeSettings({ tool_permissions: [...current].sort() });
                          }}
                        />
                      </label>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </SettingsSection>

      <SettingsSection
        title="Environment"
        open={environmentOpen}
        onOpenChange={setEnvironmentOpen}
      >
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
              <div key={`${row.key || "environment"}-${index}`} className="grid grid-cols-[minmax(0,1fr)_110px_minmax(0,1fr)_32px] gap-2">
                <Input value={row.key} onChange={(event) => updateEnvironment(index, { key: event.target.value })} placeholder="KEY" className="font-mono text-xs" />
                <Select
                  value={row.secret ? row.secret_source : "value"}
                  onChange={(event) => {
                    const source = event.target.value as "value" | "env" | "file" | "managed";
                    updateEnvironment(index, {
                      secret: source !== "value",
                      secret_source: source === "value" ? "env" : source,
                      secret_ref: source === "value" ? "" : (source === "managed" ? row.secret_ref : row.secret_ref || row.key),
                    });
                  }}
                >
                  <option value="value">Value</option>
                  {row.secret_source === "managed" ? <option value="managed">Server managed</option> : null}
                  <option value="env">Env ref</option>
                  <option value="file">File ref</option>
                </Select>
                <Input
                  value={row.secret ? row.secret_ref : row.value}
                  onChange={(event) => updateEnvironment(index, row.secret ? { secret_ref: event.target.value } : { value: event.target.value })}
                  placeholder={row.secret_source === "file" ? "secret-file" : row.secret ? row.key : "value"}
                  className="font-mono text-xs"
                  disabled={row.secret && row.secret_source === "managed"}
                />
                <Button type="button" variant="ghost" size="icon" className="h-9 w-8" onClick={() => removeEnvironment(index)} aria-label="Remove environment variable">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
          </div>
        )}

        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
          <Input value={newEnvironmentKey} onChange={(event) => setNewEnvironmentKey(event.target.value)} placeholder="KEY" className="font-mono text-xs" />
          <Input
            value={newEnvironmentValue}
            onChange={(event) => setNewEnvironmentValue(event.target.value)}
            placeholder={isSensitiveEnvironmentName(newEnvironmentKey) ? "Environment variable ref" : "value"}
            className="font-mono text-xs"
          />
          <Button type="button" variant="outline" size="sm" onClick={addEnvironment} disabled={!newEnvironmentKey.trim()}>
            <Plus className="h-4 w-4" />
            Add variable
          </Button>
        </div>
      </SettingsSection>

      {status ? <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">{status}</div> : null}
    </div>
  );
}

function SettingsSection({
  title,
  open,
  onOpenChange,
  action,
  children,
}: {
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  action?: ReactNode;
  children: ReactNode;
}) {
  const Icon = open ? ChevronDown : ChevronRight;
  return (
    <section className="rounded-md border border-border bg-muted/30">
      <div className="flex min-h-10 items-center gap-2">
        <button
          type="button"
          className="flex min-h-10 min-w-0 flex-1 items-center gap-2 px-2 text-left"
          aria-expanded={open}
          onClick={() => onOpenChange(!open)}
        >
          <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="truncate text-sm font-medium">{title}</span>
        </button>
        {open && action ? <div className="mr-2 shrink-0">{action}</div> : null}
      </div>
      {open ? <div className="grid gap-2 p-2 pt-0">{children}</div> : null}
    </section>
  );
}

function isSensitiveEnvironmentName(value: string): boolean {
  const normalized = value.trim().toUpperCase();
  return normalized.includes("KEY")
    || normalized.includes("TOKEN")
    || normalized.includes("SECRET")
    || normalized.includes("PASSWORD");
}

function modelCredentialConfigured(model: EditableGraphModel): boolean {
  if (model.credential_clear) return false;
  return Boolean(model.credential_value || model.credential_configured);
}

function modelCredentialStatus(model: EditableGraphModel): string {
  if (model.credential_clear) return "API key will be cleared when the graph is saved.";
  if (model.credential_value) return "New API key is ready to save.";
  if (model.credential_configured) return "API key is configured.";
  return "No API key configured.";
}
