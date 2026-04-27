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
  cfgMode:             document.getElementById("cfg-mode"),
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

function isNearBottom() {
  const { scrollTop, scrollHeight, clientHeight } = el.messages;
  return scrollHeight - scrollTop - clientHeight < 80;
}

function scrollToBottom(force) {
  if (force || isNearBottom()) {
    requestAnimationFrame(() => {
      el.messages.scrollTop = el.messages.scrollHeight;
    });
  }
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

// ── SSE chat ──

async function sendMessage(message) {
  if (!message.trim() || state.running) return;

  const userDiv = document.createElement("div");
  userDiv.className = "msg msg-user";
  userDiv.textContent = message;
  el.messages.appendChild(userDiv);
  scrollToBottom(true);

  setRunning(true);
  showProgress("启动中...");

  const controller = new AbortController();
  state.abortController = controller;

  let lastNodeID = "";
  let thinkBlock = null;   // { div, text }
  let contentBlock = null; // { div, text }

  function closeThinking() {
    if (thinkBlock) {
      const div = thinkBlock.div;
      div.classList.add("thinking-done");
      div.addEventListener("click", () => {
        div.classList.toggle("thinking-expanded");
      });
      thinkBlock = null;
    }
  }

  function closeContent() {
    contentBlock = null;
  }

  function onNodeChange(nodeID) {
    if (!nodeID || nodeID === lastNodeID) return;
    closeThinking();
    closeContent();
    lastNodeID = nodeID;
  }

  function getThinkBlock() {
    if (!thinkBlock) {
      const div = document.createElement("div");
      div.className = "msg msg-thinking";
      el.messages.appendChild(div);
      thinkBlock = { div, text: "" };
    }
    return thinkBlock;
  }

  function getContentBlock() {
    if (!contentBlock) {
      const div = document.createElement("div");
      div.className = "msg msg-assistant";
      el.messages.appendChild(div);
      contentBlock = { div, text: "" };
    }
    return contentBlock;
  }

  function appendPhase(icon, text, cls) {
    closeThinking();
    closeContent();
    const item = document.createElement("div");
    item.className = "phase-item " + (cls || "tl-phase");
    item.textContent = icon + " " + text;
    el.messages.appendChild(item);
    scrollToBottom();
  }

  function appendToolEvent(icon, text, cls) {
    const item = document.createElement("div");
    item.className = "phase-item " + (cls || "tl-tool");
    item.textContent = icon + " " + text;
    el.messages.appendChild(item);
    scrollToBottom();
  }

  function appendError(msg) {
    const div = document.createElement("div");
    div.className = "msg msg-error";
    div.textContent = msg;
    el.messages.appendChild(div);
    scrollToBottom();
  }

  function handleChatEvent(event) {
    const type = event.type;
    const nodeID = event.node_id || "";
    const message = event.message || "";
    const content = event.content || "";

    switch (type) {
      case "thinking": {
        if (message) {
          showProgress(message);
          appendPhase("●", message, "tl-phase");
          if (nodeID) lastNodeID = nodeID;
        }
        if (content) {
          onNodeChange(nodeID);
          const b = getThinkBlock();
          b.text += content;
          b.div.innerHTML = renderMd(b.text);
          scrollToBottom();
        }
        break;
      }
      case "planning": {
        if (message) {
          showProgress(message);
          appendPhase("📋", message, "tl-phase");
          if (nodeID) lastNodeID = nodeID;
        }
        if (content) {
          onNodeChange(nodeID);
          const b = getThinkBlock();
          b.text += content;
          b.div.innerHTML = renderMd(b.text);
          scrollToBottom();
        }
        break;
      }
      case "generating": {
        if (message && !content) {
          showProgress(message);
          appendPhase("●", message, "tl-phase");
          if (nodeID) lastNodeID = nodeID;
        }
        if (content) {
          onNodeChange(nodeID);
          const b = getContentBlock();
          b.text += content;
          b.div.innerHTML = renderMd(b.text);
          scrollToBottom();
        }
        break;
      }
      case "calling_tool": {
        if (message) {
          showProgress(message);
          closeThinking();
          closeContent();
          appendToolEvent("⚡", message, "tl-tool");
        }
        break;
      }
      case "tool_result": {
        const isErr = message.includes("失败");
        appendToolEvent(isErr ? "✗" : "✓", message || "完成", isErr ? "tl-err" : "tl-ok");
        break;
      }
      case "verifying": {
        if (message) {
          showProgress(message);
          appendPhase("🔍", message, "tl-phase");
          if (nodeID) lastNodeID = nodeID;
        }
        break;
      }
      case "finalizing": {
        if (message) {
          showProgress(message);
          appendPhase("✨", message, "tl-phase");
          if (nodeID) lastNodeID = nodeID;
        }
        if (content) {
          onNodeChange(nodeID);
          const b = getContentBlock();
          b.text += content;
          b.div.innerHTML = renderMd(b.text);
          scrollToBottom();
        }
        break;
      }
      case "complete": {
        showProgress(message || "完成");
        break;
      }
      case "error": {
        appendError(message || "未知错误");
        break;
      }
    }
  }

  try {
    const resp = await fetch("/neo/chat", {
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

      for (const line of lines) {
        if (line.startsWith("data: ")) {
          const raw = line.slice(6);
          try {
            const event = JSON.parse(raw);
            handleChatEvent(event);
          } catch {}
        }
      }
    }
  } catch (err) {
    if (err.name !== "AbortError") {
      appendError("连接错误: " + err.message);
    }
  }

  closeThinking();
  setRunning(false);
  state.abortController = null;
}

async function stopAgent() {
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

// ── config (settings + tools + mode) ──

async function loadConfig() {
  try {
    const resp = await fetch("/neo/config");
    const json = await resp.json();
    const data = json.data;
    if (!data) return;
    el.cfgSystemPrompt.value = data.system_prompt || "";
    el.cfgMaxIterations.value = data.max_iterations || "";
    el.cfgPlannerMaxSteps.value = data.planner_max_steps || "";
    el.cfgMemoryRecallLimit.value = data.memory_recall_limit || "";
    if (el.cfgMode) el.cfgMode.value = data.mode || "auto";
    renderTools(data.tools || {});
  } catch {}
}

async function saveConfig() {
  const body = {};
  const sp = el.cfgSystemPrompt.value;
  if (sp !== "") body.system_prompt = sp;
  const mi = parseInt(el.cfgMaxIterations.value);
  if (!isNaN(mi)) body.max_iterations = mi;
  const ps = parseInt(el.cfgPlannerMaxSteps.value);
  if (!isNaN(ps)) body.planner_max_steps = ps;
  const mr = parseInt(el.cfgMemoryRecallLimit.value);
  if (!isNaN(mr)) body.memory_recall_limit = mr;
  if (el.cfgMode) body.mode = el.cfgMode.value;

  try {
    const resp = await fetch("/neo/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const json = await resp.json();
    if (json.code === 200) {
      const data = json.data;
      if (data && data.tools) renderTools(data.tools);
      el.saveSettingsBtn.textContent = "已保存";
      setTimeout(() => { el.saveSettingsBtn.textContent = "保存"; }, 1500);
    }
  } catch (err) {
    alert("保存失败: " + err.message);
  }
}

// ── tools ──

function renderTools(toolsMap) {
  const names = Object.keys(toolsMap);
  if (!names.length) {
    el.toolList.innerHTML = '<div style="color:var(--muted);font-size:13px;">暂无工具</div>';
    return;
  }
  el.toolList.innerHTML = names.map(name => `
    <div class="tool-item">
      <div>
        <div class="tool-name">${escapeHTML(name)}</div>
      </div>
      <label class="toggle">
        <input type="checkbox" data-tool="${escapeHTML(name)}" ${toolsMap[name] ? "checked" : ""}>
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
  const tools = {};
  tools[name] = enabled;
  try {
    const resp = await fetch("/neo/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tools }),
    });
    const json = await resp.json();
    if (json.code === 200 && json.data && json.data.tools) {
      renderTools(json.data.tools);
    }
  } catch (err) {
    alert("操作失败: " + err.message);
    loadConfig();
  }
}

// ── history ──

async function loadHistory() {
  try {
    const resp = await fetch("/neo/history");
    const json = await resp.json();
    const messages = json.data || [];
    renderHistory(messages);
  } catch {}
}

function renderHistory(messages) {
  for (const msg of messages) {
    if (msg.role === "system") continue;
    const div = document.createElement("div");
    const text = (msg.parts || [])
      .filter(p => p.type === "text" && p.text)
      .map(p => p.text)
      .join("\n");
    if (!text) continue;
    if (msg.role === "human") {
      div.className = "msg msg-user";
      div.textContent = text;
    } else {
      div.className = "msg msg-assistant";
      div.innerHTML = renderMd(text);
    }
    el.messages.appendChild(div);
  }
  scrollToBottom(true);
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

el.saveSettingsBtn.addEventListener("click", saveConfig);

// ── init ──

loadConfig();
loadHistory();
