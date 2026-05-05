import { useEffect } from "react";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { useChat } from "./hooks/useChat";
import { useConfig } from "./hooks/useConfig";
import { ChatPage } from "./pages/ChatPage";
import { ReplayPage } from "./debug/replay/ReplayPage";
import { ReplayPageV2 } from "./debug/replay-v2/ReplayPageV2";

declare const INCLUDE_DEBUG: boolean;

export function App() {
  const chat = useChat();
  const cfg = useConfig();

  useEffect(() => {
    cfg.load();
    chat.loadHistory();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <BrowserRouter>
      <div className="flex h-screen overflow-hidden bg-background text-foreground">
        <main className="flex-1 overflow-hidden">
          <Routes>
            <Route path="/" element={<ChatPage chat={chat} cfg={cfg} />} />
            {INCLUDE_DEBUG && <Route path="/debug/replay" element={<ReplayPageV2 />} />}
            {INCLUDE_DEBUG && <Route path="/debug/replay/old" element={<ReplayPage />} />}
            {INCLUDE_DEBUG && (
              <Route path="/debug/replay/v2" element={<Navigate to="/debug/replay" replace />} />
            )}
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
