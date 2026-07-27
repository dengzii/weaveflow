import { describe, expect, test } from "bun:test";
import { parseJSONControlText, validateSchemaValue } from "./schemaForm";

describe("validateSchemaValue object lists", () => {
  const schema = {
    type: "object",
    properties: {
      members: {
        type: "array",
        items: {
          type: "object",
          properties: {
            id: { type: "string" },
            description: { type: "string" },
          },
          required: ["id", "description"],
        },
      },
    },
    required: ["members"],
  };

  test("validates required fields inside each member", () => {
    const issues = validateSchemaValue(schema, { members: [{ id: "researcher" }] });
    expect(issues).toContainEqual({ path: "members.0.description", message: "Required field." });
  });

  test("accepts complete member entries", () => {
    expect(validateSchemaValue(schema, {
      members: [{ id: "researcher", description: "Find facts." }],
    })).toEqual([]);
  });

  test("enforces minimum member count", () => {
    const schemaWithMinimum = {
      ...schema,
      properties: {
        members: { ...schema.properties.members, minItems: 1 },
      },
    };
    expect(validateSchemaValue(schemaWithMinimum, { members: [] })).toContainEqual({
      path: "members",
      message: "Expected at least 1 item.",
    });
  });
});

describe("JSON schema controls", () => {
  const schema = {
    type: "object",
    properties: {
      value: {
        type: ["null", "boolean", "number", "string", "array", "object"],
        "x-control": "json",
      },
    },
    required: ["value"],
  };

  test("distinguishes an absent value from explicit null", () => {
    expect(validateSchemaValue(schema, {})).toContainEqual({ path: "value", message: "Required field." });
    expect(validateSchemaValue(schema, { value: null })).toEqual([]);
    expect(validateSchemaValue(schema, { value: "" })).toEqual([]);
  });

  test("parses every JSON value without treating invalid text as a string", () => {
    expect(parseJSONControlText("null")).toEqual({ ok: true, value: null });
    expect(parseJSONControlText("42")).toEqual({ ok: true, value: 42 });
    expect(parseJSONControlText('["a"]')).toEqual({ ok: true, value: ["a"] });
    expect(parseJSONControlText("not-json")).toEqual({ ok: false });
  });
});
