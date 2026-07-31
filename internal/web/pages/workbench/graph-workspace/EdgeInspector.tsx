import { useState } from "react";
import { Trash2 } from "lucide-react";
import type { VirtualGraphEdge } from "../../../components/GraphCanvas";
import { Button } from "../../../components/ui/button";
import { Select } from "../../../components/ui/select";
import { isPlainRecord } from "../../../lib/utils";
import type {
  ConditionSchema,
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  RegistryInfo,
} from "../../../types";
import { JSONConfigEditor } from "./JSONConfigEditor";
import { JsonSchemaForm } from "./schemaForm";
import { Field, InspectorBlock, NodeSelect } from "./shared";
import { StateBindingsBlock } from "./StateBindingsEditor";
import { conditionForType, conditionSchemaForType, edgeNodeOptions } from "./edgeInspectorModel";

interface EdgeInspectorProps {
  conditions: ConditionSchema[];
  definition: GraphDefinition | null;
  edgeConfigText: string;
  registry: RegistryInfo | null;
  selectedEdge: GraphEdgeSpec | null;
  selectedVirtualEdge: VirtualGraphEdge | null;
  visibleVirtualNodes: GraphNodeSpec[];
  onApplyEdgeConfig: () => void;
  onChangeEdge: (update: (edge: GraphEdgeSpec) => GraphEdgeSpec) => void;
  onChangeEdgeConfigText: (value: string) => void;
  onChangeVirtualEdge: (update: (edge: VirtualGraphEdge) => VirtualGraphEdge) => void;
  onDeleteEdge: () => void;
}

export function EdgeInspector({
  conditions,
  definition,
  edgeConfigText,
  registry,
  selectedEdge,
  selectedVirtualEdge,
  visibleVirtualNodes,
  onApplyEdgeConfig,
  onChangeEdge,
  onChangeEdgeConfigText,
  onChangeVirtualEdge,
  onDeleteEdge,
}: EdgeInspectorProps) {
  const [jsonOpen, setJSONOpen] = useState(false);
  const activeEdge = selectedEdge ?? selectedVirtualEdge;
  const selectedCondition = selectedEdge?.condition ?? selectedVirtualEdge?.condition;
  const selectedConditionType = selectedCondition?.type;
  const selectedConditionSchema = conditions.find((condition) => condition.type === selectedConditionType);
  const conditionSchema = conditionSchemaForType(conditions, selectedConditionType);
  const conditionConfig = isPlainRecord(selectedCondition?.config) ? selectedCondition.config : {};
  const { sourceNodes, targetNodes } = edgeNodeOptions(definition, visibleVirtualNodes, selectedVirtualEdge);

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
    const condition = conditionForType(conditions, type, activeEdge?.from ?? "");
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
          <span className="text-xs font-medium text-muted-foreground">{selectedConditionType || "Direct"}</span>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={onDeleteEdge}
            title="Delete edge"
            aria-label="Delete edge"
            className="ml-auto"
          >
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
              <Select value={selectedConditionType ?? ""} onChange={(event) => changeCondition(event.target.value)}>
                <option value="">Direct</option>
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
        <>
          <StateBindingsBlock
            ownerID={`${activeEdge?.from ?? "edge"}_condition`}
            ports={selectedConditionSchema?.state_ports ?? []}
            dynamicPorts={selectedConditionSchema?.dynamic_state_ports}
            bindings={selectedCondition.state}
            definition={definition}
            registry={registry}
            onChange={(state) => {
              if (selectedVirtualEdge) {
                onChangeVirtualEdge((edge) => ({
                  ...edge,
                  condition: edge.condition ? { ...edge.condition, state } : edge.condition,
                }));
                return;
              }
              onChangeEdge((edge) => ({
                ...edge,
                condition: edge.condition ? { ...edge.condition, state } : edge.condition,
              }));
            }}
          />
          <InspectorBlock title="Condition Config">
            <JsonSchemaForm schema={conditionSchema} value={conditionConfig} onChange={changeConditionConfig} />
            <JSONConfigEditor
              open={jsonOpen}
              value={edgeConfigText}
              applyLabel="Apply condition"
              onOpenChange={setJSONOpen}
              onChange={onChangeEdgeConfigText}
              onApply={onApplyEdgeConfig}
            />
          </InspectorBlock>
        </>
      ) : null}
    </>
  );
}
