import { Input } from "../../components/ui/input";
import type { RuntimeEvent } from "../../types";
import { EventList } from "./EventList";

export function ObserveWorkspace({
  selectedRunId,
  streamTypes,
  events,
  onStreamTypes,
}: {
  selectedRunId: string;
  streamTypes: string;
  events: RuntimeEvent[];
  onStreamTypes: (value: string) => void;
}) {
  return (
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr] bg-background">
      <div className="flex items-center gap-3 border-b border-border p-3">
        <span className="max-w-xs truncate font-mono text-xs text-muted-foreground">{selectedRunId || "all runs"}</span>
        <Input
          value={streamTypes}
          onChange={(event) => onStreamTypes(event.target.value)}
          placeholder="nodes.started,nodes.finished,llm.content_chunk"
          className="max-w-lg"
        />
      </div>
      <div className="min-h-0 overflow-auto p-4">
        <EventList events={events} wide />
      </div>
    </div>
  );
}
