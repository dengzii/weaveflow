import { dynamicStatePortForName, resolveDefaultStatePath } from "../../../lib/graphEditor";
import { isPlainRecord } from "../../../lib/utils";
import type {
  DynamicStatePortDefinition,
  GraphDefinition,
  RegistryInfo,
  StateBinding,
  StateCapabilityDefinition,
  StateModuleDefinition,
  StatePortDefinition,
} from "../../../types";
import type { GraphLintIssue } from "./lintTypes";

interface ComponentBindingLintOptions {
  component: string;
  stableID: string;
  bindings: Record<string, StateBinding> | undefined;
  ports: StatePortDefinition[] | undefined;
  dynamicPorts: DynamicStatePortDefinition | undefined;
  selectedModules: StateModuleDefinition[];
  selectedCapabilities: Map<string, StateCapabilityDefinition>;
  registeredCapabilities: StateCapabilityDefinition[];
  rootCapabilities: Map<string, string>;
  nodeID?: string;
  edgeID?: string;
}

export function lintStateModules(
  definition: GraphDefinition,
  registry: RegistryInfo | null,
  issues: GraphLintIssue[]
): StateModuleDefinition[] {
  const refs = definition.state_modules ?? [];
  if (refs.length === 0) {
    issues.push({
      id: "graph-state-modules-missing",
      severity: "error",
      message: "Graph must reference at least one state module.",
      path: "state_modules",
    });
    return [];
  }

  const selected: StateModuleDefinition[] = [];
  const seen = new Set<string>();
  for (const [index, ref] of refs.entries()) {
    const name = ref.name?.trim();
    const version = ref.version?.trim();
    const key = `${name}\u0000${version}`;
    if (!name || !version) {
      issues.push({
        id: `graph-state-module-invalid-${index}`,
        severity: "error",
        message: "State module name and version are required.",
        path: "state_modules",
      });
      continue;
    }
    if (seen.has(key)) {
      issues.push({
        id: `graph-state-module-duplicate-${name}-${version}`,
        severity: "error",
        message: `State module "${name}" version "${version}" is duplicated.`,
        path: "state_modules",
      });
      continue;
    }
    seen.add(key);
    if (!registry) continue;
    const module = registry.state_modules.find((item) => item.name === name && item.version === version);
    if (!module) {
      issues.push({
        id: `graph-state-module-unknown-${name}-${version}`,
        severity: "error",
        message: `State module "${name}" version "${version}" is not registered.`,
        path: "state_modules",
      });
      continue;
    }
    selected.push(module);
  }
  return selected;
}

export function lintComponentBindings({
  component,
  stableID,
  bindings,
  ports,
  dynamicPorts,
  selectedModules,
  selectedCapabilities,
  registeredCapabilities,
  rootCapabilities,
  nodeID,
  edgeID,
}: ComponentBindingLintOptions): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];
  const bindingMap: Record<string, unknown> = isPlainRecord(bindings) ? { ...bindings } : {};
  const portMap = new Map((ports ?? []).map((port) => [port.name, port]));
  const contractPaths = new Map<string, { type: string; merge: string }>();

  if (ports || dynamicPorts) {
    for (const port of ports ?? []) {
      if (!bindingMap[port.name]) {
        const defaultPath = resolveDefaultStatePath(port.default_path, nodeID ?? "");
        if (defaultPath) bindingMap[port.name] = { path: defaultPath };
      }
      const path = bindingPath(bindingMap[port.name]);
      if (port.required && !path) {
        issues.push(bindingIssue(
          `${stableID}-binding-required-${port.name}`,
          `${component} requires state binding "${port.name}".`,
          `state.${port.name}`,
          nodeID,
          edgeID
        ));
      }
    }
    let dynamicCount = 0;
    for (const name of Object.keys(bindingMap)) {
      if (portMap.has(name)) continue;
      const dynamicPort = dynamicStatePortForName(name, dynamicPorts);
      if (dynamicPort) {
        dynamicCount += 1;
        portMap.set(name, dynamicPort);
        if (!bindingPath(bindingMap[name])) {
          issues.push(bindingIssue(
            `${stableID}-binding-required-${name}`,
            `${component} requires a path for dynamic state binding "${name}".`,
            `state.${name}`,
            nodeID,
            edgeID
          ));
        }
      } else {
        issues.push(bindingIssue(
          `${stableID}-binding-unknown-${name}`,
          `${component} binds unknown state port "${name}".`,
          `state.${name}`,
          nodeID,
          edgeID
        ));
      }
    }
    if (dynamicPorts && dynamicCount < (dynamicPorts.min_ports ?? 0)) {
      issues.push(bindingIssue(
        `${stableID}-binding-dynamic-min`,
        `${component} requires at least ${dynamicPorts.min_ports ?? 0} dynamic state binding(s).`,
        "state",
        nodeID,
        edgeID
      ));
    }
    if (dynamicPorts?.max_ports && dynamicCount > dynamicPorts.max_ports) {
      issues.push(bindingIssue(
        `${stableID}-binding-dynamic-max`,
        `${component} allows at most ${dynamicPorts.max_ports} dynamic state binding(s).`,
        "state",
        nodeID,
        edgeID
      ));
    }
  }

  for (const [name, rawBinding] of Object.entries(bindingMap)) {
    const path = bindingPath(rawBinding);
    if (!path) continue;
    const pathError = validateBindingPath(path);
    if (pathError) {
      issues.push(bindingIssue(
        `${stableID}-binding-path-${name}`,
        `${component} state binding "${name}" is invalid: ${pathError}`,
        `state.${name}`,
        nodeID,
        edgeID
      ));
      continue;
    }
    const port = portMap.get(name);
    if (!port) continue;

    if (port.capability) {
      const capability = selectedCapabilities.get(port.capability);
      if (!capability) {
        const registered = registeredCapabilities.some((item) => item.id === port.capability);
        issues.push(bindingIssue(
          `${stableID}-binding-capability-${name}`,
          registered
            ? `${component} state port "${name}" capability "${port.capability}" belongs to an unreferenced state module.`
            : `${component} state port "${name}" capability "${port.capability}" is not registered.`,
          `state.${name}`,
          nodeID,
          edgeID
        ));
        continue;
      }
      const existingCapability = rootCapabilities.get(path);
      if (existingCapability && existingCapability !== port.capability) {
        issues.push(bindingIssue(
          `${stableID}-binding-capability-conflict-${name}`,
          `State path "${path}" is bound to incompatible capabilities "${existingCapability}" and "${port.capability}".`,
          `state.${name}`,
          nodeID,
          edgeID
        ));
      } else {
        rootCapabilities.set(path, port.capability);
      }
      const exactField = moduleField(selectedModules, path);
      if (exactField && !schemasCompatible(exactField.schema, capability.schema)) {
        issues.push(bindingIssue(
          `${stableID}-binding-schema-${name}`,
          `${component} state port "${name}" capability schema conflicts with module field "${path}".`,
          `state.${name}`,
          nodeID,
          edgeID
        ));
      }
      for (const reference of port.contract?.fields ?? []) {
        const field = capability.fields.find((item) => item.name === reference.path);
        if (!field) {
          issues.push(bindingIssue(
            `${stableID}-binding-contract-${name}-${reference.path}`,
            `${component} state port "${name}" references missing capability field "${reference.path}".`,
            `state.${name}`,
            nodeID,
            edgeID
          ));
          continue;
        }
        const expandedPath = `${path}.${reference.path}`;
        const expandedModuleField = moduleField(selectedModules, expandedPath);
        if (expandedModuleField && !schemasCompatible(expandedModuleField.schema, field.schema)) {
          issues.push(bindingIssue(
            `${stableID}-binding-schema-${name}-${reference.path}`,
            `${component} state port "${name}" capability field "${expandedPath}" conflicts with module field "${expandedPath}".`,
            `state.${name}`,
            nodeID,
            edgeID
          ));
        }
        mergeContractPath(
          expandedPath,
          schemaType(field.schema),
          field.merge_strategy || "replace",
          component,
          stableID,
          name,
          contractPaths,
          issues,
          nodeID,
          edgeID
        );
      }
      continue;
    }

    const exactField = moduleField(selectedModules, path);
    if (exactField && !schemasCompatible(exactField.schema, port.schema)) {
      issues.push(bindingIssue(
        `${stableID}-binding-schema-${name}`,
        `${component} state port "${name}" type "${schemaType(port.schema)}" conflicts with module field "${path}" type "${schemaType(exactField.schema)}".`,
        `state.${name}`,
        nodeID,
        edgeID
      ));
    }
    mergeContractPath(
      path,
      schemaType(port.schema),
      port.merge_strategy || "replace",
      component,
      stableID,
      name,
      contractPaths,
      issues,
      nodeID,
      edgeID
    );
  }
  return issues;
}

function mergeContractPath(
  path: string,
  type: string,
  merge: string,
  component: string,
  stableID: string,
  portName: string,
  contractPaths: Map<string, { type: string; merge: string }>,
  issues: GraphLintIssue[],
  nodeID?: string,
  edgeID?: string
) {
  const existing = contractPaths.get(path);
  if (!existing) {
    contractPaths.set(path, { type, merge });
    return;
  }
  if (existing.type && type && existing.type !== type) {
    issues.push(bindingIssue(
      `${stableID}-binding-contract-type-${portName}-${path}`,
      `${component} resolves incompatible schema types "${existing.type}" and "${type}" at "${path}".`,
      `state.${portName}`,
      nodeID,
      edgeID
    ));
  }
  if (existing.merge !== merge) {
    issues.push(bindingIssue(
      `${stableID}-binding-contract-merge-${portName}-${path}`,
      `${component} resolves incompatible merge strategies "${existing.merge}" and "${merge}" at "${path}".`,
      `state.${portName}`,
      nodeID,
      edgeID
    ));
  }
}

function bindingIssue(
  id: string,
  message: string,
  path: string,
  nodeID?: string,
  edgeID?: string
): GraphLintIssue {
  return { id, severity: "error", message, path, nodeID, edgeID };
}

function bindingPath(value: unknown): string {
  return isPlainRecord(value) && typeof value.path === "string" ? value.path.trim() : "";
}

function validateBindingPath(path: string): string {
  const parts = path.split(".").map((part) => part.trim());
  if (parts.length < 2) return "a state section root cannot be bound";
  if (!["shared", "scopes", "internal", "runtime"].includes(parts[0])) {
    return "section must be shared, scopes, internal, or runtime";
  }
  if (parts.some((part) => !part)) return "path contains an empty segment";
  if (parts[0] === "internal" || parts[0] === "runtime") return `section "${parts[0]}" is reserved`;
  return "";
}

function moduleField(modules: StateModuleDefinition[], path: string) {
  for (const module of modules) {
    const field = module.fields?.find((item) => item.path === path);
    if (field) return field;
  }
  return undefined;
}

function schemasCompatible(
  left: Record<string, unknown> | undefined,
  right: Record<string, unknown> | undefined
): boolean {
  const leftType = schemaType(left);
  const rightType = schemaType(right);
  return !leftType || !rightType || leftType === rightType;
}

function schemaType(schema: Record<string, unknown> | undefined): string {
  return typeof schema?.type === "string" ? schema.type.trim() : "";
}
