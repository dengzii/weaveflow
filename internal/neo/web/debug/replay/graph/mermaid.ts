import type { GraphProjection, SourceGraph } from "./types";

export function buildMermaidDiagram(
  sourceGraph: SourceGraph,
  projection?: GraphProjection
): string {
  const lines: string[] = ["flowchart LR"];

  lines.push(
    "  classDef wfCurrent fill:#2b1e08,stroke:#f59e0b,stroke-width:1.5px,color:#fde68a"
  );
  lines.push(
    "  classDef wfDone fill:#112318,stroke:#4ade80,stroke-width:1.5px,color:#86efac"
  );
  lines.push(
    "  classDef wfVisited fill:#162034,stroke:#60a5fa,stroke-width:1px,color:#93c5fd"
  );
  lines.push(
    "  classDef wfFailed fill:#2a1218,stroke:#f87171,stroke-width:1.5px,color:#fca5a5"
  );
  lines.push(
    "  classDef wfStart fill:#071e24,stroke:#22d3ee,stroke-width:1.5px,color:#67e8f9"
  );
  lines.push(
    "  classDef wfEnd fill:#130c24,stroke:#a78bfa,stroke-width:1.5px,color:#c4b5fd"
  );

  const idMap = new Map<string, string>();
  sourceGraph.nodes.forEach((node, i) => idMap.set(node.id, `N${i}`));

  for (const node of sourceGraph.nodes) {
    const mid = idMap.get(node.id)!;
    const lbl = `"${mermaidEscape(node.name)}"`;
    if (node.type === "start" || node.type === "end") {
      lines.push(`  ${mid}([${lbl}])`);
    } else {
      lines.push(`  ${mid}[${lbl}]`);
    }
  }

  for (const edge of sourceGraph.edges) {
    const from = idMap.get(edge.from);
    const to = idMap.get(edge.to);
    if (!from || !to) continue;
    if (edge.label) {
      lines.push(`  ${from} -->|"${mermaidEscape(edge.label)}"| ${to}`);
    } else {
      lines.push(`  ${from} --> ${to}`);
    }
  }

  if (projection) {
    for (const node of sourceGraph.nodes) {
      const mid = idMap.get(node.id)!;
      let cls = "";
      if (projection.currentNodeId === node.id) cls = "wfCurrent";
      else if (projection.failedNodeIds.has(node.id)) cls = "wfFailed";
      else if (projection.completedNodeIds.has(node.id)) cls = "wfDone";
      else if (projection.visitedNodeIds.has(node.id)) cls = "wfVisited";
      else if (node.type === "start") cls = "wfStart";
      else if (node.type === "end") cls = "wfEnd";
      if (cls) lines.push(`  class ${mid} ${cls}`);
    }
  }

  return lines.join("\n");
}

function mermaidEscape(text: string): string {
  return text
    .replace(/"/g, "#quot;")
    .replace(/</g, "#lt;")
    .replace(/>/g, "#gt;");
}
