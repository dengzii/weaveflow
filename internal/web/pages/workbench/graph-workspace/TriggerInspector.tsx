import { Clock3, MessageCircle, Webhook, Zap } from "lucide-react";
import type { ChatChannelDefinition, Trigger } from "../../../types";
import { TriggerEditorForm } from "../TriggerEditorForm";
import { webhookTriggerURLs } from "../triggerEditor";
import { PanelHeader } from "./shared";

export function TriggerInspector({
  graphID,
  trigger,
  statePathSuggestions,
  chatChannels,
  onSaved,
  onDeleted,
}: {
  graphID: string;
  trigger: Trigger | null;
  statePathSuggestions: string[];
  chatChannels: ChatChannelDefinition[];
  onSaved: (trigger: Trigger) => void | Promise<void>;
  onDeleted: (trigger: Trigger) => void | Promise<void>;
}) {
  const Icon = trigger?.type === "webhook" ? Webhook : trigger?.type === "schedule" ? Clock3 : trigger?.type === "chat" ? MessageCircle : Zap;
  const title = trigger?.name || "Trigger";
  const target = { graph_id: graphID };
  const webhookURLs = trigger ? webhookTriggerURLs(trigger.id) : null;

  return (
    <section className="min-h-0 min-w-0 overflow-x-hidden overflow-y-auto border-l border-border bg-panel [overflow-wrap:anywhere]">
      <PanelHeader icon={Icon} title={title} />
      <div className="grid gap-3 p-3">
        {trigger ? (
          <div className="flex flex-wrap gap-2">
            <InspectorLabel name="Type" value={trigger.type === "webhook" ? "Webhook" : trigger.type === "schedule" ? "Schedule" : "Chat"} />
            {trigger.type === "chat" ? <InspectorLabel name="Channel" value={trigger.chat?.channel || "http"} /> : null}
            <InspectorLabel name="ID" value={trigger.id} mono />
          </div>
        ) : null}
        <div className="rounded border border-border bg-muted/40 p-2 text-xs text-muted-foreground">
          Trigger configuration remains a server resource. Only this card's position is stored in Graph metadata.
        </div>
        <TriggerEditorForm
          trigger={trigger}
          fallbackTarget={target}
          targetOptions={[{ key: graphID, label: graphID || "Current graph", target }]}
          targetLocked
          statePathSuggestions={statePathSuggestions}
          chatChannels={chatChannels}
          showIdentityFields={false}
          showTargetField={false}
          allowDelete
          onSaved={onSaved}
          onDeleted={onDeleted}
        />
        {trigger?.type === "webhook" ? (
          <div className="grid gap-1 border-t border-border pt-3 text-xs text-muted-foreground">
            <code className="break-all">POST {webhookURLs?.post}</code>
            <code className="break-all">GET {webhookURLs?.get}</code>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function InspectorLabel({ name, value, mono = false }: { name: string; value: string; mono?: boolean }) {
  return (
    <div className="inline-flex min-w-0 items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1 text-xs">
      <span className="shrink-0 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{name}</span>
      <span className={`truncate font-medium ${mono ? "font-mono" : ""}`} title={value}>{value}</span>
    </div>
  );
}
