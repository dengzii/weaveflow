import { formatTimeMs, stringifyJSON } from "../../lib/utils";
import type { RuntimeEvent } from "../../types";
import { StatusText } from "./shared";

export function EventList({ events, wide = false }: { events: RuntimeEvent[]; wide?: boolean }) {
  if (events.length === 0) {
    return <div className="text-sm text-muted-foreground">No events</div>;
  }
  return (
    <div className="grid gap-2">
      {events.map((event, index) => (
        <div key={`${event.id}-${index}`} className="rounded-md border border-border bg-background p-2">
          <div className="flex min-w-0 items-center gap-2">
            <StatusText tone={event.type.includes("failed") ? "danger" : event.type.includes("finished") ? "ok" : "neutral"}>
              {event.type}
            </StatusText>
            <span className="truncate text-xs text-muted-foreground">{event.node_id || event.run_id}</span>
            <span className="ml-auto text-xs text-muted-foreground">{formatTimeMs(event.timestamp)}</span>
          </div>
          {wide && event.payload ? (
            <pre className="mt-2 max-h-48 overflow-auto rounded bg-muted p-2 text-xs">{stringifyJSON(event.payload)}</pre>
          ) : null}
        </div>
      ))}
    </div>
  );
}
