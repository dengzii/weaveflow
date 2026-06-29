import { Braces, Settings } from "lucide-react";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import { themePreferences, useTheme, type ThemePreference } from "../../lib/theme";
import { stringifyJSON } from "../../lib/utils";
import type { RegistryInfo } from "../../types";
import { extensionPoints } from "./constants";
import { PanelHeader } from "./shared";
import { themePreferenceLabel } from "./utils";

export function SettingsWorkspace({
  graphId,
  graphVersion,
  registry,
  graphSwitchDisabled,
  onGraphId,
  onGraphVersion,
}: {
  graphId: string;
  graphVersion: string;
  registry: RegistryInfo | null;
  graphSwitchDisabled: boolean;
  onGraphId: (value: string) => void;
  onGraphVersion: (value: string) => void;
}) {
  const { preference, resolvedTheme, setPreference } = useTheme();

  return (
    <div className="grid h-full min-h-0 grid-cols-[420px_minmax(0,1fr)] bg-background">
      <section className="border-r border-border bg-panel p-4">
        <PanelHeader icon={Settings} title="Settings" inline />
        <div className="mt-4 grid gap-4">
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Graph ID</span>
            <Input value={graphId} onChange={(event) => onGraphId(event.target.value)} disabled={graphSwitchDisabled} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Graph Version</span>
            <Input value={graphVersion} onChange={(event) => onGraphVersion(event.target.value)} disabled={graphSwitchDisabled} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Theme</span>
            <Select
              value={preference}
              onChange={(event) => setPreference(event.target.value as ThemePreference)}
            >
              {themePreferences.map((item) => (
                <option key={item} value={item}>
                  {themePreferenceLabel(item)}
                </option>
              ))}
            </Select>
            <span className="text-xs text-muted-foreground">Resolved: {resolvedTheme}</span>
          </label>
        </div>
      </section>
      <section className="min-h-0 overflow-auto p-4">
        <PanelHeader icon={Braces} title="Extension Points" inline />
        <div className="mt-4 grid grid-cols-2 gap-3">
          {extensionPoints.map((item) => (
            <div key={item} className="rounded-md border border-dashed border-border bg-panel p-3">
              <div className="text-sm font-medium">{item}</div>
              <div className="mt-1 text-xs text-muted-foreground">Reserved</div>
            </div>
          ))}
        </div>
        <div className="mt-4 rounded-md border border-border bg-panel p-3">
          <div className="mb-2 text-sm font-medium">Registry Snapshot</div>
          <pre className="max-h-80 overflow-auto rounded bg-muted p-3 text-xs">
            {stringifyJSON({
              state_fields: registry?.state_fields?.length ?? 0,
              node_types: registry?.node_types?.map((node) => node.type) ?? [],
              conditions: registry?.conditions?.map((condition) => condition.type) ?? [],
            })}
          </pre>
        </div>
      </section>
    </div>
  );
}
