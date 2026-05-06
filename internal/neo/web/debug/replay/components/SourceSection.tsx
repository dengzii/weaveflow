import type { RunDetail } from "../types";
import { prettyJSON } from "../utils";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";
import { RunMetadataSection } from "./RunMetadataSection";

export function SourceSection({ detail }: { detail: RunDetail }) {
  return (
    <div className="space-y-4">
      <RunMetadataSection detail={detail} />
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base">源配置</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <div className="grid grid-cols-1 divide-y divide-border/50 lg:grid-cols-2 lg:divide-x lg:divide-y-0">
            <div>
              <div className="px-4 py-2 text-xs font-medium text-muted-foreground border-b border-border/50">
                Instance Config
              </div>
              <pre className="text-xs font-mono p-3 overflow-auto max-h-64 bg-muted/20">
                {prettyJSON(detail.source.instance)}
              </pre>
            </div>
            <div>
              <div className="px-4 py-2 text-xs font-medium text-muted-foreground border-b border-border/50">
                Graph Config
              </div>
              <pre className="text-xs font-mono p-3 overflow-auto max-h-64 bg-muted/20">
                {prettyJSON(detail.source.graph)}
              </pre>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
