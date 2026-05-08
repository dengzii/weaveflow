import { prettyJSON } from "../../replay/utils";

export function ReplayPayloadPanel({ payload }: { payload: unknown }) {
  return (
    <div className="pointer-events-auto mt-3 mr-3 hidden w-[340px] rounded-xl border border-border bg-background/92 p-3 text-foreground shadow-2xl backdrop-blur-xl 2xl:block">
      <div>
        <div className="text-sm font-semibold text-foreground">Event Payload</div>
        <div className="text-xs text-muted-foreground">Raw payload for the current event.</div>
      </div>
      <pre className="mt-3 max-h-[360px] overflow-auto rounded-lg border border-border bg-card p-3 font-mono text-[11px] leading-5 text-foreground">
        {prettyJSON(payload)}
      </pre>
    </div>
  );
}
