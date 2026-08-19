import { defineConfig } from "vitepress";

const socialLinks = [{ icon: "github" as const, link: "https://github.com/dengzii/weaveflow" }];

const localSearch = {
  provider: "local" as const,
  options: {
    locales: {
      zh: {
        translations: {
          button: {
            buttonText: "搜索文档",
            buttonAriaLabel: "搜索文档",
          },
          modal: {
            noResultsText: "没有找到相关结果",
            resetButtonTitle: "清除查询条件",
            footer: {
              selectText: "选择",
              navigateText: "切换",
              closeText: "关闭",
            },
          },
        },
      },
    },
  },
};

const englishSidebar = [
  {
    text: "Introduction",
    items: [
      { text: "Overview", link: "/" },
      { text: "Getting Started", link: "/getting-started" },
    ],
  },
  {
    text: "Core Concepts",
    items: [
      { text: "Graph Definition", link: "/concepts/graph-definition" },
      { text: "State Bindings", link: "/concepts/state-bindings" },
      { text: "Runtime Model", link: "/concepts/runtime" },
    ],
  },
  {
    text: "Guides",
    items: [
      { text: "Workbench", link: "/guides/workbench" },
      { text: "Runnable Examples", link: "/guides/examples" },
    ],
  },
  {
    text: "Reference",
    items: [{ text: "Package Map", link: "/reference/packages" }],
  },
];

const chineseSidebar = [
  {
    text: "介绍",
    items: [
      { text: "概览", link: "/zh/" },
      { text: "快速开始", link: "/zh/getting-started" },
    ],
  },
  {
    text: "核心概念",
    items: [
      { text: "图定义", link: "/zh/concepts/graph-definition" },
      { text: "状态绑定", link: "/zh/concepts/state-bindings" },
      { text: "运行时模型", link: "/zh/concepts/runtime" },
    ],
  },
  {
    text: "指南",
    items: [
      { text: "Workbench", link: "/zh/guides/workbench" },
      { text: "可运行示例", link: "/zh/guides/examples" },
    ],
  },
  {
    text: "参考",
    items: [{ text: "包结构", link: "/zh/reference/packages" }],
  },
];

export default defineConfig({
  base: "/docs/",
  srcDir: "./public",
  outDir: "../dist/docs",
  cleanUrls: true,
  lastUpdated: true,
  markdown: {
    theme: {
      light: "github-dark",
      dark: "github-dark",
    },
  },
  title: "WeaveFlow Docs",
  description: "Build, execute, and inspect deterministic agent workflows in Go.",
  locales: {
    root: {
      label: "English",
      lang: "en",
      title: "WeaveFlow Docs",
      description: "Build, execute, and inspect deterministic agent workflows in Go.",
      themeConfig: {
        siteTitle: "WeaveFlow Docs",
        nav: [
          { text: "Guide", link: "/getting-started" },
          { text: "Concepts", link: "/concepts/graph-definition" },
          { text: "Playground", link: "https://playground.weaveflow.space" },
          { text: "Website", link: "https://weaveflow.space" },
        ],
        sidebar: englishSidebar,
        socialLinks,
        search: localSearch,
        editLink: {
          pattern: "https://github.com/dengzii/weaveflow/edit/master/internal/website/docs/public/:path",
          text: "Edit this page on GitHub",
        },
        lastUpdated: { text: "Updated" },
        footer: {
          message: "Released under the MIT License.",
          copyright: "WeaveFlow",
        },
      },
    },
    zh: {
      label: "简体中文",
      lang: "zh-CN",
      link: "/zh/",
      title: "WeaveFlow 文档",
      description: "使用 Go 构建、执行和检查确定性智能体工作流。",
      themeConfig: {
        siteTitle: "WeaveFlow 文档",
        nav: [
          { text: "指南", link: "/zh/getting-started" },
          { text: "核心概念", link: "/zh/concepts/graph-definition" },
          { text: "体验平台", link: "https://playground.weaveflow.space" },
          { text: "项目首页", link: "https://weaveflow.space" },
        ],
        sidebar: chineseSidebar,
        socialLinks,
        search: localSearch,
        editLink: {
          pattern: "https://github.com/dengzii/weaveflow/edit/master/internal/website/docs/public/:path",
          text: "在 GitHub 上编辑此页",
        },
        lastUpdated: { text: "更新时间" },
        footer: {
          message: "基于 MIT 许可证发布。",
          copyright: "WeaveFlow",
        },
        docFooter: {
          prev: "上一页",
          next: "下一页",
        },
        outline: {
          label: "页面导航",
        },
        returnToTopLabel: "返回顶部",
        sidebarMenuLabel: "菜单",
        langMenuLabel: "切换语言",
        darkModeSwitchLabel: "主题",
        lightModeSwitchTitle: "切换到浅色主题",
        darkModeSwitchTitle: "切换到深色主题",
        skipToContentLabel: "跳到主要内容",
      },
    },
  },
  head: [
    ["meta", { name: "theme-color", content: "#303743" }],
    ["meta", { property: "og:type", content: "website" }],
    ["meta", { property: "og:site_name", content: "WeaveFlow Docs" }],
  ],
  themeConfig: {
    socialLinks,
    search: localSearch,
  },
});
