import { stringifyJSON } from "../../../lib/utils";
import type { GraphDefinition, RegistryInfo } from "../../../types";

export function StateModulesEditor({
  definition,
  registry,
  onChange,
}: {
  definition: GraphDefinition | null;
  registry: RegistryInfo | null;
  onChange: (modules: NonNullable<GraphDefinition["state_modules"]>) => void;
}) {
  const selected = new Set((definition?.state_modules ?? []).map((module) => `${module.name}\u0000${module.version}`));
  const modules = registry?.state_modules ?? [];

  if (modules.length === 0) {
    return (
      <pre className="max-h-40 overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-[11px]">
        {stringifyJSON(definition?.state_modules ?? [])}
      </pre>
    );
  }

  return (
    <div className="grid gap-2">
      {modules.map((module) => {
        const key = `${module.name}\u0000${module.version}`;
        const checked = selected.has(key);
        return (
          <label key={key} className="flex cursor-pointer items-start gap-2 rounded-md border border-border bg-muted/30 p-2">
            <input
              type="checkbox"
              checked={checked}
              className="mt-0.5"
              onChange={() => {
                const current = definition?.state_modules ?? [];
                onChange(
                  checked
                    ? current.filter((item) => item.name !== module.name || item.version !== module.version)
                    : [...current, { name: module.name, version: module.version }]
                );
              }}
            />
            <span className="min-w-0">
              <span className="block break-all font-mono text-xs">{module.name}@{module.version}</span>
              <span className="block text-[11px] text-muted-foreground">
                {(module.fields?.length ?? 0)} fields · {(module.capabilities?.length ?? 0)} capabilities
              </span>
            </span>
          </label>
        );
      })}
    </div>
  );
}
