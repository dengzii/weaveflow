import { useState, useEffect, useRef } from "react";
import { Link } from "react-router-dom";
import { Send, Square, Bot, PlayCircle, Wrench, Settings, Save } from "lucide-react";
import { MessageList } from "../components/MessageList";
import { Button } from "../components/ui/button";
import { Textarea } from "../components/ui/textarea";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Switch } from "../components/ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../components/ui/select";
import { cn } from "../lib/utils";
import type { useChat } from "../hooks/useChat";
import type { useConfig } from "../hooks/useConfig";
import type { Config } from "../types";

declare const INCLUDE_DEBUG: boolean;

type ChatState = ReturnType<typeof useChat>;
type ConfigState = ReturnType<typeof useConfig>;

// ── Tools popover ──────────────────────────────────────────────────────────

function ToolsPopover({ cfg }: { cfg: ConfigState }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const toolNames = Object.keys(cfg.config.tools);
  const enabledCount = toolNames.filter((n) => cfg.config.tools[n]).length;

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDown);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <Button
        type="button"
        variant={open ? "secondary" : "ghost"}
        size="sm"
        className="h-7 gap-1.5 px-2 text-xs text-muted-foreground hover:text-foreground"
        onClick={() => setOpen((v) => !v)}
      >
        <Wrench className="h-3.5 w-3.5" />
        工具
        {toolNames.length > 0 && (
          <span className={cn("tabular-nums", enabledCount < toolNames.length && "text-amber-500")}>
            {enabledCount}/{toolNames.length}
          </span>
        )}
      </Button>

      {open && (
        <div className="absolute bottom-full mb-2 left-0 z-50 w-52 bg-popover border border-border rounded-xl shadow-lg p-2">
          <p className="text-xs font-medium text-muted-foreground px-1 pb-2">工具开关</p>
          {toolNames.length === 0 ? (
            <p className="text-xs text-muted-foreground px-1 py-2">暂无工具</p>
          ) : (
            <div className="space-y-0.5">
              {toolNames.map((name) => (
                <div
                  key={name}
                  className="flex items-center justify-between rounded-lg px-1 py-1.5 hover:bg-accent"
                >
                  <span className="text-xs font-mono">{name}</span>
                  <Switch
                    checked={cfg.config.tools[name]}
                    onCheckedChange={(v) => cfg.toggleTool(name, v)}
                  />
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ── Config popover ─────────────────────────────────────────────────────────

function ConfigPopover({ cfg }: { cfg: ConfigState }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const [local, setLocal] = useState<Partial<Config>>({});

  useEffect(() => {
    if (open) {
      setLocal({
        mode: cfg.config.mode,
        max_iterations: cfg.config.max_iterations,
        planner_max_steps: cfg.config.planner_max_steps,
        memory_recall_limit: cfg.config.memory_recall_limit,
        system_prompt: cfg.config.system_prompt,
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDown);
    };
  }, [open]);

  function patch(p: Partial<Config>) {
    setLocal((l) => ({ ...l, ...p }));
  }

  function handleSave() {
    const p: Partial<Config> = {};
    if (local.system_prompt !== undefined) p.system_prompt = local.system_prompt;
    if (local.max_iterations) p.max_iterations = local.max_iterations;
    if (local.planner_max_steps) p.planner_max_steps = local.planner_max_steps;
    if (local.memory_recall_limit !== undefined) p.memory_recall_limit = local.memory_recall_limit;
    if (local.mode) p.mode = local.mode;
    cfg.save(p);
  }

  return (
    <div ref={ref} className="relative">
      <Button
        type="button"
        variant={open ? "secondary" : "ghost"}
        size="sm"
        className="h-7 gap-1.5 px-2 text-xs text-muted-foreground hover:text-foreground"
        onClick={() => setOpen((v) => !v)}
      >
        <Settings className="h-3.5 w-3.5" />
        配置
      </Button>

      {open && (
        <div className="absolute bottom-full mb-2 left-0 z-50 w-80 bg-popover border border-border rounded-xl shadow-lg p-3 space-y-3">
          <p className="text-xs font-medium text-muted-foreground">基础配置</p>

          <div className="space-y-1">
            <Label className="text-xs">模式</Label>
            <Select
              value={local.mode ?? "auto"}
              onValueChange={(v) => patch({ mode: v as Config["mode"] })}
            >
              <SelectTrigger className="h-8 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">auto</SelectItem>
                <SelectItem value="direct">direct</SelectItem>
                <SelectItem value="planner">planner</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid grid-cols-3 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">最大迭代</Label>
              <Input
                type="number"
                min={1}
                className="h-8 text-xs"
                value={local.max_iterations || ""}
                onChange={(e) => patch({ max_iterations: parseInt(e.target.value) || 0 })}
                placeholder="16"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">规划步数</Label>
              <Input
                type="number"
                min={1}
                className="h-8 text-xs"
                value={local.planner_max_steps || ""}
                onChange={(e) => patch({ planner_max_steps: parseInt(e.target.value) || 0 })}
                placeholder="6"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">记忆召回</Label>
              <Input
                type="number"
                min={0}
                className="h-8 text-xs"
                value={local.memory_recall_limit ?? ""}
                onChange={(e) => patch({ memory_recall_limit: parseInt(e.target.value) || 0 })}
                placeholder="5"
              />
            </div>
          </div>

          <div className="space-y-1">
            <Label className="text-xs">系统提示词</Label>
            <Textarea
              rows={3}
              className="text-xs font-mono resize-none"
              value={local.system_prompt ?? ""}
              onChange={(e) => patch({ system_prompt: e.target.value })}
              placeholder="输入系统提示词..."
            />
          </div>

          <Button onClick={handleSave} size="sm" className="w-full gap-1.5">
            <Save className="h-3.5 w-3.5" />
            {cfg.saveLabel}
          </Button>
        </div>
      )}
    </div>
  );
}

// ── ChatPage ───────────────────────────────────────────────────────────────

interface Props {
  chat: ChatState;
  cfg: ConfigState;
}

export function ChatPage({ chat, cfg }: Props) {
  const inputRef = useRef<HTMLTextAreaElement>(null);

  function autoResize(el: HTMLTextAreaElement) {
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 200) + "px";
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const input = inputRef.current;
    if (!input) return;
    const text = input.value.trim();
    if (!text || chat.running) return;
    input.value = "";
    autoResize(input);
    chat.sendMessage(text);
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e as unknown as React.FormEvent);
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <header className="flex items-center gap-2.5 px-5 h-12 border-b border-border shrink-0">
        <Bot className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="font-semibold text-sm">Neo</span>
        {chat.running && (
          <span className="h-1.5 w-1.5 rounded-full bg-green-500 animate-pulse" />
        )}
        {INCLUDE_DEBUG && (
          <div className="ml-auto">
            <Link to="/debug/replay">
              <Button variant="ghost" size="sm" className="h-8 gap-1.5 text-xs text-muted-foreground hover:text-foreground">
                <PlayCircle className="h-3.5 w-3.5" />
                Replay
              </Button>
            </Link>
          </div>
        )}
      </header>

      {/* Messages */}
      <MessageList messages={chat.messages} renderMd={chat.renderMd} />

      {/* Input */}
      <div className="shrink-0">
        <div className="chat-content px-6 py-4">
          <form onSubmit={handleSubmit}>
            <div className="rounded-2xl border border-border bg-background shadow-sm">
              <Textarea
                ref={inputRef}
                placeholder="输入消息… (Enter 发送，Shift+Enter 换行)"
                rows={2}
                disabled={chat.running}
                onKeyDown={handleKeyDown}
                onInput={(e) => autoResize(e.currentTarget)}
                className={cn(
                  "border-0 shadow-none bg-transparent resize-none",
                  "px-4 pt-3.5 pb-1 min-h-[80px] max-h-[200px]",
                  "focus-visible:ring-0 focus-visible:ring-offset-0",
                  "text-sm placeholder:text-muted-foreground/50"
                )}
              />
              <div className="flex items-center justify-between px-3 pb-3 pt-1">
                <div className="flex items-center gap-0.5">
                  <ToolsPopover cfg={cfg} />
                  <ConfigPopover cfg={cfg} />
                </div>
                <div className="flex items-center gap-2.5">
                  {chat.running && chat.progress && (
                    <span className="flex items-center gap-1.5 text-xs text-muted-foreground max-w-[200px] truncate">
                      <span className="h-1.5 w-1.5 rounded-full bg-green-500 animate-pulse shrink-0" />
                      {chat.progress}
                    </span>
                  )}
                  {chat.running ? (
                    <Button
                      type="button"
                      variant="destructive"
                      size="icon"
                      className="h-8 w-8 rounded-xl"
                      onClick={chat.stop}
                      title="停止"
                    >
                      <Square className="h-3.5 w-3.5" />
                    </Button>
                  ) : (
                    <Button
                      type="submit"
                      size="icon"
                      className="h-8 w-8 rounded-xl"
                      title="发送"
                    >
                      <Send className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </div>
              </div>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
