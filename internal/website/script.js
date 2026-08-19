const translations = {
  en: {
    "meta.title": "WeaveFlow | Deterministic agent workflows in Go",
    "meta.description": "WeaveFlow is a deterministic graph runtime for building, executing, and inspecting LLM agents in Go.",
    "meta.twitterDescription": "Explicit topology, checkpointed state, and inspectable runs for agent workflows in Go.",
    "meta.imageAlt": "WeaveFlow graph runtime with inspectable execution paths",
    "accessibility.skip": "Skip to content",
    "accessibility.primaryNav": "Primary navigation",
    "accessibility.footerNav": "Footer navigation",
    "accessibility.home": "WeaveFlow home",
    "accessibility.openNav": "Open navigation",
    "accessibility.closeNav": "Close navigation",
    "accessibility.installCommand": "Install command",
    "nav.why": "Why WeaveFlow",
    "nav.runtime": "Runtime",
    "nav.start": "Get started",
    "nav.playground": "Playground",
    "nav.docs": "Docs",
    "language.label": "中文",
    "language.switch": "切换为中文",
    "hero.title": "Agent workflows you can inspect, control, and recover.",
    "hero.lede": "Build reliable LLM workflows with explicit topology, checkpointed state, and a deterministic runtime that keeps every decision inspectable.",
    "hero.start": "Start building",
    "hero.playground": "Open playground",
    "graph.caption": "A running ReAct graph appends user input to a conversation, lets an LLM call tools through a conditional edge, returns tool results to the LLM, and saves a resumable checkpoint.",
    "graph.completed": "Completed",
    "graph.hasToolCalls": "has tool calls",
    "graph.toolResult": "tool result",
    "graph.receiveInput": "Receive input",
    "graph.addMessage": "Add message",
    "graph.reason": "Reason",
    "graph.runTools": "Run tools",
    "graph.complete": "Complete",
    "graph.evidence": "RUNTIME EVIDENCE",
    "graph.checkpointSaved": "Checkpoint saved",
    "why.label": "WHY WEAVEFLOW",
    "why.title": "Explicit by design. Inspectable by default.",
    "why.description": "Agent workflow problems are not limited to whether a run completes. Control flow must be visible, state access must stay constrained, and failures must leave enough evidence to understand what happened.",
    "why.topologyTitle": "Topology stays explicit.",
    "why.topologyDescription": "Define nodes, edges, routes, and state bindings in a serializable Graph instead of an implicit loop.",
    "why.stateTitle": "State has a contract.",
    "why.stateDescription": "Use State Ports and explicit paths to catch invalid access and conflicting writes before execution.",
    "why.evidenceTitle": "Every run leaves evidence.",
    "why.evidenceDescription": "Events, steps, checkpoints, and Artifacts make runtime decisions inspectable after they occur.",
    "start.label": "GET STARTED",
    "start.title": "A focused API for structured workflows.",
    "start.description": "Install the module, connect nodes, and choose a Runner. Add checkpoints, approvals, and dynamic routing only where the workflow needs them.",
    "start.contracts": "Go-native contracts and Context",
    "start.adapter": "OpenAI-compatible model adapter",
    "start.examples": "Runnable examples that require no model",
    "start.browse": "Browse all examples",
    "copy.copy": "Copy",
    "copy.copied": "Copied",
    "copy.select": "Select & copy",
    "copy.copyAria": "Copy Go example",
    "copy.copiedAria": "Go example copied",
    "copy.selectAria": "Code selected; copy it manually",
    "runtime.label": "RUNTIME",
    "runtime.title": "Control where execution goes—and what it leaves behind.",
    "runtime.routingTitle": "Dynamic routing",
    "runtime.routingDescription": "Choose the next node from structured state and an explicit Route Decision.",
    "runtime.parallelTitle": "Parallel execution",
    "runtime.parallelDescription": "Bound fan-out and merge recorded patches in a deterministic order.",
    "runtime.approvalTitle": "Human approval",
    "runtime.approvalDescription": "Pause at a safe boundary and resume with reviewed input.",
    "runtime.failureTitle": "Failure routing",
    "runtime.failureDescription": "Handle classified failures as deliberate Graph paths instead of hidden exceptions.",
    "runtime.artifactTitle": "Artifact storage",
    "runtime.artifactDescription": "Associate outputs and evidence with the Run that produced them.",
    "runtime.inspectionTitle": "Live inspection",
    "runtime.inspectionDescription": "Inspect Runs, Events, Checkpoints, and State in the local Workbench.",
    "cta.title": "Keep every workflow understandable.",
    "cta.description": "Build agent systems with explicit execution paths, inspectable state, and controlled recovery.",
    "cta.action": "Explore WeaveFlow",
    "footer.tagline": "Deterministic agent workflows in Go.",
    "footer.docs": "Documentation",
    "footer.examples": "Examples",
    "footer.playground": "Playground",
    "footer.license": "MIT License",
    "footer.openSource": "Open source under the MIT License",
  },
  zh: {
    "meta.title": "WeaveFlow | Go Agent 工作流运行时",
    "meta.description": "WeaveFlow 是一个使用 Go 构建、执行和检查 LLM Agent 的确定性 Graph 运行时。",
    "meta.twitterDescription": "通过显式 Graph 拓扑、Checkpoint 和可检查的 Run 构建可靠的 Go Agent 工作流。",
    "meta.imageAlt": "展示可检查执行路径的 WeaveFlow Graph 运行时",
    "accessibility.skip": "跳到主要内容",
    "accessibility.primaryNav": "主导航",
    "accessibility.footerNav": "页脚导航",
    "accessibility.home": "WeaveFlow 首页",
    "accessibility.openNav": "打开导航",
    "accessibility.closeNav": "关闭导航",
    "accessibility.installCommand": "安装命令",
    "nav.why": "设计原则",
    "nav.runtime": "Runtime",
    "nav.start": "快速开始",
    "nav.playground": "Playground",
    "nav.docs": "Docs",
    "language.label": "EN",
    "language.switch": "Switch to English",
    "hero.title": "Agent 工作流，可查、可控、可恢复。",
    "hero.lede": "通过显式 Graph 拓扑、Checkpoint 与确定性运行时构建可靠的 LLM Agent，让每次决策都有迹可循。",
    "hero.start": "快速开始",
    "hero.playground": "打开 Playground",
    "graph.caption": "运行中的 ReAct Graph 将用户输入写入会话，允许 LLM 通过条件 Edge 调用工具，将工具结果返回给 LLM，并保存可恢复的 Checkpoint。",
    "graph.completed": "Completed",
    "graph.hasToolCalls": "has tool calls",
    "graph.toolResult": "tool result",
    "graph.receiveInput": "Receive input",
    "graph.addMessage": "Add message",
    "graph.reason": "Reason",
    "graph.runTools": "Run tools",
    "graph.complete": "Complete",
    "graph.evidence": "RUNTIME EVIDENCE",
    "graph.checkpointSaved": "Checkpoint saved",
    "why.label": "WHY WEAVEFLOW",
    "why.title": "显式建模，默认可检查。",
    "why.description": "可靠的 Agent 工作流不只要“能够运行”。控制流需要可见，State 访问需要受约束，失败后也应保留足够证据还原现场。",
    "why.topologyTitle": "Graph 拓扑保持显式。",
    "why.topologyDescription": "在可序列化的 Graph 中明确定义节点、边、路由与 State Binding，不把控制流藏进 Agent 循环。",
    "why.stateTitle": "State 访问有明确契约。",
    "why.stateDescription": "通过 State Ports 和显式路径，在执行前发现无效访问与并发写入冲突。",
    "why.evidenceTitle": "每个 Run 都有迹可循。",
    "why.evidenceDescription": "Event、Step、Checkpoint 和 Artifact 保留完整的执行证据，便于复盘运行时决策。",
    "start.label": "GET STARTED",
    "start.title": "用一组清晰的 API 组织复杂工作流。",
    "start.description": "安装模块、连接节点并选择 Runner；只在需要的位置加入 Checkpoint、人工审批和动态路由。",
    "start.contracts": "Go 原生契约与 Context",
    "start.adapter": "兼容 OpenAI 的 Model Adapter",
    "start.examples": "无需模型即可运行的示例",
    "start.browse": "查看全部示例",
    "copy.copy": "复制",
    "copy.copied": "已复制",
    "copy.select": "选择并复制",
    "copy.copyAria": "复制 Go 示例",
    "copy.copiedAria": "Go 示例已复制",
    "copy.selectAria": "代码已选中，请手动复制",
    "runtime.label": "RUNTIME",
    "runtime.title": "执行路径清晰，运行证据完整。",
    "runtime.routingTitle": "动态路由",
    "runtime.routingDescription": "从结构化 State 和显式 Route Decision 选择下一个节点。",
    "runtime.parallelTitle": "并行执行",
    "runtime.parallelDescription": "限制并行分支数量，并按确定顺序合并 State Patch。",
    "runtime.approvalTitle": "人工审批",
    "runtime.approvalDescription": "在安全边界暂停 Run，经人工确认后继续执行。",
    "runtime.failureTitle": "失败路由",
    "runtime.failureDescription": "将已分类的失败建模为清晰的 Graph 路径，而不是隐藏异常。",
    "runtime.artifactTitle": "Artifact 存储",
    "runtime.artifactDescription": "将输出与运行证据关联到产生它们的 Run。",
    "runtime.inspectionTitle": "实时检查",
    "runtime.inspectionDescription": "在本地 Workbench 中检查 Run、Event、Checkpoint 与 State。",
    "cta.title": "让复杂工作流始终清晰可控。",
    "cta.description": "以显式执行路径、可检查 State 和受控恢复机制构建 Agent 系统。",
    "cta.action": "探索 WeaveFlow",
    "footer.tagline": "Go Agent 工作流的确定性运行时。",
    "footer.docs": "Docs",
    "footer.examples": "示例",
    "footer.playground": "Playground",
    "footer.license": "MIT 许可证",
    "footer.openSource": "基于 MIT 许可证开源",
  },
};

const languageStorageKey = "weaveflow-language";
const header = document.querySelector("[data-header]");
const menuButton = document.querySelector("[data-menu-button]");
const menu = document.querySelector("[data-menu]");
const languageToggle = document.querySelector("[data-language-toggle]");
const languageLabel = document.querySelector("[data-language-label]");
const copyButton = document.querySelector("[data-copy-button]");
const copyLabel = document.querySelector("[data-copy-label]");
const code = document.querySelector("[data-code]");
const year = document.querySelector("[data-year]");

const getStoredLanguage = () => {
  try {
    const language = window.localStorage.getItem(languageStorageKey);
    return language === "en" || language === "zh" ? language : null;
  } catch {
    return null;
  }
};

const getSystemLanguage = () => {
  const languages = navigator.languages?.length ? navigator.languages : [navigator.language];
  return languages.some((language) => language?.toLowerCase().startsWith("zh")) ? "zh" : "en";
};

let currentLanguage = getStoredLanguage() ?? getSystemLanguage();

const translate = (key) => translations[currentLanguage][key] ?? translations.en[key] ?? key;

const updateMenuLabel = () => {
  const key = menuButton?.getAttribute("aria-expanded") === "true"
    ? "accessibility.closeNav"
    : "accessibility.openNav";
  menuButton?.setAttribute("aria-label", translate(key));
};

const applyLanguage = (language, { persist = false } = {}) => {
  currentLanguage = language === "zh" ? "zh" : "en";
  document.documentElement.lang = currentLanguage === "zh" ? "zh-CN" : "en";
  document.title = translate("meta.title");

  document.querySelectorAll("[data-i18n]").forEach((element) => {
    element.textContent = translate(element.dataset.i18n);
  });
  document.querySelectorAll("[data-i18n-content]").forEach((element) => {
    element.setAttribute("content", translate(element.dataset.i18nContent));
  });
  document.querySelectorAll("[data-i18n-aria-label]").forEach((element) => {
    element.setAttribute("aria-label", translate(element.dataset.i18nAriaLabel));
  });
  document.querySelectorAll("[data-docs-link]").forEach((link) => {
    link.setAttribute("href", currentLanguage === "zh" ? "/docs/zh/" : "/docs/");
  });

  if (languageLabel) {
    languageLabel.textContent = translate("language.label");
  }
  languageToggle?.setAttribute("aria-label", translate("language.switch"));
  updateMenuLabel();

  if (persist) {
    try {
      window.localStorage.setItem(languageStorageKey, currentLanguage);
    } catch {}
  }
};

document.body.classList.add("menu-ready");
applyLanguage(currentLanguage);

const updateHeader = () => {
  header?.classList.toggle("scrolled", window.scrollY > 12);
};

const closeMenu = ({ restoreFocus = false } = {}) => {
  menuButton?.setAttribute("aria-expanded", "false");
  updateMenuLabel();
  menu?.classList.remove("open");
  if (restoreFocus) {
    menuButton?.focus();
  }
};

const openMenu = () => {
  menuButton?.setAttribute("aria-expanded", "true");
  updateMenuLabel();
  menu?.classList.add("open");
};

updateHeader();
window.addEventListener("scroll", updateHeader, { passive: true });

languageToggle?.addEventListener("click", () => {
  applyLanguage(currentLanguage === "en" ? "zh" : "en", { persist: true });
});

menuButton?.addEventListener("click", () => {
  if (menuButton.getAttribute("aria-expanded") === "true") {
    closeMenu();
    return;
  }
  openMenu();
});

menu?.querySelectorAll("a").forEach((link) => {
  link.addEventListener("click", () => closeMenu());
});

document.addEventListener("click", (event) => {
  if (!(event.target instanceof Node) || menuButton?.getAttribute("aria-expanded") !== "true") {
    return;
  }
  if (!menu?.contains(event.target) && !menuButton?.contains(event.target)) {
    closeMenu();
  }
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && menuButton?.getAttribute("aria-expanded") === "true") {
    closeMenu({ restoreFocus: true });
  }
});

window.addEventListener("resize", () => {
  if (window.innerWidth >= 861) {
    closeMenu();
  }
});

copyButton?.addEventListener("click", async () => {
  if (!code) {
    return;
  }

  try {
    if (!navigator.clipboard || !window.isSecureContext) {
      throw new Error("clipboard unavailable");
    }
    await navigator.clipboard.writeText(code.textContent ?? "");
    copyLabel?.replaceChildren(translate("copy.copied"));
    copyButton.setAttribute("aria-label", translate("copy.copiedAria"));
  } catch {
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(code);
    selection?.removeAllRanges();
    selection?.addRange(range);
    copyLabel?.replaceChildren(translate("copy.select"));
    copyButton.setAttribute("aria-label", translate("copy.selectAria"));
  }

  window.setTimeout(() => {
    copyLabel?.replaceChildren(translate("copy.copy"));
    copyButton.setAttribute("aria-label", translate("copy.copyAria"));
  }, 1800);
});

if (year) {
  year.textContent = String(new Date().getFullYear());
}
