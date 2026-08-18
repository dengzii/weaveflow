import { Handle, Position } from "@xyflow/react";
import { Clock3, MessageCircle, Repeat2, TriangleAlert, Webhook } from "lucide-react";
import {
  loopContinueHandleId,
  loopEndHandleId,
  loopEndInnerHandleId,
  loopStartHandleId,
  loopStartInnerHandleId,
} from "../lib/loopPresentation";
import {
  flowNodeAriaLabel,
  minGraphLoopHeight,
  minGraphLoopWidth,
  triggerTargetHandleID,
  type FlowNodeData,
} from "./graphCanvasModel";

export function GraphNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const status = String(data.status || "idle");
  const runtimeVisible = Boolean(data.runtimeVisible);
  const editable = Boolean(data.editable);
  const virtualKind = data.virtualKind;
  const executionCount = typeof data.executionCount === "number" && data.executionCount > 0 ? data.executionCount : 0;
  const highlighted = Boolean(data.highlighted);
  const configurationErrors = data.configurationErrors ?? [];
  const configurationInvalid = configurationErrors.length > 0;
  const showMissingBindings = Boolean(data.bindingSummary && data.missingBindings);
  const showMeta = configurationInvalid || showMissingBindings;
  const className = `debug-node${runtimeVisible ? ` debug-node-${status}` : ""}${virtualKind ? ` debug-node-virtual debug-node-virtual-${virtualKind}` : ""}${selected ? " debug-node-selected" : ""}${highlighted ? " debug-node-highlighted" : ""}${configurationInvalid ? " debug-node-config-invalid" : ""}${showMeta ? "" : " debug-node-single-line"}`;
  if (virtualKind === "start" || virtualKind === "end") {
    return (
      <div className={className} role="group" aria-label={flowNodeAriaLabel(data)}>
        {virtualKind === "start" ? (
          <Handle id={triggerTargetHandleID} type="target" position={Position.Left} isConnectable={false} />
        ) : null}
        {virtualKind === "end" ? (
          <Handle type="target" position={Position.Left} isConnectable={editable} />
        ) : null}
        <div className="debug-node-virtual-content">
          {runtimeVisible ? (
            <span
              className="debug-node-status-dot"
              role="img"
              aria-label={`Execution status: ${status}`}
              title={`Execution status: ${status}`}
            />
          ) : null}
          <div className="debug-node-virtual-label">{data.label}</div>
        </div>
        {virtualKind === "start" ? (
          <Handle type="source" position={Position.Right} isConnectable={editable} />
        ) : null}
      </div>
    );
  }

  const typeLabel = data.typeLabel || humanizeNodeType(data.type);
  return (
    <div className={className} role="group" aria-label={flowNodeAriaLabel(data)}>
      <Handle type="target" position={Position.Left} isConnectable={editable} />
      <div className="debug-node-header">
        {runtimeVisible ? (
          <span
            className="debug-node-status-dot"
            role="img"
            aria-label={`Execution status: ${status}`}
            title={`Execution status: ${status}`}
          />
        ) : null}
        <div className="debug-node-label min-w-0 flex-1 truncate">
          {data.label}
        </div>
        {configurationInvalid ? (
          <TriangleAlert
            className="debug-node-config-icon"
            aria-hidden="true"
          />
        ) : null}
        {runtimeVisible && executionCount ? <span className="debug-node-attempt">#{executionCount}</span> : null}
      </div>
      {showMeta ? (
        <div className="debug-node-meta">
          <span className="debug-node-meta-main">
            {configurationInvalid ? (
              <span
                className="debug-node-type debug-node-config-label"
                title={configurationErrors.join("\n")}
              >
                configuration error
              </span>
            ) : null}
          </span>
          {showMissingBindings ? (
            <span
              className="debug-node-binding debug-node-binding-missing"
              title={`State bindings: ${data.bindingSummary}`}
            >
              {data.bindingSummary}
            </span>
          ) : null}
        </div>
      ) : null}
      <Handle type="source" position={Position.Right} isConnectable={editable} />
      <NodeHoverCard
        data={data}
        typeLabel={typeLabel}
        configurationErrors={configurationErrors}
      />
    </div>
  );
}

export function GraphTriggerNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const enabled = Boolean(data.triggerEnabled);
  const valid = data.triggerValid !== false;
  const status = String(data.status || "idle");
  const runtimeVisible = Boolean(data.runtimeVisible);
  const TriggerIcon = data.triggerType === "schedule" ? Clock3 : data.triggerType === "chat" ? MessageCircle : Webhook;
  const className = `debug-node${runtimeVisible ? ` debug-node-${status}` : ""} debug-node-single-line debug-node-virtual debug-node-virtual-trigger ${enabled ? "debug-node-trigger-enabled" : "debug-node-trigger-disabled"}${valid ? "" : " debug-node-trigger-invalid"}${selected ? " debug-node-selected" : ""}`;
  return (
    <div className={className} role="group" aria-label={flowNodeAriaLabel(data)}>
      <div className="debug-node-header">
        <TriggerIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
        {runtimeVisible ? (
          <span
            className="debug-node-status-dot"
            role="img"
            aria-label={`Execution status: ${status}`}
            title={`Execution status: ${status}`}
          />
        ) : null}
        <div className="debug-node-label min-w-0 flex-1 truncate">
          {data.label}
        </div>
        {!valid ? (
          <TriangleAlert
            className="debug-node-trigger-invalid-icon"
            role="img"
            aria-label="Invalid trigger configuration"
          />
        ) : null}
      </div>
      <Handle type="source" position={Position.Right} isConnectable={false} />
      <NodeHoverCard data={data} typeLabel={data.triggerType || "trigger"} configurationErrors={[]} />
    </div>
  );
}

export function GraphLoopNode({ data, selected }: { data: FlowNodeData; selected?: boolean }) {
  const width = typeof data.width === "number" ? data.width : minGraphLoopWidth;
  const height = typeof data.height === "number" ? data.height : minGraphLoopHeight;
  return (
    <div
      className={`debug-loop${selected ? " debug-loop-selected" : ""}`}
      style={{ width, height }}
      role="group"
      aria-label={flowNodeAriaLabel(data)}
    >
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

function NodeHoverCard({
  data,
  typeLabel,
  configurationErrors,
}: {
  data: FlowNodeData;
  typeLabel: string;
  configurationErrors: readonly string[];
}) {
  const failed = data.runtimeVisible && data.status === "failed";
  const stateBindings = data.stateBindingPreview ?? [];
  const configuration = data.configurationPreview ?? [];
  const hoverType = shouldShowNodeType(data.label, typeLabel) ? typeLabel : "";
  return (
    <div className="debug-node-hover-card" aria-hidden="true">
      <div className="debug-node-hover-header">
        <div className="debug-node-hover-name">{data.label}</div>
        {hoverType ? <div className="debug-node-hover-type">{hoverType}</div> : null}
      </div>
      <NodePreviewSection title="State" items={stateBindings} />
      <NodePreviewSection title="Config" items={configuration} />
      {configurationErrors.length > 0 ? (
        <div className="debug-node-hover-error">
          <div className="debug-node-hover-heading">Configuration error</div>
          {configurationErrors.slice(0, 2).map((message) => <div key={message}>{message}</div>)}
          {configurationErrors.length > 2 ? <div>+{configurationErrors.length - 2} more</div> : null}
        </div>
      ) : null}
      {failed ? (
        <div className="debug-node-hover-error">
          <div className="debug-node-hover-heading">Run failed</div>
          <div>{data.errorSummary || "Node execution failed."}</div>
        </div>
      ) : null}
    </div>
  );
}

function NodePreviewSection({
  title,
  items,
}: {
  title: string;
  items: readonly { name: string; value: string }[];
}) {
  if (items.length === 0) return null;
  const visibleItems = items.slice(0, 4);
  return (
    <div className="debug-node-hover-section">
      <div className="debug-node-hover-heading">{title}</div>
      <div className="debug-node-hover-preview">
        {visibleItems.map((item) => (
          <div className="debug-node-hover-row" key={item.name}>
            <span className="debug-node-hover-key" title={item.name}>{item.name}</span>
            <span className="debug-node-hover-value" title={item.value}>{item.value}</span>
          </div>
        ))}
      </div>
      {items.length > visibleItems.length ? (
        <div className="debug-node-hover-more">+{items.length - visibleItems.length} more</div>
      ) : null}
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
  const normalizedType = humanizeNodeType(typeLabel).replace(/\s+node$/, "");
  return Boolean(normalizedType) && !normalizedLabel.includes(normalizedType);
}
