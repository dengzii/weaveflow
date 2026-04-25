const body = document.body;

const state = {
  cacheDir: body.dataset.defaultCacheDir || "",
  runs: [],
  selectedRunId: "",
  selectedSourceId: "",
  detail: null,
  replayIndex: 0,
  replayTimer: null,
};

const elements = {
  cacheForm: document.getElementById("cache-form"),
  cacheDir: document.getElementById("cache-dir"),
  statusText: document.getElementById("status-text"),
  summaryText: document.getElementById("summary-text"),
  runCount: document.getElementById("run-count"),
  runList: document.getElementById("run-list"),
  emptyState: document.getElementById("empty-state"),
  detailView: document.getElementById("detail-view"),
  overviewGrid: document.getElementById("overview-grid"),
  runStatus: document.getElementById("run-status"),
  runError: document.getElementById("run-error"),
  replaySlider: document.getElementById("replay-slider"),
  replayPosition: document.getElementById("replay-position"),
  replayCurrent: document.getElementById("replay-current"),
  replayList: document.getElementById("replay-list"),
  replayPayload: document.getElementById("replay-payload"),
  prevFrame: document.getElementById("prev-frame"),
  playToggle: document.getElementById("play-toggle"),
  nextFrame: document.getElementById("next-frame"),
  stepsTable: document.getElementById("steps-table"),
  checkpointCount: document.getElementById("checkpoint-count"),
  checkpointList: document.getElementById("checkpoint-list"),
  checkpointDetail: document.getElementById("checkpoint-detail"),
  artifactCount: document.getElementById("artifact-count"),
  artifactList: document.getElementById("artifact-list"),
  artifactDetail: document.getElementById("artifact-detail"),
  instanceDetail: document.getElementById("instance-detail"),
  graphDetail: document.getElementById("graph-detail"),
};

function setStatus(message, summary = "") {
  elements.statusText.textContent = message;
  elements.summaryText.textContent = summary;
}

async function api(path) {
  const response = await fetch(path);
  const payload = await response.json().catch(() => ({ error: "invalid response" }));
  if (!response.ok || payload.error) {
    throw new Error(payload.error || `request failed: ${response.status}`);
  }
  return payload.data;
}

function queryFor(path) {
  const url = new URL(path, window.location.origin);
  url.searchParams.set("cache_dir", state.cacheDir);
  return `${url.pathname}?${url.searchParams.toString()}`;
}

function formatTime(value) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

function formatDuration(ms) {
  if (!ms) {
    return "0 ms";
  }
  if (ms < 1000) {
    return `${ms} ms`;
  }
  if (ms < 60000) {
    return `${(ms / 1000).toFixed(2)} s`;
  }
  if (ms < 3600000) {
    return `${(ms / 60000).toFixed(2)} min`;
  }
  return `${(ms / 3600000).toFixed(2)} h`;
}

function formatBytes(bytes) {
  if (!bytes) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function prettyJSON(value) {
  if (value === undefined) {
    return "";
  }
  return JSON.stringify(value, null, 2);
}

function statusClass(status) {
  const value = String(status || "").toLowerCase();
  if (value.includes("failed") || value.includes("error")) {
    return "is-error";
  }
  if (value.includes("paused") || value.includes("warning")) {
    return "is-warning";
  }
  if (value.includes("completed") || value.includes("succeeded") || value.includes("success")) {
    return "is-success";
  }
  return "is-info";
}

function stopReplay() {
  if (state.replayTimer) {
    window.clearInterval(state.replayTimer);
    state.replayTimer = null;
  }
  elements.playToggle.textContent = "播放";
}

function renderRuns() {
  elements.runCount.textContent = String(state.runs.length);
  if (!state.runs.length) {
    elements.runList.innerHTML = `<div class="list-empty">当前目录没有找到 run 数据。</div>`;
    return;
  }

  const html = state.runs.map((item) => {
    const isActive = item.run.run_id === state.selectedRunId && item.source_id === state.selectedSourceId;
    return `
      <button class="run-card ${isActive ? "active" : ""}" data-run-id="${escapeHTML(item.run.run_id)}" data-source-id="${escapeHTML(item.source_id)}">
        <div class="run-card-top">
          <strong>${escapeHTML(item.graph_ref || item.run.graph_id || item.source_name)}</strong>
          <span class="status-badge ${statusClass(item.run.status)}">${escapeHTML(item.run.status)}</span>
        </div>
        <div class="run-card-body">
          <div class="meta-line">${escapeHTML(item.source_name)} · ${escapeHTML(item.instance_id || item.source_id)}</div>
          <div class="meta-line mono">${escapeHTML(item.run.run_id)}</div>
          <div class="meta-line">节点 ${escapeHTML(item.run.current_node_id || item.run.entry_node_id || "-")}</div>
          <div class="meta-line">步骤 ${item.step_count} · 事件 ${item.event_count} · 快照 ${item.checkpoint_count}</div>
          <div class="meta-line">开始 ${escapeHTML(formatTime(item.run.started_at))}</div>
        </div>
      </button>
    `;
  }).join("");

  elements.runList.innerHTML = html;
  elements.runList.querySelectorAll(".run-card").forEach((button) => {
    button.addEventListener("click", () => {
      selectRun(button.dataset.runId, button.dataset.sourceId);
    });
  });
}

function renderOverview(detail) {
  const run = detail.run;
  elements.runStatus.className = `status-badge ${statusClass(run.status)}`;
  elements.runStatus.textContent = run.status;

  const cards = [
    ["Graph", detail.summary.graph_ref || run.graph_id || "-"],
    ["Source", detail.source.name || detail.summary.source_name || "-"],
    ["Cache Root", detail.source.root || detail.summary.cache_root || "-"],
    ["Current Node", run.current_node_id || "-"],
    ["Last Step", run.last_step_id || "-"],
    ["Last Checkpoint", run.last_checkpoint_id || "-"],
    ["Started", formatTime(run.started_at)],
    ["Updated", formatTime(run.updated_at)],
    ["Duration", formatDuration(detail.summary.duration_ms)],
  ];

  elements.overviewGrid.innerHTML = cards.map(([label, value]) => `
    <div class="overview-card">
      <div class="overview-label">${escapeHTML(label)}</div>
      <div class="overview-value">${escapeHTML(value)}</div>
    </div>
  `).join("");

  if (run.error_message) {
    elements.runError.classList.remove("hidden");
    elements.runError.textContent = run.error_message;
  } else {
    elements.runError.classList.add("hidden");
    elements.runError.textContent = "";
  }
}

function setReplayIndex(index) {
  const replay = state.detail?.replay || [];
  if (!replay.length) {
    elements.replaySlider.max = "0";
    elements.replaySlider.value = "0";
    elements.replayPosition.textContent = "0 / 0";
    elements.replayCurrent.innerHTML = `<div class="list-empty">当前 run 没有事件。</div>`;
    elements.replayPayload.textContent = "";
    return;
  }

  state.replayIndex = Math.max(0, Math.min(index, replay.length - 1));
  elements.replaySlider.max = String(replay.length - 1);
  elements.replaySlider.value = String(state.replayIndex);
  elements.replayPosition.textContent = `${state.replayIndex + 1} / ${replay.length}`;

  const current = replay[state.replayIndex];
  elements.replayCurrent.innerHTML = `
    <div class="replay-current-card">
      <div class="timeline-dot ${statusClass(current.level)}"></div>
      <div>
        <div class="replay-title">${escapeHTML(current.title)}</div>
        <div class="replay-subtitle">${escapeHTML(current.subtitle || current.event.node_id || "")}</div>
        <div class="meta-line">${escapeHTML(formatTime(current.timestamp))}</div>
      </div>
    </div>
  `;
  elements.replayPayload.textContent = prettyJSON(current.event.payload);

  elements.replayList.querySelectorAll(".timeline-item").forEach((item, itemIndex) => {
    item.classList.toggle("active", itemIndex === state.replayIndex);
  });
}

function renderReplay(detail) {
  const replay = detail.replay || [];
  if (!replay.length) {
    elements.replayList.innerHTML = `<div class="list-empty">当前 run 没有事件。</div>`;
    setReplayIndex(0);
    return;
  }

  elements.replayList.innerHTML = replay.map((item) => `
    <button class="timeline-item" data-index="${item.index}">
      <div class="timeline-dot ${statusClass(item.level)}"></div>
      <div class="timeline-content">
        <div class="timeline-title">${escapeHTML(item.title)}</div>
        <div class="timeline-subtitle">${escapeHTML(item.subtitle || item.event.type)}</div>
      </div>
      <div class="timeline-time">${escapeHTML(formatTime(item.timestamp))}</div>
    </button>
  `).join("");

  elements.replayList.querySelectorAll(".timeline-item").forEach((button) => {
    button.addEventListener("click", () => {
      stopReplay();
      setReplayIndex(Number(button.dataset.index || "0"));
    });
  });

  setReplayIndex(Math.min(state.replayIndex, replay.length - 1));
}

function renderSteps(detail) {
  const rows = detail.steps.map((item) => `
    <tr>
      <td>
        <div>${escapeHTML(item.record.node_name || item.record.node_id)}</div>
        <div class="mono small">${escapeHTML(item.record.node_id)}</div>
      </td>
      <td><span class="status-badge ${statusClass(item.record.status)}">${escapeHTML(item.record.status)}</span></td>
      <td>${item.record.attempt}</td>
      <td>${escapeHTML(formatTime(item.record.started_at))}</td>
      <td>${escapeHTML(formatDuration(item.duration_ms))}</td>
      <td class="mono small">${escapeHTML(item.record.checkpoint_before_id || "-")} / ${escapeHTML(item.record.checkpoint_after_id || "-")}</td>
    </tr>
  `).join("");
  elements.stepsTable.innerHTML = rows || `<tr><td colspan="6" class="table-empty">暂无步骤</td></tr>`;
}

async function loadCheckpoint(checkpointId) {
  if (!checkpointId || !state.detail) {
    elements.checkpointDetail.textContent = "";
    return;
  }
  const path = queryFor(`/api/run/${encodeURIComponent(state.selectedRunId)}/checkpoint/${encodeURIComponent(checkpointId)}`) + `&source=${encodeURIComponent(state.selectedSourceId)}`;
  const detail = await api(path);
  elements.checkpointDetail.textContent = prettyJSON(detail);
}

function renderCheckpoints(detail) {
  const checkpoints = detail.checkpoints || [];
  elements.checkpointCount.textContent = String(checkpoints.length);
  if (!checkpoints.length) {
    elements.checkpointList.innerHTML = `<div class="list-empty">暂无 checkpoint。</div>`;
    elements.checkpointDetail.textContent = "";
    return;
  }

  elements.checkpointList.innerHTML = checkpoints.map((item) => `
    <button class="item-button" data-checkpoint-id="${escapeHTML(item.record.checkpoint_id)}">
      <div>
        <div>${escapeHTML(item.record.stage)} · ${escapeHTML(item.record.node_id)}</div>
        <div class="meta-line mono">${escapeHTML(item.record.checkpoint_id)}</div>
      </div>
      <div class="meta-line">${escapeHTML(formatTime(item.record.created_at))}</div>
    </button>
  `).join("");

  elements.checkpointList.querySelectorAll(".item-button").forEach((button) => {
    button.addEventListener("click", async () => {
      elements.checkpointDetail.textContent = "加载中...";
      try {
        await loadCheckpoint(button.dataset.checkpointId);
      } catch (error) {
        elements.checkpointDetail.textContent = String(error.message || error);
      }
    });
  });
}

async function loadArtifact(artifactId) {
  if (!artifactId || !state.detail) {
    elements.artifactDetail.textContent = "";
    return;
  }
  const path = queryFor(`/api/run/${encodeURIComponent(state.selectedRunId)}/artifact/${encodeURIComponent(artifactId)}`) + `&source=${encodeURIComponent(state.selectedSourceId)}`;
  const detail = await api(path);
  elements.artifactDetail.textContent = prettyJSON(detail);
}

function renderArtifacts(detail) {
  const artifacts = detail.artifacts || [];
  elements.artifactCount.textContent = String(artifacts.length);
  if (!artifacts.length) {
    elements.artifactList.innerHTML = `<div class="list-empty">暂无 artifact。</div>`;
    elements.artifactDetail.textContent = "";
    return;
  }

  elements.artifactList.innerHTML = artifacts.map((item) => `
    <button class="item-button" data-artifact-id="${escapeHTML(item.ref.id)}">
      <div>
        <div>${escapeHTML(item.ref.type || item.ref.mime_type || item.ref.id)}</div>
        <div class="meta-line mono">${escapeHTML(item.ref.id)}</div>
      </div>
      <div class="meta-line">${escapeHTML(formatBytes(item.bytes))}</div>
    </button>
  `).join("");

  elements.artifactList.querySelectorAll(".item-button").forEach((button) => {
    button.addEventListener("click", async () => {
      elements.artifactDetail.textContent = "加载中...";
      try {
        await loadArtifact(button.dataset.artifactId);
      } catch (error) {
        elements.artifactDetail.textContent = String(error.message || error);
      }
    });
  });
}

function renderSource(detail) {
  elements.instanceDetail.textContent = prettyJSON(detail.source.instance);
  elements.graphDetail.textContent = prettyJSON(detail.source.graph);
}

function renderDetail() {
  if (!state.detail) {
    elements.emptyState.classList.remove("hidden");
    elements.detailView.classList.add("hidden");
    return;
  }

  elements.emptyState.classList.add("hidden");
  elements.detailView.classList.remove("hidden");

  renderOverview(state.detail);
  renderReplay(state.detail);
  renderSteps(state.detail);
  renderCheckpoints(state.detail);
  renderArtifacts(state.detail);
  renderSource(state.detail);
}

async function selectRun(runId, sourceId) {
  stopReplay();
  state.selectedRunId = runId;
  state.selectedSourceId = sourceId;
  state.replayIndex = 0;
  renderRuns();
  setStatus("加载运行详情中...");

  try {
    const path = queryFor(`/api/run/${encodeURIComponent(runId)}`) + `&source=${encodeURIComponent(sourceId)}`;
    state.detail = await api(path);
    renderDetail();
    setStatus(`已加载 run ${runId}`, `${state.detail.summary.source_name} · ${state.detail.summary.graph_ref || "-"}`);

    const lastCheckpoint = state.detail.checkpoints.at(-1)?.record?.checkpoint_id;
    if (lastCheckpoint) {
      elements.checkpointDetail.textContent = "加载中...";
      loadCheckpoint(lastCheckpoint).catch((error) => {
        elements.checkpointDetail.textContent = String(error.message || error);
      });
    } else {
      elements.checkpointDetail.textContent = "";
    }

    const firstArtifact = state.detail.artifacts[0]?.ref?.id;
    if (firstArtifact) {
      elements.artifactDetail.textContent = "加载中...";
      loadArtifact(firstArtifact).catch((error) => {
        elements.artifactDetail.textContent = String(error.message || error);
      });
    } else {
      elements.artifactDetail.textContent = "";
    }
  } catch (error) {
    state.detail = null;
    renderDetail();
    setStatus(`加载失败: ${error.message}`);
  }
}

async function loadRuns() {
  stopReplay();
  state.cacheDir = elements.cacheDir.value.trim();
  setStatus("扫描缓存目录中...");

  try {
    const data = await api(queryFor("/api/runs"));
    state.runs = data.runs || [];
    renderRuns();

    const summary = `${data.sources?.length || 0} 个缓存源 · ${state.runs.length} 个 runs`;
    setStatus(`已扫描 ${data.cache_dir}`, summary);

    if (!state.runs.length) {
      state.detail = null;
      renderDetail();
      return;
    }

    const preserved = state.runs.find((item) => item.run.run_id === state.selectedRunId && item.source_id === state.selectedSourceId);
    const target = preserved || state.runs[0];
    await selectRun(target.run.run_id, target.source_id);
  } catch (error) {
    state.runs = [];
    state.detail = null;
    renderRuns();
    renderDetail();
    setStatus(`加载失败: ${error.message}`);
  }
}

function toggleReplay() {
  const replay = state.detail?.replay || [];
  if (!replay.length) {
    return;
  }
  if (state.replayTimer) {
    stopReplay();
    return;
  }

  elements.playToggle.textContent = "暂停";
  state.replayTimer = window.setInterval(() => {
    if (!state.detail?.replay?.length) {
      stopReplay();
      return;
    }
    if (state.replayIndex >= state.detail.replay.length - 1) {
      stopReplay();
      return;
    }
    setReplayIndex(state.replayIndex + 1);
  }, 900);
}

function bindEvents() {
  elements.cacheDir.value = state.cacheDir;

  elements.cacheForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    await loadRuns();
  });

  elements.replaySlider.addEventListener("input", () => {
    stopReplay();
    setReplayIndex(Number(elements.replaySlider.value || "0"));
  });

  elements.prevFrame.addEventListener("click", () => {
    stopReplay();
    setReplayIndex(state.replayIndex - 1);
  });

  elements.nextFrame.addEventListener("click", () => {
    stopReplay();
    setReplayIndex(state.replayIndex + 1);
  });

  elements.playToggle.addEventListener("click", toggleReplay);
}

bindEvents();
loadRuns();
