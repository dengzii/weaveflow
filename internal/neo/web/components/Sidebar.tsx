import type { Config } from "../types";

interface Props {
  config: Config;
  saveLabel: string;
  onSave: (patch: Partial<Config>) => void;
  onToggleTool: (name: string, enabled: boolean) => void;
  onChange: (patch: Partial<Config>) => void;
}

export function Sidebar({ config, saveLabel, onSave, onToggleTool, onChange }: Props) {
  const toolNames = Object.keys(config.tools);

  function handleSave() {
    const patch: Partial<Config> = {};
    if (config.system_prompt !== "") patch.system_prompt = config.system_prompt;
    if (config.max_iterations) patch.max_iterations = config.max_iterations;
    if (config.planner_max_steps) patch.planner_max_steps = config.planner_max_steps;
    if (config.memory_recall_limit !== undefined) patch.memory_recall_limit = config.memory_recall_limit;
    patch.mode = config.mode;
    onSave(patch);
  }

  return (
    <aside className="sidebar">
      <section className="card">
        <div className="section-header">
          <h2>设置</h2>
          <button type="button" className="btn-small" onClick={handleSave}>
            {saveLabel}
          </button>
        </div>
        <div className="settings-grid">
          <label>
            System Prompt
            <textarea
              rows={3}
              value={config.system_prompt}
              onChange={(e) => onChange({ system_prompt: e.target.value })}
            />
          </label>
          <label>
            Max Iterations
            <input
              type="number"
              min={1}
              value={config.max_iterations || ""}
              onChange={(e) => onChange({ max_iterations: parseInt(e.target.value) || 0 })}
            />
          </label>
          <label>
            Planner Max Steps
            <input
              type="number"
              min={1}
              value={config.planner_max_steps || ""}
              onChange={(e) => onChange({ planner_max_steps: parseInt(e.target.value) || 0 })}
            />
          </label>
          <label>
            Memory Recall Limit
            <input
              type="number"
              min={0}
              value={config.memory_recall_limit ?? ""}
              onChange={(e) => onChange({ memory_recall_limit: parseInt(e.target.value) || 0 })}
            />
          </label>
          <label>
            Mode
            <select
              value={config.mode}
              onChange={(e) => onChange({ mode: e.target.value as Config["mode"] })}
            >
              <option value="auto">auto</option>
              <option value="direct">direct</option>
              <option value="planner">planner</option>
            </select>
          </label>
        </div>
      </section>

      <section className="card">
        <div className="section-header">
          <h2>工具</h2>
        </div>
        <div className="tool-list">
          {toolNames.length === 0 ? (
            <div style={{ color: "var(--muted)", fontSize: 13 }}>暂无工具</div>
          ) : (
            toolNames.map((name) => (
              <div key={name} className="tool-item">
                <div className="tool-name">{name}</div>
                <label className="toggle">
                  <input
                    type="checkbox"
                    checked={config.tools[name]}
                    onChange={(e) => onToggleTool(name, e.target.checked)}
                  />
                  <span className="toggle-track" />
                </label>
              </div>
            ))
          )}
        </div>
      </section>
    </aside>
  );
}
