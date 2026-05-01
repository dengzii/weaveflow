import { Save } from "lucide-react";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Textarea } from "../components/ui/textarea";
import { Label } from "../components/ui/label";
import { Switch } from "../components/ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../components/ui/select";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Separator } from "../components/ui/separator";
import type { useConfig } from "../hooks/useConfig";
import type { Config } from "../types";

type ConfigState = ReturnType<typeof useConfig>;

interface Props {
  cfg: ConfigState;
}

export function SettingsPage({ cfg }: Props) {
  const { config, saveLabel, save, toggleTool, setConfig } = cfg;
  const toolNames = Object.keys(config.tools);

  function handleChange(patch: Partial<Config>) {
    setConfig((prev) => ({ ...prev, ...patch }));
  }

  function handleSave() {
    const patch: Partial<Config> = {};
    if (config.system_prompt !== "") patch.system_prompt = config.system_prompt;
    if (config.max_iterations) patch.max_iterations = config.max_iterations;
    if (config.planner_max_steps) patch.planner_max_steps = config.planner_max_steps;
    if (config.memory_recall_limit !== undefined) patch.memory_recall_limit = config.memory_recall_limit;
    patch.mode = config.mode;
    save(patch);
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-2xl mx-auto p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">设置</h1>
            <p className="text-sm text-muted-foreground mt-0.5">配置 Neo 的运行参数</p>
          </div>
          <Button onClick={handleSave} size="sm" className="gap-1.5">
            <Save className="h-4 w-4" />
            {saveLabel}
          </Button>
        </div>

        <Separator />

        {/* General Settings */}
        <Card>
          <CardHeader className="pb-4">
            <CardTitle className="text-base">基础配置</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="system-prompt">System Prompt</Label>
              <Textarea
                id="system-prompt"
                rows={4}
                value={config.system_prompt}
                onChange={(e) => handleChange({ system_prompt: e.target.value })}
                placeholder="输入系统提示词..."
                className="font-mono text-xs"
              />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="max-iter">Max Iterations</Label>
                <Input
                  id="max-iter"
                  type="number"
                  min={1}
                  value={config.max_iterations || ""}
                  onChange={(e) => handleChange({ max_iterations: parseInt(e.target.value) || 0 })}
                  placeholder="10"
                />
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="planner-steps">Planner Max Steps</Label>
                <Input
                  id="planner-steps"
                  type="number"
                  min={1}
                  value={config.planner_max_steps || ""}
                  onChange={(e) => handleChange({ planner_max_steps: parseInt(e.target.value) || 0 })}
                  placeholder="5"
                />
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="memory-limit">Memory Recall Limit</Label>
                <Input
                  id="memory-limit"
                  type="number"
                  min={0}
                  value={config.memory_recall_limit ?? ""}
                  onChange={(e) => handleChange({ memory_recall_limit: parseInt(e.target.value) || 0 })}
                  placeholder="0"
                />
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="mode">Mode</Label>
                <Select
                  value={config.mode}
                  onValueChange={(val) => handleChange({ mode: val as Config["mode"] })}
                >
                  <SelectTrigger id="mode">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">auto</SelectItem>
                    <SelectItem value="direct">direct</SelectItem>
                    <SelectItem value="planner">planner</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Tools */}
        <Card>
          <CardHeader className="pb-4">
            <CardTitle className="text-base">工具管理</CardTitle>
          </CardHeader>
          <CardContent>
            {toolNames.length === 0 ? (
              <p className="text-sm text-muted-foreground py-2">暂无可用工具</p>
            ) : (
              <div className="space-y-1">
                {toolNames.map((name, i) => (
                  <div key={name}>
                    <div className="flex items-center justify-between py-3">
                      <span className="text-sm font-mono">{name}</span>
                      <Switch
                        checked={config.tools[name]}
                        onCheckedChange={(checked) => toggleTool(name, checked)}
                      />
                    </div>
                    {i < toolNames.length - 1 && <Separator />}
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
