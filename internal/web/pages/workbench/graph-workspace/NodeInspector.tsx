import { useState } from "react";
import { Braces, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { initialStateBindings } from "../../../lib/graphEditor";
import { exampleConfigForSchema } from "../../../lib/jsonSchemaDefaults";
import { formatTime, isPlainRecord, stringifyJSON } from "../../../lib/utils";
import { runtimeFromSteps } from "../../../components/graphCanvasModel";
import type {
  GraphDefinition,
  GraphNodeSpec,
  NodeTypeSchema,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  StepRecord,
  ToolDefinition,
} from "../../../types";
import { ModelSettingsDialog } from "./GraphSettingsEditor";
import { JSONConfigEditor } from "./JSONConfigEditor";
import { JsonSchemaForm } from "./schemaForm";
import type { ModelAddHandler } from "./SchemaFormControls";
import { CollapsibleInspectorBlock, Field } from "./shared";
import { StateBindingsBlock } from "./StateBindingsEditor";
import { analyzeNodeDetails, nodeTypeForType, schemaForNodeType } from "./nodeInspectorModel";
import {
  modelsFromSettings,
  newEditableGraphModel,
  nextModelID,
  normalizeModelSettings,
} from "./graphSettingsEditorModel";

interface NodeInspectorProps {
  definition: GraphDefinition | null;
  nodeConfigText: string;
  paletteNodeTypes: NodeTypeSchema[];
  registry: RegistryInfo | null;
  registryLoaded: boolean;
  runtimeSettings: RuntimeSettings | null;
  toolDefinitions: ToolDefinition[];
  selectedNode: GraphNodeSpec;
  steps: StepRecord[];
  onApplyNodeConfig: () => void;
  onChangeNode: (update: (node: GraphNodeSpec) => GraphNodeSpec) => void;
  onChangeNodeConfigText: (value: string) => void;
  onChangeNodeID: (value: string) => void;
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
  onDeleteNode: (nodeID: string) => void;
}

export function NodeInspector({
  definition,
  nodeConfigText,
  paletteNodeTypes,
  registry,
  registryLoaded,
  runtimeSettings,
  toolDefinitions,
  selectedNode,
  steps,
  onApplyNodeConfig,
  onChangeNode,
  onChangeNodeConfigText,
  onChangeNodeID,
  onChangeRuntimeSettings,
  onDeleteNode,
}: NodeInspectorProps) {
  const [jsonOpen, setJSONOpen] = useState(false);
  const [configOpen, setConfigOpen] = useState(true);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [propertiesOpen, setPropertiesOpen] = useState(false);
  const [modelAddRequest, setModelAddRequest] = useState<ModelAddRequest | null>(null);
  const nodeTypeSchema = nodeTypeForType(paletteNodeTypes, selectedNode.type);
  const configSchema = schemaForNodeType(paletteNodeTypes, selectedNode.type);
  const nodeConfig = isPlainRecord(selectedNode.config) ? selectedNode.config : {};
  const details = analyzeNodeDetails(definition, selectedNode, nodeTypeSchema, nodeConfig, configSchema, steps, registryLoaded);
  const descriptionText = nodeTypeSchema?.description || selectedNode.description;
  const requestModelAdd: ModelAddHandler | undefined = runtimeSettings
    ? (suggestedID, onAdded) => setModelAddRequest({ suggestedID, onAdded })
    : undefined;

  return (
    <>
      {descriptionText ? (
        <section className="border-b border-border p-3 text-xs text-muted-foreground">
          <div className="line-clamp-4">{descriptionText}</div>
        </section>
      ) : null}

      <StateBindingsBlock
        ownerID={selectedNode.id}
        ports={nodeTypeSchema?.state_ports ?? []}
        dynamicPorts={nodeTypeSchema?.dynamic_state_ports}
        bindings={selectedNode.state}
        definition={definition}
        registry={registry}
        onChange={(state) => onChangeNode((node) => ({ ...node, state }))}
      />

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
              setJSONOpen((open) => !open);
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
          modelIDs={runtimeSettings?.models.map((model) => model.id) ?? []}
          onAddModel={requestModelAdd}
          onChange={(config) => onChangeNode((node) => ({ ...node, config }))}
        />
        <JSONConfigEditor
          open={jsonOpen}
          value={nodeConfigText}
          applyLabel="Apply config"
          onOpenChange={setJSONOpen}
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

        <DetailGroup title="Local Graph" rows={[["position", details.positionLabel]]} />

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

        {(nodeTypeSchema?.state_ports?.length ?? 0) > 0 ? (
          <JSONSummary title="State Ports" value={nodeTypeSchema?.state_ports} />
        ) : null}
      </CollapsibleInspectorBlock>

      <CollapsibleInspectorBlock title="Node Properties" open={propertiesOpen} onOpenChange={setPropertiesOpen}>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <Field label="ID">
            <Input
              value={selectedNode.id}
              onChange={(event) => onChangeNodeID(event.target.value)}
              className={!selectedNode.id.trim() ? "border-destructive focus:border-destructive" : undefined}
            />
          </Field>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => onDeleteNode(selectedNode.id)}
            title="Delete node"
            aria-label="Delete node"
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
                  state: initialStateBindings(schema?.state_ports, node.id),
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

      {modelAddRequest && runtimeSettings ? (
        <ModelSettingsDialog
          mode="add"
          model={newEditableGraphModel(
            modelAddRequest.suggestedID || nextModelID(modelsFromSettings(runtimeSettings))
          )}
          existingModelIDs={runtimeSettings.models.map((model) => model.id)}
          onSave={(model) => {
            const nextModels = [...modelsFromSettings(runtimeSettings), model];
            onChangeRuntimeSettings({ models: normalizeModelSettings(nextModels) });
            modelAddRequest.onAdded(model.id);
            setModelAddRequest(null);
            return true;
          }}
          onClose={() => setModelAddRequest(null)}
        />
      ) : null}
    </>
  );
}

export function NodeRuntimeInspector({
  selectedNode,
  selectedRunID,
  steps,
}: {
  selectedNode: GraphNodeSpec | null;
  selectedRunID: string;
  steps: StepRecord[];
}) {
  if (!selectedNode) {
    return (
      <div className="flex min-h-24 items-center justify-center p-4 text-center text-xs text-muted-foreground">
        Select a node on the canvas.
      </div>
    );
  }

  const nodeSteps = steps
    .filter((step) => step.node_id === selectedNode.id && (!selectedRunID || step.run_id === selectedRunID))
    .sort((left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at));
  const runtime = runtimeFromSteps(nodeSteps, selectedRunID).get(selectedNode.id);
  const latestStep = nodeSteps[0];
  const latestError = nodeSteps.find((step) => Boolean(step.error_message));

  return (
    <div className="space-y-2.5 p-3">
      <div className="grid grid-cols-2 gap-2">
        <RuntimeMetric label="Status" value={latestStep?.status || "idle"} />
        <RuntimeMetric label="Executions" value={String(runtime?.executionCount || 0)} />
        <RuntimeMetric label="Attempt" value={String(latestStep?.attempt || 0)} />
        <RuntimeMetric label="Updated" value={formatTime(latestStep?.updated_at)} />
      </div>

      {latestError?.error_message ? (
        <div className="rounded-md border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">
          <div className="mb-1 font-medium">Latest error</div>
          <div className="whitespace-pre-wrap break-words">{latestError.error_message}</div>
        </div>
      ) : null}

      {latestStep ? (
        <div className="min-w-0 rounded-md border border-border bg-background/40 px-2.5 py-2">
          <div className="text-[10px] uppercase tracking-wide text-muted-foreground">Current step</div>
          <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground" title={latestStep.step_id}>
            {latestStep.step_id}
          </div>
        </div>
      ) : (
        <div className="rounded-md border border-dashed border-border p-3 text-center text-xs text-muted-foreground">
          No execution in the selected run.
        </div>
      )}
    </div>
  );
}

function RuntimeMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-md border border-border bg-muted/50 p-2">
      <div className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-0.5 truncate text-xs font-medium" title={value}>{value || "-"}</div>
    </div>
  );
}

interface ModelAddRequest {
  suggestedID: string;
  onAdded: (modelID: string) => void;
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
          <span className="min-w-0 break-all font-mono">{value || "-"}</span>
        </div>
      ))}
    </div>
  );
}

function JSONSummary({ title, value }: { title: string; value: unknown }) {
  return (
    <details className="rounded-md border border-border bg-muted p-2">
      <summary className="cursor-pointer text-[11px] font-semibold uppercase text-muted-foreground">{title}</summary>
      <pre className="mt-2 max-h-48 overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-all text-[11px]">
        {formatJSONSummary(value)}
      </pre>
    </details>
  );
}

function formatJSONSummary(value: unknown): string {
  try {
    return stringifyJSON(value);
  } catch {
    return String(value);
  }
}
