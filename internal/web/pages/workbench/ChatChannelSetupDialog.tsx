import { useEffect, useRef, useState } from "react";
import { CheckCircle2, LoaderCircle, RefreshCw, ShieldCheck, X } from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import {
  cancelChatChannelSetup,
  getChatChannelSetup,
  startChatChannelSetup,
  submitChatChannelSetupVerification,
} from "../../api";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import type {
  ChatChannelDefinition,
  ChatChannelSetupAccount,
  ChatChannelSetupResult,
} from "../../types";
import { WorkbenchDialogOverlay } from "./shared";

const pendingStatuses = new Set(["waiting", "scanned"]);

export function ChatChannelSetupDialog({
  channel,
  triggerID,
  onClose,
  onConfirmed,
}: {
  channel: ChatChannelDefinition;
  triggerID?: string;
  onClose: () => void;
  onConfirmed: (sessionID: string, account?: ChatChannelSetupAccount) => void;
}) {
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<ChatChannelSetupResult | null>(null);
  const [verificationCode, setVerificationCode] = useState("");
  const [verificationBusy, setVerificationBusy] = useState(false);
  const [error, setError] = useState("");
  const [retryAvailable, setRetryAvailable] = useState(false);
  const sessionIDRef = useRef("");
  const confirmedRef = useRef(false);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  useEffect(() => {
    let active = true;
    confirmedRef.current = false;
    sessionIDRef.current = "";
    setResult(null);
    setVerificationCode("");
    setError("");
    setRetryAvailable(false);
    queueMicrotask(() => {
      if (!active) return;
      void startChatChannelSetup(channel.id, triggerID)
        .then((next) => {
          if (!active) return;
          sessionIDRef.current = next.session_id;
          setResult(next);
        })
        .catch((err) => {
          if (active) {
            setError(errorMessage(err));
            setRetryAvailable(true);
          }
        });
    });
    return () => {
      active = false;
      const sessionID = sessionIDRef.current;
      if (sessionID && !confirmedRef.current) {
        void cancelChatChannelSetup(channel.id, sessionID).catch(() => undefined);
      }
    };
  }, [attempt, channel.id, triggerID]);

  useEffect(() => {
    if (!result || !pendingStatuses.has(result.status)) return;
    const controller = new AbortController();
    let active = true;
    const poll = async () => {
      let current = result;
      while (active && pendingStatuses.has(current.status)) {
        try {
          current = await getChatChannelSetup(channel.id, current.session_id, controller.signal);
          if (!active) return;
          setResult(current);
          if (pendingStatuses.has(current.status)) await wait(400, controller.signal);
        } catch (err) {
          if (!active || isAbortError(err)) return;
          setError(errorMessage(err));
          setRetryAvailable(true);
          return;
        }
      }
    };
    void poll();
    return () => {
      active = false;
      controller.abort();
    };
  }, [channel.id, result?.session_id, result?.status]);

  useEffect(() => {
    if (result?.status !== "confirmed") return;
    confirmedRef.current = true;
    onConfirmed(result.session_id, result.account);
  }, [onConfirmed, result]);

  async function submitVerificationCode() {
    const code = verificationCode.trim();
    if (!result || !code || verificationBusy) return;
    setVerificationBusy(true);
    setError("");
    setRetryAvailable(false);
    try {
      const next = await submitChatChannelSetupVerification(channel.id, result.session_id, code);
      setVerificationCode("");
      setResult(next);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setVerificationBusy(false);
    }
  }

  function restart() {
    const sessionID = sessionIDRef.current;
    if (sessionID) void cancelChatChannelSetup(channel.id, sessionID).catch(() => undefined);
    sessionIDRef.current = "";
    confirmedRef.current = false;
    setRetryAvailable(false);
    setAttempt((value) => value + 1);
  }

  const status = result?.status;
  const terminalFailure = status === "expired" || status === "failed" || retryAvailable;

  return (
    <WorkbenchDialogOverlay onDismiss={onClose}>
      <div className="w-[min(440px,100%)] overflow-hidden rounded-md border border-border bg-panel shadow-xl">
        <div className="flex h-14 items-center gap-3 border-b border-border px-4">
          <ShieldCheck className="h-4 w-4 text-muted-foreground" />
          <div className="min-w-0 flex-1 truncate text-sm font-semibold">Connect {channel.title}</div>
          <Button variant="ghost" size="icon" onClick={onClose} title="Close" aria-label="Close setup">
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="grid min-h-[390px] place-items-center gap-4 p-5 text-center">
          {result?.qr_code_content && status !== "confirmed" ? (
            <div className="grid h-[248px] w-[248px] place-items-center rounded-md border border-border bg-white p-3">
              <QRCodeSVG value={result.qr_code_content} size={220} level="M" />
            </div>
          ) : status === "confirmed" ? (
            <div className="grid h-[248px] w-[248px] place-items-center rounded-md border border-[var(--status-ok-border)] bg-[var(--status-ok-bg)]">
              <CheckCircle2 className="h-16 w-16 text-[var(--status-ok-text)]" />
            </div>
          ) : terminalFailure ? (
            <div className="grid h-[248px] w-[248px] place-items-center rounded-md border border-dashed border-border bg-muted/30">
              <RefreshCw className="h-10 w-10 text-muted-foreground" />
            </div>
          ) : (
            <div className="grid h-[248px] w-[248px] place-items-center rounded-md border border-dashed border-border bg-muted/30">
              <LoaderCircle className="h-9 w-9 animate-spin text-muted-foreground" />
            </div>
          )}

          <div className="grid w-full gap-1">
            <div className="text-sm font-medium">{setupStatusLabel(status)}</div>
            {status === "confirmed" && result?.account?.label ? (
              <div className="truncate text-xs text-muted-foreground">{result.account.label}</div>
            ) : null}
            {result?.message ? <div className="text-xs text-muted-foreground">{result.message}</div> : null}
            {error ? <div className="text-xs text-destructive">{error}</div> : null}
          </div>

          {status === "verification_required" ? (
            <div className="flex w-full gap-2">
              <Input
                value={verificationCode}
                onChange={(event) => setVerificationCode(event.target.value.replace(/\D/g, "").slice(0, 32))}
                onKeyDown={(event) => {
                  if (event.key === "Enter") void submitVerificationCode();
                }}
                inputMode="numeric"
                autoComplete="one-time-code"
                autoFocus
                placeholder="Verification code"
                aria-label="WeChat verification code"
              />
              <Button onClick={() => void submitVerificationCode()} disabled={!verificationCode.trim() || verificationBusy}>
                {verificationBusy ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <ShieldCheck className="h-4 w-4" />}
                Verify
              </Button>
            </div>
          ) : null}
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          {terminalFailure ? (
            <Button variant="outline" onClick={restart}>
              <RefreshCw className="h-4 w-4" />
              New code
            </Button>
          ) : null}
          <Button onClick={onClose}>{status === "confirmed" ? "Done" : "Close"}</Button>
        </div>
      </div>
    </WorkbenchDialogOverlay>
  );
}

function setupStatusLabel(status?: ChatChannelSetupResult["status"]): string {
  switch (status) {
    case "waiting":
      return "Scan with WeChat";
    case "scanned":
      return "Confirm on your phone";
    case "verification_required":
      return "Verification required";
    case "confirmed":
      return "Connected";
    case "expired":
      return "QR code expired";
    case "failed":
      return "Connection failed";
    default:
      return "Preparing QR code";
  }
}

function wait(delay: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(resolve, delay);
    signal.addEventListener("abort", () => {
      window.clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    }, { once: true });
  });
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
