---
title: 快速开始
description: 启动 ARTEX、配置模型，完成你的第一个本地练习。
prev: false
next:
  text: 阅读完整入门教程
  link: /使用手册
---

# 快速开始

<p class="docs-lead">启动 ARTEX，连接一个可用模型，完成你的第一个本地练习。</p>

ARTEX 是一个由 AI Agent 协作完成任务的工作台。你负责说明目标和范围，它负责规划、执行并留下记录。这份文档按实际使用顺序编排，从第一次打开页面，到日常操作与高级设置。

::: tip 第一次来？
按下面三步开始即可。MCP、自定义 Agent 和通知推送，可以在熟悉基本操作后再配置。
:::

## 开始使用 ARTEX

<div class="docs-cards">
  <a class="docs-card" href="./使用手册.html#docker">
    <span class="step">STEP 01</span>
    <strong>部署并启动</strong>
    <span>准备配置，启动应用与数据库，打开你的 ARTEX 页面。</span>
  </a>
  <a class="docs-card" href="./使用手册.html#setup">
    <span class="step">STEP 02</span>
    <strong>连接模型</strong>
    <span>设置登录密码，添加模型，完成测试、保存和激活。</span>
  </a>
  <a class="docs-card" href="./使用手册.html#first-task">
    <span class="step">STEP 03</span>
    <strong>完成第一次练习</strong>
    <span>上传离线样例，观察任务执行，找到记录并验收结果。</span>
  </a>
</div>

## 选择启动方式

<div class="docs-cards two">
  <a class="docs-card" href="./使用手册.html#docker">
    <strong>Docker Compose</strong>
    <span>第一次完整使用的推荐方式，统一启动应用与 PostgreSQL。</span>
  </a>
  <a class="docs-card" href="./使用手册.html#mock">
    <strong>只体验界面</strong>
    <span>用模拟数据熟悉操作，不需要数据库或模型密钥。</span>
  </a>
  <a class="docs-card" href="./部署维护与排错.html#binary">
    <strong>下载二进制</strong>
    <span>选择对应系统和架构的发布包，配置数据库后运行。</span>
  </a>
  <a class="docs-card" href="./部署维护与排错.html#source">
    <strong>从源码运行</strong>
    <span>准备开发环境，运行前后端，或编译含网页的单文件程序。</span>
  </a>
</div>

## 继续探索

<div class="docs-cards two">
  <a class="docs-card" href="./日常使用.html">
    <strong>日常使用</strong>
    <span>任务、对话、资产、审批、发现、复测与报告交付。</span>
  </a>
  <a class="docs-card" href="./高级配置.html">
    <strong>高级配置</strong>
    <span>模型切换、预算、并发、代理，以及 Agent、MCP 和 Skill。</span>
  </a>
  <a class="docs-card" href="./部署维护与排错.html#backup">
    <strong>部署与维护</strong>
    <span>HTTPS、升级、回滚、完整备份与恢复。</span>
  </a>
  <a class="docs-card" href="./部署维护与排错.html#troubleshooting">
    <strong>遇到问题？</strong>
    <span>按现象排查启动、登录、模型、网络和文件问题。</span>
  </a>
</div>

## 阅读前须知

文档依据当前仓库编写，旧版本的界面和功能可能不同。先核对「系统配置 → 版本与更新」，再使用相应教程。镜像和二进制是否可下载，以 [Releases](https://github.com/fraternity-z/ARTEX/releases) 实际发布为准。

遵循项目约定，ARTEX 仅供个人学习、代码研究和本地隔离技术验证；不得用于对线上系统发起实际测试。完整说明见 [许可与免责声明](https://github.com/fraternity-z/ARTEX#许可与免责声明)。
