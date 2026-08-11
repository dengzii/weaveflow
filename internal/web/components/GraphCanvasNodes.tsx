import { Handle, Position } from "@xyflow/react";
import { Clock3, MessageCircle, Repeat2, Webhook } from "lucide-react";
import {
  loopContinueHandleId,
  loopEndHandleId,
  loopEndInnerHandleId,
  loopStartHandleId,
  loopStartInnerHandleId,
} from "../lib/loopPresentation";
import {
  minGraphLoopHeight,
  minGraphLoopWidth,
  triggerTargetHandleID,
  type FlowNodeData,
} from "./graphCanvasModel";

export function GraphNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const status = String(data.status || "idle");
  const editable = Boolean(data.editable);
  const virtualKind = data.virtualKind;
  const attempt = typeof data.attempt === "number" && data.attempt > 0 ? data.attempt : 0;
  const highlighted = Boolean(data.highlighted);
  const className = `debug-node debug-node-${status}${virtualKind ? ` debug-node-virtual debug-node-virtual-${virtualKind}` : ""}${selected ? " debug-node-selected" : ""}${highlighted ? " debug-node-highlighted" : ""}`;
  if (virtualKind === "start" || virtualKind === "end") {
    return (
      <div className={className}>
        {virtualKind === "start" ? (
          <Handle id={triggerTargetHandleID} type="target" position={Position.Left} isConnectable={false} />
        ) : null}
        {virtualKind === "end" ? (
          <Handle type="target" position={Position.Left} isConnectable={editable} />
        ) : null}
        <div className="debug-node-virtual-label">{data.label}</div>
        {virtualKind === "start" ? (
          <Handle type="source" position={Position.Right} isConnectable={editable} />
        ) : null}
      </div>
    );
  }

  const typeLabel = humanizeNodeType(data.type);
  const showType = !data.bindingSummary || shouldShowNodeType(data.label, typeLabel);
  return (
    <div className={className}>
      <Handle type="target" position={Position.Left} isConnectable={editable} />
      <div className="debug-node-header">
        <span
          className="debug-node-status-dot"
          role="img"
          aria-label={`Execution status: ${status}`}
          title={`Execution status: ${status}`}
        />
        <div className="debug-node-label min-w-0 flex-1 truncate" title={data.label}>
          {data.label}
        </div>
      </div>
      <div className="debug-node-meta">
        <span className="debug-node-meta-main">
          {showType ? (
            <span className="debug-node-type" title={typeLabel}>
              {typeLabel}
            </span>
          ) : null}
        </span>
        {attempt ? <span className="debug-node-attempt">#{attempt}</span> : null}
      </div>
      <Handle type="source" position={Position.Right} isConnectable={editable} />
    </div>
  );
}

export function GraphTriggerNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const enabled = Boolean(data.triggerEnabled);
  const valid = data.triggerValid !== false;
  const TriggerIcon = data.triggerType === "schedule" ? Clock3 : data.triggerType === "chat" ? MessageCircle : Webhook;
  const className = `debug-node debug-node-virtual debug-node-virtual-trigger${valid ? "" : " debug-node-trigger-invalid"}${enabled ? "" : " debug-node-trigger-disabled"}${selected ? " debug-node-selected" : ""}`;
  return (
    <div className={className}>
      <div className="debug-node-header">
        <TriggerIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
        <div className="debug-node-label min-w-0 flex-1 truncate" title={data.label}>
          {data.label}
        </div>
        {!valid ? <span className="debug-node-status-dot" title="Invalid configuration" /> : null}
      </div>
      <div className="debug-node-meta">
        <span className="debug-node-type">{data.triggerType}</span>
        <span>{valid ? (enabled ? "enabled" : "disabled") : "invalid"}</span>
      </div>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
}

export function GraphLoopNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const width = typeof data.width === "number" ? data.width : minGraphLoopWidth;
  const height = typeof data.height === "number" ? data.height : minGraphLoopHeight;
  return (
    <div className={`debug-loop${selected ? " debug-loop-selected" : ""}`} style={{ width, height }}>
      <Handle
        id={loopStartHandleId}
        type="target"
        position={Position.Left}
        className="debug-loop-handle debug-loop-handle-start"
        isConnectable={false}
        title="Loop start"
        aria-label="Loop start"
      />
      <Handle
        id={loopStartInnerHandleId}
        type="source"
        position={Position.Right}
        className="debug-loop-handle debug-loop-handle-start debug-loop-handle-pair"
        isConnectable={false}
        aria-hidden="true"
      />
      <Handle
        id={loopContinueHandleId}
        type="target"
        position={Position.Left}
        className="debug-loop-handle debug-loop-handle-continue"
        isConnectable={false}
        title="Continue loop"
        aria-label="Continue loop"
      />
      <Handle
        id={loopEndInnerHandleId}
        type="target"
        position={Position.Left}
        className="debug-loop-handle debug-loop-handle-end debug-loop-handle-pair"
        isConnectable={false}
        aria-hidden="true"
      />
      <Handle
        id={loopEndHandleId}
        type="source"
        position={Position.Right}
        className="debug-loop-handle debug-loop-handle-end"
        isConnectable={false}
        title="End loop"
        aria-label="End loop"
      />
      <span className="debug-loop-port-label debug-loop-port-label-continue">continue</span>
      <div className="debug-loop-title" data-loop-title>
        <span className="flex min-w-0 items-center gap-1.5 truncate">
          <Repeat2 className="h-3.5 w-3.5 shrink-0" />
          <span className="truncate">{data.label}</span>
        </span>
      </div>
    </div>
  );
}

function humanizeNodeType(value: string): string {
  return value
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .toLowerCase();
}

function shouldShowNodeType(label: string, typeLabel: string): boolean {
  const normalizedLabel = humanizeNodeType(label).replace(/\s+node$/, "");
  return Boolean(typeLabel) && !normalizedLabel.includes(typeLabel);
}
