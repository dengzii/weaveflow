import { type ComponentType, type FormEvent, useEffect, useState } from "react";
import { Palette, RotateCcw, Save, Server, Settings, X } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input, SensitiveInput } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import { getServerInfo } from "../../api";
import type { ServerInfo } from "../../types";
import {
  getBackendBaseUrl,
  getStoredBackendBaseUrls,
  getManagementToken,
  hasStoredBackendBaseUrl,
  resetStoredBackendBaseUrl,
  resetStoredManagementToken,
  setStoredBackendBaseUrl,
  setStoredManagementToken,
} from "../../lib/backend";
import { themePreferences, useTheme, type ThemePreference } from "../../lib/theme";
import { cn } from "../../lib/utils";
import { WorkbenchDialogOverlay } from "./shared";
import { themePreferenceLabel } from "./utils";

type SettingsSection = "server" | "appearance";

const settingsSections: Array<{
  key: SettingsSection;
  label: string;
  description: string;
  icon: ComponentType<{ className?: string }>;
}> = [
  {
    key: "server",
    label: "Server",
    description: "API",
    icon: Server,
  },
  {
    key: "appearance",
    label: "Appearance",
    description: "Theme",
    icon: Palette,
  },
];

export function SettingsDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { preference, setPreference } = useTheme();
  const [activeSection, setActiveSection] = useState<SettingsSection>("server");
  const [backendBaseUrl, setBackendBaseUrl] = useState(getBackendBaseUrl);
  const [managementToken, setManagementToken] = useState(getManagementToken);
  const [backendError, setBackendError] = useState("");
  const [serverInfo, setServerInfo] = useState<ServerInfo | null>(null);
  const [serverInfoError, setServerInfoError] = useState("");
  const hasBackendOverride = hasStoredBackendBaseUrl();
  const rememberedBackendBaseUrls = getStoredBackendBaseUrls();

  useEffect(() => {
    if (!open) return;
    let active = true;
    setServerInfo(null);
    setServerInfoError("");
    getServerInfo().then(
      (info) => {
        if (active) setServerInfo(info);
      },
      (error) => {
        if (active) setServerInfoError(error instanceof Error ? error.message : String(error));
      }
    );
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      active = false;
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [onClose, open]);

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

  if (!open) return null;

  return (
    <WorkbenchDialogOverlay onDismiss={onClose}>
      <div
        className="flex h-[min(620px,92vh)] w-[min(900px,96vw)] min-w-0 flex-col rounded-md border border-border bg-panel shadow-xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="settings-dialog-title"
      >
        <div className="flex h-14 shrink-0 items-center gap-2 border-b border-border px-4">
          <Settings className="h-4 w-4 text-muted-foreground" />
          <div id="settings-dialog-title" className="text-sm font-semibold">Settings</div>
          <Button className="ml-auto" variant="ghost" size="icon" onClick={onClose} title="Close settings">
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="grid min-h-0 flex-1 grid-cols-[220px_minmax(0,1fr)]">
          <aside className="min-h-0 overflow-auto border-r border-border bg-muted/30 p-2">
            {settingsSections.map((section) => (
              <SettingsSectionButton
                key={section.key}
                section={section}
                active={section.key === activeSection}
                onClick={() => setActiveSection(section.key)}
              />
            ))}
          </aside>

          <section className="min-h-0 overflow-auto bg-background p-6">
            {activeSection === "server" ? (
              <ServerSettings
                backendBaseUrl={backendBaseUrl}
                managementToken={managementToken}
                backendError={backendError}
                serverInfo={serverInfo}
                serverInfoError={serverInfoError}
                hasBackendOverride={hasBackendOverride}
                rememberedBackendBaseUrls={rememberedBackendBaseUrls}
                onBackendBaseUrlChange={(value) => {
                  setBackendBaseUrl(value);
                  setBackendError("");
                }}
                onManagementTokenChange={(value) => {
                  setManagementToken(value);
                  setBackendError("");
                }}
                onSave={saveBackend}
                onReset={resetBackend}
              />
            ) : (
              <AppearanceSettings
                preference={preference}
                onPreferenceChange={setPreference}
              />
            )}
          </section>
        </div>
      </div>
    </WorkbenchDialogOverlay>
  );
}

function SettingsSectionButton({
  section,
  active,
  onClick,
}: {
  section: (typeof settingsSections)[number];
  active: boolean;
  onClick: () => void;
}) {
  const Icon = section.icon;
  return (
    <button
      type="button"
      className={cn(
        "mb-1 flex w-full items-start gap-2 rounded-md px-3 py-2.5 text-left transition-colors",
        active ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground"
      )}
      onClick={onClick}
      aria-current={active ? "page" : undefined}
    >
      <Icon className="mt-0.5 h-4 w-4 shrink-0" />
      <span className="min-w-0">
        <span className="block truncate text-sm font-medium">{section.label}</span>
        <span className={cn("mt-0.5 block truncate text-xs", active ? "text-primary-foreground/70" : "text-muted-foreground")}>
          {section.description}
        </span>
      </span>
    </button>
  );
}

function ServerSettings({
  backendBaseUrl,
  managementToken,
  backendError,
  serverInfo,
  serverInfoError,
  hasBackendOverride,
  rememberedBackendBaseUrls,
  onBackendBaseUrlChange,
  onManagementTokenChange,
  onSave,
  onReset,
}: {
  backendBaseUrl: string;
  managementToken: string;
  backendError: string;
  serverInfo: ServerInfo | null;
  serverInfoError: string;
  hasBackendOverride: boolean;
  rememberedBackendBaseUrls: string[];
  onBackendBaseUrlChange: (value: string) => void;
  onManagementTokenChange: (value: string) => void;
  onSave: (event: FormEvent<HTMLFormElement>) => void;
  onReset: () => void;
}) {
  return (
    <div className="mx-auto w-full max-w-xl">
      <div className="border-b border-border pb-4">
        <div className="text-base font-semibold">Server</div>
      </div>
      <form className="mt-5 grid gap-5" onSubmit={onSave}>
        <label className="grid gap-1.5 text-sm">
          <span className="font-medium">Base URL</span>
          <Input
            value={backendBaseUrl}
            onChange={(event) => onBackendBaseUrlChange(event.target.value)}
            spellCheck={false}
            autoCapitalize="none"
            aria-invalid={backendError ? true : undefined}
            list="remembered-backend-base-urls"
          />
          <datalist id="remembered-backend-base-urls">
            {rememberedBackendBaseUrls.map((value) => <option key={value} value={value} />)}
          </datalist>
        </label>
        <label className="grid gap-1.5 text-sm">
          <span className="font-medium">Token</span>
          <SensitiveInput
            value={managementToken}
            configured={Boolean(managementToken)}
            onValueChange={onManagementTokenChange}
            placeholder="WEAVEFLOW_MANAGEMENT_TOKEN"
          />
        </label>
        {backendError ? <div className="text-xs text-destructive">{backendError}</div> : null}
        <div className="text-xs text-muted-foreground">
          {formatServerInfo(serverInfo, serverInfoError)}
        </div>
        <div className="flex gap-2 border-t border-border pt-4">
          <Button type="submit" size="sm">
            <Save className="h-3.5 w-3.5" />
            Apply
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={onReset} disabled={!hasBackendOverride}>
            <RotateCcw className="h-3.5 w-3.5" />
            Reset
          </Button>
        </div>
      </form>
    </div>
  );
}

function formatServerInfo(info: ServerInfo | null, error: string): string {
  if (error) return "Server version: Unavailable";
  if (!info) return "Server version: Loading...";
  const version = info.version || "Unknown";
  if (!info.build_time) return `Server version: ${version}`;
  const timestamp = Date.parse(info.build_time);
  const buildTime = Number.isNaN(timestamp) ? info.build_time : new Date(timestamp).toLocaleString();
  return `Server version: ${version} · Built ${buildTime}`;
}

function AppearanceSettings({
  preference,
  onPreferenceChange,
}: {
  preference: ThemePreference;
  onPreferenceChange: (preference: ThemePreference) => void;
}) {
  return (
    <div className="mx-auto w-full max-w-xl">
      <div className="border-b border-border pb-4">
        <div className="text-base font-semibold">Appearance</div>
      </div>
      <label className="mt-5 grid max-w-sm gap-1.5 text-sm">
        <span className="font-medium">Theme</span>
        <Select
          value={preference}
          onChange={(event) => onPreferenceChange(event.target.value as ThemePreference)}
        >
          {themePreferences.map((item) => (
            <option key={item} value={item}>
              {themePreferenceLabel(item)}
            </option>
          ))}
        </Select>
      </label>
    </div>
  );
}
