# Zebra AI Agent 项目介绍

Zebra 是 AI Agent 的全功能最小实现，帮助掌握 AI Agent 原理：把对话与推理、
记忆、知识、工具、多 Agent、评测、安全、可观测、规模化等各个技能，用最小可读
的纯 Go 代码实现出来，让你看懂每个技能背后的原理。它不是可直接上线的产品，
而是一张 **"技能 → 原理 → 代码" 的对照图**。

## 核心能力

- 对话与推理：六种模式——`chat` 普通对话、`plan` 规划-执行、`react` ReAct
  推理-行动、`reflect` 反思改进、`debate` 双 Agent 辩论、`supervisor` 多 Agent 路由
- 工具系统：内置计算器、单位换算、天气、搜索、翻译、文件读写、命令执行等工具，
  支持 MCP 协议扩展（stdio / HTTP），权限白名单 + 全量审计
- 技能体系（Skill）：通过 SKILL.md 让 Agent 按 SOP 执行任务，支持热更新
- 分层记忆：会话工作记忆 + Qdrant 向量长期记忆 + 用户画像 + 遗忘机制
- 知识库（RAG）：加载 docs/ 文档，检索 + 重排 + 图谱，支持热更新
- 质量闭环：评测数据集 + Judge 模型 + 红队 + 影子评测 + 反馈回流（赞/踩）
- 结构化输出：JSON Schema 校验 + response_format 强约束
- 安全：注入防护、内容审核、敏感信息脱敏、SSRF 防护、高危操作二次确认、审计
- 可观测：结构化日志（落盘 + stdout）、指标、审计、Web UI 活动轨迹
- 服务化：HTTP API / SSE 流式、会话管理、鉴权限流、异步长任务、配置热更新

## 技术栈

纯 Go 标准库，零第三方运行时依赖。三个入口：

- `cmd/server`：企业版 HTTP 服务 + Web UI 工作台
- `cmd/zebra`：本地 CLI，单机学习入口
- `cmd/mcp`：独立 MCP 服务器（HTTP / stdio）
