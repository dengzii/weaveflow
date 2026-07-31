import type { ReactNode } from "react";
import { AlertCircle } from "lucide-react";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { cn, isPlainRecord } from "../../../lib/utils";
import type { ToolDefinition } from "../../../types";
import {
  JSONValueControl,
  ObjectListControl,
  StringListControl,
  ToolIDListControl,
} from "./SchemaFormControls";
import {
  coerceEnumValue,
  formatStructuredValue,
  getPathValue,
  isObjectArraySchema,
  isStringArraySchema,
  isToolIDsField,
  normalizeConfigSchema,
  schemaProperties,
  schemaType,
  setPathValue,
  validateSchemaValue,
  type SchemaFormIssue,
} from "./schemaFormModel";

export { parseJSONControlText, validateSchemaValue } from "./schemaFormModel";
export type { JSONControlParseResult, SchemaFormIssue } from "./schemaFormModel";

interface JsonSchemaFormProps {
  schema?: Record<string, unknown>;
  unavailableReason?: string;
  value: Record<string, unknown>;
  toolDefinitions?: ToolDefinition[];
  onChange: (value: Record<string, unknown>) => void;
}

export function JsonSchemaForm({
  schema,
  unavailableReason,
  value,
  toolDefinitions = [],
  onChange,
}: JsonSchemaFormProps) {
  const normalizedSchema = normalizeConfigSchema(schema);
  if (!normalizedSchema) {
    return (
      <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">
        {unavailableReason || "No configuration schema is available for this type."}
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
  const fieldSchema = isPlainRecord(schema) ? schema : {};
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
          <div
            className={cn(
              "grid gap-3 rounded-md border border-border bg-muted/30 p-2",
              invalid && "border-destructive/70"
            )}
          >
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

  if (type === "array" && isObjectArraySchema(schema)) {
    return (
      <ObjectListControl
        value={value}
        schema={schema}
        invalid={invalid}
        onChange={onChange}
        renderItem={(itemSchema, itemValue, onItemChange) => (
          <JsonSchemaForm
            schema={itemSchema}
            value={itemValue}
            toolDefinitions={toolDefinitions}
            onChange={onItemChange}
          />
        )}
      />
    );
  }

  if (type === "array" && isStringArraySchema(schema) && isToolIDsField(path, name)) {
    return (
      <ToolIDListControl
        value={value}
        invalid={invalid}
        toolDefinitions={toolDefinitions}
        onChange={onChange}
      />
    );
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
