import { useCallback, useEffect, useId, useState } from "react";
import { CheckCircle2, Link2, Plus, Trash2 } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import type { ChatChannelDefinition, ChatChannelSetupAccount, Trigger, WebhookStateMapping } from "../../types";
import { ChatChannelSetupDialog } from "./ChatChannelSetupDialog";
import { CollapsibleInspectorBlock } from "./graph-workspace/shared";
import { JsonSchemaForm } from "./graph-workspace/schemaForm";
import {
  buildTriggerPayload,
  chatChannelDefaultConfig,
  editableChatChannelSchema,
  triggerTargetKey,
  triggerTypeName,
  type TriggerEditorStateBindings,
  type TriggerEditorValues,
  type TriggerInitialStateEntry,
  type TriggerTargetOption,
} from "./triggerEditor";

const emptyMapping = (): WebhookStateMapping => ({ parameter: "", state_path: "" });
const emptyInitialStateEntry = (): TriggerInitialStateEntry => ({ path: "", value: "" });
const requestStateBindingFields = [
  { key: "input", label: "Input", placeholder: "shared.request.input" },
  { key: "metadata", label: "Metadata", placeholder: "shared.request.metadata" },
  { key: "trigger_id", label: "Trigger ID", placeholder: "shared.trigger.id" },
  { key: "trigger_type", label: "Trigger Type", placeholder: "shared.trigger.type" },
  { key: "raw_body", label: "Raw Body", placeholder: "shared.request.raw_body" },
] as const;
const chatStateBindingFields = [
  { key: "input", label: "Input", placeholder: "shared.request.input" },
  { key: "conversation", label: "Conversation Root", placeholder: "scopes.agent.conversation" },
  { key: "raw_history", label: "Raw History", placeholder: "scopes.chat.raw_history" },
  { key: "trigger_id", label: "Trigger ID", placeholder: "scopes.chat.trigger_id" },
  { key: "channel", label: "Channel", placeholder: "scopes.chat.channel" },
  { key: "user_id", label: "User ID", placeholder: "scopes.chat.user_id" },
  { key: "conversation_id", label: "Conversation ID", placeholder: "scopes.chat.conversation_id" },
  { key: "message_id", label: "Message ID", placeholder: "scopes.chat.message_id" },
] as const;

export function TriggerEditorForm({
  trigger,
  values,
  persisted,
  chatSetupChannelID,
  chatSetupSessionID,
  targetOptions,
  targetLocked = false,
  statePathSuggestions = [],
  chatChannels = [],
  showIdentityFields = true,
  showTargetField = true,
  onChange,
}: {
  trigger: Trigger | null;
  values: TriggerEditorValues;
  persisted: boolean;
  chatSetupChannelID: string;
  chatSetupSessionID: string;
  targetOptions: TriggerTargetOption[];
  targetLocked?: boolean;
  statePathSuggestions?: string[];
  chatChannels?: ChatChannelDefinition[];
  showIdentityFields?: boolean;
  showTargetField?: boolean;
  onChange: (
    values: TriggerEditorValues,
    setup: { channelID: string; sessionID: string }
  ) => void;
}) {
  const statePathListID = useId();
  const [error, setError] = useState("");
  const [setupOpen, setSetupOpen] = useState(false);
  const [activeChatSetupSessionID, setActiveChatSetupSessionID] = useState(chatSetupSessionID);
  const [chatSetupAccount, setChatSetupAccount] = useState<ChatChannelSetupAccount | undefined>();
  const [identityOpen, setIdentityOpen] = useState(true);
  const [messageRoutingOpen, setMessageRoutingOpen] = useState(true);
  const [conversationStateOpen, setConversationStateOpen] = useState(false);
  const [stateBindingsOpen, setStateBindingsOpen] = useState(true);
  const [responseStreamingOpen, setResponseStreamingOpen] = useState(false);
  const [generalOpen, setGeneralOpen] = useState(false);
  const [sourceSettingsOpen, setSourceSettingsOpen] = useState(true);
  const [initialStateOpen, setInitialStateOpen] = useState(false);
  const selectedChatChannel = chatChannels.find((channel) => channel.id === values.chatChannel);
  const activeStateBindingFields = values.type === "chat" ? chatStateBindingFields : requestStateBindingFields;
  const hasStoredChannelCredential = Boolean(persisted && trigger?.chat?.channel === values.chatChannel);
  const channelSchema = editableChatChannelSchema(
    selectedChatChannel,
    hasStoredChannelCredential || Boolean(activeChatSetupSessionID)
  );

  useEffect(() => {
    setError("");
    setSetupOpen(false);
    setChatSetupAccount(undefined);
  }, [trigger?.id]);

  useEffect(() => {
    setActiveChatSetupSessionID(chatSetupSessionID);
  }, [chatSetupSessionID]);

  const closeSetup = useCallback(() => setSetupOpen(false), []);
  const confirmSetup = useCallback((sessionID: string, account?: ChatChannelSetupAccount) => {
    setActiveChatSetupSessionID(sessionID);
    setChatSetupAccount(account);
    setSetupOpen(false);
    onChange(values, { channelID: values.chatChannel, sessionID });
  }, [onChange, values]);

  function publish(
    nextValues: TriggerEditorValues,
    setup = { channelID: chatSetupChannelID, sessionID: activeChatSetupSessionID }
  ) {
    try {
      buildTriggerPayload(nextValues, persisted ? trigger : null, setup.sessionID);
      setError("");
    } catch (err) {
      setError(errorMessage(err));
    }
    onChange(nextValues, setup);
  }

  function change<Key extends keyof TriggerEditorValues>(key: Key, value: TriggerEditorValues[Key]) {
    const nextValues = { ...values, [key]: value };
    if (key === "chatChannel" && value !== values.chatChannel) {
      setActiveChatSetupSessionID("");
      setChatSetupAccount(undefined);
      publish(nextValues, { channelID: "", sessionID: "" });
      return;
    }
    publish(nextValues);
  }

  function updateMapping(index: number, field: keyof WebhookStateMapping, value: string) {
    change(
      "mappings",
      values.mappings.map((mapping, mappingIndex) =>
        mappingIndex === index ? { ...mapping, [field]: value } : mapping
      )
    );
  }

  function updateInitialStateEntry(index: number, field: keyof TriggerInitialStateEntry, value: string) {
    change(
      "initialStateEntries",
      values.initialStateEntries.map((entry, entryIndex) =>
        entryIndex === index ? { ...entry, [field]: value } : entry
      )
    );
  }

  function updateStateBinding(key: keyof TriggerEditorStateBindings, value: string) {
    change("stateBindings", { ...values.stateBindings, [key]: value });
  }

  return (
    <>
      <div className="min-w-0">
      {showIdentityFields ? (
        <CollapsibleInspectorBlock title="Trigger Properties" open={identityOpen} onOpenChange={setIdentityOpen}>
          <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Type</span>
            <Select
              value={values.type}
              onChange={(event) => {
                const type = event.target.value as TriggerEditorValues["type"];
                publish({ ...values, type, name: triggerTypeName(type) });
              }}
              disabled={Boolean(trigger)}
            >
              <option value="webhook">Webhook</option>
              <option value="schedule">Schedule</option>
              <option value="chat">Chat</option>
            </Select>
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">ID {trigger ? "" : "(optional)"}</span>
            <Input value={values.id} onChange={(event) => change("id", event.target.value)} placeholder="deploy-hook" disabled={Boolean(trigger)} />
          </label>
          </div>
        </CollapsibleInspectorBlock>
      ) : null}

      {values.type === "chat" ? (
        <>
          <CollapsibleInspectorBlock
            title="Chat Configuration"
            open={messageRoutingOpen}
            onOpenChange={setMessageRoutingOpen}
          >
            <div className="text-[11px] text-muted-foreground">
              Choose where messages arrive. The graph sends responses through Chat Reply nodes.
            </div>
            <label className="grid gap-1 text-sm">
              <span className="text-xs font-medium text-muted-foreground">Chat channel</span>
              <Select
                value={values.chatChannel}
                onChange={(event) => {
                  const channelID = event.target.value;
                  const definition = chatChannels.find((channel) => channel.id === channelID);
                  setSetupOpen(false);
                  setActiveChatSetupSessionID("");
                  setChatSetupAccount(undefined);
                  publish({
                    ...values,
                    chatChannel: channelID,
                    chatChannelConfig: chatChannelDefaultConfig(definition),
                  }, { channelID: "", sessionID: "" });
                }}
              >
                {!selectedChatChannel && values.chatChannel ? <option value={values.chatChannel}>{values.chatChannel} (unavailable)</option> : null}
                {chatChannels.map((channel) => <option key={channel.id} value={channel.id}>{channel.title}</option>)}
              </Select>
              {selectedChatChannel?.description ? <span className="text-[11px] text-muted-foreground">{selectedChatChannel.description}</span> : null}
            </label>
            <div className="grid gap-2 border-t border-border pt-3">
              <div className="text-xs font-medium">Channel configuration</div>
              {selectedChatChannel?.setup?.kind === "qr_code" ? (
                <div className="flex min-w-0 items-center gap-3 py-1">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5 text-xs font-medium">
                      {activeChatSetupSessionID || hasStoredChannelCredential ? (
                        <CheckCircle2 className="h-3.5 w-3.5 text-[var(--status-ok-text)]" />
                      ) : null}
                      <span>{activeChatSetupSessionID ? "Connected" : hasStoredChannelCredential ? "Credentials configured" : "Not connected"}</span>
                    </div>
                    {chatSetupAccount?.label ? (
                      <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{chatSetupAccount.label}</div>
                    ) : null}
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setActiveChatSetupSessionID("");
                      setChatSetupAccount(undefined);
                      publish(values, { channelID: "", sessionID: "" });
                      setSetupOpen(true);
                    }}
                  >
                    <Link2 className="h-4 w-4" />
                    {activeChatSetupSessionID || hasStoredChannelCredential ? "Reconnect" : "Connect"}
                  </Button>
                </div>
              ) : null}
              <JsonSchemaForm
                schema={channelSchema}
                unavailableReason={chatChannels.length === 0 ? "Chat channel registry is unavailable." : "Select an available chat channel."}
                value={values.chatChannelConfig}
                writeOnlyValuesConfigured={hasStoredChannelCredential || Boolean(activeChatSetupSessionID)}
                onChange={(value) => change("chatChannelConfig", value)}
              />
              {trigger && trigger.chat?.channel === values.chatChannel ? (
                <div className="text-[11px] text-muted-foreground">Leave sensitive fields blank to keep their configured values.</div>
              ) : null}
            </div>
          </CollapsibleInspectorBlock>

          <CollapsibleInspectorBlock title="Conversation State" open={conversationStateOpen} onOpenChange={setConversationStateOpen}>
            <label className="grid gap-1 text-sm">
              <span className="text-xs font-medium text-muted-foreground">History rounds</span>
              <Input
                type="number"
                min="0"
                max="500"
                step="1"
                value={values.chatHistoryLimit}
                onChange={(event) => change("chatHistoryLimit", event.target.value)}
                placeholder="Not loaded"
              />
            </label>
          </CollapsibleInspectorBlock>

          <CollapsibleInspectorBlock title="Response Streaming" open={responseStreamingOpen} onOpenChange={setResponseStreamingOpen}>
            <div className="text-[11px] text-muted-foreground">Control whether model updates are forwarded before the final reply.</div>
            <label className="flex items-center gap-3 rounded-md border border-border bg-muted/30 px-3 py-2">
              <input
                className="h-4 w-4"
                type="checkbox"
                checked={values.streamUpdates}
                onChange={(event) => change("streamUpdates", event.target.checked)}
              />
              <span className="min-w-0">
                <span className="block text-sm font-medium">Forward LLM content updates</span>
                <span className="block text-[11px] text-muted-foreground">Send partial responses while the graph is running.</span>
              </span>
            </label>
            {values.streamUpdates ? (
              <label className="grid gap-1 text-sm">
                <span className="text-xs font-medium text-muted-foreground">Streaming node IDs</span>
                <Input
                  value={values.streamNodeIDs}
                  onChange={(event) => change("streamNodeIDs", event.target.value)}
                  placeholder="Empty streams all LLM nodes"
                />
                <span className="text-[11px] text-muted-foreground">Optional. Separate node IDs with commas or spaces.</span>
              </label>
            ) : null}
          </CollapsibleInspectorBlock>
        </>
      ) : null}

      <CollapsibleInspectorBlock title="General" open={generalOpen} onOpenChange={setGeneralOpen}>
        <div className="text-[11px] text-muted-foreground">Target graph and execution policy.</div>
        {showTargetField ? (
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Graph</span>
            <Select
              value={triggerTargetKey(values.target)}
              onChange={(event) => {
                const selected = targetOptions.find((option) => option.key === event.target.value);
                if (selected) change("target", selected.target);
              }}
              disabled={targetLocked || targetOptions.length === 0}
            >
              {targetOptions.length === 0 ? <option value="">No graph available</option> : null}
              {targetOptions.map((option) => <option key={option.key} value={option.key}>{option.label}</option>)}
            </Select>
          </label>
        ) : null}
        <div className="grid grid-cols-2 gap-2">
          <div className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Status</span>
            <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
              <input type="checkbox" checked={values.enabled} onChange={(event) => change("enabled", event.target.checked)} />
              Enabled
            </label>
          </div>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Concurrency</span>
            <Select value={values.concurrency} onChange={(event) => change("concurrency", event.target.value as TriggerEditorValues["concurrency"])}>
              <option value="parallel">Parallel</option>
              <option value="skip">Skip while running</option>
            </Select>
          </label>
        </div>
        <div className="grid grid-cols-[minmax(0,120px)_minmax(0,1fr)] gap-2">
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Credential source</span>
            <Select
              value={values.credentialSource}
              onChange={(event) => change("credentialSource", event.target.value as TriggerEditorValues["credentialSource"])}
            >
              <option value="env">Environment</option>
              <option value="file">File</option>
            </Select>
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Credential reference</span>
            <Input
              value={values.credentialRef}
              onChange={(event) => change("credentialRef", event.target.value)}
              placeholder={values.credentialSource === "env" ? "TRIGGER_TOKEN" : "trigger.token"}
            />
          </label>
        </div>
      </CollapsibleInspectorBlock>

      {values.type === "webhook" ? (
        <CollapsibleInspectorBlock
          title="Webhook Request"
          open={sourceSettingsOpen}
          onOpenChange={setSourceSettingsOpen}
          action={
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                change("mappings", [...values.mappings, emptyMapping()]);
                setSourceSettingsOpen(true);
              }}
            >
              <Plus className="h-4 w-4" /> Add mapping
            </Button>
          }
        >
          <div className="text-[11px] text-muted-foreground">Request-to-state bindings.</div>
          <div className="grid gap-2 border-t border-border pt-3">
            <div className="text-xs font-medium">State mappings</div>
            {values.mappings.length === 0 ? <div className="rounded border border-dashed border-border p-3 text-xs text-muted-foreground">No additional state mappings.</div> : null}
            {values.mappings.map((mapping, index) => (
              <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] gap-2">
                <Input value={mapping.parameter} onChange={(event) => updateMapping(index, "parameter", event.target.value)} placeholder="user.id" aria-label={`Webhook parameter ${index + 1}`} />
                <Input
                  list={statePathSuggestions.length > 0 ? statePathListID : undefined}
                  value={mapping.state_path}
                  onChange={(event) => updateMapping(index, "state_path", event.target.value)}
                  placeholder={statePathSuggestions[0] ?? "shared.user.id"}
                  aria-label={`State path ${index + 1}`}
                />
                <Button variant="ghost" size="icon" onClick={() => change("mappings", values.mappings.filter((_, mappingIndex) => mappingIndex !== index))} title="Remove mapping" aria-label={`Remove mapping ${index + 1}`}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
          </div>
        </CollapsibleInspectorBlock>
      ) : values.type === "schedule" ? (
        <CollapsibleInspectorBlock title="Schedule" open={sourceSettingsOpen} onOpenChange={setSourceSettingsOpen}>
          <div className="text-[11px] text-muted-foreground">When this trigger should start the graph.</div>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Cron</span>
            <Input value={values.cron} onChange={(event) => change("cron", event.target.value)} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Timezone</span>
            <Input value={values.timezone} onChange={(event) => change("timezone", event.target.value)} />
          </label>
        </CollapsibleInspectorBlock>
      ) : null}

      <CollapsibleInspectorBlock title="State Bindings" open={stateBindingsOpen} onOpenChange={setStateBindingsOpen}>
        {activeStateBindingFields.map((field) => (
          <label key={field.key} className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">{field.label} state path</span>
            <Input
              list={statePathSuggestions.length > 0 ? statePathListID : undefined}
              value={values.stateBindings[field.key] ?? ""}
              onChange={(event) => updateStateBinding(field.key, event.target.value)}
              placeholder={field.placeholder}
            />
          </label>
        ))}
      </CollapsibleInspectorBlock>

      <CollapsibleInspectorBlock
        title="Initial State"
        open={initialStateOpen}
        onOpenChange={setInitialStateOpen}
        action={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              change("initialStateEntries", [...values.initialStateEntries, emptyInitialStateEntry()]);
              setInitialStateOpen(true);
            }}
          >
            <Plus className="h-4 w-4" /> Add
          </Button>
        }
      >
        <div className="text-[11px] text-muted-foreground">Optional values written before the graph starts.</div>
        {values.initialStateEntries.length === 0 ? (
          <div className="rounded border border-dashed border-border p-3 text-xs text-muted-foreground">No initial state values.</div>
        ) : null}
        {values.initialStateEntries.map((entry, index) => (
          <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] gap-2">
            <Input
              list={statePathSuggestions.length > 0 ? statePathListID : undefined}
              value={entry.path}
              onChange={(event) => updateInitialStateEntry(index, "path", event.target.value)}
              placeholder={statePathSuggestions[0] ?? "shared.path"}
              aria-label={`Initial state path ${index + 1}`}
            />
            <Input
              value={entry.value}
              onChange={(event) => updateInitialStateEntry(index, "value", event.target.value)}
              placeholder="value"
              aria-label={`Initial state value ${index + 1}`}
            />
            <Button
              variant="ghost"
              size="icon"
              onClick={() => change("initialStateEntries", values.initialStateEntries.filter((_, entryIndex) => entryIndex !== index))}
              title="Remove initial state value"
              aria-label={`Remove initial state value ${index + 1}`}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        ))}
        <div className="text-[11px] text-muted-foreground">
          Paths support shared and scopes. Boolean, number, and null values are typed; other values are stored as text.
        </div>
      </CollapsibleInspectorBlock>
      {statePathSuggestions.length > 0 ? (
        <datalist id={statePathListID}>
          {statePathSuggestions.map((path) => <option key={path} value={path} />)}
        </datalist>
      ) : null}
      {error ? (
        <div className="p-3">
          <div className="rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">{error}</div>
        </div>
      ) : null}
      </div>
      {setupOpen && selectedChatChannel?.setup?.kind === "qr_code" ? (
        <ChatChannelSetupDialog
          channel={selectedChatChannel}
          triggerID={persisted && trigger?.chat?.channel === selectedChatChannel.id ? trigger.id : undefined}
          onClose={closeSetup}
          onConfirmed={confirmSetup}
        />
      ) : null}
    </>
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
