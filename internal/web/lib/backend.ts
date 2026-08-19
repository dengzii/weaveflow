export const DEFAULT_BACKEND_BASE_URL = "http://localhost:8080";

const storageKey = "weaveflow:web:backend-base-url:v1";
const historyStorageKey = "weaveflow:web:backend-base-url-history:v1";
const managementTokenStorageKey = "weaveflow:web:management-token:v1";
const maxRememberedBackendBaseURLs = 8;

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
  const history = getStoredBackendBaseUrls().filter((item) => item !== normalized);
  window.localStorage.setItem(
    historyStorageKey,
    JSON.stringify([normalized, ...history].slice(0, maxRememberedBackendBaseURLs))
  );
  return normalized;
}

export function resetStoredBackendBaseUrl(): void {
  window.localStorage.removeItem(storageKey);
  window.localStorage.removeItem(historyStorageKey);
}

export function hasStoredBackendBaseUrl(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(storageKey) !== null;
  } catch {
    return false;
  }
}

export function getStoredBackendBaseUrls(): string[] {
  if (typeof window === "undefined") return [];
  try {
    const parsed = JSON.parse(window.localStorage.getItem(historyStorageKey) ?? "null") as unknown;
    const history = Array.isArray(parsed) ? parsed.flatMap((item) => {
      if (typeof item !== "string") return [];
      try {
        return [normalizeBackendBaseUrl(item)];
      } catch {
        return [];
      }
    }) : [];
    const current = window.localStorage.getItem(storageKey);
    const values = current ? [current, ...history] : history;
    return values.map((item) => normalizeBackendBaseUrl(item)).filter((item, index, items) => items.indexOf(item) === index);
  } catch {
    return [];
  }
}

export function getManagementToken(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(managementTokenStorageKey)?.trim() ?? "";
  } catch {
    return "";
  }
}

export function setStoredManagementToken(value: string): string {
  const token = value.trim();
  if (token) window.localStorage.setItem(managementTokenStorageKey, token);
  else window.localStorage.removeItem(managementTokenStorageKey);
  return token;
}

export function resetStoredManagementToken(): void {
  window.localStorage.removeItem(managementTokenStorageKey);
}

export function managementHeaders(input?: HeadersInit): Headers {
  const headers = new Headers(input);
  const token = getManagementToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  return headers;
}

function readStoredBackendBaseUrl(): string {
  try {
    const stored = window.localStorage.getItem(storageKey);
    return stored ? normalizeBackendBaseUrl(stored) : "";
  } catch {
    return "";
  }
}
