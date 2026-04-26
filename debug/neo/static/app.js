const NEO_ADDR = document.body.dataset.neoAddr || "http://localhost:9090";

const state = {
  running: false,
  abortController: null,
};

const el = {
  messages:       document.getElementById("messages"),
  chatForm:       document.getElementById("chat-form"),
  chatInput:      document.getElementById("chat-input"),
  sendBtn:        document.getElementById("send-btn"),
  stopBtn:        document.getElementById("stop-btn"),
  agentBadge:     document.getElementById("agent-badge"),
  progressBar:    document.getElementById("progress-bar"),
  progressText:   document.getElementById("progress-text"),
  cfgSystemPrompt:     document.getElementById("cfg-system-prompt"),
  cfgMaxIterations:    document.getElementById("cfg-max-iterations"),
  cfgPlannerMaxSteps:  document.getElementById("cfg-planner-max-steps"),
  cfgMemoryRecallLimit:document.getElementById("cfg-memory-recall-limit"),
  saveSettingsBtn:     document.getElementById("save-settings-btn"),
  toolList:            document.getElementById("tool-list"),
};

// ── markdown ──

marked.setOptions({ breaks: true, gfm: true });

function renderMd(src) {
  try { return marked.parse(src); }
  catch { return escapeHTML(src); }
}

// ── helpers ──

function escapeHTML(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function scrollToBottom() {
  el.messages.scrollTop = el.messages.scrollHeight;
}

function setRunning(running) {
  state.running = running;
  el.sendBtn.classList.toggle("hidden", running);
  el.stopBtn.classList.toggle("hidden", !running);
  el.chatInput.disabled = running;
  el.agentBadge.textContent = running ? "running" : "idle";
  el.agentBadge.className = "status-badge " + (running ? "is-warning" : "is-info");
  if (!running) {
    el.progressBar.classList.add("hidden");
  }
}

function showProgress(text) {
  el.progressText.textContent = text;
  el.progressBar.classList.remove("hidden");
}

function parseField(json, key) {
  try {
    const obj = typeof json === "string" ? JSON.parse(json) : json;
    return obj[key] || "";
  } catch { return ""; }
}

// ── SSE chat ──

async function sendMessage(message) {
  if (!message.trim() || state.running) return;

  const userDiv = document.createElement("div");
  userDiv.className = "msg msg-user";
  userDiv.textContent = message;
  el.messages.appendChild(userDiv);
  scrollToBottom();

  setRunning(true);
  showProgress("启动中...");

  const controller = new AbortController();
  state.abortController = controller;

  const timeline = document.createElement("div");
  timeline.className = "timeline";
  el.messages.appendChild(timeline);

  let thinkingDiv = null;
  let thinkingText = "";
  let answerDiv = null;
  let answerText = "";

  try {
    const resp = await fetch(NEO_ADDR + "/neo/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message }),
      signal: controller.signal,
    });

    if (!resp.ok) {
      const text = await resp.text();
      appendError("请求失败: " + (text || resp.statusText));
      setRunning(false);
      return;
    }

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop();

      let currentEvent = "";
      for (const line of lines) {
        if (line.startsWith("event: ")) {
          currentEvent = line.slice(7).trim();
        } else if (line.startsWith("data: ")) {
          handleSSEEvent(currentEvent, line.slice(6));
        }
      }
    }
  } catch (err) {
    if (err.name !== "AbortError") {
      appendError("连接错误: " + err.message);
    }
  }

  if (!timeline.children.length) timeline.remove();
  if (thinkingDiv) thinkingDiv.classList.add("thinking-done");
  setRunning(false);
  state.abortController = null;

  function addTimelineItem(icon, text, cls) {
    const item = document.createElement("span");
    item.className = "tl-item" + (cls ? " " + cls : "");
    item.textContent = icon + " " + text;
    timeline.appendChild(item);
    scrollToBottom();
  }

  function appendError(msg) {
    const div = document.createElement("div");
    div.className = "msg msg-error";
    div.textContent = msg;
    el.messages.appendChild(div);
    scrollToBottom();
  }

  function ensureThinkingDiv() {
    if (!thinkingDiv) {
      thinkingDiv = document.createElement("div");
      thinkingDiv.className = "msg msg-thinking";
      el.messages.appendChild(thinkingDiv);
    }
    return thinkingDiv;
  }

  function ensureAnswerDiv() {
    if (!answerDiv) {
      answerDiv = document.createElement("div");
      answerDiv.className = "msg msg-assistant";
      el.messages.appendChild(answerDiv);
    }
    return answerDiv;
  }

  function handleSSEEvent(eventType, data) {
    switch (eventType) {
      case "agent.phase": {
        const phase = parseField(data, "phase");
        if (phase) {
          showProgress(phase);
          addTimelineItem("●", phase, "tl-phase");
        }
        break;
      }
      case "agent.thinking": {
        const chunk = parseField(data, "text");
        if (chunk) {
          thinkingText += chunk;
          ensureThinkingDiv().innerHTML = renderMd(thinkingText);
          scrollToBottom();
        }
        break;
      }
      case "agent.content": {
        const chunk = parseField(data, "text");
        if (chunk) {
          answerText += chunk;
          ensureAnswerDiv().innerHTML = renderMd(answerText);
          scrollToBottom();
        }
        break;
      }
      case "tool.called": {
        const name = parseField(data, "tool_name");
        if (name) {
          showProgress("调用 " + name);
          addTimelineItem("⚡", name, "tl-tool");
        }
        break;
      }
      case "tool.returned": {
        addTimelineItem("✓", "完成", "tl-ok");
        break;
      }
      case "tool.failed": {
        const msg = parseField(data, "error") || "失败";
        addTimelineItem("✗", msg, "tl-err");
        break;
      }
      case "run.finished": {
        const answer = parseField(data, "answer");
        if (answer && !answerText) {
          answerText = answer;
          ensureAnswerDiv().innerHTML = renderMd(answer);
          scrollToBottom();
        }
        break;
      }
      case "error": {
        const msg = parseField(data, "msg") || parseField(data, "message") || "未知错误";
        appendError(msg);
        break;
      }
      case "done":
        break;
    }
  }
}

async function stopAgent() {
  try {
    await fetch(NEO_ADDR + "/neo/stop", { method: "POST" });
  } catch {}
  if (state.abortController) {
    state.abortController.abort();
    state.abortController = null;
  }
  const errDiv = document.createElement("div");
  errDiv.className = "msg msg-error";
  errDiv.textContent = "(已停止)";
  el.messages.appendChild(errDiv);
  scrollToBottom();
  setRunning(false);
}

// ── status ──

async function loadStatus() {
  try {
    const resp = await fetch(NEO_ADDR + "/neo/status");
    const json = await resp.json();
    const data = json.data;
    if (data && data.state === "running") {
      setRunning(true);
      showProgress(data.current_node ? "▸ " + data.current_node : "运行中...");
    }
  } catch {}
}

// ── settings ──

async function loadSettings() {
  try {
    const resp = await fetch(NEO_ADDR + "/neo/settings");
    const json = await resp.json();
    const s = json.data;
    if (!s) return;
    el.cfgSystemPrompt.value = s.system_prompt || "";
    el.cfgMaxIterations.value = s.max_iterations || "";
    el.cfgPlannerMaxSteps.value = s.planner_max_steps || "";
    el.cfgMemoryRecallLimit.value = s.memory_recall_limit || "";
  } catch {}
}

async function saveSettings() {
  const body = {};
  const sp = el.cfgSystemPrompt.value;
  if (sp !== "") body.system_prompt = sp;
  const mi = parseInt(el.cfgMaxIterations.value);
  if (!isNaN(mi)) body.max_iterations = mi;
  const ps = parseInt(el.cfgPlannerMaxSteps.value);
  if (!isNaN(ps)) body.planner_max_steps = ps;
  const mr = parseInt(el.cfgMemoryRecallLimit.value);
  if (!isNaN(mr)) body.memory_recall_limit = mr;

  try {
    const resp = await fetch(NEO_ADDR + "/neo/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const json = await resp.json();
    if (json.code === 200) {
      el.saveSettingsBtn.textContent = "已保存";
      setTimeout(() => { el.saveSettingsBtn.textContent = "保存"; }, 1500);
    }
  } catch (err) {
    alert("保存失败: " + err.message);
  }
}

// ── tools ──

async function loadTools() {
  try {
    const resp = await fetch(NEO_ADDR + "/neo/tools");
    const json = await resp.json();
    const tools = json.data || [];
    renderTools(tools);
  } catch {}
}

function renderTools(tools) {
  if (!tools.length) {
    el.toolList.innerHTML = '<div style="color:var(--muted);font-size:13px;">暂无工具</div>';
    return;
  }
  el.toolList.innerHTML = tools.map(t => `
    <div class="tool-item">
      <div>
        <div class="tool-name">${escapeHTML(t.name)}</div>
        <div class="tool-desc">${escapeHTML(t.description || "")}</div>
      </div>
      <label class="toggle">
        <input type="checkbox" data-tool="${escapeHTML(t.name)}" ${t.enabled ? "checked" : ""}>
        <span class="toggle-track"></span>
      </label>
    </div>
  `).join("");

  el.toolList.querySelectorAll("input[data-tool]").forEach(input => {
    input.addEventListener("change", () => {
      toggleTool(input.dataset.tool, input.checked);
    });
  });
}

async function toggleTool(name, enabled) {
  try {
    await fetch(NEO_ADDR + "/neo/tools/" + encodeURIComponent(name), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    });
  } catch (err) {
    alert("操作失败: " + err.message);
    loadTools();
  }
}

// ── auto-resize textarea ──

function autoResize(textarea) {
  textarea.style.height = "auto";
  textarea.style.height = Math.min(textarea.scrollHeight, 160) + "px";
}

// ── events ──

el.chatForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const msg = el.chatInput.value.trim();
  if (msg) {
    el.chatInput.value = "";
    autoResize(el.chatInput);
    sendMessage(msg);
  }
});

el.chatInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    el.chatForm.dispatchEvent(new Event("submit"));
  }
});

el.chatInput.addEventListener("input", () => autoResize(el.chatInput));

el.stopBtn.addEventListener("click", stopAgent);

el.saveSettingsBtn.addEventListener("click", saveSettings);

// ── init ──

loadStatus();
loadSettings();
loadTools();
