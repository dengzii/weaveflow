export function exampleValueForSchema(schema: unknown): unknown {
  const normalizedSchema = normalizeConfigSchema(schema);
  if (!normalizedSchema) return undefined;

  if ("default" in normalizedSchema) return cloneJSONValue(normalizedSchema.default);

  const enumValues = Array.isArray(normalizedSchema.enum) ? normalizedSchema.enum : [];
  if (enumValues.length > 0) return enumValues[0];
  if ("const" in normalizedSchema) return normalizedSchema.const;

  const typeName = Array.isArray(normalizedSchema.type) ? normalizedSchema.type[0] : normalizedSchema.type;
  switch (typeName) {
    case "string":
      return "example";
    case "integer":
    case "number":
      return 1;
    case "boolean":
      return true;
    case "array": {
      const itemValue = exampleValueForSchema(normalizedSchema.items);
      return itemValue === undefined ? [] : [itemValue];
    }
    case "object":
      return exampleConfigForSchema(normalizedSchema);
    default:
      return undefined;
  }
}

export function exampleConfigForSchema(schema: unknown): Record<string, unknown> {
  const normalizedSchema = normalizeConfigSchema(schema);
  if (!normalizedSchema || !isRecord(normalizedSchema.properties)) return {};

  const config: Record<string, unknown> = {};
  for (const [key, propertySchema] of Object.entries(normalizedSchema.properties)) {
    const wellKnown = wellKnownExampleValue(key);
    if (wellKnown !== undefined) {
      config[key] = wellKnown;
    } else if (requiredKeys(normalizedSchema).includes(key) || schemaHasDefault(propertySchema)) {
      const value = exampleValueForSchema(propertySchema);
      if (value !== undefined) config[key] = value;
    }
  }
  return config;
}

function requiredKeys(schema: Record<string, unknown>): string[] {
  if (!Array.isArray(schema.required)) return [];
  return schema.required.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

function schemaHasDefault(schema: unknown): boolean {
  const normalizedSchema = normalizeConfigSchema(schema);
  return Boolean(normalizedSchema && Object.prototype.hasOwnProperty.call(normalizedSchema, "default"));
}

function wellKnownExampleValue(key: string): unknown {
  switch (key) {
    case "graph_ref":
      return "example_graph";
    case "max_iterations":
      return 2;
    case "tool_ids":
      return [];
    case "limit":
      return 5;
    case "interrupt_message":
      return "Approval required";
    default:
      return undefined;
  }
}

function cloneJSONValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(cloneJSONValue);
  if (isRecord(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, cloneJSONValue(item)]));
  }
  return value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function normalizeConfigSchema(schema: unknown): Record<string, unknown> | undefined {
  return isRecord(schema) ? schema : undefined;
}
