import { Button } from "../../components/ui/button";
import { Textarea } from "../../components/ui/textarea";
import type { UserInputPrompt } from "./userInputModel";

export function UserInputPromptDialog({
  prompt,
  value,
  busy,
  onChange,
  onCancel,
  onSubmit,
}: {
  prompt: UserInputPrompt | null;
  value: string;
  busy: boolean;
  onChange: (value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  if (!prompt) return null;
  const canSubmit = value.trim().length > 0 && !busy;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4">
      <div className="w-[min(520px,100%)] rounded-md border border-border bg-panel shadow-xl">
        <div className="border-b border-border px-4 py-3">
          <div className="text-sm font-semibold">User input required</div>
        </div>
        <div className="p-4">
          <Textarea
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if ((event.ctrlKey || event.metaKey) && event.key === "Enter" && canSubmit) {
                event.preventDefault();
                onSubmit();
              }
            }}
            autoFocus
            aria-label="User response"
            placeholder={prompt.message || "The run is waiting for user input."}
            className="min-h-28"
          />
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            Dismiss
          </Button>
          <Button onClick={onSubmit} disabled={!canSubmit}>
            Resume run
          </Button>
        </div>
      </div>
    </div>
  );
}
