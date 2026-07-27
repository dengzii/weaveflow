import type {
  ConditionSchema,
  DynamicStatePortDefinition,
  GraphDefinition,
  GraphEdgeSpec,
  GraphNodeSpec,
  NodeTypeSchema,
  RegistryInfo,
  StateAccessMode,
  StateBinding,
  StateMergeStrategy,
  StateModuleDefinition,
  StatePortDefinition,
} from "../types";
import { exampleConfigForSchema } from "./jsonSchemaDefaults";

export const START_NODE_REF = "__start__";
export const END_NODE_REF = "__end__";

export interface NodePosition {
  x: number;
  y: number;
}

export interface ResolvedStateContractField {
  path: string;
  mode: StateAccessMode;
  required: boolean;
  mergeStrategy: StateMergeStrategy;
  type: string;
}

export function resolvedStatePortContract(
  port: StatePortDefinition,
  binding: StateBinding | undefined,
  registry: RegistryInfo | null
): ResolvedStateContractField[] {
  const root = binding?.path.trim() ?? "";
  if (!root) return [];
  if (!port.capability) {
    if (!port.mode) return [];
    return [{
      path: root,
      mode: port.mode,
      required: Boolean(port.required && stateAccessReads(port.mode)),
      mergeStrategy: port.merge_strategy ?? "replace",
      type: stateSchemaType(port.schema),
    }];
  }

  const capability = registry?.capabilities.find((item) => item.id === port.capability);
  if (!capability) return [];
  const fields = new Map(capability.fields.map((field) => [field.name, field]));
  return (port.contract?.fields ?? []).flatMap((reference) => {
    const field = fields.get(reference.path);
    if (!field) return [];
    return [{
      path: `${root}.${reference.path}`,
      mode: reference.mode,
      required: Boolean(reference.required && stateAccessReads(reference.mode)),
      mergeStrategy: field.merge_strategy ?? "replace",
      type: stateSchemaType(field.schema),
    }];
  });
}

function stateAccessReads(mode: StateAccessMode): boolean {
  return mode === "read" || mode === "read_write";
}

function stateSchemaType(schema: Record<string, unknown> | undefined): string {
  return typeof schema?.type === "string" ? schema.type.trim() : "";
}

export function createGraphDefinition(
  name: string,
  nodeType?: NodeTypeSchema,
  stateModules: StateModuleDefinition[] = []
): GraphDefinition {
  const graphName = slugify(name || "debug_graph", "debug_graph");
  const nodes = nodeType ? [createNodeFromType(nodeType, [])] : [];
  return {
    version: "2.0",
    name: graphName,
    state_modules: stateModules.map(({ name: moduleName, version }) => ({ name: moduleName, version })),
    entry_point: nodes[0]?.id,
    nodes,
    edges: [],
  };
}

export function createNodeFromType(nodeType: NodeTypeSchema, existingNodes: GraphNodeSpec[]): GraphNodeSpec {
  const baseID = slugify(nodeType.type || nodeType.title || "node", "node");
  const id = uniqueNodeId(baseID, existingNodes);
  const stateBindings = initialStateBindings(nodeType.state_ports, id);
  return {
    id,
    name: uniqueNodeName(nodeType.title || id, existingNodes),
    type: nodeType.type || "node",
    config: exampleConfigForSchema(nodeType.config_schema),
    state: stateBindings,
  };
}

export function addNodeToGraph(
  definition: GraphDefinition,
  nodeType: NodeTypeSchema,
  position?: NodePosition
): GraphDefinition {
  const node = createNodeFromType(nodeType, definition.nodes);
  const next: GraphDefinition = {
    ...definition,
    nodes: [...definition.nodes, node],
    entry_point: definition.entry_point || node.id,
  };
  return withNodePosition(next, node.id, position ?? nextDefaultPosition(definition));
}

export function updateGraphNode(
  definition: GraphDefinition,
  nodeID: string,
  update: (node: GraphNodeSpec) => GraphNodeSpec
): GraphDefinition {
  return {
    ...definition,
    nodes: definition.nodes.map((node) => (node.id === nodeID ? update({ ...node }) : node)),
  };
}

export function renameGraphNode(definition: GraphDefinition, oldID: string, nextID: string): GraphDefinition {
  const id = nextID.trim();
  if (!id || id === oldID) return definition;
  if (definition.nodes.some((node) => node.id === id && node.id !== oldID)) return definition;

  const positions = graphNodePositions(definition);
  const oldPosition = positions.get(oldID);
  const nextPositions = new Map(positions);
  nextPositions.delete(oldID);
  if (oldPosition) nextPositions.set(id, oldPosition);

  return withNodePositions({
    ...definition,
    entry_point: definition.entry_point === oldID ? id : definition.entry_point,
    finish_point: definition.finish_point === oldID ? id : definition.finish_point,
    nodes: definition.nodes.map((node) => (node.id === oldID ? { ...node, id } : node)),
    edges: (definition.edges ?? []).map((edge) => ({
      ...edge,
      from: edge.from === oldID ? id : edge.from,
      to: edge.to === oldID ? id : edge.to,
    })),
  }, nextPositions);
}

export function removeGraphNode(definition: GraphDefinition, nodeID: string): GraphDefinition {
  const nodes = definition.nodes.filter((node) => node.id !== nodeID);
  const validIds = new Set(nodes.map((node) => node.id));
  validIds.add(END_NODE_REF);
  const positions = graphNodePositions(definition);
  positions.delete(nodeID);
  return withNodePositions({
    ...definition,
    entry_point: definition.entry_point === nodeID ? undefined : definition.entry_point,
    finish_point: definition.finish_point === nodeID ? undefined : definition.finish_point,
    nodes,
    edges: (definition.edges ?? []).filter((edge) => validIds.has(edge.from) && validIds.has(edge.to)),
  }, positions);
}

export function addGraphEdge(
  definition: GraphDefinition,
  from: string,
  to: string,
  conditionType?: string,
  conditions: ConditionSchema[] = []
): GraphDefinition {
  const source = from.trim();
  const target = to.trim();
  if (!source || !target) return definition;
  if (findGraphEdgeIndex(definition, source, target) >= 0) return definition;
  const conditionSchema = conditions.find((item) => item.type === conditionType?.trim());
  const condition = conditionType?.trim()
    ? {
        type: conditionType.trim(),
        config: exampleConfigForSchema(conditionSchema?.config_schema),
        state: initialStateBindings(conditionSchema?.state_ports, source),
      }
    : undefined;
  const edge: GraphEdgeSpec = condition ? { from: source, to: target, condition } : { from: source, to: target };
  return {
    ...definition,
    edges: [...(definition.edges ?? []), edge],
  };
}

export function initialStateBindings(ports: StatePortDefinition[] | undefined, ownerID = ""): Record<string, StateBinding> {
  const bindings: Record<string, StateBinding> = {};
  for (const port of ports ?? []) {
    const defaultPath = resolveDefaultStatePath(port.default_path, ownerID);
    if (defaultPath || port.required) bindings[port.name] = { path: defaultPath };
  }
  return bindings;
}

export function dynamicStatePortForName(
  name: string,
  dynamic: DynamicStatePortDefinition | undefined
): StatePortDefinition | undefined {
  if (!dynamic || !matchesDynamicStatePortName(name, dynamic)) return undefined;
  return {
    name,
    description: dynamic.description,
    required: true,
    schema: dynamic.schema,
    mode: dynamic.mode,
    merge_strategy: dynamic.merge_strategy,
  };
}

export function matchesDynamicStatePortName(name: string, dynamic: DynamicStatePortDefinition | undefined): boolean {
  if (!dynamic) return false;
  try {
    return new RegExp(`^(?:${dynamic.name_pattern})$`).test(name);
  } catch {
    return false;
  }
}

export function nextDynamicStatePortName(
  bindings: Record<string, StateBinding> | undefined,
  staticPorts: StatePortDefinition[],
  dynamic: DynamicStatePortDefinition
): string | null {
  const occupied = new Set([...staticPorts.map((port) => port.name), ...Object.keys(bindings ?? {})]);
  for (let index = 1; index <= 10_000; index += 1) {
    const candidate = index === 1 ? "input" : `input_${index}`;
    if (!occupied.has(candidate) && matchesDynamicStatePortName(candidate, dynamic)) return candidate;
  }
  return null;
}

export function resolveDefaultStatePath(template: string | undefined, ownerID: string): string {
  const path = template?.trim() ?? "";
  if (!path) return "";
  const normalizedOwnerID = (ownerID.trim() || "node").replaceAll(".", "_");
  return path.replaceAll("{node_id}", normalizedOwnerID);
}

export function updateGraphEdge(
  definition: GraphDefinition,
  edgeID: string,
  update: (edge: GraphEdgeSpec) => GraphEdgeSpec
): GraphDefinition {
  const edges = definition.edges ?? [];
  const targetIndex = edges.findIndex((edge, index) => graphEdgeId(edge, index) === edgeID);
  if (targetIndex < 0) return definition;
  const nextEdge = update(cloneEdge(edges[targetIndex]));
  const source = nextEdge.from.trim();
  const target = nextEdge.to.trim();
  if (!source || !target) return definition;
  if (findGraphEdgeIndex(definition, source, target, targetIndex) >= 0) return definition;
  return {
    ...definition,
    edges: edges.map((edge, index) => (index === targetIndex ? { ...nextEdge, from: source, to: target } : edge)),
  };
}

export function removeGraphEdge(definition: GraphDefinition, edgeID: string): GraphDefinition {
  return {
    ...definition,
    edges: (definition.edges ?? []).filter((edge, index) => graphEdgeId(edge, index) !== edgeID),
  };
}

export function graphEdgeId(edge: GraphEdgeSpec, index: number): string {
  return `${edge.from}->${edge.to}:${edge.condition?.type ?? "direct"}:${index}`;
}

export function findGraphEdgeIndex(
  definition: GraphDefinition,
  from: string,
  to: string,
  excludeIndex = -1
): number {
  const source = from.trim();
  const target = to.trim();
  if (!source || !target) return -1;
  return (definition.edges ?? []).findIndex(
    (edge, index) => index !== excludeIndex && edge.from === source && edge.to === target
  );
}

export function graphNodePositions(definition: GraphDefinition): Map<string, NodePosition> {
  const metadata = definition.metadata;
  const web = isRecord(metadata?.web) ? metadata.web : undefined;
  const rawPositions = isRecord(web?.positions) ? web.positions : undefined;
  const result = new Map<string, NodePosition>();
  for (const [nodeID, value] of Object.entries(rawPositions ?? {})) {
    if (!isRecord(value)) continue;
    const x = Number(value.x);
    const y = Number(value.y);
    if (Number.isFinite(x) && Number.isFinite(y)) result.set(nodeID, { x, y });
  }
  return result;
}

export function withNodePosition(definition: GraphDefinition, nodeID: string, position: NodePosition): GraphDefinition {
  const positions = graphNodePositions(definition);
  positions.set(nodeID, position);
  return withNodePositions(definition, positions);
}

export function withNodePositions(definition: GraphDefinition, positions: Map<string, NodePosition>): GraphDefinition {
  const metadata = { ...(definition.metadata ?? {}) };
  const web = isRecord(metadata.web) ? { ...metadata.web } : {};
  web.positions = Object.fromEntries([...positions.entries()]);
  metadata.web = web;
  return { ...definition, metadata };
}

function nextDefaultPosition(definition: GraphDefinition): NodePosition {
  const positions = [...graphNodePositions(definition).values()];
  if (positions.length === 0) {
    return {
      x: (definition.nodes.length % 4) * 260,
      y: Math.floor(definition.nodes.length / 4) * 130,
    };
  }
  const maxX = Math.max(...positions.map((position) => position.x));
  const maxY = Math.max(...positions.map((position) => position.y));
  return { x: maxX + 260, y: maxY % 520 };
}

function uniqueNodeId(baseID: string, existingNodes: GraphNodeSpec[]): string {
  const used = new Set(existingNodes.map((node) => node.id));
  if (!used.has(baseID)) return baseID;
  for (let index = 2; index < 1000; index += 1) {
    const id = `${baseID}_${index}`;
    if (!used.has(id)) return id;
  }
  return `${baseID}_${Date.now().toString(36)}`;
}

function uniqueNodeName(baseName: string, existingNodes: GraphNodeSpec[]): string {
  const name = baseName.trim();
  const used = new Set(existingNodes.map((node) => node.name?.trim() || node.id));
  if (!used.has(name)) return name;
  for (let index = 1; index < 1000; index += 1) {
    const candidate = `${name} ${index}`;
    if (!used.has(candidate)) return candidate;
  }
  return `${name} ${Date.now().toString(36)}`;
}

function slugify(value: string, fallback: string): string {
  const normalized = value
    .trim()
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/[^a-zA-Z0-9_.-]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .toLowerCase();
  return normalized || fallback;
}

function cloneEdge(edge: GraphEdgeSpec): GraphEdgeSpec {
  return {
    ...edge,
    condition: edge.condition
      ? { ...edge.condition, config: edge.condition.config ? { ...edge.condition.config } : undefined }
      : undefined,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
