import { AlertTriangle, FileJson } from "lucide-react";
import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import type {
  ConditionSchema,
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  InitialStateRequirements,
  NodeTypeSchema,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  StepRecord,
  ToolDefinition,
} from "../../../types";
import type { GraphLintIssue } from "./lint";
import { EdgeInspector } from "./EdgeInspector";
import { GraphDefinitionInspector } from "./GraphDefinitionInspector";
import { LoopInspector } from "./LoopInspector";
import { NodeInspector } from "./NodeInspector";
import { StatusText } from "../shared";
import { PanelHeader } from "./shared";
import type { InspectorMode } from "./types";

interface GraphInspectorPanelProps {
  conditions: ConditionSchema[];
  definition: GraphDefinition | null;
  definitionText: string;
  edgeConfigText: string;
  initialRequirements: InitialStateRequirements | null;
  directInitialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  initialStateText: string;
  inspectorMode: InspectorMode;
  inspectorTitle: string;
  lintIssues: GraphLintIssue[];
  nodeConfigText: string;
  paletteNodeTypes: NodeTypeSchema[];
  registry: RegistryInfo | null;
  registryLoaded: boolean;
  runtimeSettings: RuntimeSettings | null;
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
  toolDefinitions: ToolDefinition[];
  selectedEdge: GraphEdgeSpec | null;
  selectedNode: GraphNodeSpec | null;
  selectedVirtualLoop: VirtualGraphLoop | null;
  selectedVirtualEdge: VirtualGraphEdge | null;
  steps: StepRecord[];
  visibleVirtualNodes: GraphNodeSpec[];
  onApplyEdgeConfig: () => void;
  onApplyNodeConfig: () => void;
  onChangeDefinitionText: (value: string) => void;
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
  definitionText,
  edgeConfigText,
  initialRequirements,
  directInitialRequirements,
  initialRequirementsError,
  initialStateText,
  inspectorMode,
  inspectorTitle,
  lintIssues,
  nodeConfigText,
  paletteNodeTypes,
  registry,
  registryLoaded,
  runtimeSettings,
  onChangeRuntimeSettings,
  toolDefinitions,
  selectedEdge,
  selectedNode,
  selectedVirtualLoop,
  selectedVirtualEdge,
  steps,
  visibleVirtualNodes,
  onApplyEdgeConfig,
  onApplyNodeConfig,
  onChangeDefinitionText,
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
    <section className="min-h-0 min-w-0 overflow-x-hidden overflow-y-auto border-l border-border bg-panel pb-[45vh] [overflow-wrap:anywhere]">
      <PanelHeader icon={FileJson} title={inspectorTitle} />
      <LintPanel issues={lintIssues} onSelectIssue={onSelectLintIssue} />
      {inspectorMode === "graph" ? (
        <GraphDefinitionInspector
          definition={definition}
          definitionText={definitionText}
          initialRequirements={initialRequirements}
          directInitialRequirements={directInitialRequirements}
          initialRequirementsError={initialRequirementsError}
          initialStateText={initialStateText}
          runtimeSettings={runtimeSettings}
          registry={registry}
          toolDefinitions={toolDefinitions}
          onChangeRuntimeSettings={onChangeRuntimeSettings}
          onChangeDefinitionText={onChangeDefinitionText}
          onChangeGraphField={onChangeGraphField}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      ) : null}

      {inspectorMode === "node" && selectedNode ? (
        <NodeInspector
          definition={definition}
          nodeConfigText={nodeConfigText}
          paletteNodeTypes={paletteNodeTypes}
          registry={registry}
          registryLoaded={registryLoaded}
          toolDefinitions={toolDefinitions}
          selectedNode={selectedNode}
          steps={steps}
          onApplyNodeConfig={onApplyNodeConfig}
          onChangeNode={onChangeNode}
          onChangeNodeConfigText={onChangeNodeConfigText}
          onChangeNodeID={onChangeNodeId}
          onDeleteNode={onDeleteNode}
        />
      ) : null}

      {inspectorMode === "edge" && (selectedEdge || selectedVirtualEdge) ? (
        <EdgeInspector
          conditions={conditions}
          definition={definition}
          edgeConfigText={edgeConfigText}
          registry={registry}
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
              {issue.nodeID ? <span className="min-w-0 break-all font-mono text-muted-foreground">{issue.nodeID}</span> : null}
              {issue.path && !issue.nodeID ? <span className="min-w-0 break-all font-mono text-muted-foreground">{issue.path}</span> : null}
            </div>
            <div className="line-clamp-2 text-foreground">{issue.message}</div>
          </button>
        ))}
      </div>

    </section>
  );
}
