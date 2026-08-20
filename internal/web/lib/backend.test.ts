import { describe, expect, test } from "bun:test";
import {
  DEFAULT_BACKEND_BASE_URL,
  getManagementToken,
  joinBackendUrl,
  managementHeaders,
  normalizeBackendBaseUrl,
  resetStoredManagementToken,
  resolveBackendUrl,
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

  test("supports same-origin backend paths", () => {
    expect(normalizeBackendBaseUrl("/api///")).toBe("/api");
    expect(joinBackendUrl("/api", "/runs")).toBe("/api/runs");
    expect(joinBackendUrl("/", "/runs")).toBe("/runs");
  });

  test("rejects unsupported or ambiguous base URLs", () => {
    expect(() => normalizeBackendBaseUrl("file:///tmp/api")).toThrow("http or https");
    expect(() => normalizeBackendBaseUrl("//api.example.com")).toThrow("protocol-relative");
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
