import { Trash2 } from "lucide-react";
import type { VirtualGraphLoop } from "../../../components/GraphCanvas";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { analyzeVirtualGraphLoop } from "../../../lib/loopPresentation";
import type { GraphDefinition } from "../../../types";
import { uniqueStrings } from "./graphWorkspaceModel";
import { Field, InspectorBlock } from "./shared";

interface LoopInspectorProps {
  definition: GraphDefinition | null;
  selectedVirtualLoop: VirtualGraphLoop;
  onChangeVirtualLoop: (update: (loop: VirtualGraphLoop) => VirtualGraphLoop) => void;
  onDeleteLoop: (loopID: string) => void;
}

export function LoopInspector({
  definition,
  selectedVirtualLoop,
  onChangeVirtualLoop,
  onDeleteLoop,
}: LoopInspectorProps) {
  const analysis = analyzeVirtualGraphLoop(definition, selectedVirtualLoop);
  const selectedIDs = new Set(selectedVirtualLoop.nodeIds);
  const automatic = Boolean(selectedVirtualLoop.automatic);

  return (
    <>
      <InspectorBlock title="Loop Properties">
        {automatic ? (
          <div className="rounded-md border border-border bg-muted px-2.5 py-2 text-xs text-muted-foreground">
            Automatically detected from graph edges. This group exists only in the Web UI.
          </div>
        ) : (
          <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
            <Field label="Name">
              <Input
                value={selectedVirtualLoop.name ?? ""}
                placeholder="Loop"
                onChange={(event) => onChangeVirtualLoop((loop) => ({ ...loop, name: event.target.value }))}
              />
            </Field>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => onDeleteLoop(selectedVirtualLoop.id)}
              title="Delete loop"
              aria-label="Delete loop"
              className="mt-5"
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        )}
        <div className="grid gap-1 text-xs">
          <LoopMetadata label="ID" value={selectedVirtualLoop.id} />
          <LoopMetadata label="Loop start" value={analysis.loopStartId || "-"} />
          <LoopMetadata label="Loop end" value={analysis.loopEndIds.join(", ") || "-"} />
          <LoopMetadata label="Next" value={analysis.nextNodeIds.join(", ") || "-"} />
          <LoopMetadata label="Condition" value={analysis.conditionLabels.join(", ") || "unconditional"} />
        </div>
      </InspectorBlock>

      <InspectorBlock title="Loop Nodes">
        <div className="grid max-h-80 gap-1 overflow-x-hidden overflow-y-auto">
          {(definition?.nodes ?? []).map((node) => (
            <label
              key={node.id}
              className="flex min-h-8 min-w-0 items-start gap-2 rounded-md px-2 py-1 text-sm hover:bg-accent"
            >
              <input
                type="checkbox"
                checked={selectedIDs.has(node.id)}
                disabled={automatic}
                onChange={(event) =>
                  onChangeVirtualLoop((loop) => ({
                    ...loop,
                    nodeIds: event.target.checked
                      ? uniqueStrings([...loop.nodeIds, node.id])
                      : loop.nodeIds.filter((nodeID) => nodeID !== node.id),
                  }))
                }
                className="h-4 w-4"
              />
              <span className="min-w-0 flex-1 break-words">{node.name || node.id}</span>
              <span className="max-w-24 break-all font-mono text-[11px] text-muted-foreground">{node.id}</span>
            </label>
          ))}
        </div>
      </InspectorBlock>
    </>
  );
}

function LoopMetadata({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[84px_minmax(0,1fr)] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all font-mono">{value}</span>
    </div>
  );
}
