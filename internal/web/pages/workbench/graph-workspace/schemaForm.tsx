import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { AlertCircle, Plus, Trash2, X } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { exampleConfigForSchema } from "../../../lib/jsonSchemaDefaults";
import { cn, stringifyJSON } from "../../../lib/utils";
import type { ToolDefinition } from "../../../types";

export interface SchemaFormIssue {
  path: string;
  message: string;
}

export type JSONControlParseResult =
  | { ok: true; value: unknown }
  | { ok: false };

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
    if (isRecord(schema.items)) {
      value.forEach((item, index) => {
        issues.push(...validateSchemaValue(schema.items, item, joinPath(basePath, String(index))));
      });
    }
  }
  if (properties.length === 0) return issues;

  const recordValue = isRecord(value) ? value : {};
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
    const nextValue = recordValue[key];
    if (!isEmptyValue(nextValue)) {
      issues.push(...validateSchemaValue(propertySchema, nextValue, nextPath));
    }
  }

  return issues;
}

function SchemaControlField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid min-w-0 gap-1 text-sm">
      <span className="break-words text-xs font-medium text-muted-foreground">{label}</span>
      {children}
    </div>
  );
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
    <div className="grid min-w-0 gap-1">
      <SchemaControlField label={title}>
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
      </SchemaControlField>
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

  if (schema["x-control"] === "json") {
    return <JSONValueControl value={value} invalid={invalid} onChange={onChange} />;
  }

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

  if (type === "array" && isObjectArraySchema(schema)) {
    return (
      <ObjectListControl
        value={value}
        schema={schema}
        invalid={invalid}
        toolDefinitions={toolDefinitions}
        onChange={onChange}
      />
    );
  }

  if (type === "string" && schema["x-control"] === "textarea") {
    return (
      <Textarea
        value={typeof value === "string" ? value : value == null ? "" : String(value)}
        onChange={(event) => onChange(event.target.value)}
        spellCheck={false}
        className={cn("min-h-32 resize-y", controlClass)}
      />
    );
  }

  return (
    <Input
      type={schema.writeOnly === true || schema.format === "password" ? "password" : undefined}
      value={typeof value === "string" ? value : value == null ? "" : String(value)}
      onChange={(event) => onChange(event.target.value)}
      placeholder={schema.writeOnly === true ? "Sensitive value" : undefined}
      className={controlClass}
    />
  );
}

function ObjectListControl({
  value,
  schema,
  invalid,
  toolDefinitions,
  onChange,
}: {
  value: unknown;
  schema: Record<string, unknown>;
  invalid: boolean;
  toolDefinitions: ToolDefinition[];
  onChange: (value: unknown) => void;
}) {
  const itemSchema = isRecord(schema.items) ? schema.items : { type: "object", properties: {} };
  const values = Array.isArray(value) ? value.map((item) => (isRecord(item) ? item : {})) : [];
  const itemLabel =
    typeof schema["x-item-title"] === "string" && schema["x-item-title"].trim()
      ? schema["x-item-title"].trim()
      : "Item";

  function updateItem(index: number, item: Record<string, unknown>) {
    onChange(values.map((current, currentIndex) => (currentIndex === index ? item : current)));
  }

  function removeItem(index: number) {
    onChange(values.filter((_, currentIndex) => currentIndex !== index));
  }

  function addItem() {
    onChange([...values, exampleConfigForSchema(itemSchema)]);
  }

  return (
    <div className={cn("grid gap-2", invalid && "rounded-md border border-destructive/70 p-2")}>
      {values.map((item, index) => (
        <div key={index} className="grid gap-2 border-l-2 border-border pl-3">
          <div className="flex h-7 items-center gap-2">
            <span className="text-xs font-medium text-muted-foreground">{itemLabel} {index + 1}</span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="ml-auto h-7 w-7"
              title={`Remove ${itemLabel.toLowerCase()}`}
              onClick={() => removeItem(index)}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
          <JsonSchemaForm
            schema={itemSchema}
            value={item}
            toolDefinitions={toolDefinitions}
            onChange={(nextItem) => updateItem(index, nextItem)}
          />
        </div>
      ))}
      {values.length === 0 ? <span className="text-xs text-muted-foreground">No items configured.</span> : null}
      <div>
        <Button type="button" variant="outline" size="sm" onClick={addItem}>
          <Plus className="h-3.5 w-3.5" />
          Add {itemLabel}
        </Button>
      </div>
    </div>
  );
}

function JSONValueControl({
  value,
  invalid,
  onChange,
}: {
  value: unknown;
  invalid: boolean;
  onChange: (value: unknown) => void;
}) {
  const formatted = useMemo(() => formatJSONControlValue(value), [value]);
  const [draft, setDraft] = useState(formatted);
  const [parseError, setParseError] = useState(false);

  useEffect(() => {
    setDraft(formatted);
    setParseError(false);
  }, [formatted]);

  return (
    <Textarea
      value={draft}
      onChange={(event) => {
        const text = event.target.value;
        setDraft(text);
        const parsed = parseJSONControlText(text);
        setParseError(!parsed.ok);
        if (parsed.ok) onChange(parsed.value);
      }}
      spellCheck={false}
      className={cn("min-h-24 resize-y font-mono text-xs", (invalid || parseError) && "border-destructive focus:border-destructive")}
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
  const selectedToolsByID = new Map(tools.map((tool) => [tool.id, tool]));
  const addableTools = tools.filter((tool) => !selectedSet.has(tool.id));
  const pickerRef = useRef<HTMLSpanElement | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);

  const removeValue = (toolID: string) => {
    const nextValues = values.filter((item) => item !== toolID);
    onChange(nextValues.length > 0 ? nextValues : undefined);
  };

  const addValue = (toolID: string) => {
    if (!toolID || selectedSet.has(toolID)) return;
    onChange(uniqueStrings([...values, toolID]));
    setPickerOpen(false);
  };

  useEffect(() => {
    if (!pickerOpen) return;

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target && !pickerRef.current?.contains(target)) setPickerOpen(false);
    };

    document.addEventListener("pointerdown", handlePointerDown, true);
    return () => document.removeEventListener("pointerdown", handlePointerDown, true);
  }, [pickerOpen]);

  return (
    <div
      className={cn(
        "flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border border-input bg-background p-2",
        invalid && "border-destructive"
      )}
    >
      {values.length === 0 ? <span className="text-xs text-muted-foreground">No tools selected.</span> : null}
      {values.map((toolID) => {
        const tool = selectedToolsByID.get(toolID);
        const unknown = !toolIDSet.has(toolID);
        return (
          <span
            key={toolID}
            title={tool?.description || toolID}
            className={cn(
              "inline-flex min-h-7 max-w-full items-start gap-1 rounded-md border py-1 pl-2 pr-1 text-xs",
              unknown ? "border-destructive/60 bg-destructive/10 text-destructive" : "border-border bg-muted text-foreground"
            )}
          >
            <span className="min-w-0 break-all whitespace-normal font-mono">{tool ? toolLabel(tool) : `${toolID} (unavailable)`}</span>
            <button
              type="button"
              className="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-background hover:text-foreground"
              title="Remove tool"
              aria-label={`Remove ${toolID}`}
              onMouseDown={(event) => {
                event.preventDefault();
                event.stopPropagation();
              }}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                removeValue(toolID);
              }}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </span>
        );
      })}

      <span ref={pickerRef} className="relative flex w-full min-w-0">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={addableTools.length === 0}
          onMouseDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            if (addableTools.length > 0) setPickerOpen((open) => !open);
          }}
        >
          <Plus className="h-4 w-4" />
          Add
        </Button>
        {pickerOpen ? (
          <div className="absolute inset-x-0 top-full z-30 mt-1 grid max-h-64 w-full min-w-0 justify-items-stretch gap-1 overflow-x-hidden overflow-y-auto rounded-md border border-border bg-background p-1 shadow-lg">
            {addableTools.map((tool) => (
              <button
                key={tool.id}
                type="button"
                className="inline-grid max-w-full min-w-0 gap-0.5 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent"
                onClick={() => addValue(tool.id)}
              >
                <span className="break-all font-mono font-medium">{toolLabel(tool)}</span>
                {tool.description ? <span className="line-clamp-2 text-muted-foreground">{tool.description}</span> : null}
              </button>
            ))}
          </div>
        ) : null}
      </span>
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

function normalizeConfigSchema(schema: unknown): Record<string, unknown> | undefined {
  return isRecord(schema) ? schema : undefined;
}

function isObjectArraySchema(schema: Record<string, unknown>): boolean {
  if (!isRecord(schema.items)) return false;
  return schema["x-control"] === "object-list" || schemaType(schema.items, undefined) === "object";
}

function isToolIDsField(path: string, name: string): boolean {
  const normalizedName = name.trim().toLowerCase();
  if (normalizedName === "tool_ids") return true;
  const parts = path.split(".").map((part) => part.trim().toLowerCase()).filter(Boolean);
  return parts.at(-1) === "tool_ids";
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

function requiredFieldMissing(record: Record<string, unknown>, key: string, schema: unknown): boolean {
  if (!Object.prototype.hasOwnProperty.call(record, key) || record[key] === undefined) return true;
  if (isRecord(schema) && schema["x-control"] === "json") return false;
  return isEmptyValue(record[key]);
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

export function parseJSONControlText(text: string): JSONControlParseResult {
  if (!text.trim()) return { ok: true, value: undefined };
  try {
    return { ok: true, value: JSON.parse(text) as unknown };
  } catch {
    return { ok: false };
  }
}

function formatJSONControlValue(value: unknown): string {
  if (value === undefined) return "";
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

