import { useEffect, useMemo, useState, type ReactNode } from "react";
import { AlertTriangle, Braces, ChevronDown, ChevronRight, FileJson, Plus, Trash2 } from "lucide-react";
import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import { END_NODE_REF, graphNodePositions } from "../../../lib/graphEditor";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { exampleConfigForSchema } from "../../../lib/jsonSchemaDefaults";
import { cn, formatTime, stringifyJSON } from "../../../lib/utils";
import type {
  ConditionSchema,
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  GraphSettings,
  GraphSettingsUpdate,
  InitialStateRequirements,
  InitialStateRequirement,
  NodeTypeSchema,
  StepRecord,
  ToolDefinition,
} from "../../../types";
import type { GraphLintIssue } from "./lint";
import { JsonSchemaForm } from "./schemaForm";
import { StatusText, type StatusTone } from "../shared";
import { Field, InspectorBlock, NodeSelect, PanelHeader } from "./shared";
import type { InspectorMode } from "./types";

interface GraphInspectorPanelProps {
  conditions: ConditionSchema[];
  definition: GraphDefinition | null;
  edgeConfigText: string;
  initialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  initialStateText: string;
  inspectorMode: InspectorMode;
  inspectorTitle: string;
  lintIssues: GraphLintIssue[];
  nodeConfigText: string;
  paletteNodeTypes: NodeTypeSchema[];
  registryLoaded: boolean;
  graphSettings: GraphSettings | null;
  onUpdateGraphSettings: (settings: GraphSettingsUpdate) => Promise<GraphSettings>;
  toolDefinitions: ToolDefinition[];
  selectedEdge: GraphEdgeSpec | null;
  selectedNode: GraphNodeSpec | null;
  selectedVirtualLoop: VirtualGraphLoop | null;
  selectedVirtualEdge: VirtualGraphEdge | null;
  steps: StepRecord[];
  visibleVirtualNodes: GraphNodeSpec[];
  onApplyEdgeConfig: () => void;
  onApplyNodeConfig: () => void;
  onChangeEdge: (update: (edge: GraphEdgeSpec) => GraphEdgeSpec) => void;
  onChangeEdgeConfigText: (value: string) => void;
  onChangeVirtualLoop: (update: (loop: VirtualGraphLoop) => VirtualGraphLoop) => void;
  onChangeVirtualEdge: (update: (edge: VirtualGraphEdge) => VirtualGraphEdge) => void;
  onChangeGraphField: <Key extends keyof GraphDefinition>(key: Key, value: GraphDefinition[Key]) => void;
  onChangeInitialStateText: (value: string) => void;
  onChangeNode: (update: (node: GraphNodeSpec) => GraphNodeSpec) => void;
  onChangeNodeConfigText: (value: string) => void;
  onChangeNodeId: (value: string) => void;
  onDeleteEdge: () => void;
  onDeleteLoop: (loopId: string) => void;
  onDeleteNode: (nodeId: string) => void;
  onSelectLintIssue?: (issue: GraphLintIssue) => void;
}

export function GraphInspectorPanel({
  conditions,
  definition,
  edgeConfigText,
  initialRequirements,
  initialRequirementsError,
  initialStateText,
  inspectorMode,
  inspectorTitle,
  lintIssues,
  nodeConfigText,
  paletteNodeTypes,
  registryLoaded,
  graphSettings,
  onUpdateGraphSettings,
  toolDefinitions,
  selectedEdge,
  selectedNode,
  selectedVirtualLoop,
  selectedVirtualEdge,
  steps,
  visibleVirtualNodes,
  onApplyEdgeConfig,
  onApplyNodeConfig,
  onChangeEdge,
  onChangeEdgeConfigText,
  onChangeVirtualLoop,
  onChangeVirtualEdge,
  onChangeGraphField,
  onChangeInitialStateText,
  onChangeNode,
  onChangeNodeConfigText,
  onChangeNodeId,
  onDeleteEdge,
  onDeleteLoop,
  onDeleteNode,
  onSelectLintIssue,
}: GraphInspectorPanelProps) {
  return (
    <section className="min-h-0 overflow-auto border-l border-border bg-panel">
      <PanelHeader icon={FileJson} title={inspectorTitle} />
      <LintPanel issues={lintIssues} onSelectIssue={onSelectLintIssue} />
      {inspectorMode === "graph" ? (
        <GraphInspector
          definition={definition}
          initialRequirements={initialRequirements}
          initialRequirementsError={initialRequirementsError}
          initialStateText={initialStateText}
          graphSettings={graphSettings}
          onUpdateGraphSettings={onUpdateGraphSettings}
          onChangeGraphField={onChangeGraphField}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      ) : null}

      {inspectorMode === "node" && selectedNode ? (
        <NodeInspector
          definition={definition}
          nodeConfigText={nodeConfigText}
          paletteNodeTypes={paletteNodeTypes}
          registryLoaded={registryLoaded}
          toolDefinitions={toolDefinitions}
          selectedNode={selectedNode}
          steps={steps}
          onApplyNodeConfig={onApplyNodeConfig}
          onChangeNode={onChangeNode}
          onChangeNodeConfigText={onChangeNodeConfigText}
          onChangeNodeId={onChangeNodeId}
          onDeleteNode={onDeleteNode}
        />
      ) : null}

      {inspectorMode === "edge" && (selectedEdge || selectedVirtualEdge) ? (
        <EdgeInspector
          conditions={conditions}
          definition={definition}
          edgeConfigText={edgeConfigText}
          selectedEdge={selectedEdge}
          selectedVirtualEdge={selectedVirtualEdge}
          visibleVirtualNodes={visibleVirtualNodes}
          onApplyEdgeConfig={onApplyEdgeConfig}
          onChangeEdge={onChangeEdge}
          onChangeEdgeConfigText={onChangeEdgeConfigText}
          onChangeVirtualEdge={onChangeVirtualEdge}
          onDeleteEdge={onDeleteEdge}
        />
      ) : null}

      {inspectorMode === "loop" && selectedVirtualLoop ? (
        <LoopInspector
          definition={definition}
          selectedVirtualLoop={selectedVirtualLoop}
          onChangeVirtualLoop={onChangeVirtualLoop}
          onDeleteLoop={onDeleteLoop}
        />
      ) : null}
    </section>
  );
}

function LintPanel({
  issues,
  onSelectIssue,
}: {
  issues: GraphLintIssue[];
  onSelectIssue?: (issue: GraphLintIssue) => void;
}) {
  if (issues.length === 0) return null;

  return (
    <section className="grid gap-1 border-b border-border p-3">
      <div className="flex min-h-7 items-center gap-2">
        <AlertTriangle className="h-4 w-4 text-amber-600 dark:text-amber-300" />
        <div className="text-xs font-semibold uppercase text-muted-foreground">Lint</div>
      </div>
      <div className="grid gap-1">
        {issues.map((issue) => (
          <button
            key={issue.id}
            type="button"
            onClick={() => onSelectIssue?.(issue)}
            className="grid gap-1 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent"
          >
            <div className="flex min-w-0 items-center gap-2">
              <StatusText tone={issue.severity === "error" ? "danger" : "warn"}>{issue.severity}</StatusText>
              {issue.nodeId ? <span className="truncate font-mono text-muted-foreground">{issue.nodeId}</span> : null}
              {issue.path && !issue.nodeId ? <span className="truncate font-mono text-muted-foreground">{issue.path}</span> : null}
            </div>
            <div className="line-clamp-2 text-foreground">{issue.message}</div>
          </button>
        ))}
      </div>

    </section>
  );
}

function GraphInspector({
  definition,
  initialRequirements,
  initialRequirementsError,
  initialStateText,
  graphSettings,
  onUpdateGraphSettings,
  onChangeGraphField,
  onChangeInitialStateText,
}: Pick<
  GraphInspectorPanelProps,
  | "definition"
  | "graphSettings"
  | "onUpdateGraphSettings"
  | "initialRequirements"
  | "initialRequirementsError"
  | "initialStateText"
  | "onChangeGraphField"
  | "onChangeInitialStateText"
>) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const requiredInitialState = initialRequirements?.required ?? [];
  const hasEndEdge = (definition?.edges ?? []).some((edge) => edge.to === END_NODE_REF);
  const hasInitialStateHints = Boolean(
    initialRequirements &&
      (initialRequirements.unresolved.length > 0 ||
        initialRequirements.provided_by_upstream.length > 0 ||
        (initialRequirements.warnings?.length ?? 0) > 0)
  );

  return (
    <>
      <InspectorBlock title="Name">
        <Input
          aria-label="Name"
          value={definition?.name ?? ""}
          onChange={(event) => onChangeGraphField("name", event.target.value)}
          disabled={!definition}
        />
      </InspectorBlock>

      <InspectorBlock title="Description">
        <Textarea
          aria-label="Description"
          value={definition?.description ?? ""}
          onChange={(event) => onChangeGraphField("description", event.target.value)}
          disabled={!definition}
          className="h-20 text-xs"
        />
      </InspectorBlock>

      <InspectorBlock title="Routing">
        <Field label="Entry point">
          <NodeSelect
            value={definition?.entry_point ?? ""}
            nodes={definition?.nodes ?? []}
            disabled={!definition}
            className={!definition?.entry_point ? "border-destructive focus:border-destructive" : undefined}
            onChange={(value) => onChangeGraphField("entry_point", value)}
          />
        </Field>
        <Field label="Finish point">
          <NodeSelect
            value={definition?.finish_point ?? ""}
            nodes={definition?.nodes ?? []}
            disabled={!definition}
            className={!definition?.finish_point && !hasEndEdge ? "border-destructive focus:border-destructive" : undefined}
            onChange={(value) => onChangeGraphField("finish_point", value)}
          />
        </Field>
      </InspectorBlock>

      <CollapsibleInspectorBlock title="Graph Settings" open={settingsOpen} onOpenChange={setSettingsOpen}>
        <GraphSettingsEditor settings={graphSettings} onUpdateGraphSettings={onUpdateGraphSettings} />
      </CollapsibleInspectorBlock>

      <InspectorBlock title="Run Input">
        {hasInitialStateHints ? <InitialStateRequirementList requirements={initialRequirements} showRequired={false} /> : null}
        <RunInputEditor
          requirements={requiredInitialState}
          analysisError={initialRequirementsError}
          initialStateText={initialStateText}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      </InspectorBlock>

    </>
  );
}

const MODEL_API_KEY_MASK = "********";

interface EditableGraphModel {
  id: string;
  enabled: boolean;
  provider: string;
  model: string;
  base_url: string;
  api_key: string;
  api_key_configured: boolean;
}

interface EditableEnvironmentVariable {
  key: string;
  value: string;
}

function GraphSettingsEditor({
  settings,
  onUpdateGraphSettings,
}: {
  settings: GraphSettings | null;
  onUpdateGraphSettings: (settings: GraphSettingsUpdate) => Promise<GraphSettings>;
}) {
  const [models, setModels] = useState<EditableGraphModel[]>([]);
  const [memoryEnabled, setMemoryEnabled] = useState(false);
  const [memoryDirectory, setMemoryDirectory] = useState("");
  const [environmentRows, setEnvironmentRows] = useState<EditableEnvironmentVariable[]>([]);
  const [newEnvironmentKey, setNewEnvironmentKey] = useState("");
  const [newEnvironmentValue, setNewEnvironmentValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState("");

  useEffect(() => {
    setModels(modelsFromSettings(settings));
    setMemoryEnabled(settings?.memory.enabled ?? false);
    setMemoryDirectory(settings?.memory.directory ?? "");
    setEnvironmentRows(environmentRowsFromSettings(settings));
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
    let modelUpdates: GraphSettingsUpdate["models"];
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
      await onUpdateGraphSettings({
        environment,
        models: modelUpdates,
        memory: {
          enabled: memoryEnabled,
          directory: memoryDirectory.trim(),
        },
      });
      setModels((current) => current.map((model) => ({ ...model, api_key: modelAPIKeyDisplayValue(model) })));
      setStatus("Saved");
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="grid gap-3">
      <div className="grid gap-2 rounded-md border border-border bg-muted/30 p-2">
        <div className="flex min-h-8 items-center gap-2">
          <span className="text-sm font-medium">Models</span>
          <Button type="button" variant="outline" size="sm" className="ml-auto" onClick={addModel}>
            <Plus className="h-4 w-4" />
            Add
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
            Add
          </Button>
        </div>
      </div>

      {status ? <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">{status}</div> : null}
      <Button variant="outline" size="sm" onClick={() => void save()} disabled={saving}>
        <Braces className="h-4 w-4" />
        {saving ? "Saving" : "Save Settings"}
      </Button>
    </div>
  );
}

function modelsFromSettings(settings: GraphSettings | null): EditableGraphModel[] {
  const configured = Array.isArray(settings?.models) ? settings.models : settings?.model ? [settings.model] : [];
  return configured.map((model, index) => ({
    id: model.id || (index === 0 ? "default" : `model-${index + 1}`),
    enabled: model.enabled,
    provider: model.provider || "openai",
    model: model.model ?? "",
    base_url: model.base_url ?? "",
    api_key: model.api_key_configured ? MODEL_API_KEY_MASK : "",
    api_key_configured: model.api_key_configured,
  }));
}

function nextModelID(models: EditableGraphModel[]): string {
  if (models.length === 0 && !models.some((model) => model.id.trim() === "default")) {
    return "default";
  }
  const existing = new Set(models.map((model) => model.id.trim()).filter(Boolean));
  let index = models.length + 1;
  let id = `model-${index}`;
  while (existing.has(id)) {
    index += 1;
    id = `model-${index}`;
  }
  return id;
}

function modelAPIKeyDisplayValue(model: EditableGraphModel): string {
  const apiKey = model.api_key.trim();
  if (apiKey && apiKey !== MODEL_API_KEY_MASK) return MODEL_API_KEY_MASK;
  return model.api_key_configured ? MODEL_API_KEY_MASK : "";
}

function normalizeModelSettings(models: EditableGraphModel[]): GraphSettingsUpdate["models"] {
  const seen = new Set<string>();
  return models.map((model, index) => {
    const id = model.id.trim();
    if (!id) {
      throw new Error(`Model ${index + 1} id is required.`);
    }
    if (seen.has(id)) {
      throw new Error(`Duplicate model id: ${id}`);
    }
    seen.add(id);
    const apiKey = model.api_key.trim();
    return {
      id,
      enabled: model.enabled,
      provider: model.provider || "openai",
      model: model.model.trim(),
      base_url: model.base_url.trim(),
      api_key: apiKey && apiKey !== MODEL_API_KEY_MASK ? apiKey : undefined,
    };
  });
}

function environmentRowsFromSettings(settings: GraphSettings | null): EditableEnvironmentVariable[] {
  return Object.entries(editableEnvironment(settings))
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({ key, value }));
}

function normalizeEnvironmentSettings(rows: EditableEnvironmentVariable[]): Record<string, string> {
  const output: Record<string, string> = {};
  const seen = new Set<string>();
  for (const [index, row] of rows.entries()) {
    const key = row.key.trim();
    if (!key) {
      throw new Error(`Environment ${index + 1} key is required.`);
    }
    if (seen.has(key)) {
      throw new Error(`Duplicate environment key: ${key}`);
    }
    seen.add(key);
    output[key] = row.value;
  }
  return output;
}

function editableEnvironment(settings: GraphSettings | null): Record<string, string> {
  const input = settings?.environment ?? {};
  const output: Record<string, string> = {};
  for (const [key, value] of Object.entries(input)) {
    if (key === "OPENAI_MODEL" || key === "OPENAI_BASE_URL") continue;
    output[key] = value;
  }
  return output;
}

function RunInputEditor({
  requirements,
  analysisError,
  initialStateText,
  onChangeInitialStateText,
}: {
  requirements: InitialStateRequirement[];
  analysisError: string;
  initialStateText: string;
  onChangeInitialStateText: (value: string) => void;
}) {
  const [jsonOpen, setJsonOpen] = useState(false);
  const parsed = useMemo(() => parseInitialStateText(initialStateText), [initialStateText]);

  if (requirements.length === 0) {
    return (
      <div className="grid gap-3">
        <div
          className={
            analysisError
              ? "rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive"
              : "rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground"
          }
        >
          {analysisError ? `Cannot build input form: ${analysisError}` : "No form fields detected from the graph state contracts."}
        </div>
        <JsonRunInputEditor
          open={jsonOpen}
          initialStateText={initialStateText}
          onOpenChange={setJsonOpen}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      </div>
    );
  }

  return (
    <div className="grid gap-3">
      {parsed.error ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
          JSON is invalid. Form edits will rebuild the run input from valid fields.
        </div>
      ) : null}

      <div className="grid gap-3">
        {requirements.map((requirement) => (
          <RunInputField
            key={requirement.path}
            requirement={requirement}
            value={getPathValue(parsed.root, requirement.path)}
            invalid={!hasFilledRequirementValue(getPathValue(parsed.root, requirement.path), requirement.type)}
            onChange={(value) =>
              onChangeInitialStateText(stringifyJSON(updateInitialStatePath(initialStateText, requirement.path, value)))
            }
          />
        ))}
      </div>

      <JsonRunInputEditor
        open={jsonOpen}
        initialStateText={initialStateText}
        onOpenChange={setJsonOpen}
        onChangeInitialStateText={onChangeInitialStateText}
      />
    </div>
  );
}

function JsonRunInputEditor({
  open,
  initialStateText,
  onOpenChange,
  onChangeInitialStateText,
}: {
  open: boolean;
  initialStateText: string;
  onOpenChange: (open: boolean) => void;
  onChangeInitialStateText: (value: string) => void;
}) {
  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => onOpenChange(!open)}>
          <Braces className="h-4 w-4" />
          {open ? "Hide JSON" : "Edit JSON"}
        </Button>
      </div>

      {open ? (
        <Textarea
          value={initialStateText}
          onChange={(event) => onChangeInitialStateText(event.target.value)}
          spellCheck={false}
          className="h-40 text-xs"
        />
      ) : null}
    </div>
  );
}

function RunInputField({
  requirement,
  value,
  invalid,
  onChange,
}: {
  requirement: InitialStateRequirement;
  value: unknown;
  invalid: boolean;
  onChange: (value: unknown) => void;
}) {
  const type = (requirement.type ?? "").toLowerCase();
  const description = requirement.message || requirement.description;
  const sourceTitle = requirement.nodes?.length ? `Used by ${requirement.nodes.join(", ")}` : undefined;

  return (
    <div className="grid gap-1" title={sourceTitle}>
      <span className="truncate font-mono text-xs font-medium">{requirement.path}</span>
      {renderRunInputControl(type, value, onChange, invalid)}
      {invalid ? <div className="text-xs text-destructive">Required value is missing.</div> : null}
      {requirement.type ? <div className="text-[11px] text-muted-foreground">{requirement.type}</div> : null}
      {description ? <div className="line-clamp-2 text-xs text-muted-foreground">{description}</div> : null}
    </div>
  );
}

function renderRunInputControl(type: string, value: unknown, onChange: (value: unknown) => void, invalid: boolean) {
  const invalidClass = invalid ? "border-destructive focus:border-destructive" : undefined;
  if (type === "boolean" || type === "bool") {
    return (
      <label className={cn("flex h-9 items-center gap-2 rounded-md border border-input bg-background px-3 text-sm", invalid && "border-destructive")}>
        <input
          type="checkbox"
          checked={Boolean(value)}
          onChange={(event) => onChange(event.target.checked)}
          className="h-4 w-4"
        />
        <span>{Boolean(value) ? "true" : "false"}</span>
      </label>
    );
  }

  if (["number", "float", "float64", "integer", "int", "int64"].includes(type)) {
    return (
      <Input
        type="number"
        value={typeof value === "number" && Number.isFinite(value) ? String(value) : ""}
        className={invalidClass}
        onChange={(event) => {
          const next = event.target.value;
          onChange(next.trim() === "" ? null : Number(next));
        }}
      />
    );
  }

  if (["object", "map", "array", "list"].includes(type)) {
    return (
      <Textarea
        value={formatJSONFieldValue(value)}
        onChange={(event) => {
          try {
            onChange(JSON.parse(event.target.value));
          } catch {
            onChange(event.target.value);
          }
        }}
        spellCheck={false}
        className={cn("h-24 text-xs", invalidClass)}
      />
    );
  }

  return (
    <Input
      value={typeof value === "string" ? value : value == null ? "" : String(value)}
      onChange={(event) => onChange(event.target.value)}
      className={invalidClass}
    />
  );
}

function NodeInspector({
  definition,
  nodeConfigText,
  paletteNodeTypes,
  registryLoaded,
  toolDefinitions,
  selectedNode,
  steps,
  onApplyNodeConfig,
  onChangeNode,
  onChangeNodeConfigText,
  onChangeNodeId,
  onDeleteNode,
}: Pick<
  GraphInspectorPanelProps,
  | "definition"
  | "nodeConfigText"
  | "paletteNodeTypes"
  | "registryLoaded"
  | "toolDefinitions"
  | "selectedNode"
  | "steps"
  | "onApplyNodeConfig"
  | "onChangeNode"
  | "onChangeNodeConfigText"
  | "onChangeNodeId"
  | "onDeleteNode"
>) {
  const [jsonOpen, setJsonOpen] = useState(false);
  const [configOpen, setConfigOpen] = useState(true);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [propertiesOpen, setPropertiesOpen] = useState(false);
  if (!selectedNode) return null;
  const nodeTypeSchema = nodeTypeForType(paletteNodeTypes, selectedNode.type);
  const configSchema = schemaForNodeType(paletteNodeTypes, selectedNode.type);
  const nodeConfig = isPlainRecord(selectedNode.config) ? selectedNode.config : {};
  const details = analyzeNodeDetails(definition, selectedNode, nodeTypeSchema, nodeConfig, configSchema, steps, registryLoaded);
  const descriptionText = nodeTypeSchema?.description || selectedNode.description;

  return (
    <>
      {descriptionText ? (
        <section className="border-b border-border p-3 text-xs text-muted-foreground">
          <div className="line-clamp-4">{descriptionText}</div>
        </section>
      ) : null}

      <CollapsibleInspectorBlock
        title="Config"
        open={configOpen}
        onOpenChange={setConfigOpen}
        action={
          <Button
            type="button"
            variant={jsonOpen ? "outline" : "ghost"}
            size="icon"
            title={jsonOpen ? "Hide JSON" : "Edit JSON"}
            aria-label={jsonOpen ? "Hide JSON" : "Edit JSON"}
            className="h-8 w-8"
            onClick={(event) => {
              event.stopPropagation();
              setJsonOpen((open) => !open);
            }}
          >
            <Braces className="h-4 w-4" />
          </Button>
        }
      >
        <JsonSchemaForm
          schema={configSchema}
          unavailableReason={
            registryLoaded
              ? selectedNode.type
                ? `Type "${selectedNode.type}" is not present in the loaded registry.`
                : "This node does not define a type."
              : "Registry has not loaded from /registry."
          }
          value={nodeConfig}
          toolDefinitions={toolDefinitions}
          onChange={(config) => onChangeNode((node) => ({ ...node, config }))}
        />
        <JsonConfigEditor
          open={jsonOpen}
          value={nodeConfigText}
          applyLabel="Apply Config"
          onOpenChange={setJsonOpen}
          onChange={onChangeNodeConfigText}
          onApply={onApplyNodeConfig}
          showToggle={false}
        />
      </CollapsibleInspectorBlock>

      <CollapsibleInspectorBlock title="Node Details" open={detailsOpen} onOpenChange={setDetailsOpen}>
        <DetailGroup
          title="Definition"
          rows={[
            ["index", details.indexLabel],
            ["role", details.roles.join(", ") || "-"],
            ["type", details.typeLabel],
            ["schema", details.schemaLabel],
            ["config", `${details.configKeys.length} keys`],
            ["incoming", String(details.incoming.length)],
            ["outgoing", String(details.outgoing.length)],
          ]}
        />

        <DetailGroup
          title="Local Graph"
          rows={[
            ["position", details.positionLabel],
          ]}
        />

        {details.steps.length > 0 ? (
          <DetailGroup
            title="Runtime"
            rows={[
              ["steps", String(details.steps.length)],
              ["last status", details.latestStep?.status ?? "-"],
              ["attempt", String(details.latestStep?.attempt || 0)],
              ["updated", formatTime(details.latestStep?.updated_at)],
            ]}
          />
        ) : null}

        {details.latestStep?.error_message ? (
          <div className="rounded-md border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">
            <div className="mb-1 font-medium">Last error</div>
            <div className="line-clamp-4">{details.latestStep.error_message}</div>
          </div>
        ) : null}

        {nodeTypeSchema?.state_contract ? (
          <JSONSummary title="State Contract" value={nodeTypeSchema.state_contract} />
        ) : null}
      </CollapsibleInspectorBlock>

      <CollapsibleInspectorBlock title="Node Properties" open={propertiesOpen} onOpenChange={setPropertiesOpen}>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <Field label="ID">
            <Input
              value={selectedNode.id}
              onChange={(event) => onChangeNodeId(event.target.value)}
              className={!selectedNode.id.trim() ? "border-destructive focus:border-destructive" : undefined}
            />
          </Field>
          <Button
            variant="ghost"
            size="icon"
            onClick={() => onDeleteNode(selectedNode.id)}
            title="Delete node"
            className="mt-5"
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
        <Field label="Name">
          <Input
            value={selectedNode.name ?? ""}
            onChange={(event) => onChangeNode((node) => ({ ...node, name: event.target.value }))}
          />
        </Field>
        <Field label="Type">
          <Select
            value={selectedNode.type ?? ""}
            onChange={(event) =>
              onChangeNode((node) => {
                const schema = nodeTypeForType(paletteNodeTypes, event.target.value);
                return {
                  ...node,
                  type: event.target.value,
                  name: node.name || schema?.title || node.name,
                  config: exampleConfigForSchema(schema?.config_schema),
                };
              })
            }
            className={!selectedNode.type?.trim() ? "border-destructive focus:border-destructive" : undefined}
          >
            {selectedNode.type && !nodeTypeForType(paletteNodeTypes, selectedNode.type) ? (
              <option value={selectedNode.type}>{selectedNode.type}</option>
            ) : null}
            {paletteNodeTypes.map((nodeType) => (
              <option key={nodeType.type} value={nodeType.type}>
                {nodeType.title || nodeType.type}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Description">
          <Textarea
            value={selectedNode.description ?? ""}
            onChange={(event) => onChangeNode((node) => ({ ...node, description: event.target.value }))}
            className="h-20 text-xs"
          />
        </Field>
      </CollapsibleInspectorBlock>
    </>
  );
}

function DetailGroup({ title, rows }: { title: string; rows: Array<[string, string]> }) {
  return (
    <div className="rounded-md border border-border bg-muted p-2">
      <div className="mb-2 text-[11px] font-semibold uppercase text-muted-foreground">{title}</div>
      <DetailRows rows={rows} />
    </div>
  );
}

function DetailRows({ rows }: { rows: Array<[string, string]> }) {
  return (
    <div className="grid gap-1">
      {rows.map(([label, value]) => (
        <div key={label} className="grid grid-cols-[84px_minmax(0,1fr)] gap-2 text-xs">
          <span className="text-muted-foreground">{label}</span>
          <span className="break-words font-mono">{value || "-"}</span>
        </div>
      ))}
    </div>
  );
}

function JSONSummary({ title, value }: { title: string; value: unknown }) {
  return (
    <details className="rounded-md border border-border bg-muted p-2">
      <summary className="cursor-pointer text-[11px] font-semibold uppercase text-muted-foreground">{title}</summary>
      <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-[11px]">{formatJSONSummary(value)}</pre>
    </details>
  );
}

function analyzeNodeDetails(
  definition: GraphDefinition | null,
  node: GraphNodeSpec,
  nodeTypeSchema: NodeTypeSchema | undefined,
  nodeConfig: Record<string, unknown>,
  configSchema: Record<string, unknown> | undefined,
  steps: StepRecord[],
  registryLoaded: boolean
) {
  const nodes = definition?.nodes ?? [];
  const nodeIndex = nodes.findIndex((item) => item.id === node.id);
  const edges = definition?.edges ?? [];
  const incoming = edges.filter((edge) => edge.to === node.id);
  const outgoing = edges.filter((edge) => edge.from === node.id);
  const position = definition ? graphNodePositions(definition).get(node.id) : undefined;
  const schemaFields = configSchemaFields(configSchema);
  const nodeSteps = steps
    .filter((step) => step.node_id === node.id)
    .sort((left, right) => timeValue(right.updated_at) - timeValue(left.updated_at));
  const roles: string[] = [];
  if (definition?.entry_point === node.id) roles.push("entry");
  if (definition?.finish_point === node.id) roles.push("finish");
  if (outgoing.some((edge) => edge.to === END_NODE_REF)) roles.push("end edge");

  return {
    incoming,
    outgoing,
    steps: nodeSteps,
    latestStep: nodeSteps[0],
    roles,
    configKeys: Object.keys(nodeConfig).sort((left, right) => left.localeCompare(right)),
    schemaFields,
    indexLabel: nodeIndex >= 0 ? `${nodeIndex + 1} of ${nodes.length}` : "-",
    positionLabel: position ? `${Math.round(position.x)}, ${Math.round(position.y)}` : "-",
    schemaLabel: configSchema ? `${schemaFields.length} fields` : "none",
    typeLabel: nodeTypeSchema
      ? nodeTypeSchema.title && nodeTypeSchema.title !== nodeTypeSchema.type
        ? `${nodeTypeSchema.title} (${nodeTypeSchema.type})`
        : nodeTypeSchema.type
      : node.type
        ? `${node.type} (${registryLoaded ? "unregistered" : "registry unavailable"})`
        : "-",
  };
}

function timeValue(value?: string): number {
  if (!value) return 0;
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}

function configSchemaFields(schema: Record<string, unknown> | undefined): string[] {
  const properties = isPlainRecord(schema?.properties) ? schema.properties : {};
  const required = new Set(
    Array.isArray(schema?.required) ? schema.required.filter((item): item is string => typeof item === "string") : []
  );
  return Object.keys(properties)
    .sort((left, right) => left.localeCompare(right))
    .map((key) => (required.has(key) ? `${key} *` : key));
}

function nodeTypeForType(nodeTypes: NodeTypeSchema[], type?: string): NodeTypeSchema | undefined {
  const normalizedType = normalizeNodeType(type);
  if (!normalizedType) return undefined;
  return nodeTypes.find((nodeType) => normalizeNodeType(nodeType.type) === normalizedType);
}

function normalizeNodeType(type?: string): string {
  return (type ?? "").trim();
}

function formatJSONSummary(value: unknown): string {
  try {
    return stringifyJSON(value);
  } catch {
    return String(value);
  }
}

function CollapsibleInspectorBlock({
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
    <section className="border-b border-border last:border-b-0">
      <div className="flex min-h-11 items-center gap-2 px-3 hover:bg-accent">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => onOpenChange(!open)}
          className="flex min-h-11 min-w-0 flex-1 items-center gap-2 text-left"
        >
          <Icon className="h-4 w-4 text-muted-foreground" />
          <span className="text-xs font-semibold uppercase text-muted-foreground">{title}</span>
        </button>
        {action ? <div className="shrink-0" onClick={(event) => event.stopPropagation()}>{action}</div> : null}
      </div>
      {open ? <div className="grid gap-3 p-3 pt-0">{children}</div> : null}
    </section>
  );
}

function LoopInspector({
  definition,
  selectedVirtualLoop,
  onChangeVirtualLoop,
  onDeleteLoop,
}: Pick<
  GraphInspectorPanelProps,
  "definition" | "selectedVirtualLoop" | "onChangeVirtualLoop" | "onDeleteLoop"
>) {
  if (!selectedVirtualLoop) return null;
  const analysis = analyzeVirtualLoop(definition, selectedVirtualLoop);
  const selectedIds = new Set(selectedVirtualLoop.nodeIds);

  return (
    <>
      <InspectorBlock title="Loop Properties">
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <Field label="Name">
            <Input
              value={selectedVirtualLoop.name ?? ""}
              placeholder="Loop group"
              onChange={(event) => onChangeVirtualLoop((loop) => ({ ...loop, name: event.target.value }))}
            />
          </Field>
          <Button
            variant="ghost"
            size="icon"
            onClick={() => onDeleteLoop(selectedVirtualLoop.id)}
            title="Delete loop"
            className="mt-5"
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
        <div className="grid gap-1 text-xs">
          <div className="grid grid-cols-[84px_minmax(0,1fr)] gap-2">
            <span className="text-muted-foreground">id</span>
            <span className="truncate font-mono">{selectedVirtualLoop.id}</span>
          </div>
          <div className="grid grid-cols-[84px_minmax(0,1fr)] gap-2">
            <span className="text-muted-foreground">loop start</span>
            <span className="truncate font-mono">{analysis.loopStartId || "-"}</span>
          </div>
          <div className="grid grid-cols-[84px_minmax(0,1fr)] gap-2">
            <span className="text-muted-foreground">next</span>
            <span className="truncate font-mono">{analysis.nextNodeId || "-"}</span>
          </div>
          <div className="grid grid-cols-[84px_minmax(0,1fr)] gap-2">
            <span className="text-muted-foreground">condition</span>
            <span className="truncate font-mono">{analysis.conditionTypes.join(", ") || "-"}</span>
          </div>
        </div>
      </InspectorBlock>

      <InspectorBlock title="Loop Nodes">
        <div className="grid max-h-80 gap-1 overflow-auto">
          {(definition?.nodes ?? []).map((node) => (
            <label
              key={node.id}
              className="flex min-h-8 items-center gap-2 rounded-md px-2 text-sm hover:bg-accent"
            >
              <input
                type="checkbox"
                checked={selectedIds.has(node.id)}
                onChange={(event) =>
                  onChangeVirtualLoop((loop) => ({
                    ...loop,
                    nodeIds: event.target.checked
                      ? uniqueStrings([...loop.nodeIds, node.id])
                      : loop.nodeIds.filter((nodeID) => nodeID !== node.id),
                  }))
                }
                className="h-4 w-4"
              />
              <span className="min-w-0 flex-1 truncate">{node.name || node.id}</span>
              <span className="max-w-24 truncate font-mono text-[11px] text-muted-foreground">{node.id}</span>
            </label>
          ))}
        </div>
      </InspectorBlock>
    </>
  );
}

function EdgeInspector({
  conditions,
  definition,
  edgeConfigText,
  selectedEdge,
  selectedVirtualEdge,
  visibleVirtualNodes,
  onApplyEdgeConfig,
  onChangeEdge,
  onChangeEdgeConfigText,
  onChangeVirtualEdge,
  onDeleteEdge,
}: Pick<
  GraphInspectorPanelProps,
  | "conditions"
  | "definition"
  | "edgeConfigText"
  | "selectedEdge"
  | "selectedVirtualEdge"
  | "visibleVirtualNodes"
  | "onApplyEdgeConfig"
  | "onChangeEdge"
  | "onChangeEdgeConfigText"
  | "onChangeVirtualEdge"
  | "onDeleteEdge"
>) {
  const [jsonOpen, setJsonOpen] = useState(false);
  const activeEdge = selectedEdge ?? selectedVirtualEdge;
  const selectedCondition = selectedEdge?.condition ?? selectedVirtualEdge?.condition;
  const selectedConditionType = selectedCondition?.type;
  const conditionSchema = schemaForCondition(conditions, selectedConditionType);
  const rawConditionConfig = selectedCondition?.config;
  const conditionConfig = isPlainRecord(rawConditionConfig) ? rawConditionConfig : {};
  const realNodes = definition?.nodes ?? [];
  const endNodes = visibleVirtualNodes.filter((node) => node.id === END_NODE_REF);
  const sourceNodes = selectedVirtualEdge
    ? selectedVirtualEdge.kind === "entry"
      ? visibleVirtualNodes.filter((node) => node.type === "start")
      : realNodes
    : realNodes;
  const targetNodes = selectedVirtualEdge
    ? selectedVirtualEdge.kind === "finish"
      ? visibleVirtualNodes.filter((node) => node.type === "end")
      : realNodes
    : [...realNodes, ...endNodes];

  function changeEdgeFrom(value: string) {
    if (selectedVirtualEdge) {
      onChangeVirtualEdge((edge) => ({ ...edge, from: value }));
      return;
    }
    onChangeEdge((edge) => ({ ...edge, from: value }));
  }

  function changeEdgeTo(value: string) {
    if (selectedVirtualEdge) {
      onChangeVirtualEdge((edge) => ({ ...edge, to: value }));
      return;
    }
    onChangeEdge((edge) => ({ ...edge, to: value }));
  }

  function changeCondition(type: string) {
    const schema = conditions.find((condition) => condition.type === type);
    const condition = type ? { type, config: exampleConfigForSchema(schema?.config_schema) } : undefined;
    if (selectedVirtualEdge) {
      onChangeVirtualEdge((edge) => ({ ...edge, condition }));
      return;
    }
    onChangeEdge((edge) => ({ ...edge, condition }));
  }

  function changeConditionConfig(config: Record<string, unknown>) {
    if (selectedVirtualEdge) {
      onChangeVirtualEdge((edge) => ({
        ...edge,
        condition: edge.condition ? { ...edge.condition, config } : edge.condition,
      }));
      return;
    }
    onChangeEdge((edge) => ({
      ...edge,
      condition: edge.condition ? { ...edge.condition, config } : edge.condition,
    }));
  }

  return (
    <>
      <InspectorBlock title="Edge Properties">
        <div className="mb-2 flex items-center gap-2">
          <span className="text-xs font-medium text-muted-foreground">{selectedConditionType || "direct"}</span>
          <Button variant="ghost" size="icon" onClick={onDeleteEdge} title="Delete edge" className="ml-auto">
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
        {activeEdge ? (
          <>
            <div className="grid grid-cols-2 gap-2">
              <Field label="From">
                <NodeSelect
                  value={activeEdge.from}
                  nodes={sourceNodes}
                  disabled={selectedVirtualEdge?.kind === "entry"}
                  onChange={changeEdgeFrom}
                />
              </Field>
              <Field label="To">
                <NodeSelect
                  value={activeEdge.to}
                  nodes={targetNodes}
                  disabled={selectedVirtualEdge?.kind === "finish"}
                  onChange={changeEdgeTo}
                />
              </Field>
            </div>
            <Field label="Condition">
              <Select
                value={selectedConditionType ?? ""}
                onChange={(event) => {
                  changeCondition(event.target.value);
                }}
              >
                <option value="">direct</option>
                {selectedConditionType && !conditions.some((condition) => condition.type === selectedConditionType) ? (
                  <option value={selectedConditionType}>{selectedConditionType}</option>
                ) : null}
                {conditions.map((condition) => (
                  <option key={condition.type} value={condition.type}>
                    {condition.title || condition.type}
                  </option>
                ))}
              </Select>
            </Field>
          </>
        ) : null}
      </InspectorBlock>

      {selectedCondition ? (
        <InspectorBlock title="Condition Config">
          <JsonSchemaForm
            schema={conditionSchema}
            value={conditionConfig}
            onChange={changeConditionConfig}
          />
          <JsonConfigEditor
            open={jsonOpen}
            value={edgeConfigText}
            applyLabel="Apply Condition"
            onOpenChange={setJsonOpen}
            onChange={onChangeEdgeConfigText}
            onApply={onApplyEdgeConfig}
          />
        </InspectorBlock>
      ) : null}
    </>
  );
}

function JsonConfigEditor({
  open,
  value,
  applyLabel,
  onOpenChange,
  onChange,
  onApply,
  showToggle = true,
}: {
  open: boolean;
  value: string;
  applyLabel: string;
  onOpenChange: (open: boolean) => void;
  onChange: (value: string) => void;
  onApply: () => void;
  showToggle?: boolean;
}) {
  return (
    <div className="grid gap-2">
      {showToggle ? (
        <div>
          <Button variant="ghost" size="sm" onClick={() => onOpenChange(!open)}>
            <Braces className="h-4 w-4" />
            {open ? "Hide JSON" : "Edit JSON"}
          </Button>
        </div>
      ) : null}
      {open ? (
        <>
          <Textarea
            value={value}
            onChange={(event) => onChange(event.target.value)}
            spellCheck={false}
            className="h-44 text-xs"
          />
          <Button variant="outline" size="sm" onClick={onApply}>
            <Braces className="h-4 w-4" />
            {applyLabel}
          </Button>
        </>
      ) : null}
    </div>
  );
}

function schemaForNodeType(nodeTypes: NodeTypeSchema[], type?: string): Record<string, unknown> | undefined {
  const schema = nodeTypeForType(nodeTypes, type)?.config_schema;
  return isPlainRecord(schema) ? schema : undefined;
}

function schemaForCondition(conditions: ConditionSchema[], type?: string): Record<string, unknown> | undefined {
  const schema = conditions.find((condition) => condition.type === type)?.config_schema;
  return isPlainRecord(schema) ? schema : undefined;
}

function analyzeVirtualLoop(definition: GraphDefinition | null, loop: VirtualGraphLoop) {
  const nodeIds = uniqueStrings(loop.nodeIds).filter((nodeID) => definition?.nodes.some((node) => node.id === nodeID));
  const nodeIdSet = new Set(nodeIds);
  const incoming = new Map(nodeIds.map((nodeID) => [nodeID, 0]));
  for (const edge of definition?.edges ?? []) {
    if (!nodeIdSet.has(edge.from) || !nodeIdSet.has(edge.to) || edge.from === edge.to) continue;
    if (edge.condition) continue;
    incoming.set(edge.to, (incoming.get(edge.to) ?? 0) + 1);
  }

  const loopStartId = nodeIds.find((nodeID) => (incoming.get(nodeID) ?? 0) === 0) ?? nodeIds[0] ?? "";
  const conditionTypes: string[] = [];
  const conditionSources = new Set<string>();
  for (const edge of definition?.edges ?? []) {
    if (loopStartId && nodeIdSet.has(edge.from) && edge.to === loopStartId && edge.condition?.type) {
      conditionTypes.push(edge.condition.type);
      conditionSources.add(edge.from);
    }
  }

  const edges = definition?.edges ?? [];
  const preferredExit = edges.find((edge) => conditionSources.has(edge.from) && !nodeIdSet.has(edge.to) && !edge.condition);
  const fallbackExit = edges.find((edge) => nodeIdSet.has(edge.from) && !nodeIdSet.has(edge.to) && !edge.condition);

  return {
    loopStartId,
    nextNodeId: (preferredExit ?? fallbackExit)?.to ?? "",
    conditionTypes: uniqueStrings(conditionTypes),
  };
}

function InitialStateRequirementList({
  requirements,
  showRequired = true,
}: {
  requirements: InitialStateRequirements | null;
  showRequired?: boolean;
}) {
  const required = showRequired ? (requirements?.required ?? []) : [];
  const provided = requirements?.provided_by_upstream ?? [];
  const unresolved = requirements?.unresolved ?? [];
  const warnings = requirements?.warnings ?? [];
  if (!requirements) {
    return <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">Requirements unavailable</div>;
  }
  if (required.length === 0 && provided.length === 0 && unresolved.length === 0 && warnings.length === 0) {
    return <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">No required initial state</div>;
  }
  return (
    <div className="grid gap-2">
      {required.length > 0 ? <RequirementGroup title="Required" tone="warn" items={required} /> : null}
      {unresolved.length > 0 ? <RequirementGroup title="Unresolved" tone="danger" items={unresolved} /> : null}
      {provided.length > 0 ? <RequirementGroup title="Provided" tone="ok" items={provided} /> : null}
      {warnings.length > 0 ? (
        <div className="rounded-md border border-border bg-muted p-2">
          <div className="mb-2 flex items-center gap-2">
            <StatusText tone="warn">Warnings</StatusText>
            <span className="text-xs text-muted-foreground">{warnings.length}</span>
          </div>
          <div className="grid gap-1">
            {warnings.map((warning, index) => (
              <div key={`${warning.code ?? "warning"}-${index}`} className="text-xs text-muted-foreground">
                {warning.message ?? warning.code ?? "warning"}
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function RequirementGroup({
  title,
  tone,
  items,
}: {
  title: string;
  tone: StatusTone;
  items: InitialStateRequirement[];
}) {
  return (
    <div className="rounded-md border border-border bg-muted p-2">
      <div className="mb-2 flex items-center gap-2">
        <StatusText tone={tone}>{title}</StatusText>
        <span className="text-xs text-muted-foreground">{items.length}</span>
      </div>
      <div className="grid gap-1">
        {items.map((item) => (
          <div key={`${title}-${item.path}`} className="min-w-0 text-xs">
            <div className="truncate font-mono text-foreground">{item.path}</div>
            <div className="truncate text-muted-foreground">
              {[item.type, item.nodes?.length ? `nodes:${item.nodes.join(",")}` : "", item.sources?.length ? `sources:${item.sources.join(",")}` : ""]
                .filter(Boolean)
                .join(" / ")}
            </div>
            {item.message || item.description ? (
              <div className="line-clamp-2 text-muted-foreground">{item.message || item.description}</div>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}

function parseInitialStateText(text: string): { root: Record<string, unknown>; error: string | null } {
  try {
    const parsed = JSON.parse(text) as unknown;
    return {
      root: normalizeInitialStateRoot(parsed),
      error: null,
    };
  } catch (err) {
    return {
      root: normalizeInitialStateRoot({}),
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

function normalizeInitialStateRoot(value: unknown): Record<string, unknown> {
  const root = isPlainRecord(value) ? { ...value } : {};
  for (const section of ["shared", "scopes", "internal", "runtime"]) {
    if (!isPlainRecord(root[section])) root[section] = {};
  }
  return root;
}

function updateInitialStatePath(currentText: string, path: string, value: unknown): Record<string, unknown> {
  const parsed = parseInitialStateText(currentText);
  const root = normalizeInitialStateRoot(parsed.root);
  setPathValue(root, path, value);
  return root;
}

function getPathValue(root: Record<string, unknown>, path: string): unknown {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  let cursor: unknown = root;
  for (const part of parts) {
    if (!isPlainRecord(cursor) || !Object.prototype.hasOwnProperty.call(cursor, part)) return undefined;
    cursor = cursor[part];
  }
  return cursor;
}

function hasFilledRequirementValue(value: unknown, type?: string): boolean {
  if (value === null || value === undefined) return false;
  if ((type ?? "").toLowerCase() === "string") return typeof value === "string" && value.trim().length > 0;
  if (typeof value === "string") return value.trim().length > 0;
  return true;
}

function setPathValue(root: Record<string, unknown>, path: string, value: unknown) {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  if (parts.length === 0) return;
  let cursor = root;
  for (const part of parts.slice(0, -1)) {
    const next = cursor[part];
    if (!isPlainRecord(next)) cursor[part] = {};
    cursor = cursor[part] as Record<string, unknown>;
  }
  const leaf = parts[parts.length - 1];
  if (!leaf) return;
  cursor[leaf] = cloneJSONValue(value);
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function cloneJSONValue(value: unknown): unknown {
  if (Array.isArray(value)) return [...value];
  if (isPlainRecord(value)) return { ...value };
  return value;
}

function formatJSONFieldValue(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  return stringifyJSON(value);
}

function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const item = value.trim();
    if (!item || seen.has(item)) continue;
    seen.add(item);
    result.push(item);
  }
  return result;
}


