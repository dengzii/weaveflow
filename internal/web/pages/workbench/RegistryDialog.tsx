import { useEffect, useMemo, useState, type ComponentType } from "react";
import { Braces, ChevronRight, FileJson, ListTree, Search, Settings, X } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { groupNodeTypes } from "../../lib/nodeGroups";
import { cn } from "../../lib/utils";
import type {
  ConditionSchema,
  NodeTypeSchema,
  RegistryInfo,
  StateCapabilityDefinition,
  StateModuleDefinition,
  ToolDefinition,
} from "../../types";
import { WorkbenchDialogOverlay } from "./shared";

type RegistrySectionKey = "nodes" | "tools" | "conditions" | "modules" | "capabilities" | "schema";

interface RegistrySection {
  key: RegistrySectionKey;
  label: string;
  count: number;
  icon: ComponentType<{ className?: string }>;
  items: RegistryItem[];
  groups?: RegistryItemGroup[];
}

interface RegistryItemGroup {
  name: string;
  items: RegistryItem[];
}

interface RegistryItem {
  key: string;
  title: string;
  description?: string;
  definition: unknown;
  searchText: string;
}

export function RegistryDialog({
  open,
  registry,
  toolDefinitions,
  onClose,
}: {
  open: boolean;
  registry: RegistryInfo | null;
  toolDefinitions: ToolDefinition[];
  onClose: () => void;
}) {
  const [activeSection, setActiveSection] = useState<RegistrySectionKey>("nodes");
  const [query, setQuery] = useState("");

  useEffect(() => {
    if (!open) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose, open]);

  useEffect(() => {
    if (open) return;
    setQuery("");
  }, [open]);

  const sections = useMemo<RegistrySection[]>(
    () => [
      {
        key: "nodes",
        label: "Nodes",
        count: registry?.node_types?.length ?? 0,
        icon: ListTree,
        items: (registry?.node_types ?? []).map(nodeTypeItem),
        groups: groupNodeTypes(registry?.node_types ?? [], registry?.node_groups ?? []).map((group) => ({
          name: group.name,
          items: group.nodeTypes.map(nodeTypeItem),
        })),
      },
      {
        key: "tools",
        label: "Tools",
        count: toolDefinitions.length,
        icon: Settings,
        items: toolDefinitions.map(toolItem),
      },
      {
        key: "conditions",
        label: "Conditions",
        count: registry?.conditions?.length ?? 0,
        icon: Braces,
        items: (registry?.conditions ?? []).map(conditionItem),
      },
      {
        key: "modules",
        label: "State Modules",
        count: registry?.state_modules?.length ?? 0,
        icon: FileJson,
        items: (registry?.state_modules ?? []).map(stateModuleItem),
      },
      {
        key: "capabilities",
        label: "Capabilities",
        count: registry?.capabilities?.length ?? 0,
        icon: FileJson,
        items: (registry?.capabilities ?? []).map(capabilityItem),
      },
      {
        key: "schema",
        label: "Graph Schema",
        count: registry?.graph_schema ? 1 : 0,
        icon: FileJson,
        items: registry?.graph_schema
          ? [
              {
                key: "graph_schema",
                title: "Graph Definition Schema",
                definition: registry.graph_schema,
                searchText: searchableText("Graph Definition Schema", "json schema", "", registry.graph_schema),
              },
            ]
          : [],
      },
    ],
    [registry, toolDefinitions]
  );

  const active = sections.find((section) => section.key === activeSection) ?? sections[0];
  const normalizedQuery = query.trim().toLowerCase();
  const filteredItems = normalizedQuery
    ? active.items.filter((item) => item.searchText.includes(normalizedQuery))
    : active.items;
  const filteredGroups = (active.groups ?? [])
    .map((group) => ({
      ...group,
      items:
        normalizedQuery && !group.name.toLowerCase().includes(normalizedQuery)
          ? group.items.filter((item) => item.searchText.includes(normalizedQuery))
          : group.items,
    }))
    .filter((group) => group.items.length > 0);
  const emptyLabel = !registry && active.key !== "tools" ? "Registry unavailable" : "No matching entries";

  if (!open) return null;

  return (
    <WorkbenchDialogOverlay onDismiss={onClose}>
      <div className="flex h-[min(760px,92vh)] w-[min(1120px,96vw)] min-w-0 flex-col rounded-md border border-border bg-panel shadow-xl">
        <div className="flex h-14 shrink-0 items-center gap-3 border-b border-border px-4">
          <div className="flex min-w-0 items-center gap-2">
            <Braces className="h-4 w-4 text-muted-foreground" />
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">System Registry</div>
            </div>
          </div>
          <div className="relative ml-auto w-72">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search registry"
              className="h-8 pl-8 text-xs"
              autoFocus
            />
          </div>
          <Button variant="ghost" size="icon" onClick={onClose} title="Close registry">
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="grid min-h-0 flex-1 grid-cols-[220px_minmax(0,1fr)]">
          <aside className="min-h-0 overflow-auto border-r border-border bg-muted/30 p-2">
            {sections.map((section) => (
              <RegistrySectionButton
                key={section.key}
                section={section}
                active={section.key === active.key}
                onClick={() => setActiveSection(section.key)}
              />
            ))}
          </aside>

          <section className="min-h-0 min-w-0 overflow-auto p-4">
            <div className="grid min-w-0 gap-3">
              {active.groups && filteredGroups.length > 0 ? (
                filteredGroups.map((group) => <RegistryItemGroupCards key={group.name} group={group} />)
              ) : !active.groups && filteredItems.length > 0 ? (
                filteredItems.map((item) => <RegistryDefinitionCard key={item.key} item={item} />)
              ) : (
                <EmptyState label={emptyLabel} />
              )}
            </div>
          </section>
        </div>
      </div>
    </WorkbenchDialogOverlay>
  );
}

function RegistryItemGroupCards({ group }: { group: RegistryItemGroup }) {
  return (
    <section className="grid min-w-0 gap-2">
      <div className="flex items-center gap-2 px-1 text-xs font-semibold uppercase text-muted-foreground">
        <span>{group.name}</span>
        <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]">{group.items.length}</span>
      </div>
      <div className="grid min-w-0 gap-3">
        {group.items.map((item) => (
          <RegistryDefinitionCard key={item.key} item={item} />
        ))}
      </div>
    </section>
  );
}

function RegistrySectionButton({
  section,
  active,
  onClick,
}: {
  section: RegistrySection;
  active: boolean;
  onClick: () => void;
}) {
  const Icon = section.icon;
  return (
    <button
      className={cn(
        "mb-1 flex h-10 w-full items-center gap-2 rounded-md px-2 text-left text-sm transition-colors",
        active ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground"
      )}
      onClick={onClick}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="min-w-0 flex-1 truncate">{section.label}</span>
      <span
        className={cn(
          "rounded px-1.5 py-0.5 font-mono text-[11px]",
          active ? "bg-primary-foreground/15 text-primary-foreground" : "bg-muted text-muted-foreground"
        )}
      >
        {section.count}
      </span>
    </button>
  );
}

function RegistryDefinitionCard({ item }: { item: RegistryItem }) {
  return (
    <article className="w-full min-w-0 overflow-hidden rounded-md border border-border bg-background">
      <div className="grid gap-1 border-b border-border px-3 py-2">
        <div className="truncate text-sm font-semibold">{item.title}</div>
        {item.description ? <div className="text-xs text-muted-foreground">{item.description}</div> : null}
      </div>
      <details className="group">
        <summary className="flex h-9 cursor-pointer list-none items-center gap-2 border-b-0 px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground [&::-webkit-details-marker]:hidden">
          <ChevronRight className="h-3.5 w-3.5 transition-transform group-open:rotate-90" />
          Raw JSON
        </summary>
        <pre className="max-h-80 max-w-full overflow-x-auto overflow-y-auto whitespace-pre border-t border-border p-3 text-xs leading-relaxed">
          {formatDefinition(item.definition)}
        </pre>
      </details>
    </article>
  );
}

function EmptyState({ label }: { label: string }) {
  return (
    <div className="flex h-48 items-center justify-center rounded-md border border-dashed border-border bg-muted/30 text-sm text-muted-foreground">
      {label}
    </div>
  );
}

function nodeTypeItem(item: NodeTypeSchema): RegistryItem {
  const definition = {
    type: item.type,
    title: item.title,
    description: item.description,
    config_schema: item.config_schema,
    state_ports: item.state_ports,
    dynamic_state_ports: item.dynamic_state_ports,
  };
  const nodeName = item.title?.trim() || "Node";
  return {
    key: `node:${item.type}`,
    title: nodeName,
    description: item.description,
    definition,
    searchText: searchableText(item.type, item.title, item.description, definition),
  };
}

function toolItem(item: ToolDefinition): RegistryItem {
  const definition = {
    id: item.id,
    name: item.name,
    description: item.description,
    parameters: item.parameters,
    strict: item.strict,
  };
  const toolName = item.name?.trim() || "Tool";
  return {
    key: `tool:${item.id}`,
    title: toolName,
    description: item.description,
    definition,
    searchText: searchableText(item.id, item.name, item.description, definition),
  };
}

function conditionItem(item: ConditionSchema): RegistryItem {
  const definition = {
    type: item.type,
    title: item.title,
    description: item.description,
    config_schema: item.config_schema,
    state_ports: item.state_ports,
    dynamic_state_ports: item.dynamic_state_ports,
  };
  const conditionName = item.title?.trim() || "Condition";
  return {
    key: `condition:${item.type}`,
    title: conditionName,
    description: item.description,
    definition,
    searchText: searchableText(item.type, item.title, item.description, definition),
  };
}

function stateModuleItem(item: StateModuleDefinition): RegistryItem {
  return {
    key: `module:${item.name}:${item.version}`,
    title: `${item.name}@${item.version}`,
    definition: item,
    searchText: searchableText(item.name, item.version, item),
  };
}

function capabilityItem(item: StateCapabilityDefinition): RegistryItem {
  return {
    key: `capability:${item.id}`,
    title: item.id,
    definition: item,
    searchText: searchableText(item.id, item),
  };
}

function searchableText(...parts: unknown[]): string {
  return parts
    .map((part) => {
      if (typeof part === "string") return part;
      return formatDefinition(part);
    })
    .join("\n")
    .toLowerCase();
}

function formatDefinition(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2) ?? "";
  } catch {
    return String(value);
  }
}
