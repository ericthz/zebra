# zebra 项目介绍

zebra 是一个企业级 AI Agent 参考实现，纯 Go 标准库编写。

## 核心能力
- 多协议 LLM 适配：Ollama / OpenAI / Anthropic，支持流式输出
- 工具系统：内置计算器、天气、搜索、翻译等，支持权限白名单
- 技能体系（Skill）：通过 SKILL.md 让 Agent 按 SOP 执行任务
- 分层记忆：会话工作记忆 + Qdrant 长期向量记忆
- 本地执行：沙箱内的文件读写与 shell 命令
- 服务化：HTTP API、SSE 流式、会话管理、鉴权限流

## 技术栈
Go 标准库、零第三方运行时依赖。三个入口：server（HTTP 服务）、demo（CLI）、mcp（MCP 服务器）。
