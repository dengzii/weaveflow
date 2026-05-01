import { NavLink, useNavigate } from "react-router-dom";
import { MessageSquare, Settings, Plus, Bot, Bug, PlayCircle } from "lucide-react";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Separator } from "./ui/separator";

declare const INCLUDE_DEBUG: boolean;

interface Props {
  running: boolean;
  debugEnabled: boolean;
}

export function AppSidebar({ running, debugEnabled }: Props) {
  const navigate = useNavigate();

  const navItem = ({ isActive }: { isActive: boolean }) =>
    cn(
      "flex items-center gap-2.5 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full",
      isActive
        ? "bg-sidebar-accent text-sidebar-accent-foreground"
        : "text-sidebar-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground"
    );

  return (
    <aside className="flex flex-col w-56 h-full border-r border-sidebar-border bg-sidebar shrink-0">
      {/* Brand */}
      <div className="flex items-center gap-2.5 px-4 h-14 border-b border-sidebar-border">
        <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-sidebar-primary">
          <Bot className="h-4 w-4 text-sidebar-primary-foreground" />
        </div>
        <span className="font-semibold text-sm text-sidebar-foreground">Neo</span>
        {running && (
          <span className="ml-auto h-2 w-2 rounded-full bg-green-500 animate-pulse" />
        )}
      </div>

      {/* New chat */}
      <div className="p-3">
        <Button
          variant="outline"
          size="sm"
          className="w-full justify-start gap-2 border-sidebar-border text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          onClick={() => navigate("/")}
        >
          <Plus className="h-4 w-4" />
          新对话
        </Button>
      </div>

      <Separator className="bg-sidebar-border" />

      {/* Main navigation */}
      <nav className="flex-1 p-3 space-y-1">
        <NavLink to="/" end className={navItem}>
          <MessageSquare className="h-4 w-4" />
          对话
        </NavLink>
        <NavLink to="/settings" className={navItem}>
          <Settings className="h-4 w-4" />
          设置
        </NavLink>
      </nav>

      {/* Debug section — only rendered when INCLUDE_DEBUG is true */}
      {debugEnabled && INCLUDE_DEBUG && (
        <>
          <Separator className="bg-sidebar-border" />
          <div className="p-3 space-y-1">
            <div className="flex items-center gap-1.5 px-3 py-1">
              <Bug className="h-3 w-3 text-muted-foreground" />
              <span className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
                Debug
              </span>
            </div>
            <NavLink to="/debug/replay" className={navItem}>
              <PlayCircle className="h-4 w-4" />
              Replay
            </NavLink>
          </div>
        </>
      )}
    </aside>
  );
}
