import { type ReactNode, useEffect, useRef, useState } from "react";
import { Bot, ChevronDown, ChevronRight, KeyRound, Pencil, Plus, Trash2, X } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import type { RuntimeSettings, RuntimeSettingsUpdate } from "../../../types";
import type { ToolDefinition } from "../../../types";
import { WorkbenchDialogOverlay } from "../shared";
import { Field } from "./shared";
import {
  environmentRowsFromSettings,
  modelIDValidationError,
  modelsFromSettings,
  newEditableGraphModel,
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
  const [modelDialog, setModelDialog] = useState<{ index?: number; model: EditableGraphModel } | null>(null);
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
    setModelDialog(null);
    setStatus("");
  }, [settings]);

  function addModel() {
    setModelDialog({ model: newEditableGraphModel(nextModelID(models)) });
  }

  function updateEnvironment(index: number, update: Partial<EditableEnvironmentVariable>) {
    const nextRows = environmentRows.map((row, rowIndex) => (rowIndex === index ? { ...row, ...update } : row));
    setEnvironmentRows(nextRows);
    publish(models, nextRows);
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

  function saveModel(model: EditableGraphModel) {
    const nextModels = modelDialog?.index === undefined
      ? [...models, model]
      : models.map((current, index) => (index === modelDialog.index ? model : current));
    if (!publish(nextModels, environmentRows)) return false;
    setModels(nextModels);
    setModelDialog(null);
    return true;
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
  ): boolean {
    let environment: Record<string, string>;
    let environmentSecrets: RuntimeSettingsUpdate["environment_secrets"];
    let modelUpdates: RuntimeSettingsUpdate["models"];
    try {
      ({ environment, environmentSecrets } = normalizeEnvironmentSettings(nextEnvironmentRows));
      modelUpdates = normalizeModelSettings(nextModels);
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
      return false;
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
      return true;
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
      return false;
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
              <div key={`${model.id || "model"}-${index}`} className="rounded-md border border-border bg-background p-2">
                <div className="flex min-w-0 items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-mono text-xs font-semibold">{model.id || "unnamed"}</span>
                      <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${model.enabled ? "bg-[var(--status-ok-bg)] text-[var(--status-ok-text)]" : "bg-muted text-muted-foreground"}`}>
                        {model.enabled ? "enabled" : "disabled"}
                      </span>
                    </div>
                    <div className="mt-1 truncate text-xs text-muted-foreground">
                      {model.provider || "openai"} · {model.api_format || "chat_completions"} · {model.model || "model name not set"}
                    </div>
                    <div className="mt-0.5 flex min-w-0 gap-2 text-[11px] text-muted-foreground">
                      <span className="min-w-0 flex-1 truncate" title={model.base_url || "Default provider URL"}>
                        {model.base_url || "Default provider URL"}
                      </span>
                      <span className="shrink-0">{modelCredentialConfigured(model) ? "API key set" : "No API key"}</span>
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => setModelDialog({ index, model: { ...model } })}
                    aria-label={`Edit model ${model.id}`}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button type="button" variant="ghost" size="icon" className="h-8 w-8" onClick={() => removeModel(index)} aria-label={`Remove model ${model.id}`}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
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
      {modelDialog ? (
        <ModelSettingsDialog
          mode={modelDialog.index === undefined ? "add" : "edit"}
          model={modelDialog.model}
          existingModelIDs={models
            .filter((_, index) => index !== modelDialog.index)
            .map((model) => model.id)}
          onSave={saveModel}
          onClose={() => setModelDialog(null)}
        />
      ) : null}
    </div>
  );
}

export function ModelSettingsDialog({
  mode,
  model,
  existingModelIDs,
  onSave,
  onClose,
}: {
  mode: "add" | "edit";
  model: EditableGraphModel;
  existingModelIDs: readonly string[];
  onSave: (model: EditableGraphModel) => boolean | void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState<EditableGraphModel>(() => ({ ...model }));
  const [error, setError] = useState("");
  const idError = modelIDValidationError(existingModelIDs, draft.id);
  const title = mode === "add" ? "Add model" : "Edit model";

  function update(update: Partial<EditableGraphModel>) {
    setDraft((current) => ({ ...current, ...update }));
    setError("");
  }

  function setCredential() {
    const value = draft.credential_input.trim();
    if (!value) {
      setError("API key is required.");
      return;
    }
    update({
      credential_configured: true,
      credential_input: "",
      credential_value: value,
      credential_clear: false,
      credential_dirty: true,
    });
  }

  function clearCredential() {
    update({
      credential_configured: false,
      credential_input: "",
      credential_value: "",
      credential_clear: true,
      credential_dirty: true,
    });
  }

  function submit() {
    if (idError) {
      setError(idError);
      return;
    }
    const credentialValue = draft.credential_input.trim();
    const credentialDirty = Boolean(draft.credential_dirty || credentialValue);
    const nextDraft: EditableGraphModel = {
      ...draft,
      id: draft.id.trim(),
      credential_configured: credentialDirty && credentialValue ? true : draft.credential_configured,
      credential_input: "",
      credential_value: credentialDirty ? credentialValue || draft.credential_value : "",
      credential_clear: credentialDirty ? (credentialValue ? false : draft.credential_clear) : false,
      credential_dirty: credentialDirty,
    };
    try {
      normalizeModelSettings([nextDraft]);
      if (onSave(nextDraft) === false) return;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  }

  return (
    <WorkbenchDialogOverlay onDismiss={onClose}>
      <form
        className="flex max-h-[min(760px,92vh)] w-[min(620px,96vw)] flex-col overflow-hidden rounded-md border border-border bg-panel shadow-xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="model-settings-dialog-title"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <div className="flex h-14 shrink-0 items-center gap-3 border-b border-border px-4">
          <Bot className="h-4 w-4 text-muted-foreground" />
          <div id="model-settings-dialog-title" className="text-sm font-semibold">{title}</div>
          <Button type="button" variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label={`Close ${title.toLowerCase()} dialog`}>
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="grid min-h-0 gap-3 overflow-y-auto p-4 sm:grid-cols-2">
          <Field label="Model ID" className="sm:col-span-2">
            <Input
              autoFocus
              value={draft.id}
              onChange={(event) => update({ id: event.target.value })}
              placeholder="default"
              disabled={mode === "edit"}
              className={`font-mono text-xs ${idError ? "border-destructive focus:border-destructive" : ""}`}
            />
            {idError ? <span className="text-xs text-destructive">{idError}</span> : null}
            {mode === "edit" ? <span className="text-xs text-muted-foreground">Model ID cannot be changed after creation.</span> : null}
          </Field>
          <Field label="Provider">
            <Select value={draft.provider} onChange={(event) => update({ provider: event.target.value })}>
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
            <Select value={draft.api_format} onChange={(event) => update({ api_format: event.target.value })}>
              <option value="chat_completions">Chat Completions</option>
              <option value="responses">Responses</option>
            </Select>
          </Field>
          <Field label="Model name">
            <Input value={draft.model} onChange={(event) => update({ model: event.target.value })} placeholder="gpt-5" />
          </Field>
          <Field label="Base URL">
            <Input value={draft.base_url} onChange={(event) => update({ base_url: event.target.value })} placeholder="https://api.openai.com/v1" />
          </Field>
          <div className="grid min-w-0 gap-1 sm:col-span-2">
            <span className="text-xs font-medium text-muted-foreground">API key</span>
            <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
              <Input
                type="password"
                autoComplete="new-password"
                value={draft.credential_input}
                onChange={(event) => update({ credential_input: event.target.value })}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    setCredential();
                  }
                }}
                placeholder={modelCredentialConfigured(draft) ? "Enter a replacement key" : "Enter API key"}
                className="font-mono text-xs"
              />
              <Button type="button" variant="outline" size="sm" onClick={setCredential} disabled={!draft.credential_input.trim()}>
                <KeyRound className="h-4 w-4" />
                {modelCredentialConfigured(draft) ? "Replace" : "Set"}
              </Button>
              <Button type="button" variant="ghost" size="sm" onClick={clearCredential} disabled={!modelCredentialConfigured(draft)}>
                <X className="h-4 w-4" />
                Clear
              </Button>
            </div>
            <span className="text-xs text-muted-foreground">{modelCredentialStatus(draft)}</span>
          </div>
          <label className="flex items-center gap-2 text-xs sm:col-span-2">
            <input type="checkbox" checked={draft.enabled} onChange={(event) => update({ enabled: event.target.checked })} className="h-4 w-4" />
            Enabled
          </label>
          <Field label="Pricing currency">
            <Input value={draft.pricing_currency} onChange={(event) => update({ pricing_currency: event.target.value })} placeholder="USD" />
          </Field>
          <Field label="Input / 1M">
            <Input type="number" min="0" step="any" value={draft.input_per_million} onChange={(event) => update({ input_per_million: event.target.value })} />
          </Field>
          <Field label="Cached input / 1M">
            <Input type="number" min="0" step="any" value={draft.cached_input_per_million} onChange={(event) => update({ cached_input_per_million: event.target.value })} />
          </Field>
          <Field label="Output / 1M">
            <Input type="number" min="0" step="any" value={draft.output_per_million} onChange={(event) => update({ output_per_million: event.target.value })} />
          </Field>
          <Field label="Extra body (JSON)" className="sm:col-span-2">
            <Textarea
              value={draft.extra_body}
              onChange={(event) => update({ extra_body: event.target.value })}
              placeholder={'{\n  "top_k": 40\n}'}
              className="min-h-24 font-mono text-xs"
            />
          </Field>
          {error && error !== idError ? <div className="text-xs text-destructive sm:col-span-2">{error}</div> : null}
        </div>

        <div className="flex shrink-0 justify-end gap-2 border-t border-border px-4 py-3">
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={Boolean(idError)}>{mode === "add" ? "Add model" : "Save changes"}</Button>
        </div>
      </form>
    </WorkbenchDialogOverlay>
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
