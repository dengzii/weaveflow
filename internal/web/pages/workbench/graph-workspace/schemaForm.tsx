import { AlertCircle } from "lucide-react";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { normalizeConfigSchema } from "../../../lib/schemaCompat";
import { cn, stringifyJSON } from "../../../lib/utils";
import { Field } from "./shared";

export interface SchemaFormIssue {
  path: string;
  message: string;
}

interface JsonSchemaFormProps {
  schema?: Record<string, unknown>;
  unavailableReason?: string;
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
}

export function JsonSchemaForm({ schema, unavailableReason, value, onChange }: JsonSchemaFormProps) {
  const normalizedSchema = normalizeConfigSchema(schema);
  if (!normalizedSchema) {
    return (
      <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">
        {unavailableReason || "No config schema is available for this type."}
      </div>
    );
  }

  const properties = schemaProperties(normalizedSchema);
  if (properties.length === 0) {
    return (
      <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">
        This schema does not define editable fields.
      </div>
    );
  }

  const issues = validateSchemaValue(normalizedSchema, value);
  return (
    <div className="grid gap-3">
      {properties.map(([key, propertySchema]) => (
        <SchemaField
          key={key}
          path={key}
          name={key}
          schema={propertySchema}
          rootValue={value}
          issues={issues}
          onChange={onChange}
        />
      ))}
    </div>
  );
}

export function validateSchemaValue(
  schema: unknown,
  value: unknown,
  basePath = ""
): SchemaFormIssue[] {
  if (!isRecord(schema)) return [];
  const issues: SchemaFormIssue[] = [];
  const type = schemaType(schema, value);
  const required = requiredKeys(schema);

  if (basePath && !isEmptyValue(value)) {
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
  if (properties.length === 0) return issues;

  const recordValue = isRecord(value) ? value : {};
  for (const key of required) {
    if (isEmptyValue(recordValue[key])) {
      issues.push({
        path: joinPath(basePath, key),
        message: "Required field.",
      });
    }
  }

  for (const [key, propertySchema] of properties) {
    const nextPath = joinPath(basePath, key);
    const nextValue = recordValue[key];
    if (!isEmptyValue(nextValue)) {
      issues.push(...validateSchemaValue(propertySchema, nextValue, nextPath));
    }
  }

  return issues;
}

function SchemaField({
  path,
  name,
  schema,
  rootValue,
  issues,
  onChange,
}: {
  path: string;
  name: string;
  schema: unknown;
  rootValue: Record<string, unknown>;
  issues: SchemaFormIssue[];
  onChange: (value: Record<string, unknown>) => void;
}) {
  const fieldSchema = isRecord(schema) ? schema : {};
  const value = getPathValue(rootValue, path);
  const title = typeof fieldSchema.title === "string" && fieldSchema.title.trim() ? fieldSchema.title : name;
  const description =
    typeof fieldSchema.description === "string" && fieldSchema.description.trim()
      ? fieldSchema.description
      : "";
  const fieldIssues = issues.filter((issue) => issue.path === path);
  const invalid = fieldIssues.length > 0;
  const type = schemaType(fieldSchema, value);
  const childProperties = schemaProperties(fieldSchema);

  const setValue = (nextValue: unknown) => {
    onChange(setPathValue(rootValue, path, nextValue));
  };

  return (
    <div className="grid gap-1">
      <Field label={title}>
        {childProperties.length > 0 && type === "object" ? (
          <div className={cn("grid gap-3 rounded-md border border-border bg-muted/30 p-2", invalid && "border-destructive/70")}>
            {childProperties.map(([key, propertySchema]) => (
              <SchemaField
                key={`${path}.${key}`}
                path={`${path}.${key}`}
                name={key}
                schema={propertySchema}
                rootValue={rootValue}
                issues={issues}
                onChange={onChange}
              />
            ))}
          </div>
        ) : (
          renderSchemaControl(type, fieldSchema, value, setValue, invalid)
        )}
      </Field>
      {fieldIssues.map((issue) => (
        <div key={`${issue.path}-${issue.message}`} className="flex items-center gap-1 text-xs text-destructive">
          <AlertCircle className="h-3.5 w-3.5" />
          <span>{issue.message}</span>
        </div>
      ))}
      {description ? <div className="text-xs text-muted-foreground">{description}</div> : null}
    </div>
  );
}

function renderSchemaControl(
  type: string,
  schema: Record<string, unknown>,
  value: unknown,
  onChange: (value: unknown) => void,
  invalid: boolean
) {
  const enumValues = Array.isArray(schema.enum) ? schema.enum : [];
  const controlClass = invalid ? "border-destructive focus:border-destructive" : undefined;

  if (enumValues.length > 0) {
    return (
      <Select
        value={value == null ? "" : String(value)}
        onChange={(event) => onChange(coerceEnumValue(event.target.value, enumValues))}
        className={controlClass}
      >
        <option value="">-</option>
        {enumValues.map((item, index) => (
          <option key={`${String(item)}-${index}`} value={String(item)}>
            {String(item)}
          </option>
        ))}
      </Select>
    );
  }

  if (type === "boolean") {
    return (
      <label
        className={cn(
          "flex h-9 items-center gap-2 rounded-md border border-input bg-background px-3 text-sm",
          invalid && "border-destructive"
        )}
      >
        <input
          type="checkbox"
          checked={Boolean(value)}
          onChange={(event) => onChange(event.target.checked)}
          className="h-4 w-4"
        />
        <span>{Boolean(value) ? "true" : "false"}</span>
      </label>
    );
  }

  if (type === "number" || type === "integer") {
    return (
      <Input
        type="number"
        value={typeof value === "number" && Number.isFinite(value) ? String(value) : ""}
        onChange={(event) => {
          const next = event.target.value.trim();
          if (!next) {
            onChange(undefined);
            return;
          }
          const numberValue = Number(next);
          onChange(Number.isFinite(numberValue) ? numberValue : next);
        }}
        className={controlClass}
      />
    );
  }

  if (type === "object" || type === "array") {
    return (
      <Textarea
        value={formatStructuredValue(value, type)}
        onChange={(event) => {
          const text = event.target.value;
          if (!text.trim()) {
            onChange(undefined);
            return;
          }
          try {
            onChange(JSON.parse(text) as unknown);
          } catch {
            onChange(text);
          }
        }}
        spellCheck={false}
        className={cn("h-24 text-xs", controlClass)}
      />
    );
  }

  return (
    <Input
      value={typeof value === "string" ? value : value == null ? "" : String(value)}
      onChange={(event) => onChange(event.target.value)}
      className={controlClass}
    />
  );
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
      return isRecord(value) ? null : { path, message: "Expected a JSON object." };
    default:
      return null;
  }
}

function schemaProperties(schema: Record<string, unknown>): Array<[string, unknown]> {
  if (!isRecord(schema.properties)) return [];
  return Object.entries(schema.properties);
}

function schemaType(schema: Record<string, unknown>, value: unknown): string {
  const rawType = Array.isArray(schema.type) ? schema.type.find((item) => item !== "null") : schema.type;
  if (typeof rawType === "string" && rawType.trim()) return rawType;
  if (isRecord(schema.properties)) return "object";
  if (Array.isArray(schema.enum) && schema.enum.length > 0) return typeof schema.enum[0];
  if (Array.isArray(value)) return "array";
  if (isRecord(value)) return "object";
  if (typeof value === "number") return Number.isInteger(value) ? "integer" : "number";
  if (typeof value === "boolean") return "boolean";
  return "string";
}

function requiredKeys(schema: Record<string, unknown>): string[] {
  if (!Array.isArray(schema.required)) return [];
  return schema.required.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

function getPathValue(root: Record<string, unknown>, path: string): unknown {
  const parts = path.split(".").filter(Boolean);
  let current: unknown = root;
  for (const part of parts) {
    if (!isRecord(current)) return undefined;
    current = current[part];
  }
  return current;
}

function setPathValue(root: Record<string, unknown>, path: string, value: unknown): Record<string, unknown> {
  const parts = path.split(".").filter(Boolean);
  if (parts.length === 0) return { ...root };
  const nextRoot = { ...root };
  let cursor = nextRoot;
  for (const part of parts.slice(0, -1)) {
    const existing = cursor[part];
    cursor[part] = isRecord(existing) ? { ...existing } : {};
    cursor = cursor[part] as Record<string, unknown>;
  }
  const leaf = parts[parts.length - 1];
  if (!leaf) return nextRoot;
  if (value === undefined) {
    delete cursor[leaf];
  } else {
    cursor[leaf] = value;
  }
  return nextRoot;
}

function joinPath(prefix: string, key: string): string {
  return prefix ? `${prefix}.${key}` : key;
}

function isEmptyValue(value: unknown): boolean {
  return value === undefined || value === null || (typeof value === "string" && value.trim() === "");
}

function coerceEnumValue(value: string, enumValues: unknown[]): unknown {
  const match = enumValues.find((item) => String(item) === value);
  return match ?? value;
}

function formatStructuredValue(value: unknown, type: string): string {
  if (value === undefined || value === null) return type === "array" ? "[]" : "{}";
  if (typeof value === "string") return value;
  return stringifyJSON(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
