import { type FormEvent, useState } from "react";
import { Braces, RotateCcw, Save, Server, Settings } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input, SensitiveInput } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import {
  getBackendBaseUrl,
  getManagementToken,
  hasStoredBackendBaseUrl,
  resetStoredBackendBaseUrl,
  resetStoredManagementToken,
  setStoredBackendBaseUrl,
  setStoredManagementToken,
} from "../../lib/backend";
import { themePreferences, useTheme, type ThemePreference } from "../../lib/theme";
import { stringifyJSON } from "../../lib/utils";
import type { RegistryInfo } from "../../types";
import { extensionPoints } from "./constants";
import { PanelHeader } from "./shared";
import { themePreferenceLabel } from "./utils";

export function SettingsWorkspace({ registry }: { registry: RegistryInfo | null }) {
  const { preference, resolvedTheme, setPreference } = useTheme();
  const [backendBaseUrl, setBackendBaseUrl] = useState(getBackendBaseUrl);
  const [managementToken, setManagementToken] = useState(getManagementToken);
  const [backendError, setBackendError] = useState("");
  const hasBackendOverride = hasStoredBackendBaseUrl();

  function saveBackend(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      setStoredBackendBaseUrl(backendBaseUrl);
      setStoredManagementToken(managementToken);
      window.location.reload();
    } catch (error) {
      setBackendError(error instanceof Error ? error.message : String(error));
    }
  }

  function resetBackend() {
    try {
      resetStoredBackendBaseUrl();
      resetStoredManagementToken();
      window.location.reload();
    } catch (error) {
      setBackendError(error instanceof Error ? error.message : String(error));
    }
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-[420px_minmax(0,1fr)] bg-background">
      <section className="border-r border-border bg-panel p-4">
        <PanelHeader icon={Settings} title="Settings" inline />
        <div className="mt-4 grid gap-4">
          <form className="grid gap-2 border-b border-border pb-4" onSubmit={saveBackend}>
            <div className="flex items-center gap-2 text-sm font-medium">
              <Server className="h-4 w-4 text-muted-foreground" />
              Server API
            </div>
            <label className="grid gap-1 text-sm">
              <span className="text-xs font-medium text-muted-foreground">Backend base URL</span>
              <Input
                value={backendBaseUrl}
                onChange={(event) => {
                  setBackendBaseUrl(event.target.value);
                  setBackendError("");
                }}
                spellCheck={false}
                autoCapitalize="none"
                aria-invalid={backendError ? true : undefined}
              />
            </label>
            <label className="grid gap-1 text-sm">
              <span className="text-xs font-medium text-muted-foreground">Management token</span>
              <SensitiveInput
                value={managementToken}
                configured={Boolean(managementToken)}
                onValueChange={(value) => {
                  setManagementToken(value);
                  setBackendError("");
                }}
                placeholder="WEAVEFLOW_MANAGEMENT_TOKEN"
              />
            </label>
            {backendError ? <div className="text-xs text-destructive">{backendError}</div> : null}
            <div className="flex gap-2">
              <Button type="submit" size="sm">
                <Save className="h-3.5 w-3.5" />
                Apply
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={resetBackend} disabled={!hasBackendOverride}>
                <RotateCcw className="h-3.5 w-3.5" />
                Reset
              </Button>
            </div>
          </form>
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
              state_modules: registry?.state_modules?.map((module) => `${module.name}@${module.version}`) ?? [],
              capabilities: registry?.capabilities?.map((capability) => capability.id) ?? [],
              node_types: registry?.node_types?.map((node) => node.type) ?? [],
              conditions: registry?.conditions?.map((condition) => condition.type) ?? [],
            })}
          </pre>
        </div>
      </section>
    </div>
  );
}
