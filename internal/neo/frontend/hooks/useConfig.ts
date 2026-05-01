import { useState, useCallback } from "react";
import type { Config, ApiResponse } from "../types";

const DEFAULT_CONFIG: Config = {
  system_prompt: "",
  max_iterations: 0,
  planner_max_steps: 0,
  memory_recall_limit: 0,
  tools: {},
  mode: "auto",
};

export function useConfig() {
  const [config, setConfig] = useState<Config>(DEFAULT_CONFIG);
  const [saveLabel, setSaveLabel] = useState("保存");

  const load = useCallback(async () => {
    try {
      const resp = await fetch("/neo/config");
      const json: ApiResponse<Config> = await resp.json();
      if (json.data) setConfig(json.data);
    } catch { /* ignore */ }
  }, []);

  const save = useCallback(async (patch: Partial<Config>) => {
    try {
      const resp = await fetch("/neo/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(patch),
      });
      const json: ApiResponse<Config> = await resp.json();
      if (json.code === 200 && json.data) {
        setConfig(json.data);
        setSaveLabel("已保存");
        setTimeout(() => setSaveLabel("保存"), 1500);
      }
    } catch (err) {
      alert("保存失败: " + (err as Error).message);
    }
  }, []);

  const toggleTool = useCallback(async (name: string, enabled: boolean) => {
    try {
      const resp = await fetch("/neo/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ tools: { [name]: enabled } }),
      });
      const json: ApiResponse<Config> = await resp.json();
      if (json.code === 200 && json.data) setConfig(json.data);
    } catch {
      load();
    }
  }, [load]);

  return { config, setConfig, saveLabel, load, save, toggleTool };
}
