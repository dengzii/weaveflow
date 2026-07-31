import { Panel } from "@xyflow/react";
import { Focus, Lock, Maximize2, Network, Unlock, ZoomIn, ZoomOut } from "lucide-react";

export function GraphCanvasControls({
  interactive,
  canAutoLayout,
  hasSelection,
  onAutoLayout,
  onFitView,
  onFitSelection,
  onToggleInteractive,
  onZoomIn,
  onZoomOut,
}: {
  interactive: boolean;
  canAutoLayout: boolean;
  hasSelection: boolean;
  onAutoLayout?: () => void;
  onFitView: () => void;
  onFitSelection: () => void;
  onToggleInteractive: () => void;
  onZoomIn: () => void;
  onZoomOut: () => void;
}) {
  return (
    <Panel position="top-right" className="react-flow__controls vertical" aria-label="Canvas controls">
      <button type="button" className="react-flow__controls-button" title="Zoom in" aria-label="Zoom in" onClick={onZoomIn}>
        <ZoomIn className="h-3.5 w-3.5" />
      </button>
      <button type="button" className="react-flow__controls-button" title="Zoom out" aria-label="Zoom out" onClick={onZoomOut}>
        <ZoomOut className="h-3.5 w-3.5" />
      </button>
      <button type="button" className="react-flow__controls-button" title="Fit view" aria-label="Fit view" onClick={onFitView}>
        <Maximize2 className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        className="react-flow__controls-button"
        title="Auto layout"
        aria-label="Auto layout"
        onClick={onAutoLayout}
        disabled={!canAutoLayout || !onAutoLayout}
      >
        <Network className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        className="react-flow__controls-button"
        title="Fit selection"
        aria-label="Fit selection"
        onClick={onFitSelection}
        disabled={!hasSelection}
      >
        <Focus className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        className="react-flow__controls-button"
        title={interactive ? "Lock canvas" : "Unlock canvas"}
        aria-label={interactive ? "Lock canvas" : "Unlock canvas"}
        onClick={onToggleInteractive}
      >
        {interactive ? <Unlock className="h-3.5 w-3.5" /> : <Lock className="h-3.5 w-3.5" />}
      </button>
    </Panel>
  );
}
