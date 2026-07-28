import type { NodeGroup, NodeTypeSchema } from "../types";

export interface GroupedNodeTypes {
  name: string;
  nodeTypes: NodeTypeSchema[];
}

export interface PartitionedNodeTypes {
  groups: GroupedNodeTypes[];
  ungroupedNodeTypes: NodeTypeSchema[];
}

export function partitionNodeTypes(nodeTypes: NodeTypeSchema[], nodeGroups: NodeGroup[]): PartitionedNodeTypes {
  const nodeTypesByType = new Map(nodeTypes.map((nodeType) => [nodeType.type, nodeType]));
  const assignedNodeTypes = new Set<string>();
  const groups: GroupedNodeTypes[] = [];

  for (const group of nodeGroups) {
    const members: NodeTypeSchema[] = [];
    for (const nodeType of group.node_types) {
      if (assignedNodeTypes.has(nodeType)) continue;
      const definition = nodeTypesByType.get(nodeType);
      if (!definition) continue;
      assignedNodeTypes.add(nodeType);
      members.push(definition);
    }
    if (members.length > 0) {
      groups.push({ name: group.name, nodeTypes: members });
    }
  }

  const ungroupedNodeTypes = nodeTypes.filter((nodeType) => !assignedNodeTypes.has(nodeType.type));
  return { groups, ungroupedNodeTypes };
}

export function groupNodeTypes(nodeTypes: NodeTypeSchema[], nodeGroups: NodeGroup[]): GroupedNodeTypes[] {
  const { groups, ungroupedNodeTypes } = partitionNodeTypes(nodeTypes, nodeGroups);
  if (ungroupedNodeTypes.length > 0) {
    return [...groups, { name: "Other", nodeTypes: ungroupedNodeTypes }];
  }
  return groups;
}
