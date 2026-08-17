import { isPlainRecord, stringifyJSON } from "../../../lib/utils";
import type { ToolDefinition } from "../../../types";

const unsafePathSegments = new Set(["__proto__", "prototype", "constructor"]);

export interface SchemaFormIssue {
  path: string;
  message: string;
}

export type JSONControlParseResult =
  | { ok: true; value: unknown }
  | { ok: false };

export function validateSchemaValue(
  schema: unknown,
  value: unknown,
  basePath = ""
): SchemaFormIssue[] {
  if (!isPlainRecord(schema)) return [];
  const issues: SchemaFormIssue[] = [];
  const type = schemaType(schema, value);
  const required = requiredKeys(schema);

  if (basePath && !isEmptyValue(value) && schema["x-control"] !== "json") {
    const typeIssue = validateSchemaType(type, value, basePath);
    if (typeIssue) issues.push(typeIssue);
  }

  const enumValues = Array.isArray(schema.enum) ? schema.enum : [];
  if (enumValues.length > 0 && !isEmptyValue(value) && !enumValues.some((item) => Object.is(item, value))) {
    issues.push({
      path: basePath,
      message: "Value must match one of the allowed options.",
    });
  }

  const properties = schemaProperties(schema);
  if (type === "array" && Array.isArray(value)) {
    const minItems = typeof schema.minItems === "number" ? schema.minItems : 0;
    if (value.length < minItems) {
      issues.push({ path: basePath, message: `Expected at least ${minItems} item${minItems === 1 ? "" : "s"}.` });
    }
    if (isPlainRecord(schema.items)) {
      value.forEach((item, index) => {
        issues.push(...validateSchemaValue(schema.items, item, joinPath(basePath, String(index))));
      });
    }
  }
  if (properties.length === 0) return issues;

  const recordValue = isPlainRecord(value) ? value : {};
  const propertySchemas = new Map(properties);
  for (const key of required) {
    if (requiredFieldMissing(recordValue, key, propertySchemas.get(key))) {
      issues.push({
        path: joinPath(basePath, key),
        message: "Required field.",
      });
    }
  }

  for (const [key, propertySchema] of properties) {
    const nextPath = joinPath(basePath, key);
    const nextValue = Object.prototype.hasOwnProperty.call(recordValue, key) ? recordValue[key] : undefined;
    if (!isEmptyValue(nextValue)) {
      issues.push(...validateSchemaValue(propertySchema, nextValue, nextPath));
    }
  }

  return issues;
}

export function schemaProperties(schema: Record<string, unknown>): Array<[string, unknown]> {
  if (!isPlainRecord(schema.properties)) return [];
  return Object.entries(schema.properties);
}

export function schemaType(schema: Record<string, unknown>, value: unknown): string {
  const rawType = Array.isArray(schema.type) ? schema.type.find((item) => item !== "null") : schema.type;
  if (typeof rawType === "string" && rawType.trim()) return rawType;
  if (isPlainRecord(schema.properties)) return "object";
  if (Array.isArray(schema.enum) && schema.enum.length > 0) return typeof schema.enum[0];
  if (Array.isArray(value)) return "array";
  if (isPlainRecord(value)) return "object";
  if (typeof value === "number") return Number.isInteger(value) ? "integer" : "number";
  if (typeof value === "boolean") return "boolean";
  return "string";
}

export function isStringArraySchema(schema: Record<string, unknown>): boolean {
  if (!isPlainRecord(schema.items)) return false;
  return schemaType(schema.items, undefined) === "string";
}

export function normalizeConfigSchema(schema: unknown): Record<string, unknown> | undefined {
  return isPlainRecord(schema) ? schema : undefined;
}

export function isObjectArraySchema(schema: Record<string, unknown>): boolean {
  if (!isPlainRecord(schema.items)) return false;
  return schema["x-control"] === "object-list" || schemaType(schema.items, undefined) === "object";
}

export function isToolIDsField(path: string, name: string): boolean {
  const normalizedName = name.trim().toLowerCase();
  if (normalizedName === "tool_ids") return true;
  const parts = path.split(".").map((part) => part.trim().toLowerCase()).filter(Boolean);
  return parts.at(-1) === "tool_ids";
}

export function isModelIDField(
  path: string,
  name: string,
  schema: Record<string, unknown>
): boolean {
  if (schema["x-control"] === "model-id") return true;
  const title = typeof schema.title === "string" ? schema.title : "";
  return [name, path.split(".").at(-1) ?? "", title].some((value) => (
    value.trim().toLowerCase().replace(/[\s_-]+/g, "") === "modelid"
  ));
}

export function getPathValue(root: Record<string, unknown>, path: string): unknown {
  const parts = schemaPathSegments(path);
  if (!parts) return undefined;
  let current: unknown = root;
  for (const part of parts) {
    if (!isPlainRecord(current) || !Object.prototype.hasOwnProperty.call(current, part)) return undefined;
    current = current[part];
  }
  return current;
}

export function setPathValue(root: Record<string, unknown>, path: string, value: unknown): Record<string, unknown> {
  const parts = schemaPathSegments(path);
  if (!parts) return { ...root };
  const nextRoot = { ...root };
  let cursor = nextRoot;
  for (const part of parts.slice(0, -1)) {
    const existing = cursor[part];
    cursor[part] = isPlainRecord(existing) ? { ...existing } : {};
    cursor = cursor[part] as Record<string, unknown>;
  }
  const leaf = parts[parts.length - 1];
  if (value === undefined) {
    delete cursor[leaf];
  } else {
    cursor[leaf] = value;
  }
  return nextRoot;
}

export function coerceEnumValue(value: string, enumValues: unknown[]): unknown {
  const match = enumValues.find((item) => String(item) === value);
  return match ?? value;
}

export function formatStructuredValue(value: unknown, type: string): string {
  if (value === undefined || value === null) return type === "array" ? "[]" : "{}";
  if (typeof value === "string") return value;
  return stringifyJSON(value);
}

export function parseJSONControlText(text: string): JSONControlParseResult {
  if (!text.trim()) return { ok: true, value: undefined };
  try {
    return { ok: true, value: JSON.parse(text) as unknown };
  } catch {
    return { ok: false };
  }
}

export function formatJSONControlValue(value: unknown): string {
  if (value === undefined) return "";
  return stringifyJSON(value);
}

export function stringListValues(value: unknown): string[] {
  if (Array.isArray(value)) return value.map((item) => (item == null ? "" : String(item)));
  if (typeof value !== "string") return [];

  const text = value.trim();
  if (!text) return [];

  try {
    const parsed = JSON.parse(text) as unknown;
    if (Array.isArray(parsed)) return parsed.map((item) => (item == null ? "" : String(item)));
  } catch {
    // Existing line-based values remain editable when they are not JSON.
  }

  return text
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function uniqueToolDefinitions(tools: ToolDefinition[]): ToolDefinition[] {
  const seen = new Set<string>();
  const result: ToolDefinition[] = [];
  for (const tool of tools) {
    const id = tool.id?.trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    result.push({ ...tool, id });
  }
  return result;
}

export function toolLabel(tool: ToolDefinition): string {
  const name = tool.name?.trim();
  if (!name || name === tool.id) return tool.id;
  return `${tool.id} (${name})`;
}

export function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    result.push(trimmed);
  }
  return result;
}

function validateSchemaType(type: string, value: unknown, path: string): SchemaFormIssue | null {
  switch (type) {
    case "string":
      return typeof value === "string" ? null : { path, message: "Expected a string." };
    case "integer":
      return typeof value === "number" && Number.isInteger(value) ? null : { path, message: "Expected an integer." };
    case "number":
      return typeof value === "number" && Number.isFinite(value) ? null : { path, message: "Expected a number." };
    case "boolean":
      return typeof value === "boolean" ? null : { path, message: "Expected true or false." };
    case "array":
      return Array.isArray(value) ? null : { path, message: "Expected a JSON array." };
    case "object":
      return isPlainRecord(value) ? null : { path, message: "Expected a JSON object." };
    default:
      return null;
  }
}

function requiredKeys(schema: Record<string, unknown>): string[] {
  if (!Array.isArray(schema.required)) return [];
  return schema.required.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

function joinPath(prefix: string, key: string): string {
  return prefix ? `${prefix}.${key}` : key;
}

function isEmptyValue(value: unknown): boolean {
  return value === undefined || value === null || (typeof value === "string" && value.trim() === "");
}

function requiredFieldMissing(record: Record<string, unknown>, key: string, schema: unknown): boolean {
  if (!Object.prototype.hasOwnProperty.call(record, key) || record[key] === undefined) return true;
  if (isPlainRecord(schema) && schema["x-control"] === "json") return false;
  return isEmptyValue(record[key]);
}

function schemaPathSegments(path: string): string[] | null {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  if (parts.length === 0 || parts.some((part) => unsafePathSegments.has(part))) return null;
  return parts;
}
