import { BrowserRouter, Navigate, Route, Routes, useNavigate, useParams } from "react-router";
import { ThemeProvider } from "./lib/theme";
import { WorkbenchPage, workspaceTabs, type WorkspaceTab } from "./pages/WorkbenchPage";

export function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Navigate to="/app/graph" replace />} />
          <Route path="/app" element={<Navigate to="/app/graph" replace />} />
          <Route path="/app/:tab" element={<WorkbenchRoute />} />
          <Route path="*" element={<Navigate to="/app/graph" replace />} />
        </Routes>
      </BrowserRouter>
    </ThemeProvider>
  );
}

function WorkbenchRoute() {
  const params = useParams();
  const navigate = useNavigate();
  if (!isWorkspaceTab(params.tab)) {
    return <Navigate to="/app/graph" replace />;
  }

  return <WorkbenchPage tab={params.tab} onTabChange={(nextTab) => navigate(`/app/${nextTab}`)} />;
}

function isWorkspaceTab(value: unknown): value is WorkspaceTab {
  return typeof value === "string" && workspaceTabs.includes(value as WorkspaceTab);
}
