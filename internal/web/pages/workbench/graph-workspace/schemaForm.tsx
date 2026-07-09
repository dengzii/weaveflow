import { useMemo, useState } from "react";
import { AlertCircle, Plus, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { normalizeConfigSchema } from "../../../lib/schemaCompat";
import { cn, stringifyJSON } from "../../../lib/utils";
import type { ToolDefinition } from "../../../types";
import { Field } from "./shared";

export interface SchemaFormIssue {
  path: string;
  message: string;
}

interface JsonSchemaFormProps {
  schema?: Record<string, unknown>;
  unavailableReason?: string;
  value: Record<string, unknown>;
  toolDefinitions?: ToolDefinition[];
  onChange: (value: Record<string, unknown>) => void;
}

export function JsonSchemaForm({ schema, unavailableReason, value, toolDefinitions = [], onChange }: JsonSchemaFormProps) {
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
          toolDefinitions={toolDefinitions}
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
  toolDefinitions,
  onChange,
}: {
  path: string;
  name: string;
  schema: unknown;
  rootValue: Record<string, unknown>;
  issues: SchemaFormIssue[];
  toolDefinitions: ToolDefinition[];
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
                toolDefinitions={toolDefinitions}
                onChange={onChange}
              />
            ))}
          </div>
        ) : (
          renderSchemaControl(type, fieldSchema, value, setValue, invalid, path, name, toolDefinitions)
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
  invalid: boolean,
  path: string,
  name: string,
  toolDefinitions: ToolDefinition[]
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

  if (type === "array" && isStringArraySchema(schema) && isToolIDsField(path, name)) {
    return <ToolIDListControl value={value} invalid={invalid} toolDefinitions={toolDefinitions} onChange={onChange} />;
  }

  if (type === "array" && isStringArraySchema(schema)) {
    return <StringListControl value={value} invalid={invalid} onChange={onChange} />;
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

function ToolIDListControl({
  value,
  invalid,
  toolDefinitions,
  onChange,
}: {
  value: unknown;
  invalid: boolean;
  toolDefinitions: ToolDefinition[];
  onChange: (value: unknown) => void;
}) {
  const values = uniqueStrings(stringListValues(value));
  const tools = useMemo(() => uniqueToolDefinitions(toolDefinitions), [toolDefinitions]);
  const toolIDs = tools.map((tool) => tool.id);
  const toolIDSet = new Set(toolIDs);
  const selectedSet = new Set(values);
  const addableToolIDs = toolIDs.filter((id) => !selectedSet.has(id));
  const [pendingToolID, setPendingToolID] = useState("");
  const selectedPendingToolID = addableToolIDs.includes(pendingToolID) ? pendingToolID : addableToolIDs[0] ?? "";

  const updateValue = (index: number, nextValue: string) => {
    if (!toolIDSet.has(nextValue)) return;
    onChange(uniqueStrings(values.map((item, itemIndex) => (itemIndex === index ? nextValue : item))));
  };

  const removeValue = (index: number) => {
    const nextValues = values.filter((_, itemIndex) => itemIndex !== index);
    onChange(nextValues.length > 0 ? nextValues : undefined);
  };

  const addValue = () => {
    if (!selectedPendingToolID) return;
    onChange(uniqueStrings([...values, selectedPendingToolID]));
    setPendingToolID("");
  };

  return (
    <div className="grid gap-2">
      {values.map((item, index) => {
        const unknown = !toolIDSet.has(item);
        const selectClass = invalid || unknown ? "border-destructive focus:border-destructive" : undefined;
        const selectableTools = tools.filter((tool) => tool.id === item || !selectedSet.has(tool.id));
        return (
          <div key={`${item}-${index}`} className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
            <Select value={item} onChange={(event) => updateValue(index, event.target.value)} className={selectClass}>
              {unknown ? <option value={item}>{item} (unavailable)</option> : null}
              {selectableTools.map((tool) => (
                <option key={tool.id} value={tool.id}>
                  {toolLabel(tool)}
                </option>
              ))}
            </Select>
            <Button type="button" variant="ghost" size="icon" title="Remove tool" onClick={() => removeValue(index)}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        );
      })}
      <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
        <Select
          value={selectedPendingToolID}
          disabled={!selectedPendingToolID}
          onChange={(event) => setPendingToolID(event.target.value)}
        >
          {selectedPendingToolID ? null : <option value="">No tools</option>}
          {addableToolIDs.map((id) => {
            const tool = tools.find((item) => item.id === id);
            return (
              <option key={id} value={id}>
                {tool ? toolLabel(tool) : id}
              </option>
            );
          })}
        </Select>
        <Button type="button" variant="outline" size="sm" disabled={!selectedPendingToolID} onClick={addValue}>
          <Plus className="h-4 w-4" />
          Add
        </Button>
      </div>
    </div>
  );
}

function StringListControl({
  value,
  invalid,
  onChange,
}: {
  value: unknown;
  invalid: boolean;
  onChange: (value: unknown) => void;
}) {
  const values = stringListValues(value);
  const controlClass = invalid ? "border-destructive focus:border-destructive" : undefined;

  const updateValue = (index: number, nextValue: string) => {
    onChange(values.map((item, itemIndex) => (itemIndex === index ? nextValue : item)));
  };

  const removeValue = (index: number) => {
    const nextValues = values.filter((_, itemIndex) => itemIndex !== index);
    onChange(nextValues.length > 0 ? nextValues : undefined);
  };

  return (
    <div className="grid gap-2">
      {values.map((item, index) => (
        <div key={index} className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <Input
            value={item}
            onChange={(event) => updateValue(index, event.target.value)}
            className={controlClass}
          />
          <Button type="button" variant="ghost" size="icon" title="Remove value" onClick={() => removeValue(index)}>
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" className="w-fit" onClick={() => onChange([...values, ""])}>
        <Plus className="h-4 w-4" />
        Add
      </Button>
    </div>
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

function isStringArraySchema(schema: Record<string, unknown>): boolean {
  if (!isRecord(schema.items)) return false;
  return schemaType(schema.items, undefined) === "string";
}

function isToolIDsField(path: string, name: string): boolean {
  const normalizedName = name.trim().toLowerCase();
  if (normalizedName === "tool_ids" || normalizedName === "tools_ids") return true;
  const parts = path.split(".").map((part) => part.trim().toLowerCase()).filter(Boolean);
  return parts.at(-1) === "tool_ids" || parts.at(-1) === "tools_ids";
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

function stringListValues(value: unknown): string[] {
  if (Array.isArray(value)) return value.map((item) => (item == null ? "" : String(item)));
  if (typeof value !== "string") return [];

  const text = value.trim();
  if (!text) return [];

  try {
    const parsed = JSON.parse(text) as unknown;
    if (Array.isArray(parsed)) return parsed.map((item) => (item == null ? "" : String(item)));
  } catch {
    // Fall back to line parsing below.
  }

  return text
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function uniqueToolDefinitions(tools: ToolDefinition[]): ToolDefinition[] {
  const seen = new Set<string>();
  const out: ToolDefinition[] = [];
  for (const tool of tools) {
    const id = tool.id?.trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    out.push({ ...tool, id });
  }
  return out;
}

function toolLabel(tool: ToolDefinition): string {
  const name = tool.name?.trim();
  if (!name || name === tool.id) return tool.id;
  return `${tool.id} (${name})`;
}

function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
