import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Blocks, MessageSquare, RefreshCw, Search, Settings2 } from "lucide-react";
import { apiFetch } from "../api";
import type { ApiResponse, RegistryData, RegistryFieldRef } from "../types";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Badge } from "../components/ui/badge";
import { cn } from "../lib/utils";

type JsonObject = Record<string, unknown>;
type StateAccessor = {
  nodeType: string;
  nodeTitle: string;
  mode: string;
};

function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="overflow-x-auto border border-border bg-muted/35 p-2 text-[10px] leading-5 text-foreground">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

function schemaTypeLabel(schema: JsonObject) {
  const type = schema.type;
  if (typeof type === "string" && type) return type;
  if (Array.isArray(type) && type.length) return type.join(" | ");
  if (Array.isArray(schema.enum) && schema.enum.length) return "enum";
  if (schema.const !== undefined) return "const";
  return "any";
}

function formatConfigValue(value: unknown) {
  if (value === null) return "null";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return `${value.length} items`;
  if (typeof value === "object") return `${Object.keys(value as Record<string, unknown>).length} fields`;
  return String(value);
}

function requiredSchemaKeys(raw: unknown) {
  if (!Array.isArray(raw)) return [];
  return raw.filter((item): item is string => typeof item === "string" && item.length > 0);
}

function configSchemaProperties(schema: Record<string, unknown>) {
  const properties = schema.properties;
  if (!properties || typeof properties !== "object" || Array.isArray(properties)) return [];
  return Object.entries(properties as JsonObject);
}

function ConfigSchemaField({
  name,
  schema,
  required,
  exampleValue,
}: {
  name: string;
  schema: JsonObject;
  required: boolean;
  exampleValue: unknown;
}) {
  const enumValues = Array.isArray(schema.enum) ? schema.enum : [];
  return (
    <div className="py-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <code className="text-[11px] font-semibold text-foreground">{name}</code>
        <Badge variant="secondary">{schemaTypeLabel(schema)}</Badge>
        {required ? <Badge variant="warning">required</Badge> : null}
        {schema.default !== undefined ? <Badge variant="outline">default</Badge> : null}
        {exampleValue !== undefined ? <Badge variant="outline">example</Badge> : null}
      </div>
      {typeof schema.description === "string" && schema.description ? (
        <div className="mt-1.5 text-xs leading-5 text-muted-foreground">{schema.description}</div>
      ) : null}
      <div className="mt-1.5 flex flex-wrap gap-1.5">
        {schema.const !== undefined ? <Badge variant="outline">const: {formatConfigValue(schema.const)}</Badge> : null}
        {enumValues.map((value) => (
          <Badge key={`${name}-${String(value)}`} variant="outline">
            {String(value)}
          </Badge>
        ))}
      </div>
      {schema.default !== undefined ? (
        <div className="mt-1 text-[11px] text-muted-foreground">
          default: <code>{formatConfigValue(schema.default)}</code>
        </div>
      ) : null}
      {exampleValue !== undefined ? (
        <div className="mt-1 text-[11px] text-muted-foreground">
          example: <code>{formatConfigValue(exampleValue)}</code>
        </div>
      ) : null}
    </div>
  );
}

function ConfigValue({ label, value }: { label: string; value: unknown }) {
  const isObject = value !== null && typeof value === "object" && !Array.isArray(value);
  const isArray = Array.isArray(value);

  if (!isObject && !isArray) {
    return (
      <div className="py-1.5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 text-[11px] font-medium text-muted-foreground">{label}</div>
          <code className="max-w-[70%] break-all text-[11px] text-foreground">{formatConfigValue(value)}</code>
        </div>
      </div>
    );
  }

  if (isArray) {
    return (
      <div className="py-1.5">
        <div className="flex items-center justify-between gap-3">
          <div className="text-[11px] font-medium text-muted-foreground">{label}</div>
          <Badge variant="outline">{formatConfigValue(value)}</Badge>
        </div>
        {value.length ? (
          <div className="mt-1 divide-y divide-border border-l border-border pl-3">
            {value.map((item, index) => (
              <ConfigValue key={`${label}.${index}`} label={`[${index}]`} value={item} />
            ))}
          </div>
        ) : null}
      </div>
    );
  }

  const entries = Object.entries(value);
  return (
    <div className="py-1.5">
      <div className="flex items-center justify-between gap-3">
        <div className="text-[11px] font-medium text-muted-foreground">{label}</div>
        <Badge variant="outline">{formatConfigValue(value)}</Badge>
      </div>
      {entries.length ? (
        <div className="mt-1 divide-y divide-border border-l border-border pl-3">
          {entries.map(([key, child]) => (
            <ConfigValue key={`${label}.${key}`} label={key} value={child} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function FieldRow({
  field,
  accessors,
  currentNodeType,
}: {
  field: RegistryFieldRef;
  accessors: StateAccessor[];
  currentNodeType: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const relatedAccessors = accessors.filter((accessor) => accessor.nodeType !== currentNodeType);

  return (
    <>
      <tr
        className="cursor-pointer border-b border-border transition-colors hover:bg-muted/20"
        onClick={() => setExpanded((v) => !v)}
      >
        <td className="py-2 pl-3 pr-2 align-top">
          <code className="text-[11px] font-semibold text-foreground">{field.path}</code>
          {field.path_config_key ? (
            <div className="mt-0.5 text-[10px] text-muted-foreground">
              key: <code>{field.path_config_key}</code>
            </div>
          ) : null}
        </td>
        <td className="px-2 py-2 align-top">
          <Badge variant="secondary">{field.mode}</Badge>
        </td>
        <td className="px-2 py-2 align-top">
          <div className="flex flex-wrap gap-1">
            {field.required ? <Badge variant="warning">required</Badge> : null}
            {field.dynamic ? <Badge variant="outline">dynamic</Badge> : null}
            {field.merge_strategy ? <Badge variant="outline">{field.merge_strategy}</Badge> : null}
          </div>
        </td>
        <td className="px-2 py-2 align-top">
          <Badge variant={relatedAccessors.length ? "outline" : "secondary"}>{relatedAccessors.length}</Badge>
        </td>
        <td className="py-2 pl-2 pr-3 align-top text-[11px] leading-5 text-muted-foreground">
          {field.description ?? ""}
        </td>
      </tr>
      {expanded ? (
        <tr className="border-b border-border bg-muted/10">
          <td colSpan={5} className="px-4 py-2">
            <div className="mb-1 text-[11px] font-medium text-muted-foreground">Nodes accessing this state</div>
            {relatedAccessors.length ? (
              <div className="divide-y divide-border">
                {relatedAccessors.map((accessor) => (
                  <div key={`${field.path}:${accessor.nodeType}:${accessor.mode}`} className="py-1.5">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-xs font-semibold text-foreground">{accessor.nodeTitle}</span>
                      <Badge variant="secondary">{accessor.mode}</Badge>
                    </div>
                    <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">{accessor.nodeType}</div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-xs text-muted-foreground">No other nodes access this state.</div>
            )}
          </td>
        </tr>
      ) : null}
    </>
  );
}

export function PlaygroundPage() {
  const [data, setData] = useState<RegistryData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [selectedType, setSelectedType] = useState<string>("");
  const [leftTab, setLeftTab] = useState<"nodes" | "conditions">("nodes");

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const response = await apiFetch<ApiResponse<RegistryData>>("/neo/registry");
      const next = response.data;
      setData(next);
      setSelectedType((current) => current || next.node_types[0]?.schema.type || "");
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  const filteredNodeTypes = useMemo(() => {
    const term = query.trim().toLowerCase();
    const source = data?.node_types ?? [];
    if (!term) return source;
    return source.filter((item) => {
      const schema = item.schema;
      return [schema.type, schema.title ?? "", schema.description ?? ""]
        .join(" ")
        .toLowerCase()
        .includes(term);
    });
  }, [data, query]);

  const filteredConditions = useMemo(() => {
    const term = query.trim().toLowerCase();
    const source = data?.conditions ?? [];
    if (!term) return source;
    return source.filter((item) =>
      [item.type, item.title ?? "", item.description ?? ""]
        .join(" ")
        .toLowerCase()
        .includes(term)
    );
  }, [data, query]);

  const selected = useMemo(() => {
    if (!filteredNodeTypes.length) return null;
    return filteredNodeTypes.find((item) => item.schema.type === selectedType) ?? filteredNodeTypes[0];
  }, [filteredNodeTypes, selectedType]);

  const selectedConfigFields = useMemo(() => {
    if (!selected) return [];
    return configSchemaProperties(selected.schema.config_schema);
  }, [selected]);

  const selectedRequiredConfigKeys = useMemo(() => {
    if (!selected) return new Set<string>();
    return new Set(requiredSchemaKeys(selected.schema.config_schema.required));
  }, [selected]);

  const stateAccessorsByPath = useMemo(() => {
    const accessors = new Map<string, StateAccessor[]>();
    for (const nodeType of data?.node_types ?? []) {
      for (const field of nodeType.resolved_state_contract?.fields ?? []) {
        const items = accessors.get(field.path) ?? [];
        items.push({
          nodeType: nodeType.schema.type,
          nodeTitle: nodeType.schema.title || nodeType.schema.type,
          mode: field.mode,
        });
        accessors.set(field.path, items);
      }
    }
    for (const items of accessors.values()) {
      items.sort((a, b) => a.nodeTitle.localeCompare(b.nodeTitle) || a.mode.localeCompare(b.mode));
    }
    return accessors;
  }, [data]);

  useEffect(() => {
    if (selected && selected.schema.type !== selectedType) {
      setSelectedType(selected.schema.type);
    }
  }, [selected, selectedType]);

  return (
    <div className="h-full overflow-hidden bg-background">
      <div className="mx-auto flex h-full max-w-[1500px] min-h-0 flex-col px-5 py-5">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-border pb-4">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center border border-border">
              <Blocks className="h-4 w-4 text-foreground" />
            </div>
            <div>
              <div className="text-sm font-semibold">Registry</div>
              <div className="text-xs text-muted-foreground">default graph node definitions</div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Link to="/">
              <Button variant="outline" size="sm" className="gap-1.5">
                <MessageSquare className="h-4 w-4" />
                Chat
              </Button>
            </Link>
            <Link to="/settings">
              <Button variant="outline" size="sm" className="gap-1.5">
                <Settings2 className="h-4 w-4" />
                Settings
              </Button>
            </Link>
            <Button variant="default" size="sm" className="gap-1.5" onClick={() => void load()} disabled={loading}>
              <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} />
              Refresh
            </Button>
          </div>
        </div>

        {error ? (
          <div className="mb-4 border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        ) : null}

        <div className="grid min-h-0 flex-1 border border-border bg-card xl:grid-cols-[320px_minmax(0,1fr)]">
          <div className="flex min-h-0 flex-col overflow-hidden border-r border-border">
            <div className="flex border-b border-border">
              <button
                type="button"
                onClick={() => setLeftTab("nodes")}
                className={cn(
                  "flex-1 border-b-2 px-3 py-2 text-xs font-medium transition-colors",
                  leftTab === "nodes"
                    ? "-mb-px border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground"
                )}
              >
                Nodes {data?.node_types.length ?? 0}
              </button>
              <button
                type="button"
                onClick={() => setLeftTab("conditions")}
                className={cn(
                  "flex-1 border-b-2 px-3 py-2 text-xs font-medium transition-colors",
                  leftTab === "conditions"
                    ? "-mb-px border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground"
                )}
              >
                Conditions {data?.conditions.length ?? 0}
              </button>
            </div>
            <div className="border-b border-border px-3 py-2">
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={leftTab === "nodes" ? "Search nodes..." : "Search conditions..."}
                  className="pl-8"
                />
              </div>
            </div>
            <div className="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
              {loading && !data ? <div className="px-3 py-2 text-xs text-muted-foreground">Loading...</div> : null}
              {leftTab === "nodes" ? (
                <>
                  {!loading && filteredNodeTypes.length === 0 ? (
                    <div className="px-3 py-2 text-xs text-muted-foreground">No matching node types.</div>
                  ) : null}
                  {filteredNodeTypes.map((item) => {
                    const active = selected?.schema.type === item.schema.type;
                    return (
                      <button
                        key={item.schema.type}
                        type="button"
                        onClick={() => setSelectedType(item.schema.type)}
                        className={cn(
                          "w-full border-l-2 px-3 py-2.5 text-left transition-colors",
                          active
                            ? "border-l-primary bg-muted/40"
                            : "border-l-transparent hover:bg-muted/20"
                        )}
                      >
                        <div className="flex items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="truncate text-sm font-semibold">{item.schema.title || item.schema.type}</div>
                            <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
                              {item.schema.type}
                            </div>
                          </div>
                          <Badge variant={item.resolved_state_contract ? "success" : "outline"}>
                            {item.resolved_state_contract?.fields?.length ?? 0}
                          </Badge>
                        </div>
                      </button>
                    );
                  })}
                </>
              ) : (
                <>
                  {!loading && filteredConditions.length === 0 ? (
                    <div className="px-3 py-2 text-xs text-muted-foreground">No matching conditions.</div>
                  ) : null}
                  {filteredConditions.map((condition) => (
                    <div key={condition.type} className="px-3 py-2.5">
                      <div className="text-sm font-semibold">{condition.title || condition.type}</div>
                      <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">{condition.type}</div>
                      {condition.description ? (
                        <div className="mt-1.5 text-xs leading-5 text-muted-foreground">{condition.description}</div>
                      ) : null}
                    </div>
                  ))}
                </>
              )}
            </div>
          </div>

          <div className="min-h-0 overflow-y-auto">
            {selected ? (
              <div className="divide-y divide-border">
                <div className="px-4 py-3">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <div className="text-lg font-semibold">{selected.schema.title || selected.schema.type}</div>
                      <div className="mt-1 font-mono text-[11px] text-muted-foreground">{selected.schema.type}</div>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      <Badge variant="secondary">{selected.resolved_state_contract?.fields.length ?? 0} fields</Badge>
                      {selected.resolve_error ? <Badge variant="destructive">resolve error</Badge> : null}
                      {selected.schema.state_contract ? <Badge variant="outline">schema contract</Badge> : null}
                    </div>
                  </div>
                  {selected.schema.description ? (
                    <div className="mt-2 text-xs leading-5 text-muted-foreground">{selected.schema.description}</div>
                  ) : null}
                </div>

                <details className="group">
                  <summary className="cursor-pointer list-none px-4 py-2.5 text-sm font-medium">
                    Config Schema
                  </summary>
                  <div className="border-t border-border px-4 pb-3 pt-2">
                    <JsonBlock value={selected.schema.config_schema} />
                  </div>
                </details>

                <details className="group">
                  <summary className="cursor-pointer list-none px-4 py-2.5 text-sm font-medium">
                    Raw Node Schema
                  </summary>
                  <div className="border-t border-border px-4 pb-3 pt-2">
                    <JsonBlock value={selected.schema} />
                  </div>
                </details>

                <div className="grid 2xl:grid-cols-[minmax(0,1.1fr)_minmax(360px,0.9fr)]">
                  <section className="px-4 py-3">
                    <div className="mb-2 text-sm font-semibold">Resolved Contract</div>
                    <div>
                      {selected.resolve_error ? (
                        <div className="mb-2 border border-destructive/40 bg-destructive/5 px-2 py-2 text-xs text-destructive">
                          {selected.resolve_error}
                        </div>
                      ) : null}
                      {selected.resolved_state_contract?.fields?.length ? (
                        <table className="w-full border-collapse text-xs">
                          <thead>
                            <tr className="border-b border-border text-left">
                              <th className="py-1.5 pl-3 pr-2 font-medium text-muted-foreground">Path</th>
                              <th className="px-2 py-1.5 font-medium text-muted-foreground">Mode</th>
                              <th className="px-2 py-1.5 font-medium text-muted-foreground">Flags</th>
                              <th className="px-2 py-1.5 font-medium text-muted-foreground">Nodes</th>
                              <th className="py-1.5 pl-2 pr-3 font-medium text-muted-foreground">Description</th>
                            </tr>
                          </thead>
                          <tbody>
                            {selected.resolved_state_contract.fields.map((field) => (
                              <FieldRow
                                key={`${selected.schema.type}:${field.path}:${field.mode}`}
                                field={field}
                                accessors={stateAccessorsByPath.get(field.path) ?? []}
                                currentNodeType={selected.schema.type}
                              />
                            ))}
                          </tbody>
                        </table>
                      ) : !selected.resolve_error ? (
                        <div className="text-xs text-muted-foreground">No resolved contract fields.</div>
                      ) : null}
                    </div>
                  </section>

                  <section className="border-t border-border px-4 py-3 2xl:border-t-0 2xl:border-l">
                    <div className="mb-2 flex items-center justify-between gap-2">
                      <div className="text-sm font-semibold">Node Config</div>
                      <Badge variant="outline">{selectedConfigFields.length} fields</Badge>
                    </div>
                    {selectedConfigFields.length ? (
                      <div className="divide-y divide-border">
                        {selectedConfigFields.map(([key, value]) => (
                          <ConfigSchemaField
                            key={key}
                            name={key}
                            schema={(value as JsonObject) ?? {}}
                            required={selectedRequiredConfigKeys.has(key)}
                            exampleValue={selected.example_config?.[key]}
                          />
                        ))}
                      </div>
                    ) : (
                      <div className="text-xs text-muted-foreground">No config fields.</div>
                    )}
                    {Object.entries(selected.example_config ?? {}).length ? (
                      <details className="group border-t border-border pt-2">
                        <summary className="cursor-pointer list-none py-2 text-[11px] font-medium text-muted-foreground">
                          Parsed Example Config
                        </summary>
                        <div className="divide-y divide-border border-t border-border pt-1">
                          {Object.entries(selected.example_config ?? {}).map(([key, value]) => (
                            <ConfigValue key={key} label={key} value={value} />
                          ))}
                        </div>
                      </details>
                    ) : null}
                  </section>
                </div>
              </div>
            ) : (
              <div className="px-4 py-4 text-sm text-muted-foreground">
                Select a node type to inspect the registry definition.
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
