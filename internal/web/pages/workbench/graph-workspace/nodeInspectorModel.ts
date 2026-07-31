import { END_NODE_REF, graphNodePositions } from "../../../lib/graphEditor";
import { isPlainRecord } from "../../../lib/utils";
import type { GraphDefinition, GraphNodeSpec, NodeTypeSchema, StepRecord } from "../../../types";

export function analyzeNodeDetails(
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

export function configSchemaFields(schema: Record<string, unknown> | undefined): string[] {
  const properties = isPlainRecord(schema?.properties) ? schema.properties : {};
  const required = new Set(
    Array.isArray(schema?.required) ? schema.required.filter((item): item is string => typeof item === "string") : []
  );
  return Object.keys(properties)
    .sort((left, right) => left.localeCompare(right))
    .map((key) => (required.has(key) ? `${key} *` : key));
}

export function nodeTypeForType(nodeTypes: NodeTypeSchema[], type?: string): NodeTypeSchema | undefined {
  const normalizedType = type?.trim();
  if (!normalizedType) return undefined;
  return nodeTypes.find((nodeType) => nodeType.type.trim() === normalizedType);
}

export function schemaForNodeType(
  nodeTypes: NodeTypeSchema[],
  type?: string
): Record<string, unknown> | undefined {
  const schema = nodeTypeForType(nodeTypes, type)?.config_schema;
  return isPlainRecord(schema) ? schema : undefined;
}

function timeValue(value?: string): number {
  if (!value) return 0;
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}
