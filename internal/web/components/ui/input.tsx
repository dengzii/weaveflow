import * as React from "react";
import { cn } from "../../lib/utils";

export const SENSITIVE_VALUE_MASK = "**********";

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        "h-9 w-full rounded-md border border-input bg-background px-3 text-sm outline-none transition-colors placeholder:text-muted-foreground focus:border-ring disabled:cursor-not-allowed disabled:opacity-60",
        className
      )}
      {...props}
    />
  )
);

Input.displayName = "Input";

interface SensitiveInputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type" | "value" | "onChange"> {
  value: string;
  configured?: boolean;
  onValueChange: (value: string) => void;
}

export function SensitiveInput({
  value,
  configured = false,
  onValueChange,
  onFocus,
  onBlur,
  ...props
}: SensitiveInputProps) {
  const [editing, setEditing] = React.useState(false);
  const hasValue = configured || value.length > 0;

  return (
    <Input
      {...props}
      type={editing ? "password" : "text"}
      value={editing ? value : hasValue ? SENSITIVE_VALUE_MASK : ""}
      onChange={(event) => onValueChange(event.target.value)}
      onFocus={(event) => {
        setEditing(true);
        onFocus?.(event);
      }}
      onBlur={(event) => {
        setEditing(false);
        onBlur?.(event);
      }}
    />
  );
}
