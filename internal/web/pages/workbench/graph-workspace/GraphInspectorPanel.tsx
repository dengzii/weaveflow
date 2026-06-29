import { useMemo, useState, type ReactNode } from "react";
import { AlertTriangle, Braces, ChevronDown, ChevronRight, FileJson, Trash2 } from "lucide-react";
import type { VirtualGraphEdge } from "../../../components/GraphCanvas";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { exampleConfigForSchema } from "../../../lib/jsonSchemaDefaults";
import { cn, stringifyJSON } from "../../../lib/utils";
import type {
  ConditionSchema,
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  InitialStateRequirements,
  InitialStateRequirement,
  NodeTypeSchema,
} from "../../../types";
import type { GraphLintIssue } from "./lint";
import { JsonSchemaForm } from "./schemaForm";
import { StatusText, type StatusTone } from "../shared";
import { Field, InfoRows, InspectorBlock, NodeSelect, PanelHeader } from "./shared";
import type { InspectorMode } from "./types";
import { displayNodeRef } from "./utils";

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
  selectedEdge: GraphEdgeSpec | null;
  selectedNode: GraphNodeSpec | null;
  selectedVirtualEdge: VirtualGraphEdge | null;
  visibleVirtualNodes: GraphNodeSpec[];
  onApplyEdgeConfig: () => void;
  onApplyNodeConfig: () => void;
  onChangeEdge: (update: (edge: GraphEdgeSpec) => GraphEdgeSpec) => void;
  onChangeEdgeConfigText: (value: string) => void;
  onChangeGraphField: <Key extends keyof GraphDefinition>(key: Key, value: GraphDefinition[Key]) => void;
  onChangeInitialStateText: (value: string) => void;
  onChangeNode: (update: (node: GraphNodeSpec) => GraphNodeSpec) => void;
  onChangeNodeConfigText: (value: string) => void;
  onChangeNodeId: (value: string) => void;
  onDeleteEdge: () => void;
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
  selectedEdge,
  selectedNode,
  selectedVirtualEdge,
  visibleVirtualNodes,
  onApplyEdgeConfig,
  onApplyNodeConfig,
  onChangeEdge,
  onChangeEdgeConfigText,
  onChangeGraphField,
  onChangeInitialStateText,
  onChangeNode,
  onChangeNodeConfigText,
  onChangeNodeId,
  onDeleteEdge,
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
          onChangeGraphField={onChangeGraphField}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      ) : null}

      {inspectorMode === "node" && selectedNode ? (
        <NodeInspector
          nodeConfigText={nodeConfigText}
          paletteNodeTypes={paletteNodeTypes}
          selectedNode={selectedNode}
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
          onDeleteEdge={onDeleteEdge}
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
  return (
    <section className="grid gap-1 border-b border-border p-3">
      <div className="flex min-h-7 items-center gap-2">
        {issues.length > 0 ? <AlertTriangle className="h-4 w-4 text-amber-600 dark:text-amber-300" /> : null}
        <div className="text-xs font-semibold uppercase text-muted-foreground">Lint</div>
      </div>
      {issues.length > 0 ? (
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
      ) : null}
    </section>
  );
}

function GraphInspector({
  definition,
  initialRequirements,
  initialRequirementsError,
  initialStateText,
  onChangeGraphField,
  onChangeInitialStateText,
}: Pick<
  GraphInspectorPanelProps,
  | "definition"
  | "initialRequirements"
  | "initialRequirementsError"
  | "initialStateText"
  | "onChangeGraphField"
  | "onChangeInitialStateText"
>) {
  const requiredInitialState = initialRequirements?.required ?? [];
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
            className={!definition?.finish_point ? "border-destructive focus:border-destructive" : undefined}
            onChange={(value) => onChangeGraphField("finish_point", value)}
          />
        </Field>
      </InspectorBlock>

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
  nodeConfigText,
  paletteNodeTypes,
  selectedNode,
  onApplyNodeConfig,
  onChangeNode,
  onChangeNodeConfigText,
  onChangeNodeId,
  onDeleteNode,
}: Pick<
  GraphInspectorPanelProps,
  | "nodeConfigText"
  | "paletteNodeTypes"
  | "selectedNode"
  | "onApplyNodeConfig"
  | "onChangeNode"
  | "onChangeNodeConfigText"
  | "onChangeNodeId"
  | "onDeleteNode"
>) {
  const [jsonOpen, setJsonOpen] = useState(false);
  const [propertiesOpen, setPropertiesOpen] = useState(false);
  if (!selectedNode) return null;
  const configSchema = schemaForNodeType(paletteNodeTypes, selectedNode.type);
  const nodeConfig = isPlainRecord(selectedNode.config) ? selectedNode.config : {};

  return (
    <>
      <InspectorBlock title="Config">
        <JsonSchemaForm
          schema={configSchema}
          value={nodeConfig}
          onChange={(config) => onChangeNode((node) => ({ ...node, config }))}
        />
        <JsonConfigEditor
          open={jsonOpen}
          value={nodeConfigText}
          applyLabel="Apply Config"
          onOpenChange={setJsonOpen}
          onChange={onChangeNodeConfigText}
          onApply={onApplyNodeConfig}
        />
      </InspectorBlock>

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
                const schema = paletteNodeTypes.find((item) => item.type === event.target.value);
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
            {selectedNode.type && !paletteNodeTypes.some((item) => item.type === selectedNode.type) ? (
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

function CollapsibleInspectorBlock({
  title,
  open,
  onOpenChange,
  children,
}: {
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}) {
  const Icon = open ? ChevronDown : ChevronRight;
  return (
    <section className="border-b border-border last:border-b-0">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => onOpenChange(!open)}
        className="flex min-h-11 w-full items-center gap-2 px-3 text-left hover:bg-accent"
      >
        <Icon className="h-4 w-4 text-muted-foreground" />
        <span className="text-xs font-semibold uppercase text-muted-foreground">{title}</span>
      </button>
      {open ? <div className="grid gap-3 p-3 pt-0">{children}</div> : null}
    </section>
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
  | "onDeleteEdge"
>) {
  const [jsonOpen, setJsonOpen] = useState(false);
  const conditionSchema = schemaForCondition(conditions, selectedEdge?.condition?.type);
  const rawConditionConfig = selectedEdge?.condition?.config;
  const conditionConfig = isPlainRecord(rawConditionConfig) ? rawConditionConfig : {};
  return (
    <>
      <InspectorBlock title="Edge Properties">
        <div className="mb-2 flex items-center gap-2">
          <span className="text-xs font-medium text-muted-foreground">{selectedVirtualEdge ? selectedVirtualEdge.kind : selectedEdge?.condition?.type || "direct"}</span>
          <Button variant="ghost" size="icon" onClick={onDeleteEdge} title="Delete edge" className="ml-auto">
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
        {selectedVirtualEdge ? (
          <InfoRows
            rows={[
              ["from", displayNodeRef(selectedVirtualEdge.from, definition, visibleVirtualNodes)],
              ["to", displayNodeRef(selectedVirtualEdge.to, definition, visibleVirtualNodes)],
            ]}
          />
        ) : selectedEdge ? (
          <>
            <div className="grid grid-cols-2 gap-2">
              <Field label="From">
                <NodeSelect
                  value={selectedEdge.from}
                  nodes={definition?.nodes ?? []}
                  onChange={(value) => onChangeEdge((edge) => ({ ...edge, from: value }))}
                />
              </Field>
              <Field label="To">
                <NodeSelect
                  value={selectedEdge.to}
                  nodes={definition?.nodes ?? []}
                  onChange={(value) => onChangeEdge((edge) => ({ ...edge, to: value }))}
                />
              </Field>
            </div>
            <Field label="Condition">
              <Select
                value={selectedEdge.condition?.type ?? ""}
                onChange={(event) => {
                  const value = event.target.value;
                  const schema = conditions.find((condition) => condition.type === value);
                  onChangeEdge((edge) => ({
                    ...edge,
                    condition: value ? { type: value, config: exampleConfigForSchema(schema?.config_schema) } : undefined,
                  }));
                }}
              >
                <option value="">direct</option>
                {selectedEdge.condition?.type &&
                !conditions.some((condition) => condition.type === selectedEdge.condition?.type) ? (
                  <option value={selectedEdge.condition.type}>{selectedEdge.condition.type}</option>
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

      {selectedEdge?.condition && !selectedVirtualEdge ? (
        <InspectorBlock title="Condition Config">
          <JsonSchemaForm
            schema={conditionSchema}
            value={conditionConfig}
            onChange={(config) =>
              onChangeEdge((edge) => ({
                ...edge,
                condition: edge.condition ? { ...edge.condition, config } : edge.condition,
              }))
            }
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
}: {
  open: boolean;
  value: string;
  applyLabel: string;
  onOpenChange: (open: boolean) => void;
  onChange: (value: string) => void;
  onApply: () => void;
}) {
  return (
    <div className="grid gap-2">
      <div>
        <Button variant="ghost" size="sm" onClick={() => onOpenChange(!open)}>
          <Braces className="h-4 w-4" />
          {open ? "Hide JSON" : "Edit JSON"}
        </Button>
      </div>
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
  const schema = nodeTypes.find((nodeType) => nodeType.type === type)?.config_schema;
  return isPlainRecord(schema) ? schema : undefined;
}

function schemaForCondition(conditions: ConditionSchema[], type?: string): Record<string, unknown> | undefined {
  const schema = conditions.find((condition) => condition.type === type)?.config_schema;
  return isPlainRecord(schema) ? schema : undefined;
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
