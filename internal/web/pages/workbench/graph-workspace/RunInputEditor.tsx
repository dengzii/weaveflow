import { useMemo, useState } from "react";
import { Braces } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Textarea } from "../../../components/ui/textarea";
import { cn, stringifyJSON } from "../../../lib/utils";
import type { InitialStateRequirement } from "../../../types";
import {
  formatJSONFieldValue,
  getPathValue,
  hasFilledRequirementValue,
  parseInitialStateText,
  updateInitialStatePath,
} from "./runInputModel";

interface RunInputEditorProps {
  requirements: InitialStateRequirement[];
  analysisError: string;
  initialStateText: string;
  onChangeInitialStateText: (value: string) => void;
}

export function RunInputEditor({
  requirements,
  analysisError,
  initialStateText,
  onChangeInitialStateText,
}: RunInputEditorProps) {
  const [jsonOpen, setJSONOpen] = useState(false);
  const parsed = useMemo(() => parseInitialStateText(initialStateText), [initialStateText]);

  if (requirements.length === 0) {
    return (
      <div className="grid gap-3">
        <div
          className={
            analysisError
              ? "rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive"
              : "rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground"
          }
        >
          {analysisError ? `Cannot build input form: ${analysisError}` : "No form fields detected from the graph state contracts."}
        </div>
        <JSONRunInputEditor
          open={jsonOpen}
          initialStateText={initialStateText}
          onOpenChange={setJSONOpen}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      </div>
    );
  }

  return (
    <div className="grid gap-3">
      {parsed.error ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
          Run input JSON is invalid. Editing a form field will rebuild it from valid values.
        </div>
      ) : null}

      <div className="grid gap-3">
        {requirements.map((requirement) => {
          const value = getPathValue(parsed.root, requirement.path);
          return (
            <RunInputField
              key={requirement.path}
              requirement={requirement}
              value={value}
              invalid={!hasFilledRequirementValue(value, requirement.type)}
              onChange={(nextValue) =>
                onChangeInitialStateText(
                  stringifyJSON(updateInitialStatePath(initialStateText, requirement.path, nextValue))
                )
              }
            />
          );
        })}
      </div>

      <JSONRunInputEditor
        open={jsonOpen}
        initialStateText={initialStateText}
        onOpenChange={setJSONOpen}
        onChangeInitialStateText={onChangeInitialStateText}
      />
    </div>
  );
}

function JSONRunInputEditor({
  open,
  initialStateText,
  onOpenChange,
  onChangeInitialStateText,
}: {
  open: boolean;
  initialStateText: string;
  onOpenChange: (open: boolean) => void;
  onChangeInitialStateText: (value: string) => void;
}) {
  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={() => onOpenChange(!open)}>
          <Braces className="h-4 w-4" />
          {open ? "Hide JSON" : "Edit JSON"}
        </Button>
      </div>

      {open ? (
        <Textarea
          aria-label="Run input JSON"
          value={initialStateText}
          onChange={(event) => onChangeInitialStateText(event.target.value)}
          spellCheck={false}
          className="h-40 font-mono text-xs"
        />
      ) : null}
    </div>
  );
}

function RunInputField({
  requirement,
  value,
  invalid,
  onChange,
}: {
  requirement: InitialStateRequirement;
  value: unknown;
  invalid: boolean;
  onChange: (value: unknown) => void;
}) {
  const type = (requirement.type ?? "").toLowerCase();
  const description = requirement.message || requirement.description;
  const sourceTitle = requirement.nodes?.length ? `Used by ${requirement.nodes.join(", ")}` : undefined;

  return (
    <div className="grid gap-1" title={sourceTitle}>
      <span className="min-w-0 break-all font-mono text-xs font-medium">{requirement.path}</span>
      {renderRunInputControl(requirement.path, type, value, onChange, invalid)}
      {invalid ? <div className="text-xs text-destructive">Required value is missing or has the wrong type.</div> : null}
      {requirement.type ? <div className="text-[11px] text-muted-foreground">{requirement.type}</div> : null}
      {description ? <div className="line-clamp-2 text-xs text-muted-foreground">{description}</div> : null}
    </div>
  );
}

function renderRunInputControl(
  path: string,
  type: string,
  value: unknown,
  onChange: (value: unknown) => void,
  invalid: boolean
) {
  const invalidClass = invalid ? "border-destructive focus:border-destructive" : undefined;
  if (type === "boolean" || type === "bool") {
    return (
      <label className={cn("flex h-9 items-center gap-2 rounded-md border border-input bg-background px-3 text-sm", invalid && "border-destructive")}>
        <input
          type="checkbox"
          aria-label={path}
          checked={typeof value === "boolean" ? value : false}
          onChange={(event) => onChange(event.target.checked)}
          className="h-4 w-4"
        />
        <span>{typeof value === "boolean" && value ? "true" : "false"}</span>
      </label>
    );
  }

  if (["number", "float", "float64", "integer", "int", "int64"].includes(type)) {
    return (
      <Input
        type="number"
        aria-label={path}
        value={typeof value === "number" && Number.isFinite(value) ? String(value) : ""}
        className={invalidClass}
        onChange={(event) => {
          const nextValue = event.target.value;
          onChange(nextValue.trim() === "" ? null : Number(nextValue));
        }}
      />
    );
  }

  if (["object", "map", "array", "list"].includes(type)) {
    return (
      <Textarea
        aria-label={path}
        value={formatJSONFieldValue(value)}
        onChange={(event) => {
          try {
            onChange(JSON.parse(event.target.value));
          } catch {
            onChange(event.target.value);
          }
        }}
        spellCheck={false}
        className={cn("h-24 font-mono text-xs", invalidClass)}
      />
    );
  }

  return (
    <Input
      aria-label={path}
      value={typeof value === "string" ? value : value == null ? "" : String(value)}
      onChange={(event) => onChange(event.target.value)}
      className={invalidClass}
    />
  );
}
