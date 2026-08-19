import { describe, expect, test } from "bun:test";
import {
  DEFAULT_BACKEND_BASE_URL,
  getStoredBackendBaseUrls,
  getManagementToken,
  joinBackendUrl,
  managementHeaders,
  normalizeBackendBaseUrl,
  resetStoredManagementToken,
  resolveBackendUrl,
  resetStoredBackendBaseUrl,
  setStoredBackendBaseUrl,
  setStoredManagementToken,
} from "./backend";

describe("backend URL configuration", () => {
  test("defaults to the local debug server", () => {
    expect(resolveBackendUrl("/graph")).toBe(`${DEFAULT_BACKEND_BASE_URL}/graph`);
  });

  test("normalizes host-only input and trailing slashes", () => {
    expect(normalizeBackendBaseUrl("localhost:9090///")).toBe("http://localhost:9090");
  });

  test("preserves a backend route prefix", () => {
    expect(joinBackendUrl("https://api.example.com/debug/", "/runs?limit=10")).toBe(
      "https://api.example.com/debug/runs?limit=10"
    );
  });

  test("rejects unsupported or ambiguous base URLs", () => {
    expect(() => normalizeBackendBaseUrl("file:///tmp/api")).toThrow("http or https");
    expect(() => normalizeBackendBaseUrl("https://api.example.com?tenant=one")).toThrow("query or fragment");
    expect(() => normalizeBackendBaseUrl("https://user:secret@api.example.com")).toThrow("credentials");
  });
});

describe("management token configuration", () => {
  test("stores the token locally and adds a bearer header", () => {
    const values = new Map<string, string>();
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: {
        localStorage: {
          getItem: (key: string) => values.get(key) ?? null,
          setItem: (key: string, value: string) => values.set(key, value),
          removeItem: (key: string) => values.delete(key),
        },
      },
    });
    try {
      expect(setStoredManagementToken(" token-value ")).toBe("token-value");
      expect(getManagementToken()).toBe("token-value");
      expect(managementHeaders({ Accept: "application/json" }).get("Authorization"))
        .toBe("Bearer token-value");
      resetStoredManagementToken();
      expect(getManagementToken()).toBe("");
    } finally {
      Reflect.deleteProperty(globalThis, "window");
    }
  });
});

describe("backend URL history", () => {
  test("remembers unique normalized URLs with the newest first", () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    };
    Object.defineProperty(globalThis, "window", { configurable: true, value: { localStorage: storage } });
    try {
      setStoredBackendBaseUrl("localhost:9090///");
      setStoredBackendBaseUrl("https://api.example.com");
      setStoredBackendBaseUrl("localhost:9090");
      expect(getStoredBackendBaseUrls()).toEqual([
        "http://localhost:9090",
        "https://api.example.com",
      ]);
      resetStoredBackendBaseUrl();
      expect(getStoredBackendBaseUrls()).toEqual([]);
    } finally {
      Reflect.deleteProperty(globalThis, "window");
    }
  });
});
