import { useEffect } from "react";
import { BrowserRouter, Routes, Route } from "react-router-dom";
import { useChat } from "./hooks/useChat";
import { useConfig } from "./hooks/useConfig";
import { ChatPage } from "./pages/ChatPage";
import { ReplayPage } from "./debug/replay/ReplayPage";

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
            {INCLUDE_DEBUG && <Route path="/debug/replay" element={<ReplayPage />} />}
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
