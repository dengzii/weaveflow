import type { ThemePreference } from "../../lib/theme";

export function statusTone(status?: string): "neutral" | "ok" | "warn" | "danger" | "live" {
  switch (status) {
    case "completed":
    case "finished":
    case "succeeded":
      return "ok";
    case "running":
    case "pending":
      return "live";
    case "paused":
      return "warn";
    case "failed":
    case "canceled":
      return "danger";
    default:
      return "neutral";
  }
}

export function themePreferenceLabel(preference: ThemePreference): string {
  switch (preference) {
    case "light":
      return "Light";
    case "dark":
      return "Dark";
    default:
      return "System";
  }
}
