import { describe, expect, test } from "bun:test";
import { validateSchemaValue } from "./schemaForm";

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
