export const DEFAULT_BACKEND_BASE_URL = "http://localhost:8080";

const storageKey = "weaveflow:web:backend-base-url:v1";

export function normalizeBackendBaseUrl(value: string): string {
  let candidate = value.trim();
  if (!candidate) {
    throw new Error("Backend base URL is required");
  }
  if (!/^[a-z][a-z\d+.-]*:\/\//i.test(candidate)) {
    candidate = `http://${candidate}`;
  }

  const url = new URL(candidate);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("Backend base URL must use http or https");
  }
  if (url.username || url.password) {
    throw new Error("Backend base URL must not contain credentials");
  }
  if (url.search || url.hash) {
    throw new Error("Backend base URL must not contain a query or fragment");
  }

  return url.toString().replace(/\/+$/, "");
}

export function joinBackendUrl(baseUrl: string, path: string): string {
  const suffix = path.startsWith("/") ? path : `/${path}`;
  return `${normalizeBackendBaseUrl(baseUrl)}${suffix}`;
}

export function getBackendBaseUrl(): string {
  if (typeof window === "undefined") return DEFAULT_BACKEND_BASE_URL;

  const stored = readStoredBackendBaseUrl();
  if (stored) return stored;

  const configured = window.__WEAVEFLOW_CONFIG__?.backendBaseUrl;
  if (typeof configured === "string") {
    try {
      return normalizeBackendBaseUrl(configured);
    } catch {
      // Ignore invalid deployment configuration and keep the UI recoverable.
    }
  }
  return DEFAULT_BACKEND_BASE_URL;
}

export function resolveBackendUrl(path: string): string {
  return joinBackendUrl(getBackendBaseUrl(), path);
}

export function setStoredBackendBaseUrl(value: string): string {
  const normalized = normalizeBackendBaseUrl(value);
  window.localStorage.setItem(storageKey, normalized);
  return normalized;
}

export function resetStoredBackendBaseUrl(): void {
  window.localStorage.removeItem(storageKey);
}

export function hasStoredBackendBaseUrl(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(storageKey) !== null;
  } catch {
    return false;
  }
}

function readStoredBackendBaseUrl(): string {
  try {
    const stored = window.localStorage.getItem(storageKey);
    return stored ? normalizeBackendBaseUrl(stored) : "";
  } catch {
    return "";
  }
}
