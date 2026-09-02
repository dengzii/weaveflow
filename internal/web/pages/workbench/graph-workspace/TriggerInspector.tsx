import { Clock3, MessageCircle, Webhook, Zap } from "lucide-react";
import type { ChatChannelDefinition, Trigger } from "../../../types";
import { TriggerEditorForm } from "../TriggerEditorForm";
import { triggerTypeName, webhookCurlCommand, webhookTriggerURL, type TriggerEditorValues } from "../triggerEditor";
import type { TriggerDraftSetup } from "./useGraphTriggers";
import { PanelHeader } from "./shared";

export function TriggerInspector({
  graphID,
  trigger,
  values,
  persisted,
  chatSetupChannelID,
  chatSetupSessionID,
  statePathSuggestions,
  chatChannels,
  onChange,
}: {
  graphID: string;
  trigger: Trigger | null;
  values: TriggerEditorValues;
  persisted: boolean;
  chatSetupChannelID: string;
  chatSetupSessionID: string;
  statePathSuggestions: string[];
  chatChannels: ChatChannelDefinition[];
  onChange: (values: TriggerEditorValues, setup: TriggerDraftSetup) => void;
}) {
  const Icon = trigger?.type === "webhook" ? Webhook : trigger?.type === "schedule" ? Clock3 : trigger?.type === "chat" ? MessageCircle : Zap;
  const title = trigger ? (trigger.name?.trim() || triggerTypeName(trigger.type)) : "Trigger";
  const target = { graph_id: graphID };
  const webhookURL = trigger ? webhookTriggerURL(graphID, trigger.id) : "";

  return (
    <section className="h-full min-h-0 min-w-0 overflow-x-hidden overflow-y-auto border-l border-border bg-panel pb-[45vh] [overflow-wrap:anywhere]">
      <PanelHeader icon={Icon} title={title} />
      <div className="grid gap-3 border-b border-border p-3">
        {trigger ? (
          <div className="flex flex-wrap gap-2">
            <InspectorLabel name="Type" value={trigger.type === "webhook" ? "Webhook" : trigger.type === "schedule" ? "Schedule" : "Chat"} />
            {trigger.type === "chat" ? <InspectorLabel name="Channel" value={trigger.chat?.channel || "http"} /> : null}
            <InspectorLabel name="ID" value={trigger.id} mono />
          </div>
        ) : null}
        <div className="rounded border border-border bg-muted/40 p-2 text-xs text-muted-foreground">
          Triggers start this graph from webhooks, schedules, or chat messages.
        </div>
      </div>
      <TriggerEditorForm
        trigger={trigger}
        values={values}
        persisted={persisted}
        chatSetupChannelID={chatSetupChannelID}
        chatSetupSessionID={chatSetupSessionID}
        targetOptions={[{ key: graphID, label: graphID || "Current graph", target }]}
        targetLocked
        statePathSuggestions={statePathSuggestions}
        chatChannels={chatChannels}
        showIdentityFields={false}
        showTargetField={false}
        onChange={onChange}
      />
      {trigger?.type === "webhook" ? (
        <div className="grid gap-2 border-t border-border p-3 text-xs text-muted-foreground">
          <div className="font-medium">Test command</div>
          <code className="block break-all rounded border border-border bg-muted/40 p-2 font-mono text-[11px] text-foreground">
            {webhookCurlCommand(webhookURL, Boolean(trigger.credential))}
          </code>
        </div>
      ) : null}
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
