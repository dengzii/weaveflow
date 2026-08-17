import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JsonSchemaForm, parseJSONControlText, validateSchemaValue } from "./schemaForm";
import { setPathValue } from "./schemaFormModel";

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

  test("renders object arrays as structured item editors", () => {
    const html = renderToStaticMarkup(createElement(JsonSchemaForm, {
      schema,
      value: { members: [{ id: "researcher", description: "Find facts." }] },
      onChange: () => {},
    }));

    expect(html).toContain("Item 1");
    expect(html).toContain("Add Item");
    expect(html).not.toContain("<textarea");
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

  test("rejects unsafe schema paths", () => {
    expect(setPathValue({}, "__proto__.polluted", "yes")).toEqual({});
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });

  test("renders sensitive values with a fixed ten-character mask", () => {
    const html = renderToStaticMarkup(createElement(JsonSchemaForm, {
      schema: {
        type: "object",
        properties: {
          secret: { type: "string", writeOnly: true },
          api_key: { type: "string", format: "password" },
        },
      },
      value: { secret: "short", api_key: "much-longer-api-key" },
      onChange: () => {},
    }));

    expect(html.match(/value="\*{10}"/g)).toHaveLength(2);
    expect(html).not.toContain("short");
    expect(html).not.toContain("much-longer-api-key");
  });

  test("renders configured write-only values as masked when the value is redacted", () => {
    const html = renderToStaticMarkup(createElement(JsonSchemaForm, {
      schema: {
        type: "object",
        properties: {
          secret: { type: "string", writeOnly: true },
        },
      },
      value: {},
      writeOnlyValuesConfigured: true,
      onChange: () => {},
    }));

    expect(html).toContain('value="**********"');
  });

  test("renders model ID suggestions and quick add for model fields", () => {
    const html = renderToStaticMarkup(createElement(JsonSchemaForm, {
      schema: {
        type: "object",
        properties: {
          model_id: { type: "string", title: "Model ID" },
        },
      },
      value: { model_id: "default" },
      modelIDs: ["default", "reviewer"],
      onAddModel: () => {},
      onChange: () => {},
    }));

    expect(html).toContain('<option value="default"></option>');
    expect(html).toContain('<option value="reviewer"></option>');
    expect(html).toContain('aria-label="Add model"');
  });
});
