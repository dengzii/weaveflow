import { cn } from "../../lib/utils";

export function Badge({
  children,
  tone = "neutral",
  className,
}: {
  children: React.ReactNode;
  tone?: "neutral" | "ok" | "warn" | "danger" | "live";
  className?: string;
}) {
  return (
    <span
      className={cn(
        "badge inline-flex h-6 items-center rounded px-2 text-xs font-medium",
        tone === "ok" && "badge-ok",
        tone === "warn" && "badge-warn",
        tone === "danger" && "badge-danger",
        tone === "live" && "badge-live",
        className
      )}
    >
      {children}
    </span>
  );
}
