import type { RunDetail } from "../types";
import { formatTime, formatDuration, statusVariant } from "../utils";
import { Badge } from "../../../components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";

export function StepsSection({ detail }: { detail: RunDetail }) {
  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">步骤</CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b">
                {["节点", "状态", "尝试", "开始", "耗时", "Checkpoint"].map((h) => (
                  <th
                    key={h}
                    className="text-left px-4 py-2 text-xs font-medium text-muted-foreground"
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {detail.steps.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-6 text-center text-sm text-muted-foreground">
                    暂无步骤
                  </td>
                </tr>
              ) : (
                detail.steps.map((item) => (
                  <tr
                    key={item.record.step_id}
                    className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                  >
                    <td className="px-4 py-2.5">
                      <div>{item.record.node_name || item.record.node_id}</div>
                      {item.record.node_name && (
                        <div className="text-xs font-mono text-muted-foreground">
                          {item.record.node_id}
                        </div>
                      )}
                    </td>
                    <td className="px-4 py-2.5">
                      <Badge variant={statusVariant(item.record.status)}>
                        {item.record.status}
                      </Badge>
                    </td>
                    <td className="px-4 py-2.5 text-muted-foreground">{item.record.attempt}</td>
                    <td className="px-4 py-2.5 text-muted-foreground">
                      {formatTime(item.record.started_at)}
                    </td>
                    <td className="px-4 py-2.5">{formatDuration(item.duration_ms)}</td>
                    <td className="px-4 py-2.5 text-xs font-mono text-muted-foreground">
                      {item.record.checkpoint_before_id || "-"}/
                      {item.record.checkpoint_after_id || "-"}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );
}
