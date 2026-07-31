import { useEffect, useState } from "react";
import { ChevronRight, Plus, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import {
  dynamicStatePortForName,
  nextDynamicStatePortName,
  resolveDefaultStatePath,
  resolvedStatePortContract,
} from "../../../lib/graphEditor";
import type {
  DynamicStatePortDefinition,
  GraphDefinition,
  RegistryInfo,
  StateBinding,
  StatePortDefinition,
} from "../../../types";
import { CollapsibleInspectorBlock } from "./shared";
import {
  bindingPathMetadata,
  compatibleBindingPaths,
  dynamicStateAliasError,
  sanitizeHTMLID,
  stateSchemaType,
} from "./stateBindingsModel";

interface StateBindingsProps {
  ownerID: string;
  ports: StatePortDefinition[];
  dynamicPorts: DynamicStatePortDefinition | undefined;
  bindings: Record<string, StateBinding> | undefined;
  definition: GraphDefinition | null;
  registry: RegistryInfo | null;
  onChange: (bindings: Record<string, StateBinding>) => void;
}

export function StateBindingsBlock(props: StateBindingsProps) {
  const [open, setOpen] = useState(true);
  return (
    <CollapsibleInspectorBlock title="State Bindings" open={open} onOpenChange={setOpen}>
      <StateBindingsEditor {...props} />
    </CollapsibleInspectorBlock>
  );
}

function StateBindingsEditor({
  ownerID,
  ports,
  dynamicPorts,
  bindings,
  definition,
  registry,
  onChange,
}: StateBindingsProps) {
  if (ports.length === 0 && !dynamicPorts) {
    return <div className="text-xs text-muted-foreground">This component declares no state ports.</div>;
  }

  const staticNames = new Set(ports.map((port) => port.name));
  const dynamicNames = Object.keys(bindings ?? {}).filter((name) => !staticNames.has(name)).sort();
  const maximumReached = Boolean(dynamicPorts?.max_ports && dynamicNames.length >= dynamicPorts.max_ports);

  return (
    <div className="grid gap-2">
      {ports.map((port) => {
        const binding = bindings?.[port.name];
        const defaultPath = resolveDefaultStatePath(port.default_path, ownerID);
        const effectiveBinding = binding ?? (defaultPath ? { path: defaultPath } : undefined);
        const options = compatibleBindingPaths(port, ownerID, definition, registry);
        const listID = `state-path-${sanitizeHTMLID(ownerID)}-${sanitizeHTMLID(port.name)}`;
        const resolvedContract = resolvedStatePortContract(port, effectiveBinding, registry);
        const bindingMetadata = effectiveBinding?.path.trim()
          ? bindingPathMetadata(effectiveBinding.path.trim(), port, definition, registry, false, false, false)
          : "";
        return (
          <div key={port.name} className="rounded-md border border-border bg-muted/30 p-2">
            <div className="mb-2 flex min-w-0 items-start gap-2">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="font-mono text-xs font-semibold">
                    {port.required ? <span className="mr-0.5 text-destructive" aria-label="required">*</span> : null}
                    {port.name}
                  </span>
                  {!port.required ? <StatePortBadge label="optional" /> : null}
                  {port.mode ? <StatePortBadge label={port.mode} /> : null}
                  {port.capability ? <StatePortBadge label={port.capability} /> : null}
                  {!port.capability && stateSchemaType(port.schema) ? <StatePortBadge label={stateSchemaType(port.schema)} /> : null}
                </div>
                {port.description ? <div className="mt-1 text-[11px] text-muted-foreground">{port.description}</div> : null}
              </div>
              {!effectiveBinding && !port.required ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7 shrink-0"
                  title={`Bind ${port.name}`}
                  aria-label={`Bind ${port.name}`}
                  onClick={() => onChange({ ...(bindings ?? {}), [port.name]: { path: options[0] ?? "" } })}
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              ) : null}
            </div>

            {effectiveBinding || port.required ? (
              <div className="flex min-w-0 items-center gap-1.5">
                <Input
                  list={listID}
                  aria-label={`${port.name} state path`}
                  value={effectiveBinding?.path ?? ""}
                  placeholder={options[0] ?? "shared.path"}
                  className={!effectiveBinding?.path.trim() && port.required ? "border-destructive focus:border-destructive" : undefined}
                  onChange={(event) => onChange({
                    ...(bindings ?? {}),
                    [port.name]: { path: event.target.value },
                  })}
                />
                <datalist id={listID}>
                  {options.map((path) => (
                    <option
                      key={path}
                      value={path}
                      label={bindingPathMetadata(path, port, definition, registry)}
                    />
                  ))}
                </datalist>
                {!port.required ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8 shrink-0"
                    title={`Remove ${port.name} binding`}
                    aria-label={`Remove ${port.name} binding`}
                    onClick={() => {
                      const next = { ...(bindings ?? {}) };
                      delete next[port.name];
                      onChange(next);
                    }}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                ) : null}
              </div>
            ) : null}

            {bindingMetadata ? (
              <div className="mt-1 break-words text-[10px] text-muted-foreground">
                {bindingMetadata}
              </div>
            ) : null}

            {resolvedContract.length > 0 ? (
              <details className="group mt-2">
                <summary className="flex cursor-pointer list-none items-center gap-1 text-[11px] text-muted-foreground [&::-webkit-details-marker]:hidden">
                  <ChevronRight className="h-3 w-3 transition-transform group-open:rotate-90" />
                  Resolved Contract · {resolvedContract.length} fields
                </summary>
                <div className="mt-1 grid gap-1 border-l border-border pl-2">
                  {resolvedContract.map((field) => (
                    <div key={`${field.path}:${field.mode}`} className="grid min-w-0 gap-0.5 text-[11px]">
                      <span className="break-all font-mono">{field.path}</span>
                      <span className="break-words text-muted-foreground">
                        {[field.mode, field.required ? "required" : "", field.type, field.mergeStrategy].filter(Boolean).join(" · ")}
                      </span>
                    </div>
                  ))}
                </div>
              </details>
            ) : (port.contract?.fields?.length ?? 0) > 0 ? (
              <details className="group mt-2">
                <summary className="flex cursor-pointer list-none items-center gap-1 text-[11px] text-muted-foreground [&::-webkit-details-marker]:hidden">
                  <ChevronRight className="h-3 w-3 transition-transform group-open:rotate-90" />
                  Relative Contract · {port.contract?.fields?.length ?? 0} fields
                </summary>
                <div className="mt-1 grid gap-1 border-l border-border pl-2">
                  {port.contract?.fields?.map((field) => (
                    <div key={`${field.path}:${field.mode}`} className="grid min-w-0 gap-0.5 text-[11px]">
                      <span className="break-all font-mono">{field.path}</span>
                      <span className="break-words text-muted-foreground">{field.mode}{field.required ? " · required" : ""}</span>
                    </div>
                  ))}
                </div>
              </details>
            ) : null}
          </div>
        );
      })}
      {dynamicPorts ? (
        <div className="grid gap-2 rounded-md border border-dashed border-border p-2">
          <div className="flex min-w-0 items-center gap-2">
            <div className="min-w-0 flex-1">
              <div className="text-xs font-semibold">Dynamic Inputs</div>
              <div className="mt-0.5 break-words text-[11px] text-muted-foreground">
                Bind aliases used by CEL as <span className="font-mono">inputs.&lt;alias&gt;</span>.
                {(dynamicPorts.min_ports ?? 0) > 0 ? ` At least ${dynamicPorts.min_ports} required.` : ""}
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 shrink-0"
              disabled={maximumReached}
              onClick={() => {
                const name = nextDynamicStatePortName(bindings, ports, dynamicPorts);
                if (!name) return;
                onChange({ ...(bindings ?? {}), [name]: { path: "" } });
              }}
            >
              <Plus className="mr-1 h-3.5 w-3.5" />
              Add input
            </Button>
          </div>
          {dynamicNames.length === 0 ? (
            <div className="rounded bg-muted/40 px-2 py-3 text-center text-[11px] text-muted-foreground">
              No dynamic inputs bound yet.
            </div>
          ) : dynamicNames.map((name) => (
            <DynamicStateBindingEditor
              key={name}
              ownerID={ownerID}
              name={name}
              binding={bindings?.[name] ?? { path: "" }}
              bindings={bindings ?? {}}
              staticNames={staticNames}
              dynamicPorts={dynamicPorts}
              definition={definition}
              registry={registry}
              onChange={onChange}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function DynamicStateBindingEditor({
  ownerID,
  name,
  binding,
  bindings,
  staticNames,
  dynamicPorts,
  definition,
  registry,
  onChange,
}: {
  ownerID: string;
  name: string;
  binding: StateBinding;
  bindings: Record<string, StateBinding>;
  staticNames: Set<string>;
  dynamicPorts: DynamicStatePortDefinition;
  definition: GraphDefinition | null;
  registry: RegistryInfo | null;
  onChange: (bindings: Record<string, StateBinding>) => void;
}) {
  const [alias, setAlias] = useState(name);
  useEffect(() => setAlias(name), [name]);
  const normalizedAlias = alias.trim();
  const aliasError = dynamicStateAliasError(alias, name, bindings, staticNames, dynamicPorts);
  const port = dynamicStatePortForName(name, dynamicPorts) ?? {
    name,
    description: dynamicPorts.description,
    required: true,
    schema: dynamicPorts.schema,
    mode: dynamicPorts.mode,
    merge_strategy: dynamicPorts.merge_strategy,
  };
  const options = compatibleBindingPaths(port, ownerID, definition, registry);
  const listID = `state-path-${sanitizeHTMLID(ownerID)}-${sanitizeHTMLID(name)}`;
  const commitAlias = () => {
    if (aliasError || normalizedAlias === name) return;
    const next = { ...bindings };
    delete next[name];
    next[normalizedAlias] = binding;
    onChange(next);
  };

  return (
    <div className="rounded-md border border-border bg-muted/30 p-2">
      <div className="mb-2 flex min-w-0 items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <StatePortBadge label={dynamicPorts.mode} />
            <StatePortBadge label={dynamicPorts.merge_strategy} />
            {stateSchemaType(dynamicPorts.schema) ? <StatePortBadge label={stateSchemaType(dynamicPorts.schema)} /> : null}
          </div>
          {dynamicPorts.description ? <div className="mt-1 text-[11px] text-muted-foreground">{dynamicPorts.description}</div> : null}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-8 w-8 shrink-0"
          title={`Remove ${name} input`}
          aria-label={`Remove ${name} input`}
          onClick={() => {
            const next = { ...bindings };
            delete next[name];
            onChange(next);
          }}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="grid gap-2 sm:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
        <div className="min-w-0">
          <div className="mb-1 text-[10px] font-medium uppercase text-muted-foreground">Alias</div>
          <Input
            aria-label={`${name} state alias`}
            value={alias}
            className={aliasError ? "border-destructive focus:border-destructive" : undefined}
            onChange={(event) => setAlias(event.target.value)}
            onBlur={commitAlias}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.currentTarget.blur();
            }}
          />
          {aliasError ? <div className="mt-1 break-words text-[10px] text-destructive">{aliasError}</div> : null}
        </div>
        <div className="min-w-0">
          <div className="mb-1 text-[10px] font-medium uppercase text-muted-foreground">State Path</div>
          <Input
            list={listID}
            aria-label={`${name} state path`}
            value={binding.path}
            placeholder={options[0] ?? "shared.path"}
            className={!binding.path.trim() ? "border-destructive focus:border-destructive" : undefined}
            onChange={(event) => onChange({ ...bindings, [name]: { path: event.target.value } })}
          />
          <datalist id={listID}>
            {options.map((path) => <option key={path} value={path} label={bindingPathMetadata(path, port, definition, registry)} />)}
          </datalist>
        </div>
      </div>
    </div>
  );
}

function StatePortBadge({ label }: { label: string }) {
  return <span className="max-w-full break-all rounded bg-background px-1.5 py-0.5 text-left font-mono text-[10px] leading-4 text-muted-foreground">{label}</span>;
}
