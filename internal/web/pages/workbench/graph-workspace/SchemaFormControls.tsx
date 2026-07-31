import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Plus, Trash2, X } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Textarea } from "../../../components/ui/textarea";
import { exampleConfigForSchema } from "../../../lib/jsonSchemaDefaults";
import { cn, isPlainRecord } from "../../../lib/utils";
import type { ToolDefinition } from "../../../types";
import {
  formatJSONControlValue,
  parseJSONControlText,
  stringListValues,
  toolLabel,
  uniqueStrings,
  uniqueToolDefinitions,
} from "./schemaFormModel";

export function ObjectListControl({
  value,
  schema,
  invalid,
  onChange,
  renderItem,
}: {
  value: unknown;
  schema: Record<string, unknown>;
  invalid: boolean;
  onChange: (value: unknown) => void;
  renderItem: (
    schema: Record<string, unknown>,
    value: Record<string, unknown>,
    onChange: (value: Record<string, unknown>) => void
  ) => ReactNode;
}) {
  const itemSchema = isPlainRecord(schema.items) ? schema.items : { type: "object", properties: {} };
  const values = Array.isArray(value) ? value.map((item) => (isPlainRecord(item) ? item : {})) : [];
  const itemLabel =
    typeof schema["x-item-title"] === "string" && schema["x-item-title"].trim()
      ? schema["x-item-title"].trim()
      : "Item";

  const updateItem = (index: number, item: Record<string, unknown>) => {
    onChange(values.map((current, currentIndex) => (currentIndex === index ? item : current)));
  };

  const removeItem = (index: number) => {
    onChange(values.filter((_, currentIndex) => currentIndex !== index));
  };

  const addItem = () => {
    onChange([...values, exampleConfigForSchema(itemSchema)]);
  };

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
          {renderItem(itemSchema, item, (nextItem) => updateItem(index, nextItem))}
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

export function JSONValueControl({
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

export function ToolIDListControl({
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
          Add tool
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

export function StringListControl({
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
        Add value
      </Button>
    </div>
  );
}
