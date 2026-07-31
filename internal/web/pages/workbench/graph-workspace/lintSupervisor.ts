import { isPlainRecord } from "../../../lib/utils";
import type { GraphDefinition } from "../../../types";
import type { GraphLintIssue } from "./lintTypes";

export function lintSupervisorTopology(definition: GraphDefinition): GraphLintIssue[] {
  const issues: GraphLintIssue[] = [];
  const workers = definition.nodes.filter((node) => node.type === "supervisor_worker");
  const workerByMemberID = new Map<string, typeof workers[number]>();
  for (const worker of workers) {
    const workerID = configString(worker.config, "worker_id");
    if (!workerID) continue;
    const key = workerID.toLowerCase();
    const existing = workerByMemberID.get(key);
    if (existing) {
      issues.push({
        id: `supervisor-worker-duplicate-${key}-${worker.id}`,
        severity: "error",
        message: `Supervisor Worker nodes "${existing.id}" and "${worker.id}" share worker_id "${workerID}".`,
        nodeID: worker.id,
        path: "worker_id",
      });
      continue;
    }
    workerByMemberID.set(key, worker);
  }

  for (const supervisor of definition.nodes.filter((node) => node.type === "supervisor")) {
    const rawMembers = supervisor.config?.members;
    const members = Array.isArray(rawMembers) ? rawMembers.filter(isPlainRecord) : [];
    if (members.length === 0) {
      issues.push({
        id: `supervisor-members-empty-${supervisor.id}`,
        severity: "error",
        message: `Supervisor "${supervisor.id}" needs at least one configured member.`,
        nodeID: supervisor.id,
        path: "members",
      });
    }
    const seen = new Set<string>();
    for (const member of members) {
      const memberID = recordString(member, "id");
      if (!memberID) continue;
      const memberKey = memberID.toLowerCase();
      if (seen.has(memberKey)) {
        issues.push({
          id: `supervisor-member-duplicate-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor "${supervisor.id}" has duplicate member ID "${memberID}".`,
          nodeID: supervisor.id,
          path: "members",
        });
        continue;
      }
      seen.add(memberKey);

      const worker = workerByMemberID.get(memberKey);
      if (!worker) {
        issues.push({
          id: `supervisor-worker-missing-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor member "${memberID}" has no matching Supervisor Worker node.`,
          nodeID: supervisor.id,
          path: "members",
        });
        continue;
      }

      const routeEdgeIndex = (definition.edges ?? []).findIndex((edge) =>
        edge.from === supervisor.id
        && edge.to === worker.id
        && edge.condition?.type === "supervisor_route_equals"
        && configString(edge.condition.config, "worker_id").toLowerCase() === memberKey
      );
      if (routeEdgeIndex < 0) {
        issues.push({
          id: `supervisor-route-missing-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor member "${memberID}" needs a supervisor_route_equals edge to worker node "${worker.id}".`,
          nodeID: supervisor.id,
          path: "members",
        });
      }

      const returnsToSupervisor = (definition.edges ?? []).some((edge) =>
        edge.from === worker.id && edge.to === supervisor.id && !edge.condition
      );
      if (!returnsToSupervisor) {
        issues.push({
          id: `supervisor-return-missing-${supervisor.id}-${memberKey}`,
          severity: "error",
          message: `Supervisor worker "${worker.id}" needs a direct edge back to "${supervisor.id}".`,
          nodeID: worker.id,
        });
      }
    }

    const hasSynthesisFallback = (definition.edges ?? []).some((edge) =>
      edge.from === supervisor.id
      && !edge.condition
      && definition.nodes.some((node) => node.id === edge.to && node.type === "supervisor_synthesis")
    );
    if (!hasSynthesisFallback) {
      issues.push({
        id: `supervisor-synthesis-missing-${supervisor.id}`,
        severity: "error",
        message: `Supervisor "${supervisor.id}" needs a direct fallback edge to a Supervisor Synthesis node.`,
        nodeID: supervisor.id,
      });
    }
  }
  return issues;
}

function configString(config: Record<string, unknown> | undefined, key: string): string {
  if (!config) return "";
  return typeof config[key] === "string" ? config[key].trim() : "";
}

function recordString(record: Record<string, unknown>, key: string): string {
  return typeof record[key] === "string" ? record[key].trim() : "";
}
