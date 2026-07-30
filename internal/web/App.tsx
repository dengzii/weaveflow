import { BrowserRouter, Navigate, Route, Routes, useNavigate } from "react-router";
import { ThemeProvider } from "./lib/theme";
import { WorkbenchPage, type WorkspaceTab } from "./pages/WorkbenchPage";

export function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Navigate to="/app/graph" replace />} />
          <Route path="/app" element={<Navigate to="/app/graph" replace />} />
          <Route path="/app/graph" element={<WorkbenchRoute tab="graph" />} />
          <Route path="/app/settings" element={<WorkbenchRoute tab="settings" />} />
          <Route path="/app/*" element={<Navigate to="/app/graph" replace />} />
          <Route path="*" element={<Navigate to="/app/graph" replace />} />
        </Routes>
      </BrowserRouter>
    </ThemeProvider>
  );
}

function WorkbenchRoute({ tab }: { tab: WorkspaceTab }) {
  const navigate = useNavigate();

  return <WorkbenchPage tab={tab} onTabChange={(nextTab) => navigate(`/app/${nextTab}`)} />;
}
