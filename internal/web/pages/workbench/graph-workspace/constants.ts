import { END_NODE_REF, START_NODE_REF } from "../../../lib/graphEditor";
import type { NodeTypeSchema } from "../../../types";

export const defaultVirtualNodeIds = [START_NODE_REF, END_NODE_REF];

export const virtualNodeTypes: NodeTypeSchema[] = [
  { type: "start", title: "Start", description: "Graph entry" },
  { type: "end", title: "End", description: "Graph finish" },
];

export const fallbackNodeTypes: NodeTypeSchema[] = [{ type: "node", title: "Node" }];
