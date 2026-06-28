import { Box, Braces, Database } from "lucide-react";
import type {
  ArtifactDetail,
  ArtifactRef,
  CheckpointDetail,
  CheckpointRecord,
  RegistryInfo,
} from "../../types";
import { ResourceDetail, ResourceList } from "./ResourceViews";
import { ResourceColumn } from "./shared";

export function ManageWorkspace({
  checkpoints,
  artifacts,
  selectedCheckpointId,
  selectedArtifactId,
  checkpointDetail,
  artifactDetail,
  resourceStatus,
  registry,
  selectedRunId,
  onSelectCheckpoint,
  onSelectArtifact,
}: {
  checkpoints: CheckpointRecord[];
  artifacts: ArtifactRef[];
  selectedCheckpointId: string;
  selectedArtifactId: string;
  checkpointDetail: CheckpointDetail | null;
  artifactDetail: ArtifactDetail | null;
  resourceStatus: string;
  registry: RegistryInfo | null;
  selectedRunId: string;
  onSelectCheckpoint: (checkpoint: CheckpointRecord) => void;
  onSelectArtifact: (artifact: ArtifactRef) => void;
}) {
  return (
    <div className="grid h-full min-h-0 grid-cols-[300px_300px_minmax(0,1fr)] bg-background">
      <ResourceColumn title="Checkpoints" icon={Database}>
        <ResourceList
          items={checkpoints.map((item) => ({
            id: item.checkpoint_id,
            meta: `${item.stage} / ${item.node_id}`,
            source: item,
          }))}
          selectedId={selectedCheckpointId}
          onSelect={(item) => onSelectCheckpoint(item.source)}
          empty={selectedRunId ? "No checkpoints" : "No run selected"}
        />
      </ResourceColumn>
      <ResourceColumn title="Artifacts" icon={Box}>
        <ResourceList
          items={artifacts.map((item) => ({
            id: item.id,
            meta: `${item.type || "artifact"} / ${item.mime_type || ""}`,
            source: item,
          }))}
          selectedId={selectedArtifactId}
          onSelect={(item) => onSelectArtifact(item.source)}
          empty={selectedRunId ? "No artifacts" : "No run selected"}
        />
      </ResourceColumn>
      <ResourceColumn title="Detail" icon={Braces}>
        <div className="grid gap-3">
          {resourceStatus ? <div className="text-xs text-muted-foreground">{resourceStatus}</div> : null}
          <ResourceDetail checkpoint={checkpointDetail} artifact={artifactDetail} />
          <div className="rounded-md border border-border bg-panel p-3">
            <div className="mb-2 text-sm font-medium">Registry Snapshot</div>
            <ResourceList
              items={(registry?.node_types ?? []).slice(0, 12).map((item) => ({
                id: item.type,
                meta: item.title || "node type",
                source: item,
              }))}
              empty="Registry unavailable"
            />
          </div>
        </div>
      </ResourceColumn>
    </div>
  );
}
