import type { GraphEdgeMeta, GraphNodeMeta, SourceGraph } from "./types";
import { SYNTHETIC_END_ID, SYNTHETIC_START_ID } from "./types";
import { objectValue, stringValue } from "./utils";

export function parseSourceGraph(raw: unknown): SourceGraph | null {
  if (!raw || typeof raw !== "object") return null;
  const value = raw as Record<string, unknown>;
  const entryPoint = stringValue(value.entry_point);
  const finishPoint = stringValue(value.finish_point);
  const rawNodes = Array.isArray(value.nodes) ? value.nodes : [];
  const rawEdges = Array.isArray(value.edges) ? value.edges : [];

  const nodes = rawNodes
    .map((node) => {
      if (!node || typeof node !== "object") return null;
      const item = node as Record<string, unknown>;
      const id = stringValue(item.id);
      if (!id) return null;
      return {
        id,
        name: stringValue(item.name) || id,
        type: stringValue(item.type) || "node",
        description: stringValue(item.description),
        config: objectValue(item.config),
      };
    })
    .filter((item): item is GraphNodeMeta => Boolean(item));

  const nodeIds = new Set(nodes.map((item) => item.id));
  const hasEntryNode = entryPoint && nodeIds.has(entryPoint);
  const needsEndNode =
    finishPoint === "__end__" ||
    rawEdges.some((edge) => {
      if (!edge || typeof edge !== "object") return false;
      return stringValue((edge as Record<string, unknown>).to) === "__end__";
    });

  if (hasEntryNode) {
    nodes.unshift({
      id: SYNTHETIC_START_ID,
      name: "START",
      type: "start",
      description: "Graph entry",
      config: null,
    });
    nodeIds.add(SYNTHETIC_START_ID);
  }

  if (needsEndNode || (finishPoint && nodeIds.has(finishPoint))) {
    nodes.push({
      id: SYNTHETIC_END_ID,
      name: "END",
      type: "end",
      description: "Graph exit",
      config: null,
    });
    nodeIds.add(SYNTHETIC_END_ID);
  }

  const edges = rawEdges
    .map((edge, index) => {
      if (!edge || typeof edge !== "object") return null;
      const item = edge as Record<string, unknown>;
      const from = stringValue(item.from);
      const rawTo = stringValue(item.to);
      const to = rawTo === "__end__" ? SYNTHETIC_END_ID : rawTo;
      if (!from || !to || !nodeIds.has(from) || !nodeIds.has(to)) return null;
      const condition = objectValue(item.condition);
      return {
        id: `${from}-->${to}#${index}`,
        from,
        to,
        label: conditionLabel(condition),
        conditional: Boolean(condition),
      };
    })
    .filter((item): item is GraphEdgeMeta => Boolean(item));

  if (hasEntryNode) {
    edges.unshift({
      id: `${SYNTHETIC_START_ID}-->${entryPoint}#synthetic`,
      from: SYNTHETIC_START_ID,
      to: entryPoint,
      label: "",
      conditional: false,
    });
  }

  if (
    finishPoint &&
    finishPoint !== "__end__" &&
    nodeIds.has(finishPoint) &&
    nodeIds.has(SYNTHETIC_END_ID) &&
    !edges.some((edge) => edge.from === finishPoint && edge.to === SYNTHETIC_END_ID)
  ) {
    edges.push({
      id: `${finishPoint}-->${SYNTHETIC_END_ID}#synthetic`,
      from: finishPoint,
      to: SYNTHETIC_END_ID,
      label: "",
      conditional: false,
    });
  }

  return {
    entry_point: hasEntryNode ? SYNTHETIC_START_ID : entryPoint,
    finish_point: nodeIds.has(SYNTHETIC_END_ID) ? SYNTHETIC_END_ID : finishPoint,
    nodes,
    edges,
  };
}

function conditionLabel(condition: Record<string, unknown> | null): string {
  if (!condition) return "";
  const type = stringValue(condition.type);
  const config = objectValue(condition.config);

  if (type === "expression" || type === "expression_conditions") {
    const singleExpression =
      stringValue(config?.expression) ||
      stringValue(config?.expr) ||
      stringValue(config?.value);
    if (singleExpression) return singleExpression;

    const expressions = Array.isArray(config?.expressions) ? config.expressions : [];
    const rendered = expressions
      .map((item) => renderExpression(objectValue(item)))
      .filter(Boolean);
    if (rendered.length > 0) {
      const joiner = stringValue(config?.match).toLowerCase() === "any" ? " OR " : " AND ";
      return rendered.join(joiner);
    }
  }

  return type;
}

function renderExpression(expression: Record<string, unknown> | null): string {
  if (!expression) return "";
  const op = stringValue(expression.op);
  const value1 = stringValue(expression.value1);
  const value2 = stringValue(expression.value2);
  const operator = operatorLabel(op);

  if (value1 && value2 && operator) {
    return `${value1} ${operator} ${value2}`;
  }
  if (value1 && value2) {
    return `${value1} ${op || "?"} ${value2}`;
  }
  return "";
}

function operatorLabel(op: string): string {
  switch (op) {
    case "equals":
      return "==";
    case "not_equals":
      return "!=";
    case "gt":
      return ">";
    case "gte":
      return ">=";
    case "lt":
      return "<";
    case "lte":
      return "<=";
    case "contains":
      return "contains";
    case "in":
      return "in";
    default:
      return op;
  }
}
