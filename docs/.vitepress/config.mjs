import { defineConfig } from 'vitepress';
import { tokenize } from './search.mjs';

const repo = 'https://github.com/fraternity-z/ARTEX';
const base = process.env.DOCS_BASE || '/';

export default defineConfig({
  lang: 'zh-CN',
  title: 'ARTEX 文档',
  description: '从快速启动、第一次任务到模型配置、部署维护的 ARTEX 中文使用手册。',
  base,
  head: [['link', { rel: 'icon', href: `${base}favicon.ico` }]],
  srcExclude: ['node_modules/**'],
  // 教程中的本机应用入口不是文档站页面；其他失效链接仍阻止构建。
  ignoreDeadLinks: [/^http:\/\/localhost:(5173|8787)\/?$/],
  lastUpdated: true,
  themeConfig: {
    siteTitle: 'ARTEX 文档',
    nav: [
      { text: '使用指南', link: '/', activeMatch: '^/(?:$|使用手册|日常使用)' },
      { text: '部署安装', link: '/部署维护与排错', activeMatch: '/部署维护与排错' },
      { text: '高级配置', link: '/高级配置', activeMatch: '/高级配置' },
      { text: '帮助支持', link: '/部署维护与排错#troubleshooting' },
    ],
    sidebar: [
      { text: '开始使用', items: [
        { text: '文档首页', link: '/' },
        { text: '快速开始', link: '/使用手册' },
        { text: '认识 ARTEX', link: '/使用手册#concepts' },
        { text: 'Docker 快速启动', link: '/使用手册#docker' },
        { text: '账号与模型配置', link: '/使用手册#setup' },
        { text: '完成第一个任务', link: '/使用手册#first-task' },
      ] },
      { text: '日常使用', collapsed: false, items: [
        { text: '创建任务与模板', link: '/日常使用#create' },
        { text: '运行状态与暂停', link: '/日常使用#control' },
        { text: '任务详情与对话', link: '/日常使用#detail' },
        { text: '资产管理与同步', link: '/日常使用#assets' },
        { text: '发现、归并与复测', link: '/日常使用#findings' },
        { text: '流量证据与报告', link: '/日常使用#evidence' },
        { text: '计划任务', link: '/日常使用#schedule' },
        { text: '工作空间与归档', link: '/日常使用#workspace' },
      ] },
      { text: '高级配置', collapsed: true, items: [
        { text: '配置文件与环境变量', link: '/高级配置#config' },
        { text: '模型与故障转移', link: '/高级配置#llm' },
        { text: '预算、并发与重试', link: '/高级配置#budgets' },
        { text: '流量、代理与搜索', link: '/高级配置#network' },
        { text: 'Agent 与提示词', link: '/高级配置#agents' },
        { text: 'MCP、Skill 与工具', link: '/高级配置#extensions' },
        { text: '范围、拦截与审批', link: '/高级配置#intercept' },
        { text: '通知推送', link: '/高级配置#notifications' },
        { text: '访问验证', link: '/高级配置#access' },
      ] },
      { text: '部署与维护', collapsed: true, items: [
        { text: '端口与访问方式', link: '/部署维护与排错#ports' },
        { text: '二进制部署', link: '/部署维护与排错#binary' },
        { text: '源码运行与编译', link: '/部署维护与排错#source' },
        { text: 'HTTPS 反向代理', link: '/部署维护与排错#https' },
        { text: '升级与回滚', link: '/部署维护与排错#update' },
        { text: '备份与恢复', link: '/部署维护与排错#backup' },
        { text: '密码维护与排错', link: '/部署维护与排错#password' },
      ] },
      { text: '参考与帮助', collapsed: true, items: [
        { text: '流量证据技术说明', link: '/漏洞流量证据' },
        { text: '故障排查速查表', link: '/部署维护与排错#troubleshooting' },
        { text: '文档站维护与发布', link: '/文档站维护' },
        { text: '提交问题', link: `${repo}/issues` },
      ] },
    ],
    outline: { level: [2, 3], label: '本页目录' },
    socialLinks: [{ icon: 'github', link: repo }],
    editLink: { pattern: `${repo}/edit/main/docs/:path`, text: '在 GitHub 上编辑此页' },
    lastUpdated: { text: '最后更新', formatOptions: { dateStyle: 'medium' } },
    docFooter: { prev: '上一篇', next: '下一篇' },
    sidebarMenuLabel: '文档导航',
    returnToTopLabel: '返回顶部',
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换为浅色',
    darkModeSwitchTitle: '切换为深色',
    skipToContentLabel: '跳转到内容',
    notFound: { title: '找不到这篇文档', quote: '请使用左侧导航或搜索查找内容。', linkLabel: '返回文档首页', linkText: '返回文档首页' },
    search: {
      provider: 'local',
      options: {
        miniSearch: { options: { tokenize }, searchOptions: { prefix: true, fuzzy: 0.2 } },
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            displayDetails: '显示详细内容', resetButtonTitle: '清空搜索',
            backButtonTitle: '返回', noResultsText: '没有找到相关内容',
            footer: { selectText: '选择', selectKeyAriaLabel: '回车', navigateText: '切换', navigateUpKeyAriaLabel: '向上', navigateDownKeyAriaLabel: '向下', closeText: '关闭', closeKeyAriaLabel: 'Esc' },
          },
        },
      },
    },
  },
});
