import { describe, expect, test } from "bun:test";
import {
  DEFAULT_BACKEND_BASE_URL,
  joinBackendUrl,
  normalizeBackendBaseUrl,
  resolveBackendUrl,
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
