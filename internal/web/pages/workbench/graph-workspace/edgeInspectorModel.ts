import type { VirtualGraphEdge } from "../../../components/GraphCanvas";
import { END_NODE_REF, initialStateBindings } from "../../../lib/graphEditor";
import { exampleConfigForSchema } from "../../../lib/jsonSchemaDefaults";
import { isPlainRecord } from "../../../lib/utils";
import type {
  ConditionSchema,
  GraphConditionSpec,
  GraphDefinition,
  GraphNodeSpec,
} from "../../../types";

export function conditionSchemaForType(
  conditions: ConditionSchema[],
  type?: string
): Record<string, unknown> | undefined {
  const schema = conditions.find((condition) => condition.type === type)?.config_schema;
  return isPlainRecord(schema) ? schema : undefined;
}

export function conditionForType(
  conditions: ConditionSchema[],
  type: string,
  ownerID: string
): GraphConditionSpec | undefined {
  if (!type) return undefined;
  const schema = conditions.find((condition) => condition.type === type);
  return {
    type,
    config: exampleConfigForSchema(schema?.config_schema),
    state: initialStateBindings(schema?.state_ports, ownerID),
  };
}

export function edgeNodeOptions(
  definition: GraphDefinition | null,
  visibleVirtualNodes: GraphNodeSpec[],
  selectedVirtualEdge: VirtualGraphEdge | null
): { sourceNodes: GraphNodeSpec[]; targetNodes: GraphNodeSpec[] } {
  const realNodes = definition?.nodes ?? [];
  const endNodes = visibleVirtualNodes.filter((node) => node.id === END_NODE_REF);
  if (!selectedVirtualEdge) {
    return { sourceNodes: realNodes, targetNodes: [...realNodes, ...endNodes] };
  }
  return {
    sourceNodes: selectedVirtualEdge.kind === "entry"
      ? visibleVirtualNodes.filter((node) => node.type === "start")
      : realNodes,
    targetNodes: selectedVirtualEdge.kind === "finish"
      ? visibleVirtualNodes.filter((node) => node.type === "end")
      : realNodes,
  };
}
