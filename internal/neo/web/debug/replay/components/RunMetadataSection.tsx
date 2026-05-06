import {ChevronDown} from "lucide-react";
import type {RunDetail} from "../types";
import {formatTime, prettyJSON, statusVariant} from "../utils";
import {Badge} from "../../../components/ui/badge";
import {Card, CardContent, CardHeader, CardTitle} from "../../../components/ui/card";
import {Collapsible, CollapsibleContent, CollapsibleTrigger} from "../../../components/ui/collapsible";
import {cn} from "../../../lib/utils";

type Props = {
    detail: RunDetail;
    compact?: boolean;
};

export function RunMetadataSection({detail, compact = false}: Props) {
    const meta = objectValue(detail.metadata);
    if (!meta) return null;

    const request = objectValue(meta.request);
    const config = objectValue(meta.config);
    const enabledTools = stringArray(meta.enabled_tools);
    const initialState = meta.initial_state;
    const finalState = meta.final_state;
    const finalAnswer = stringValue(meta.final_answer);

    const metrics: Array<[string, string]> = [
        ["Status", stringValue(meta.status) || detail.run.status || "-"],
        ["Started", formatTime(stringValue(meta.started_at) || detail.run.started_at)],
        ["Finished", formatTime(stringValue(meta.finished_at) || detail.run.finished_at)],
        ["Graph", stringValue(meta.graph_id) || detail.run.graph_id || "-"],
        ["Version", stringValue(meta.graph_version) || detail.run.graph_version || "-"],
        ["Execution", stringValue(meta.execution_root) || "-"],
    ];

    const configItems: Array<[string, string]> = [
        ["Mode", stringValue(config?.mode) || "-"],
        ["Scope", stringValue(config?.state_scope) || "-"],
        ["Max Iter", numberText(config?.max_iterations)],
        ["Planner Steps", numberText(config?.planner_max_steps)],
        ["Memory Recall", numberText(config?.memory_recall_limit)],
    ];

    const content = (
        <div className={cn("space-y-4", compact ? "" : "p-0")}>
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="text-sm font-semibold">Run Metadata</div>
                </div>
            </div>

            <div className="grid grid-cols-2 gap-3">
                {metrics.map(([label, value]) => (
                    <MetadataField key={label} label={label} value={value}/>
                ))}
            </div>

            {request?.message ? (
                <div className="rounded-lg border border-border bg-card p-3">
                    <div className="mb-1 text-xs font-medium text-muted-foreground">Request</div>
                    <div className="text-sm whitespace-pre-wrap break-words">{String(request.message)}</div>
                </div>
            ) : null}

            <div className="rounded-lg border border-border bg-card p-3">
                <div className="mb-2 text-xs font-medium text-muted-foreground">Config</div>
                <div className="grid grid-cols-2 gap-3">
                    {configItems.map(([label, value]) => (
                        <MetadataField key={label} label={label} value={value}/>
                    ))}
                </div>
            </div>

            {enabledTools.length ? (
                <div className="rounded-lg border border-border bg-card p-3">
                    <div className="mb-2 text-xs font-medium text-muted-foreground">Enabled Tools</div>
                    <div className="flex flex-wrap gap-2">
                        {enabledTools.map((tool) => (
                            <Badge key={tool} variant="outline" className="font-mono text-[11px]">
                                {tool}
                            </Badge>
                        ))}
                    </div>
                </div>
            ) : null}

            {finalAnswer ? (
                <div className="rounded-lg border border-border bg-card p-3">
                    <div className="mb-1 text-xs font-medium text-muted-foreground">Final Answer</div>
                    <div className="text-sm whitespace-pre-wrap break-words">{finalAnswer}</div>
                </div>
            ) : null}

            {stringValue(meta.error) ? (
                <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-3">
                    <div className="mb-1 text-xs font-medium text-destructive">Error</div>
                    <div className="text-sm whitespace-pre-wrap break-words text-destructive">
                        {stringValue(meta.error)}
                    </div>
                </div>
            ) : null}

            <div className="space-y-2">
                <JSONBlock title="Initial State" value={initialState} defaultOpen={false}/>
                <JSONBlock title="Final State" value={finalState} defaultOpen={false}/>
                <JSONBlock title="Raw Metadata" value={meta} defaultOpen={false}/>
            </div>
        </div>
    );

    if (compact) {
        return content;
    }

    return (
        <Card>
            <CardHeader className="pb-3">
                <CardTitle className="text-base">运行元数据</CardTitle>
            </CardHeader>
            <CardContent>{content}</CardContent>
        </Card>
    );
}

function MetadataField({label, value}: { label: string; value: string }) {
    return (
        <div>
            <div className="text-xs text-muted-foreground mb-0.5">{label}</div>
            <div className="text-sm font-medium break-words">{value || "-"}</div>
        </div>
    );
}

function JSONBlock({
                       title,
                       value,
                       defaultOpen,
                   }: {
    title: string;
    value: unknown;
    defaultOpen?: boolean;
}) {
    if (value === undefined || value === null) return null;

    return (
        <Collapsible defaultOpen={defaultOpen} className="rounded-lg border border-border bg-card">
            <CollapsibleTrigger className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left">
                <span className="text-xs font-medium text-muted-foreground">{title}</span>
                <ChevronDown className="h-3.5 w-3.5 text-muted-foreground"/>
            </CollapsibleTrigger>
            <CollapsibleContent>
        <pre
            className="max-h-[260px] overflow-auto border-t border-border px-3 py-3 font-mono text-[11px] leading-5 text-foreground">
          {prettyJSON(value)}
        </pre>
            </CollapsibleContent>
        </Collapsible>
    );
}

function objectValue(value: unknown): Record<string, unknown> | null {
    return value && typeof value === "object" && !Array.isArray(value)
        ? (value as Record<string, unknown>)
        : null;
}

function stringValue(value: unknown): string {
    return typeof value === "string" ? value : "";
}

function stringArray(value: unknown): string[] {
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function numberText(value: unknown): string {
    return typeof value === "number" ? String(value) : "-";
}
