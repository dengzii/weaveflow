import { Braces } from "lucide-react";
import { Button } from "../../../components/ui/button";
import { Textarea } from "../../../components/ui/textarea";

export function JSONConfigEditor({
  open,
  value,
  applyLabel,
  onOpenChange,
  onChange,
  onApply,
  showToggle = true,
}: {
  open: boolean;
  value: string;
  applyLabel: string;
  onOpenChange: (open: boolean) => void;
  onChange: (value: string) => void;
  onApply: () => void;
  showToggle?: boolean;
}) {
  return (
    <div className="grid gap-2">
      {showToggle ? (
        <div>
          <Button type="button" variant="ghost" size="sm" onClick={() => onOpenChange(!open)}>
            <Braces className="h-4 w-4" />
            {open ? "Hide JSON" : "Edit JSON"}
          </Button>
        </div>
      ) : null}
      {open ? (
        <>
          <Textarea
            aria-label={`${applyLabel} JSON`}
            value={value}
            onChange={(event) => onChange(event.target.value)}
            spellCheck={false}
            className="h-44 font-mono text-xs"
          />
          <Button type="button" variant="outline" size="sm" onClick={onApply}>
            <Braces className="h-4 w-4" />
            {applyLabel}
          </Button>
        </>
      ) : null}
    </div>
  );
}
