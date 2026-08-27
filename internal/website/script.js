const translations = {
  en: {
    "meta.title": "WeaveFlow | Build inspectable agent workflows in Go",
    "meta.description": "WeaveFlow is a graph runtime written in Go for building, executing, and inspecting LLM agent workflows.",
    "meta.twitterDescription": "Build reliable Go agent workflows with explicit topology, checkpointed state, and inspectable runs.",
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
    "language.switch": "Switch to Chinese",
    "hero.title": "Build agent workflows you can inspect, control, and recover",
    "hero.lede": "Build reliable LLM workflows with explicit topology, checkpointed state, and a runtime that keeps every decision inspectable.",
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
    "why.title": "Explicit by design — inspectable by default",
    "why.description": "An agent workflow is more than a final response: you need to see the path it took, constrain the state it touched, and understand failures after the fact.",
    "why.topologyTitle": "Topology stays explicit",
    "why.topologyDescription": "Define nodes, edges, routes, and state bindings in a serializable Graph instead of an implicit loop.",
    "why.stateTitle": "State has a contract",
    "why.stateDescription": "Use State Ports and explicit paths to catch invalid access and conflicting writes before execution.",
    "why.evidenceTitle": "Every run leaves evidence",
    "why.evidenceDescription": "Events, Steps, Checkpoints, and Artifacts preserve the evidence behind each runtime decision.",
    "start.label": "GET STARTED",
    "start.title": "A focused API for structured workflows",
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
    "runtime.title": "Make execution paths explicit—and keep the evidence",
    "runtime.routingTitle": "Dynamic routing",
    "runtime.routingDescription": "Choose the next node from structured state and an explicit Route Decision.",
    "runtime.parallelTitle": "Parallel execution",
    "runtime.parallelDescription": "Run bounded fan-out and merge recorded patches in a defined order.",
    "runtime.approvalTitle": "Human approval",
    "runtime.approvalDescription": "Pause at a safe boundary and resume with reviewed input.",
    "runtime.failureTitle": "Failure routing",
    "runtime.failureDescription": "Handle classified failures as deliberate Graph paths instead of hidden exceptions.",
    "runtime.artifactTitle": "Artifact storage",
    "runtime.artifactDescription": "Associate outputs and evidence with the Run that produced them.",
    "runtime.inspectionTitle": "Live inspection",
    "runtime.inspectionDescription": "Inspect Runs, Events, Checkpoints, and State in the local Workbench.",
    "cta.title": "Keep every workflow understandable",
    "cta.description": "Build agent systems with explicit execution paths, inspectable state, and controlled recovery.",
    "cta.action": "Explore WeaveFlow",
    "footer.tagline": "A Go runtime for inspectable agent workflows",
    "footer.docs": "Documentation",
    "footer.examples": "Examples",
    "footer.playground": "Playground",
    "footer.license": "MIT License",
    "footer.openSource": "Open source under the MIT License",
  },
  zh: {
    "meta.title": "WeaveFlow｜用 Go 构建可检查的智能体工作流",
    "meta.description": "WeaveFlow 是一个用 Go 构建的图运行时，用于搭建、执行和检查 LLM 智能体工作流。",
    "meta.twitterDescription": "用 Go 构建可靠的智能体工作流：拓扑清晰、状态可恢复、每次运行都可检查。",
    "meta.imageAlt": "展示可检查执行路径的 WeaveFlow 图运行时",
    "accessibility.skip": "跳到主要内容",
    "accessibility.primaryNav": "主导航",
    "accessibility.footerNav": "页脚导航",
    "accessibility.home": "WeaveFlow 首页",
    "accessibility.openNav": "打开导航",
    "accessibility.closeNav": "关闭导航",
    "accessibility.installCommand": "安装命令",
    "nav.why": "为什么选择 WeaveFlow",
    "nav.runtime": "运行时",
    "nav.start": "快速开始",
    "nav.playground": "体验平台",
    "nav.docs": "文档",
    "language.label": "EN",
    "language.switch": "切换为英文",
    "hero.title": "构建可检查、可控、可恢复的智能体工作流",
    "hero.lede": "借助明确的拓扑、带检查点的状态和完整的运行记录，构建可靠的 LLM 工作流，让每个决策都有迹可循。",
    "hero.start": "快速开始",
    "hero.playground": "打开体验平台",
    "graph.caption": "运行中的 ReAct 图会将用户输入追加到会话中；LLM 通过条件边调用工具，工具结果再返回给模型，最后保存可恢复的检查点。",
    "graph.completed": "已完成",
    "graph.hasToolCalls": "包含工具调用",
    "graph.toolResult": "工具结果",
    "graph.receiveInput": "接收输入",
    "graph.addMessage": "添加消息",
    "graph.reason": "模型推理",
    "graph.runTools": "执行工具",
    "graph.complete": "完成",
    "graph.evidence": "运行证据",
    "graph.checkpointSaved": "检查点已保存",
    "why.label": "为什么选择 WEAVEFLOW",
    "why.title": "显式建模，默认可检查",
    "why.description": "智能体工作流不只是产出最终答案：你还需要看清执行路径、限制状态访问，并在失败后还原现场。",
    "why.topologyTitle": "拓扑始终清晰可见",
    "why.topologyDescription": "将节点、边、路由和状态绑定清楚地写入可序列化的图中，不把控制流藏在智能体循环里。",
    "why.stateTitle": "状态访问有明确契约",
    "why.stateDescription": "通过状态端口和显式路径，在执行前发现无效访问与并发写入冲突。",
    "why.evidenceTitle": "每次运行都留下证据",
    "why.evidenceDescription": "事件、步骤、检查点和制品保留每个运行时决策的依据，方便事后复盘。",
    "start.label": "快速开始",
    "start.title": "用清晰的 API 组织结构化工作流",
    "start.description": "安装模块、连接节点并选择运行器；只在确有需要时加入检查点、人工审批和动态路由。",
    "start.contracts": "Go 原生契约与上下文",
    "start.adapter": "兼容 OpenAI 的模型适配器",
    "start.examples": "无需模型即可运行的示例",
    "start.browse": "查看全部示例",
    "copy.copy": "复制",
    "copy.copied": "已复制",
    "copy.select": "选择并复制",
    "copy.copyAria": "复制 Go 示例",
    "copy.copiedAria": "Go 示例已复制",
    "copy.selectAria": "代码已选中，请手动复制",
    "runtime.label": "运行时",
    "runtime.title": "明确执行路径，也保留完整运行证据",
    "runtime.routingTitle": "动态路由",
    "runtime.routingDescription": "根据结构化状态和显式路由决策选择下一个节点。",
    "runtime.parallelTitle": "并行执行",
    "runtime.parallelDescription": "限制并行分支数量，并按明确顺序合并状态补丁。",
    "runtime.approvalTitle": "人工审批",
    "runtime.approvalDescription": "在安全边界暂停运行，经人工确认后继续执行。",
    "runtime.failureTitle": "失败路由",
    "runtime.failureDescription": "将已分类的失败建模为清晰的图路径，而不是隐藏异常。",
    "runtime.artifactTitle": "制品存储",
    "runtime.artifactDescription": "将输出和运行证据关联到产生它们的那次运行。",
    "runtime.inspectionTitle": "实时检查",
    "runtime.inspectionDescription": "在本地 Workbench 中检查运行记录、事件、检查点和状态。",
    "cta.title": "让复杂工作流始终清晰可控",
    "cta.description": "以显式执行路径、可检查状态和受控恢复机制构建智能体系统。",
    "cta.action": "探索 WeaveFlow",
    "footer.tagline": "用 Go 构建的可检查智能体工作流运行时",
    "footer.docs": "文档",
    "footer.examples": "示例",
    "footer.playground": "体验平台",
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
