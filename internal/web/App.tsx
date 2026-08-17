import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { ThemeProvider } from "./lib/theme";
import { WorkbenchPage } from "./pages/WorkbenchPage";

export function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Navigate to="/app/graph" replace />} />
          <Route path="/app" element={<Navigate to="/app/graph" replace />} />
          <Route path="/app/graph" element={<WorkbenchPage />} />
          <Route path="/app/*" element={<Navigate to="/app/graph" replace />} />
          <Route path="*" element={<Navigate to="/app/graph" replace />} />
        </Routes>
      </BrowserRouter>
    </ThemeProvider>
  );
}
