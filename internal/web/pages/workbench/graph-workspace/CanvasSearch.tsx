import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import type { CanvasSearchController } from "./useCanvasSearch";

export function CanvasSearch({ search }: { search: CanvasSearchController }) {
  if (!search.open) return null;
  const hasMatches = search.matches.length > 0;

  return (
    <div className="absolute left-1/2 top-4 z-40 flex w-[min(480px,calc(100%-2rem))] -translate-x-1/2 items-center gap-1 rounded-md border border-border bg-panel p-2 shadow-lg">
      <Search className="mx-1 h-4 w-4 shrink-0 text-muted-foreground" />
      <Input
        ref={search.inputRef}
        value={search.query}
        onChange={(event) => search.setQuery(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            search.setOpen(false);
            return;
          }
          if (event.key === "Enter") {
            event.preventDefault();
            search.move(event.shiftKey ? -1 : 1);
          }
        }}
        placeholder="Search nodes"
        className="h-8 min-w-0"
      />
      <span className="w-14 shrink-0 text-center text-xs tabular-nums text-muted-foreground">
        {search.query.trim() ? `${hasMatches ? search.matchIndex + 1 : 0}/${search.matches.length}` : "0/0"}
      </span>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => search.move(-1)}
        disabled={!hasMatches}
        title="Previous match"
        aria-label="Previous match"
      >
        <ChevronUp className="h-4 w-4" />
      </Button>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => search.move(1)}
        disabled={!hasMatches}
        title="Next match"
        aria-label="Next match"
      >
        <ChevronDown className="h-4 w-4" />
      </Button>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => search.setOpen(false)}
        title="Close search"
        aria-label="Close search"
      >
        <X className="h-4 w-4" />
      </Button>
    </div>
  );
}
