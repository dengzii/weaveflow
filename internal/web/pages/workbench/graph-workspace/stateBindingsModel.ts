import { matchesDynamicStatePortName } from "../../../lib/graphEditor";
import type {
  DynamicStatePortDefinition,
  GraphDefinition,
  RegistryInfo,
  StateBinding,
  StatePortDefinition,
} from "../../../types";

export function compatibleBindingPaths(
  port: StatePortDefinition,
  ownerID: string,
  definition: GraphDefinition | null,
  registry: RegistryInfo | null
): string[] {
  const options: string[] = [];
  const add = (path: string | undefined) => {
    const value = path?.trim();
    if (value && !options.includes(value)) options.push(value);
  };
  const suffix = port.capability ? capabilityPathSuffix(port.capability) : port.name;
  add(`shared.${suffix}`);
  add(`scopes.${ownerID}.${suffix}`);

  const selectedModuleKeys = new Set(
    (definition?.state_modules ?? []).map((module) => `${module.name}\u0000${module.version}`)
  );
  if (!port.capability) {
    for (const module of registry?.state_modules ?? []) {
      if (!selectedModuleKeys.has(`${module.name}\u0000${module.version}`)) continue;
      for (const field of module.fields ?? []) {
        if (stateSchemasCompatible(field.schema, port.schema)) add(field.path);
      }
    }
  }

  for (const node of definition?.nodes ?? []) {
    const nodeType = registry?.node_types.find((item) => item.type === node.type);
    for (const [name, binding] of Object.entries(node.state ?? {})) {
      const candidatePort = nodeType?.state_ports?.find((item) => item.name === name);
      if (statePortsCompatible(port, candidatePort) && (Boolean(port.capability) || statePortWrites(candidatePort))) {
        add(binding.path);
      }
    }
  }
  for (const edge of definition?.edges ?? []) {
    const condition = edge.condition;
    if (!condition) continue;
    const conditionType = registry?.conditions.find((item) => item.type === condition.type);
    for (const [name, binding] of Object.entries(condition.state ?? {})) {
      const candidatePort = conditionType?.state_ports?.find((item) => item.name === name);
      if (port.capability && statePortsCompatible(port, candidatePort)) add(binding.path);
    }
  }
  return options;
}

export function bindingPathMetadata(
  path: string,
  port: StatePortDefinition,
  definition: GraphDefinition | null,
  registry: RegistryInfo | null,
  includeCapability = true,
  includeProducers = true,
  includeConsumers = true
): string {
  const details: string[] = [];
  if (port.capability) {
    if (includeCapability) details.push(`capability ${port.capability}`);
  } else if (stateSchemaType(port.schema)) {
    details.push(`type ${stateSchemaType(port.schema)}`);
  }

  const modules = (registry?.state_modules ?? [])
    .filter((module) => module.fields?.some((field) => field.path === path))
    .map((module) => `${module.name}@${module.version}`);
  if (modules.length > 0) details.push(`module ${modules.join(", ")}`);

  const producers: string[] = [];
  const consumers: string[] = [];
  for (const node of definition?.nodes ?? []) {
    const nodeType = registry?.node_types.find((item) => item.type === node.type);
    for (const [name, binding] of Object.entries(node.state ?? {})) {
      if (binding.path.trim() !== path) continue;
      const candidatePort = nodeType?.state_ports?.find((item) => item.name === name);
      if (statePortWrites(candidatePort)) producers.push(node.id);
      if (statePortReads(candidatePort)) consumers.push(node.id);
    }
  }
  if (includeProducers && producers.length > 0) details.push(`produced by ${uniqueStrings(producers).join(", ")}`);
  if (includeConsumers && consumers.length > 0) details.push(`consumed by ${uniqueStrings(consumers).join(", ")}`);
  return details.join(" · ") || (includeCapability ? "custom absolute path" : "");
}

export function dynamicStateAliasError(
  alias: string,
  currentName: string,
  bindings: Record<string, StateBinding>,
  staticNames: Set<string>,
  dynamicPorts: DynamicStatePortDefinition
): string {
  const normalizedAlias = alias.trim();
  if (!normalizedAlias) return "Alias is required.";
  if (staticNames.has(normalizedAlias)) return `Alias "${normalizedAlias}" conflicts with a static port.`;
  if (normalizedAlias !== currentName && Object.hasOwn(bindings, normalizedAlias)) {
    return `Alias "${normalizedAlias}" is already bound.`;
  }
  if (!matchesDynamicStatePortName(normalizedAlias, dynamicPorts)) {
    return `Alias must match ${dynamicPorts.name_pattern}.`;
  }
  return "";
}

export function stateSchemaType(schema: Record<string, unknown> | undefined): string {
  return typeof schema?.type === "string" ? schema.type.trim() : "";
}

export function sanitizeHTMLID(value: string): string {
  return value.replace(/[^a-zA-Z0-9_-]+/g, "-");
}

function statePortsCompatible(left: StatePortDefinition, right: StatePortDefinition | undefined): boolean {
  if (!right) return false;
  if (left.capability || right.capability) return Boolean(left.capability) && left.capability === right.capability;
  return stateSchemasCompatible(left.schema, right.schema);
}

function statePortReads(port: StatePortDefinition | undefined): boolean {
  if (!port) return false;
  if (port.capability) {
    return (port.contract?.fields ?? []).some((field) => field.mode === "read" || field.mode === "read_write");
  }
  return port.mode === "read" || port.mode === "read_write";
}

function statePortWrites(port: StatePortDefinition | undefined): boolean {
  if (!port) return false;
  if (port.capability) {
    return (port.contract?.fields ?? []).some((field) => field.mode === "write" || field.mode === "read_write");
  }
  return port.mode === "write" || port.mode === "read_write";
}

function stateSchemasCompatible(
  left: Record<string, unknown> | undefined,
  right: Record<string, unknown> | undefined
): boolean {
  const leftType = stateSchemaType(left);
  const rightType = stateSchemaType(right);
  return !leftType || !rightType || leftType === rightType;
}

function capabilityPathSuffix(capability: string): string {
  const parts = capability.split(".").filter(Boolean);
  const tail = parts.at(-1)?.match(/^v\d+$/i) ? parts.at(-2) : parts.at(-1);
  return tail?.replace(/[^a-zA-Z0-9_-]+/g, "_") || "capability";
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
