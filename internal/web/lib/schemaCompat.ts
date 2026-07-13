type JsonRecord = Record<string, unknown>;

const fieldNameKeys = ["name", "key", "path", "id"] as const;
const fieldTitleKeys = ["title", "label"] as const;
const fieldDescriptionKeys = ["description", "help", "hint"] as const;
const fieldTypeKeys = ["type", "value_type", "valueType", "schema_type", "schemaType"] as const;
const fieldDefaultKeys = ["default", "default_value", "defaultValue"] as const;
const fieldEnumKeys = ["enum", "options", "choices", "values"] as const;

export function normalizeConfigSchema(schema: unknown): JsonRecord | undefined {
  if (Array.isArray(schema)) {
    const fieldProperties = fieldListProperties(schema);
    return fieldProperties ? { type: "object", properties: fieldProperties.properties, required: fieldProperties.required } : undefined;
  }
  if (!isRecord(schema)) return undefined;

  const fieldProperties = fieldListProperties(schema.fields);
  if (!fieldProperties) return schema;

  const properties = isRecord(schema.properties) ? { ...fieldProperties.properties, ...schema.properties } : fieldProperties.properties;
  const required = uniqueStrings([...requiredKeys(schema), ...fieldProperties.required]);
  const { fields: _fields, ...rest } = schema;
  return {
    ...rest,
    type: typeof schema.type === "string" && schema.type.trim() ? schema.type : "object",
    properties,
    required,
  };
}

function fieldListProperties(fields: unknown): { properties: JsonRecord; required: string[] } | null {
  const fieldEntries = fieldEntriesFromList(fields);
  if (!fieldEntries) return null;

  const properties: JsonRecord = {};
  const required: string[] = [];
  for (const [entryName, field] of fieldEntries) {
    if (!isRecord(field)) continue;
    const name = stringFromKeys(field, fieldNameKeys) || entryName;
    if (!name) continue;
    setPropertySchema(properties, name, schemaFromField(field, fieldTitle(name)));
    if (isRequiredField(field)) addRequiredPath(properties, required, name);
  }

  if (Object.keys(properties).length === 0) return null;
  return { properties, required };
}

function fieldEntriesFromList(fields: unknown): Array<[string, unknown]> | null {
  if (Array.isArray(fields)) return fields.map((field) => ["", field]);
  if (isRecord(fields)) return Object.entries(fields);
  return null;
}

function setPropertySchema(properties: JsonRecord, path: string, schema: JsonRecord) {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  if (parts.length <= 1) {
    properties[path] = schema;
    return;
  }

  let cursor = properties;
  for (const part of parts.slice(0, -1)) {
    const existing = cursor[part];
    if (!isRecord(existing)) {
      cursor[part] = { type: "object", title: part, properties: {} };
    }
    const objectSchema = cursor[part] as JsonRecord;
    if (!isRecord(objectSchema.properties)) objectSchema.properties = {};
    cursor = objectSchema.properties as JsonRecord;
  }
  const leaf = parts[parts.length - 1];
  if (leaf) cursor[leaf] = schema;
}

function fieldTitle(path: string): string {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  return parts.at(-1) ?? path;
}

function addRequiredPath(properties: JsonRecord, rootRequired: string[], path: string) {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  if (parts.length <= 1) {
    rootRequired.push(path);
    return;
  }

  rootRequired.push(parts[0]);
  let cursor = properties;
  for (let index = 0; index < parts.length - 1; index += 1) {
    const part = parts[index];
    const objectSchema = cursor[part];
    if (!isRecord(objectSchema)) return;
    const nextRequired = requiredKeys(objectSchema);
    objectSchema.required = uniqueStrings([...nextRequired, parts[index + 1]]);
    if (!isRecord(objectSchema.properties)) return;
    cursor = objectSchema.properties as JsonRecord;
  }
}

function schemaFromField(field: JsonRecord, name: string): JsonRecord {
  const baseSchema = normalizeConfigSchema(field.schema) ?? schemaKeywordsFromField(field);
  const next: JsonRecord = { ...baseSchema };
  const type = normalizeFieldType(stringFromKeys(field, fieldTypeKeys));
  const title = stringFromKeys(field, fieldTitleKeys);
  const description = stringFromKeys(field, fieldDescriptionKeys);
  const enumValues = enumValuesFromField(field);
  const nestedFields = fieldListProperties(field.fields);

  if (type && !next.type) next.type = type;
  if (title && !next.title) next.title = title;
  if (description && !next.description) next.description = description;
  if (enumValues && !Array.isArray(next.enum)) next.enum = enumValues;
  if (nestedFields && !isRecord(next.properties)) {
    next.type = "object";
    next.properties = nestedFields.properties;
    next.required = uniqueStrings([...requiredKeys(next), ...nestedFields.required]);
  }

  for (const key of fieldDefaultKeys) {
    if (Object.prototype.hasOwnProperty.call(field, key) && !Object.prototype.hasOwnProperty.call(next, "default")) {
      next.default = field[key];
      break;
    }
  }

  if (isRecord(field.items) && !next.items) next.items = field.items;
  if (isRecord(field.properties) && !isRecord(next.properties)) next.properties = field.properties;
  if (!next.type && isRecord(next.properties)) next.type = "object";
  if (!next.title) next.title = name;
  return next;
}

function schemaKeywordsFromField(field: JsonRecord): JsonRecord {
  const schemaKeys = [
    "$ref",
    "type",
    "properties",
    "required",
    "items",
    "enum",
    "const",
    "default",
    "title",
    "description",
    "additionalProperties",
    "oneOf",
    "anyOf",
    "allOf",
    "format",
    "x-control",
    "pattern",
    "minimum",
    "maximum",
    "minLength",
    "maxLength",
    "minItems",
    "maxItems",
  ];
  const schema: JsonRecord = {};
  for (const key of schemaKeys) {
    if (Object.prototype.hasOwnProperty.call(field, key)) schema[key] = field[key];
  }
  return schema;
}

function isRequiredField(field: JsonRecord): boolean {
  if (field.required === true || field.required === "true") return true;
  if (field.optional === false || field.optional === "false") return true;
  return false;
}

function enumValuesFromField(field: JsonRecord): unknown[] | null {
  for (const key of fieldEnumKeys) {
    const raw = field[key];
    if (!Array.isArray(raw)) continue;
    const values = raw
      .map((item) => {
        if (!isRecord(item)) return item;
        if (Object.prototype.hasOwnProperty.call(item, "value")) return item.value;
        if (Object.prototype.hasOwnProperty.call(item, "id")) return item.id;
        if (Object.prototype.hasOwnProperty.call(item, "name")) return item.name;
        return undefined;
      })
      .filter((item) => item !== undefined);
    return values.length > 0 ? values : null;
  }
  return null;
}

function stringFromKeys(record: JsonRecord, keys: readonly string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function normalizeFieldType(type: string): string {
  switch (type.toLowerCase()) {
    case "bool":
      return "boolean";
    case "int":
    case "int32":
    case "int64":
      return "integer";
    case "float":
    case "float32":
    case "float64":
    case "double":
      return "number";
    case "list":
      return "array";
    case "map":
      return "object";
    case "text":
      return "string";
    case "any":
    case "json":
      return "";
    default:
      return type;
  }
}

function requiredKeys(schema: JsonRecord): string[] {
  if (!Array.isArray(schema.required)) return [];
  return schema.required.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

function uniqueStrings(values: string[]): string[] {
  return Array.from(new Set(values.filter((value) => value.trim() !== "")));
}

function isRecord(value: unknown): value is JsonRecord {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
