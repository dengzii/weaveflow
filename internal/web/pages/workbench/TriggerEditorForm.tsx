import { useCallback, useEffect, useId, useRef, useState } from "react";
import { CheckCircle2, Link2, Plus, Trash2 } from "lucide-react";
import { cancelChatChannelSetup, createTrigger, deleteTrigger, updateTrigger } from "../../api";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import type { ChatChannelDefinition, ChatChannelSetupAccount, Trigger, TriggerTarget, WebhookStateMapping } from "../../types";
import { ChatChannelSetupDialog } from "./ChatChannelSetupDialog";
import { JsonSchemaForm } from "./graph-workspace/schemaForm";
import {
  buildTriggerPayload,
  chatChannelDefaultConfig,
  editableChatChannelSchema,
  triggerEditorValues,
  triggerTargetKey,
  type TriggerEditorValues,
  type TriggerInitialStateEntry,
  type TriggerTargetOption,
} from "./triggerEditor";

const emptyMapping = (): WebhookStateMapping => ({ parameter: "", state_path: "" });
const emptyInitialStateEntry = (): TriggerInitialStateEntry => ({ path: "", value: "" });

export function TriggerEditorForm({
  trigger,
  fallbackTarget,
  targetOptions,
  targetLocked = false,
  statePathSuggestions = [],
  chatChannels = [],
  showIdentityFields = true,
  showTargetField = true,
  allowDelete = false,
  onSaved,
  onDeleted,
}: {
  trigger: Trigger | null;
  fallbackTarget: TriggerTarget;
  targetOptions: TriggerTargetOption[];
  targetLocked?: boolean;
  statePathSuggestions?: string[];
  chatChannels?: ChatChannelDefinition[];
  showIdentityFields?: boolean;
  showTargetField?: boolean;
  allowDelete?: boolean;
  onSaved: (trigger: Trigger) => void | Promise<void>;
  onDeleted?: (trigger: Trigger) => void | Promise<void>;
}) {
  const statePathListID = useId();
  const [values, setValues] = useState<TriggerEditorValues>(() => triggerEditorValues(trigger, fallbackTarget));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [setupOpen, setSetupOpen] = useState(false);
  const [chatSetupSessionID, setChatSetupSessionID] = useState("");
  const [chatSetupAccount, setChatSetupAccount] = useState<ChatChannelSetupAccount | undefined>();
  const chatSetupSessionRef = useRef<{ channelID: string; sessionID: string } | null>(null);
  const selectedChatChannel = chatChannels.find((channel) => channel.id === values.chatChannel);
  const hasStoredChannelCredential = Boolean(trigger && trigger.chat?.channel === values.chatChannel);
  const channelSchema = editableChatChannelSchema(
    selectedChatChannel,
    hasStoredChannelCredential || Boolean(chatSetupSessionID)
  );

  const discardChatSetupSession = useCallback(() => {
    const setup = chatSetupSessionRef.current;
    chatSetupSessionRef.current = null;
    if (setup) {
      void cancelChatChannelSetup(setup.channelID, setup.sessionID).catch(() => undefined);
    }
  }, []);

  useEffect(() => {
    discardChatSetupSession();
    setValues(triggerEditorValues(trigger, fallbackTarget));
    setError("");
    setSetupOpen(false);
    setChatSetupSessionID("");
    setChatSetupAccount(undefined);
  }, [discardChatSetupSession, fallbackTarget.graph_id, trigger?.id, trigger?.updated_at]);

  useEffect(() => () => discardChatSetupSession(), [discardChatSetupSession]);

  const closeSetup = useCallback(() => setSetupOpen(false), []);
  const confirmSetup = useCallback((sessionID: string, account?: ChatChannelSetupAccount) => {
    chatSetupSessionRef.current = { channelID: values.chatChannel, sessionID };
    setChatSetupSessionID(sessionID);
    setChatSetupAccount(account);
  }, [values.chatChannel]);

  function change<Key extends keyof TriggerEditorValues>(key: Key, value: TriggerEditorValues[Key]) {
    setValues((current) => ({ ...current, [key]: value }));
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

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const payload = buildTriggerPayload(values, trigger, chatSetupSessionID);
      const saved = trigger
        ? await updateTrigger(trigger.id, payload)
        : await createTrigger(payload);
      chatSetupSessionRef.current = null;
      setChatSetupSessionID("");
      setChatSetupAccount(undefined);
      setSetupOpen(false);
      await onSaved(saved);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!trigger || !window.confirm(`Delete trigger ${trigger.id}?`)) return;
    setBusy(true);
    setError("");
    try {
      await deleteTrigger(trigger.id);
      discardChatSetupSession();
      await onDeleted?.(trigger);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="grid gap-4">
      {showIdentityFields ? (
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Type</span>
            <Select value={values.type} onChange={(event) => change("type", event.target.value as TriggerEditorValues["type"])} disabled={Boolean(trigger)}>
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
      ) : null}

      {values.type === "chat" ? (
        <>
          <section className="grid gap-3 rounded-md border border-primary/30 bg-muted/20 p-3">
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <div className="text-xs font-semibold">Message routing</div>
                <div className="mt-0.5 text-[11px] text-muted-foreground">
                  Choose where messages arrive and which graph state is returned as the reply.
                </div>
              </div>
              <span className="rounded bg-primary px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary-foreground">
                Primary
              </span>
            </div>
            <label className="grid gap-1 text-sm">
              <span className="text-xs font-medium text-muted-foreground">Chat channel</span>
              <Select
                value={values.chatChannel}
                onChange={(event) => {
                  const channelID = event.target.value;
                  const definition = chatChannels.find((channel) => channel.id === channelID);
                  discardChatSetupSession();
                  setSetupOpen(false);
                  setChatSetupSessionID("");
                  setChatSetupAccount(undefined);
                  setValues((current) => ({
                    ...current,
                    chatChannel: channelID,
                    chatChannelConfig: chatChannelDefaultConfig(definition),
                  }));
                }}
              >
                {!selectedChatChannel && values.chatChannel ? <option value={values.chatChannel}>{values.chatChannel} (unavailable)</option> : null}
                {chatChannels.map((channel) => <option key={channel.id} value={channel.id}>{channel.title}</option>)}
              </Select>
              {selectedChatChannel?.description ? <span className="text-[11px] text-muted-foreground">{selectedChatChannel.description}</span> : null}
            </label>
            <label className="grid gap-1 text-sm">
              <span className="text-xs font-medium text-muted-foreground">Reply state path</span>
              <Input
                list={statePathSuggestions.length > 0 ? statePathListID : undefined}
                value={values.replyPath}
                onChange={(event) => change("replyPath", event.target.value)}
                placeholder="shared.final.answer"
              />
              <span className="text-[11px] text-muted-foreground">The final value at this path is sent back to the chat channel.</span>
            </label>
            <div className="grid gap-2 border-t border-border pt-3">
              <div className="text-xs font-medium">Channel configuration</div>
              {selectedChatChannel?.setup?.kind === "qr_code" ? (
                <div className="flex min-w-0 items-center gap-3 py-1">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5 text-xs font-medium">
                      {chatSetupSessionID || hasStoredChannelCredential ? (
                        <CheckCircle2 className="h-3.5 w-3.5 text-[var(--status-ok-text)]" />
                      ) : null}
                      <span>{chatSetupSessionID ? "Connected" : hasStoredChannelCredential ? "Credentials configured" : "Not connected"}</span>
                    </div>
                    {chatSetupAccount?.label ? (
                      <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{chatSetupAccount.label}</div>
                    ) : null}
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      discardChatSetupSession();
                      setChatSetupSessionID("");
                      setChatSetupAccount(undefined);
                      setSetupOpen(true);
                    }}
                  >
                    <Link2 className="h-4 w-4" />
                    {chatSetupSessionID || hasStoredChannelCredential ? "Reconnect" : "Connect"}
                  </Button>
                </div>
              ) : null}
              <JsonSchemaForm
                schema={channelSchema}
                unavailableReason={chatChannels.length === 0 ? "Chat channel registry is unavailable." : "Select an available chat channel."}
                value={values.chatChannelConfig}
                onChange={(value) => change("chatChannelConfig", value)}
              />
              {trigger && trigger.chat?.channel === values.chatChannel ? (
                <div className="text-[11px] text-muted-foreground">Leave sensitive fields blank to keep their configured values.</div>
              ) : null}
            </div>
          </section>

          <section className="grid gap-3 rounded-md border border-border p-3">
            <div>
              <div className="text-xs font-semibold">Response streaming</div>
              <div className="mt-0.5 text-[11px] text-muted-foreground">Control whether model updates are forwarded before the final reply.</div>
            </div>
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
          </section>
        </>
      ) : null}

      <section className="grid gap-3 rounded-md border border-border p-3">
        <div>
          <div className="text-xs font-semibold">General</div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">Name, target graph, and execution policy.</div>
        </div>
        <label className="grid gap-1 text-sm">
          <span className="text-xs font-medium text-muted-foreground">Name</span>
          <Input value={values.name} onChange={(event) => change("name", event.target.value)} placeholder="Deploy webhook" />
        </label>
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
      </section>

      {values.type === "webhook" ? (
        <section className="grid gap-3 rounded-md border border-border p-3">
          <div>
            <div className="text-xs font-semibold">Webhook request</div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">Authentication and request-to-state bindings.</div>
          </div>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">API key</span>
            <Input type="password" value={values.apiKey} onChange={(event) => change("apiKey", event.target.value)} placeholder={trigger ? "Unchanged" : "Optional"} />
          </label>
          <div className="grid gap-2 border-t border-border pt-3">
            <div className="flex items-center gap-2">
              <div className="min-w-0 flex-1 text-xs font-medium">State mappings</div>
              <Button variant="outline" size="sm" onClick={() => change("mappings", [...values.mappings, emptyMapping()])}>
                <Plus className="h-4 w-4" /> Add
              </Button>
            </div>
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
        </section>
      ) : values.type === "schedule" ? (
        <section className="grid gap-3 rounded-md border border-border p-3">
          <div>
            <div className="text-xs font-semibold">Schedule</div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">When this trigger should start the graph.</div>
          </div>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Cron</span>
            <Input value={values.cron} onChange={(event) => change("cron", event.target.value)} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Timezone</span>
            <Input value={values.timezone} onChange={(event) => change("timezone", event.target.value)} />
          </label>
        </section>
      ) : null}

      <section className="grid gap-2 rounded-md border border-border p-3">
        <div className="flex items-start gap-2">
          <div className="min-w-0 flex-1">
            <div className="text-xs font-semibold">Initial state</div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">Optional values written before the graph starts.</div>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => change("initialStateEntries", [...values.initialStateEntries, emptyInitialStateEntry()])}
          >
            <Plus className="h-4 w-4" /> Add
          </Button>
        </div>
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
      </section>
      {statePathSuggestions.length > 0 ? (
        <datalist id={statePathListID}>
          {statePathSuggestions.map((path) => <option key={path} value={path} />)}
        </datalist>
      ) : null}
      {error ? <div className="rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">{error}</div> : null}
      <div className="flex gap-2">
        <Button className="flex-1" onClick={() => void submit()} disabled={busy || !triggerTargetKey(values.target)}>
          {busy ? (trigger ? "Saving..." : "Creating...") : (trigger ? "Save changes" : "Create trigger")}
        </Button>
        {allowDelete && trigger ? (
          <Button variant="danger" size="icon" onClick={() => void remove()} disabled={busy} title="Delete trigger" aria-label="Delete trigger">
            <Trash2 className="h-4 w-4" />
          </Button>
        ) : null}
      </div>
      </div>
      {setupOpen && selectedChatChannel?.setup?.kind === "qr_code" ? (
        <ChatChannelSetupDialog
          channel={selectedChatChannel}
          triggerID={trigger?.chat?.channel === selectedChatChannel.id ? trigger.id : undefined}
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
