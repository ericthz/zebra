# Zebra — AI Agent 全功能最小实现（学习原理）

> 版本：v0.8 · 里程碑：P0~P57 · 许可：Apache-2.0 · 语言：纯 Go 标准库（零第三方运行时依赖）

**Zebra 的目的只有一个：把 AI Agent 的各个技能——对话与推理、记忆、知识、工具、多 Agent、评测、安全、可观测、规模化——用最小可读的代码实现出来，让你看懂每个技能背后的原理。**

三条承诺：

1. **纯 Go 标准库，零第三方运行时依赖**——不依赖任何框架/SDK，代码即原理。
2. **每个技能 = 最小可运行实现 + 详细中文注释（为什么/怎么做/生产演化方向）+ 单元测试 + 端到端验证**。
3. **以功能为单位提交（P 里程碑）**——从 P0 到 P57，每个技能的演进都可以通过 `git log` 追溯。

> 这不是一个可直接上线的产品，而是一张 **"技能 → 原理 → 代码" 的对照地图**。

---

## 目录

- [1. 项目定位与阅读方式](#1-项目定位与阅读方式)
- [2. AI Agent 能力全景（原理地图）](#2-ai-agent-能力全景原理地图)
- [3. 架构设计](#3-架构设计)
- [4. 快速开始](#4-快速开始)
- [5. 配置与环境变量](#5-配置与环境变量)
- [6. API 参考](#6-api-参考)
- [7. 企业能力地图（A~E 基线）](#7-企业能力地图ae-基线)
- [8. 交付路线图（P0~P57）](#8-交付路线图p0p57)
- [9. 差距清单与生产化路径](#9-差距清单与生产化路径)
- [10. 工程化与质量保障](#10-工程化与质量保障)
- [11. 开发规范](#11-开发规范)
- [12. 许可证](#12-许可证)

---

## 1. 项目定位与阅读方式

### 1.1 定位

面向**想真正理解 AI Agent 原理**的开发者：不是"会用某个 SDK"，而是"知道 Agent 内部发生了什么、为什么这样设计、换一种做法会怎样"。

### 1.2 现状基线

| 维度 | 现状 |
|---|---|
| 代码规模 | 171 个 `.go` 文件（含 67 个测试），约 1.8 万行 |
| 包数量 | 31 个（`cmd/` 3 个入口 + `internal/` 27 个 + `test/` 评测） |
| 运行时依赖 | 零第三方，纯 Go 标准库 |
| 质量门禁 | `go build` / `go vet` 零警告，`go test ./...` 全绿 |
| 覆盖范围 | AI Agent 主流技能点全覆盖（见 [§2 原理地图](#2-ai-agent-能力全景原理地图)） |

### 1.3 阅读方式（推荐顺序）

1. 从 [§2 原理地图](#2-ai-agent-能力全景原理地图) 挑一个想学的技能点；
2. 打开"代码入口"列对应的文件，**先读文件头注释**（每段注释都按"为什么 → 怎么做 → 生产演化方向"组织）；
3. 跑对应包的测试，观察行为：`go test ./internal/<包>/ -v`；
4. 用 CLI / HTTP 端到端体验（见 [§4](#4-快速开始)）；
5. 想系统过一遍：按 [§8 路线图](#8-交付路线图p0p57) 从 P0 逐个 `git show <commit>`，看每个技能"从零到一"的提交。

---

## 2. AI Agent 能力全景（原理地图）

> 这张表是本仓库的核心：**技能点 → 代码入口 → 一句话原理**。
> 每个入口文件的头注释都是该技能的"原理讲义"。

### 2.1 对话与推理

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| 工具调用循环 | `internal/agent/agent.go` | 模型返回工具请求 → 执行 → 结果回填 → 再调，直到输出纯文本 |
| 并行工具调用 | `internal/agent/agent.go` | 同轮互不依赖的工具并发执行，按调用顺序回填不失序 |
| 参数纠错 / 循环检测 | `internal/agent/agent.go` | 参数解析失败反馈重试；连续相同调用判定死循环并中止 |
| 规划-执行 | `internal/agent/plan.go` | 先拆解为步骤（JSON）再逐步执行，子步骤不写历史 |
| ReAct 轨迹 | `internal/agent/react.go` | 每步输出 思考/行动/答案 三元组，工具观察回填后继续 |
| 反思 | `internal/agent/reflect.go` | 生成后让模型批判-改进，失败回退原文 |
| 自一致性 | `internal/agent/reflect.go` | 独立采样多份回答再择优，降低单次随机性 |
| 多 Agent Supervisor | `internal/supervisor/` | LLM 路由 + 关键词兜底，专业 Worker 各司其职 |
| 多 Agent 辩论 | `internal/agent/debate.go` | 双立场独立作答 → 交换观点 → 评审选优 |
| 结构化输出强约束 | `internal/schema/` `internal/provider/structured.go` | 生成前 response_format + 生成后 schema 校验，双保险 |

### 2.2 记忆与上下文

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| 分层记忆 | `internal/memory/` | 工作记忆（会话内）+ 长期记忆（向量/Redis）按层检索 |
| 用户画像 | `internal/memory/profile.go` | 规则/LLM 从对话抽取事实，按置信度合并去重 |
| 遗忘机制 | `internal/memory/forget.go` | TTL 保鲜 + 容量裁剪 + 被遗忘权，记忆"只进不出"是缺陷 |
| 画像冲突消解 | `internal/memory/profile.go` | 同 key 异值记录冲突、可裁决回退，不静默覆盖 |
| 上下文工程 | `internal/agent/context.go` | token 估算 + 滑动窗口裁剪 + 摘要器接口 |
| LLM 摘要压缩 | `internal/agent/summarize.go` | 旧对话语义压缩为 system 摘要，长对话控 token 保语义 |
| 查询改写 | `internal/agent/rewrite.go` | 结构化改写问题（补全指代），提升检索与回答质量 |

### 2.3 知识与 RAG

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| 分块 / 向量索引 | `internal/rag/` | 文档 → 分块 → 嵌入 → 余弦相似度检索 |
| BM25 混合检索 | `internal/rag/bm25.go` | 关键词精确命中与向量语义互补，z-score 归一融合 |
| LLM 重排 | `internal/rag/rerank.go` | 对 topK 候选片段二次打相关分，失败回退原序 |
| 引用溯源 | `internal/rag/index.go` `internal/agent/agent.go` | 命中片段带【来源】标记注入，回答可引用、防幻觉 |
| 知识图谱 | `internal/kg/` | 三元组（实体-关系-实体）规则抽取 + 按实体反查 |

### 2.4 工具生态

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| 内置工具 | `internal/tool/builtin.go` | 计算/搜索/翻译/IP 等，JSON Schema 描述入参 |
| 本地执行沙箱 | `internal/tool/exec*.go` | 目录白名单 + 只读模式 + 超时/输出截断 |
| 命令沙箱 | `internal/tool/exec_shell.go` | 命令黑名单 + 超时强杀 + 输出截断 |
| 文档产出 | `internal/docgen/` | docx（zip+OOXML）/ PDF / SVG 图表，零依赖生成 |
| 网络抓取 | `internal/tool/fetch.go` | SSRF 防护（协议/内网/域名白名单）后抓取文本 |
| MCP 协议栈 | `internal/mcp/` | JSON-RPC 2.0，stdio/HTTP 双传输，握手/工具/调用 |
| 插件动态加载 | `internal/plugin/` | JSON 定义 HTTP 工具，运行时注册、热重载可卸载 |

### 2.5 技能体系

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| Skill 技能包 | `internal/skill/` `skills/` | SKILL.md 元数据 + 程序性指令，检索命中才注入（懒加载） |

### 2.6 评测与质量

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| LLM-as-Judge | `internal/eval/judge.go` | 忠实/相关/安全三维打分，结构化输出 |
| 评测数据集管理 | `internal/eval/dataset.go` | 用例目录化 + 批量跑分 + BaselineDiff 回归对比 |
| 红队评测 | `test/eval/cases/redteam.json` | 注入/越狱用例 + 安全分门槛，防能力退化 |
| 反馈回流 | `internal/eval/dataset.go` `internal/server/feedback.go` | 用户"踩"→ 问答对自动进数据集，纳入回归 |
| 影子模式 | `internal/eval/shadow.go` | 候选模型同题独立回答，Judge 双评对比 |
| 金丝雀切换/自动回滚 | `internal/eval/stats.go` `internal/server/shadow.go` | 胜率达标 promote；质量回退自动切回原主 |

### 2.7 安全与合规

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| Prompt 注入防护 | `internal/safety/safety.go` | 工具结果强制隔离标记 + 注入特征检测 |
| 内容审核 | `internal/safety/safety.go` | 输入/输出双端 Moderator 接口 |
| 敏感数据脱敏 | `internal/safety/safety.go` | 日志/审计强制 Redact |
| SSRF 防护 | `internal/safety/ssrf.go` | 协议 / 内网 / 域名三重白名单 |
| 审计 + 高危二次确认 | `internal/safety/audit.go` `internal/tool/registry.go` | 全量留痕；高危工具人工确认 |
| 被遗忘权 / 租户隔离 | `internal/server/forget.go` `internal/memory/` | 删除用户全链路数据；记忆按租户分 collection/key |

### 2.8 可观测与运营

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| 结构化日志 | `cmd/zebra` `cmd/server` | CLI 落文件 / server stdout+文件双写 |
| 指标 | `internal/server/health.go` | Prometheus 文本格式计数器/直方图 |
| 启动清单 / banner / 执行痕迹 | `internal/observe/` `internal/console/` | 资产盘点、ASCII 标题、工具/技能调用痕迹 |
| 成本归因 | `internal/cost/` | 按 用户×会话×模型 估算 token 成本 |
| 语义缓存 | `internal/cache/` | 相似问题命中直接回答案（23.8s → 21ms） |
| 热更新 | `internal/server/reload.go` | 技能/提示词/知识库/插件不重启重载 |
| 反馈闭环 | `internal/feedback/` | 赞踩 + 指标 + 审计 + 回流评测 |

### 2.9 规模化与体验

| 技能点 | 代码入口 | 一句话原理 |
|---|---|---|
| HTTP / SSE / Web UI | `internal/server/` | JSON、SSE 流式、零构建前端三形态 |
| 会话管理 | `internal/server/session.go` `redis_session.go` | TTL + Touch，内存/Redis 可插拔 |
| 异步任务 | `internal/task/` | 状态机 + 消费者队列 + 检查点，内存/Redis |
| 水平扩展骨架 | `internal/redis/` | 手写 RESP 客户端；会话/任务/记忆均可迁 Redis |
| 多租户 / RBAC / 限流 | `internal/server/auth.go` | Principal + 白名单 + 令牌桶 |
| CLI 行编辑 | `internal/console/readline.go` | raw 模式 + UTF-8 感知退格（中文不再卡） |
| 语音交互 | `internal/provider/voice.go` | OpenAI 兼容 ASR/TTS 全链路 |

---

## 3. 架构设计

### 3.1 架构总览

```
                    ┌─────────────────────────────────────────────┐
                    │                 cmd/（3 个入口）              │
                    │   server(企业API)   zebra(CLI)  mcp(独立服务)  │
                    └───────────────┬─────────────────────────────┘
                                    │
┌───────────────────────────────────▼───────────────────────────┐
│  internal/server —— HTTP API / SSE / 会话 / 鉴权 / 限流 / 日志   │
│  指标 / Web UI / 异步任务 / 反馈 / 热更新 / 影子评测 / 知识图谱     │
└───────┬──────────────┬───────────────┬───────────────┬─────────┘
        │              │               │               │
┌───────▼─────┐ ┌──────▼─────┐ ┌───────▼─────┐ ┌──────▼───────┐
│ agent       │ │ tool       │ │ memory      │ │ mcp          │
│ 编排/规划/   │ │ 工具+沙箱   │ │ 分层记忆/画像 │ │ MCP 协议栈    │
│ ReAct/反思/  │ │ 插件/审计   │ │ 遗忘/冲突    │ │ 握手/双传输   │
│ 辩论/查询改写 │ │ 校验/权限   │ │ Redis/Qdrant │ │              │
└───────┬─────┘ └────────────┘ └─────────────┘ └──────────────┘
        │
┌───────▼─────────────┐      ┌─────────────────┐      ┌───────────┐
│ provider            │      │ safety          │      │ rag / kg  │
│ 多协议LLM/语音/重试   │      │ 注入/审核/脱敏/SSRF│      │ 检索/重排/图谱│
│ 路由/熔断/结构化输出   │      │ 审计            │      │           │
└─────────────────────┘      └─────────────────┘      └───────────┘

评测/质量：internal/eval（Judge / 数据集 / 红队 / 影子 / 金丝雀）
可观测/体验：internal/observe + console（清单 / banner / 行编辑 / 配色）
水平扩展：internal/redis + redistest（RESP 客户端与测试工具）
```

### 3.2 设计原则

- **分层依赖倒置（Layered Dependency Inversion）**：下层不依赖上层，上层通过接口依赖下层；装配只发生在入口，每个组件可替换。
- **接口驱动（Interface-Driven Design）**：`SessionStore`、`Memory`、`Summarizer`、`Moderator`、`Embedder`、`Extractor`、`Reranker` 等均为接口，生产实现方向写在注释里。
- **横切集中（Cross-cutting Concerns）**：鉴权、限流、日志、恢复、审计作为中间件/回调统一挂载，业务代码不感知。

### 3.3 目录结构

```
├── cmd/                3 个入口（server / zebra / mcp）
├── internal/
│   ├── agent/          编排：工具循环/规划/ReAct/反思/辩论/上下文/画像注入
│   ├── provider/       LLM 多协议 + 路由/熔断/重试 + 结构化输出 + 语音
│   ├── tool/           工具 + 权限 + 校验 + 审计 + 本地沙箱 + 文档/插件工具
│   ├── memory/         分层记忆 + 画像 + 遗忘/冲突 + 规则/LLM 抽取 + Redis 记忆
│   ├── docgen/         Word / PDF / SVG 图表产出（零依赖）
│   ├── rag/            分块 + BM25/向量混合检索 + LLM 重排
│   ├── kg/             知识图谱（三元组抽取/查询）
│   ├── eval/           Judge + 数据集 + 红队 + 影子评测 + 金丝雀
│   ├── plugin/         插件动态加载（JSON 定义 HTTP 工具）
│   ├── redis/          纯标准库 RESP 客户端
│   ├── redistest/      假 Redis 测试服务器（多包共用）
│   ├── config/         零依赖 .env 加载 + 日志文件
│   ├── observe/        启动清单共享渲染 + banner
│   ├── console/        终端排版/配色/raw 行编辑
│   ├── mcp/            MCP 协议栈（客户端/服务端/stdio/HTTP）
│   ├── server/         HTTP API + 会话 + 鉴权 + 限流 + Web UI + 运维端点
│   ├── safety/         注入防护 + 审核 + 脱敏 + SSRF + 审计
│   ├── prompt/         模板版本化 + 热更新
│   ├── feedback/       反馈闭环
│   ├── task/           异步长任务 + 检查点（内存/Redis）
│   ├── supervisor/     多 Agent 路由
│   └── notify/ schedule/ cost/ cache/ schema/   出站/调度/成本/缓存/校验
├── skills/             技能包示例（SKILL.md）
├── prompts/            文件化提示词模板（热更新）
├── plugins/            插件示例（JSON）
├── docs/               RAG 知识库示例文档
├── test/eval/          LLM 评测骨架 + 用例集（golden / redteam / feedback）
├── workspace/          本地执行沙箱工作目录
└── Dockerfile / docker-compose.yml / Makefile / .github/workflows/ci.yml
```

---

## 4. 快速开始

### 4.1 前置条件

- Go 1.21+（纯标准库，无第三方依赖）
- Ollama（本地 LLM 与嵌入，可选；也可用 OpenAI 兼容网关）

### 4.2 本地 CLI（最快体验 Agent 本体）

```bash
ollama pull qwen3.5:0.8b-mlx
go run ./cmd/zebra
# 输入：北京今天天气怎么样？ → 观察工具调用、技能注入、RAG 检索的执行痕迹
```

CLI 与 server 使用**同一套装配逻辑**：自动加载 `.env`，配置了 `MCP_MODE` 则挂载
MCP 工具，配置了可用 `QDRANT_URL` 或 `REDIS_URL` 则启用长期记忆，加载 `docs/`
知识库；未就绪自动降级不阻断。诊断日志写入 `zebra.log`（`ZEBRA_LOG=off` 回退
stderr），终端只显示清单与对话。

### 4.3 企业版 HTTP 服务

```bash
go run ./cmd/server
# 另开终端：
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer admin-key" \
  -H "Content-Type: application/json" \
  -d '{"message":"北京今天天气怎么样？","confirm_risky":true}'
```

server 的 JSON 日志**双写** stdout 与 `server.log`（`LOG_FILE` 可改，`LOG_FILE=off` 关闭落盘）。

### 4.4 一键起全套依赖（Ollama + Qdrant + 服务）

```bash
docker compose up --build
```

### 4.5 独立 MCP 服务器

```bash
make build                 # 产出 bin/zebra-mcp
go run ./cmd/mcp -http :9000   # HTTP 模式
# 或 stdio 模式：.env 配 MCP_MODE=stdio + MCP_COMMAND=bin/zebra-mcp
```

### 4.6 快速自检

```bash
go build ./... && go vet ./... && go test ./...
curl :8080/healthz   # ok
curl :8080/readyz    # ready
```

---

## 5. 配置与环境变量

全部配置通过环境变量注入（`.env.example` 为模板）；启动时自动加载工作目录下的 `.env`（真实环境变量优先，`.env` 只填充未设置的项）。

### 5.1 模型与协议

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OLLAMA_BASE_URL` / `OLLAMA_MODEL` | `http://localhost:11434` / `qwen3.5:0.8b-mlx` | 主模型（Ollama，支持流式） |
| `FALLBACK_BASE_URL` / `FALLBACK_MODEL` | 空 | OpenAI 兼容备选模型（降级） |
| `ANTHROPIC_API_KEY` / `ANTHROPIC_MODEL` | 空 | 可选 Anthropic 备选 |
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` | 空 | 嵌入（OpenAI 兼容） |
| `EMBED_MODEL` / `OPENAI_EMBED_MODEL` | `nomic-embed-text:v1.5` / `text-embedding-3-small` | 嵌入模型名 |
| `HTTP_TIMEOUT` | `60` | LLM 请求超时秒数 |
| `ZEBRA_SUMMARIZER` | 空 | `llm` 时启用 LLM 对话摘要压缩 |
| `ZEBRA_QUERY_REWRITE` | 空 | `1` 时启用查询改写（提升检索） |

### 5.2 服务与安全

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADDR` | `:8080` | 服务监听地址 |
| `ADMIN_KEY` / `USER_KEY` | `admin-key` / `user-key` | RBAC 两级 API Key |
| `ZEBRA_LOG` | `zebra.log` | Zebra CLI 诊断日志路径（`off`=stderr） |
| `LOG_FILE` | `server.log` | server 日志双写文件路径（`off`=仅 stdout） |
| `EVAL_CASES_DIR` | `test/eval/cases` | 反馈回流评测数据集目录 |
| `MCP_MODE` / `MCP_COMMAND` / `MCP_HTTP_URL` | 空 | MCP 远端工具（stdio/http；stdio 建议指向预编译 `bin/zebra-mcp`） |

### 5.3 本地执行沙箱

| 变量 | 默认值 | 说明 |
|---|---|---|
| `EXEC_WORKDIR` | `workspace` | 沙箱工作目录白名单 |
| `EXEC_READONLY` | `1` | 1=只读模式（禁止写文件/执行命令） |

### 5.4 主动出站与缓存

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WEBHOOK_URL` / `WEBHOOK_SECRET` | 空 | Webhook 通知（HMAC 签名） |
| `CACHE_MAX_ENTRIES` | `200` | 语义缓存最大条目数 |

### 5.5 记忆画像与影子评测

| 变量 | 默认值 | 说明 |
|---|---|---|
| `QDRANT_URL` / `QDRANT_COLLECTION` / `EMBED_VECTOR_SIZE` | 空 / `zebra_mem` / `768` | 向量长期记忆（不可用自动降级） |
| `PROFILE_TTL_HOURS` | `720` | 画像事实保鲜期（小时，默认 30 天） |
| `PROFILE_LLM` | `1` | 画像抽取：1=LLM+规则回退，0=纯规则 |
| `ZEBRA_SHADOW_MODEL` | 空 | 影子评测候选模型（设置即开启） |
| `ZEBRA_SHADOW_OPENAI` | `0` | 1=候选走 OpenAI 兼容后端 |
| `ZEBRA_SHADOW_BASE_URL` | 空 | 候选模型网关地址 |
| `ZEBRA_SHADOW_SAMPLE` | `10` | 影子自动采样率百分比（0=仅显式触发） |

### 5.6 语音与水平扩展

| 变量 | 默认值 | 说明 |
|---|---|---|
| `VOICE_BASE_URL` / `VOICE_API_KEY` | 空 | OpenAI 兼容语音网关（设置即开启） |
| `VOICE_ASR_MODEL` / `VOICE_TTS_MODEL` | `whisper-1` / `tts-1` | 转写/合成模型 |
| `VOICE_TONE` | `alloy` | 合成音色 |
| `REDIS_URL` / `REDIS_PASSWORD` / `REDIS_DB` | 空 | 会话/异步任务/长期记忆（无 Qdrant 时）水平扩展 |

---

## 6. API 参考

### 6.1 端点一览

| 方法 | 路径 | 说明 | 鉴权 |
|---|---|---|---|
| POST | `/v1/chat` | 非流式对话（`mode`: `plan` / `supervisor` / `reflect` / `react` / `debate`） | 用户 |
| POST | `/v1/chat/stream` | SSE 流式对话 | 用户 |
| DELETE | `/v1/user/data` | 被遗忘权：删除当前用户全链路数据 | 用户 |
| GET | `/v1/user/profile` | 查看画像事实 + 冲突记录 | 用户 |
| POST | `/v1/user/profile/forget` | 删除一条画像事实 | 用户 |
| POST | `/v1/user/profile/resolve` | 裁决画像冲突（`keep: old/new`） | 用户 |
| POST | `/v1/tasks` | 提交异步长任务（立即返回 id） | 用户 |
| GET | `/v1/tasks` | 任务列表 | 用户 |
| GET | `/v1/tasks/{id}` | 任务详情/进度/检查点 | 用户 |
| POST | `/v1/feedback` | 提交反馈（赞/踩 + 评论；踩自动回流评测集） | 用户 |
| GET | `/v1/feedback` | 我的反馈列表 + 正负计数 | 用户 |
| GET | `/v1/knowledge` | 知识图谱查询（`?entity=xxx`） | 用户 |
| POST | `/v1/admin/reload` | 热更新技能/提示词/知识库/插件 | admin |
| POST | `/v1/eval/shadow` | 触发一次影子评测（同步返回对比结论） | admin |
| GET | `/v1/eval/shadow` | 影子评测记录 | admin |
| GET | `/v1/eval/shadow/stats` | 影子看板 + 灰度切换建议 | admin |
| POST | `/v1/eval/shadow/promote` | 候选模型提升为主模型（质量回退自动切回） | admin |
| POST | `/v1/voice/chat` | 语音对话全链路（音频→文本→Agent→音频 base64） | 用户 |
| POST | `/v1/voice/transcribe` | 语音转写（multipart 上传） | 用户 |
| POST | `/v1/voice/synthesize` | 文本合成语音 | 用户 |
| GET | `/healthz` `/readyz` | 存活/就绪探针 | 免鉴权 |
| GET | `/metrics` `/metrics/cost` | Prometheus 指标 / 成本归因 | 免鉴权 |
| GET | `/` | 零构建 Web UI（SSE 聊天） | 免鉴权 |

### 6.2 对话示例

```bash
# 普通对话（user 角色仅开放部分工具）
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer user-key" -H "Content-Type: application/json" \
  -d '{"message":"计算 12*8 等于多少？"}'

# 流式对话（SSE）
curl -N -X POST :8080/v1/chat/stream \
  -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"message":"讲讲北京和上海的天气","stream":true}'

# 多轮：先建会话拿 session_id，再续接
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"message":"我的名字叫小明"}'
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"session_id":"<上一步返回>","message":"我叫什么名字？"}'
```

### 6.3 高级能力示例

```bash
# 五种推理模式（plan / supervisor / reflect / react / debate）
curl -X POST :8080/v1/chat -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"message":"计算 (23+19)*5 并告诉我今天日期","mode":"plan"}'
curl -X POST :8080/v1/chat -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"message":"北京天气怎么样？","mode":"react"}'

# 影子评测看板 / 切换 / 自动回滚（仅 admin）
curl :8080/v1/eval/shadow/stats -H "Authorization: Bearer admin-key"
curl -X POST :8080/v1/eval/shadow/promote -H "Authorization: Bearer admin-key"

# 知识图谱：按实体反查关系
curl ":8080/v1/knowledge?entity=工具调用" -H "Authorization: Bearer user-key"

# 语音对话（multipart file = 音频）
curl -X POST :8080/v1/voice/chat -H "Authorization: Bearer user-key" -F "file=@voice.wav"

# 画像：查看 / 精细遗忘 / 冲突裁决
curl :8080/v1/user/profile -H "Authorization: Bearer user-key"
curl -X POST :8080/v1/user/profile/resolve -H "Authorization: Bearer user-key" \
  -d '{"key":"name","keep":"old"}'
```

**鉴权与限流体验**：不带 `Authorization` → 401；高频调用 → 429；
`user-key` 调用 `web_search` → 工具执行错误（白名单收回权限）。

---

## 7. 企业能力地图（A~E 基线）

> A~E 是"企业骨架"能力基线（第一轮），每一项标注：**现实现 / 生产演化方向**。

### 7.1 服务化与访问层

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| A1 | HTTP API 服务（HTTP API Server） | `internal/server/server.go` `chat.go` | `/v1/chat`(JSON) + `/v1/chat/stream`(SSE)；生产加 gRPC/网关/版本化路由 |
| A2 | 会话管理（Session Management） | `internal/server/session.go` `redis_session.go` | 内存 + Redis 双实现：TTL 过期 + `Touch` 续期 |
| A3 | 认证鉴权（Authentication & RBAC） | `internal/server/auth.go` `middleware.go` | Bearer API Key + admin/user 两级 RBAC；生产接 OIDC/SSO |
| A4 | 多租户隔离（Multi-tenancy Isolation） | `session.go` `memory/` `tool/registry.go` | 会话/记忆/工具按租户隔离 |

### 7.2 可靠性工程

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| B5 | 可观测性（Observability） | `middleware.go` `health.go` | slog 结构化日志 + 请求 ID + `/metrics`；生产加 OTel |
| B6 | 限流与配额（Rate Limiting & Quota） | `auth.go` | 按用户令牌桶限流，429 拒绝 |
| B7 | 熔断/降级/容错（Circuit Breaker / Fallback） | `provider/http.go` `router.go` | 重试→熔断；多模型 fallback；依赖不可用自动降级 |
| B8 | 健康检查/优雅停机 | `health.go` `server.go` | `/healthz` `/readyz`；信号 + 平滑退出 |
| B9 | 错误恢复（Error Recovery） | `middleware.go` `agent.go` | panic 兜底；ctx 可取消；落库失败不阻塞对话 |

### 7.3 Agent 能力补全

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| C10 | 流式输出（Streaming） | `provider/*.go` `agent/stream.go` | Ollama/OpenAI 真流式；Anthropic 回退；SSE 逐字推送 |
| C11 | 上下文工程（Context Engineering） | `agent/context.go` | token 估算 + 滑动窗口 + Summarizer 接口 |
| C12 | 记忆系统升级（Layered Memory） | `memory/*.go` | 工作 + 长期（Qdrant/Redis）；画像 + 遗忘 + 冲突 |
| C13 | 结构化输出（Structured Output） | `tool/tool.go` `schema/` | 工具 schema 校验 + JSON Schema 校验器 |
| C14 | 多模态（Multimodal） | `provider/provider.go` `voice.go` | text/image 内容块；语音 ASR/TTS |
| C15 | 多模型路由（Multi-Model Routing） | `provider/router.go` | 顺序 fallback + Promote/自动回滚 |
| C16 | Prompt 管理（Prompt Management） | `prompt/prompt.go` | 模板版本化 + 灰度切换 + 文件化热更新 |

### 7.4 安全与合规

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| D17 | Prompt 注入防护 | `safety/safety.go` | 工具结果隔离标记 + 注入特征检测 |
| D18 | 内容安全审核 | `safety/safety.go` | `Moderator` 输入/输出双端 |
| D19 | 敏感数据治理 | `safety/safety.go` | 日志/审计强制 `Redact` |
| D20 | 工具安全边界 | `tool/registry.go` `safety/audit.go` | 角色白名单 + 高危确认 + 全量审计 |

### 7.5 工程化与测试

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| E | 单元测试 | `internal/*/*_test.go` | 67 个测试文件，覆盖全部技能点 |
| E | LLM 评测 | `test/eval/` | golden 回归 + 红队评测（`ZEBRA_EVAL=1` 开启） |
| E | 容器化 | `Dockerfile` `docker-compose.yml` | 多阶段构建 + distroless + 一键依赖编排 |
| E | CI/CD | `.github/workflows/ci.yml` `Makefile` | 提交自动 build+vet+test；`make eval` 真实模型评测 |

---

## 8. 交付路线图（P0~P63）

> 实施原则：**以功能为单位实现，完成一个提交一个**；每个里程碑含实现 + 单元测试 + 端到端验证。P 编号即提交历史（`git log --oneline` 可逐项追溯）；部分轮次收尾为 docs 提交（如 P44/P50/P54），未逐一列行。

| 里程碑 | 能力 | 关键落点 | 端到端验收 |
|---|---|---|---|
| ✓ P0 | 基线：差距分析 + 基线提交 | `README.md` | 可编译、可测试、可运行 |
| ✓ P1 | 技能体系 Skill（程序性知识包） | `internal/skill/` `skills/` | 命中技能自动注入 SOP |
| ✓ P2 | 本地执行（文件读写 + 命令沙箱） | `internal/tool/exec*.go` | Agent 真实建文件 + 执行命令 |
| ✓ P3 | LLM 质量闭环（Judge + 工具成功率指标） | `internal/eval/` | Judge 自动打分 + `/metrics` |
| ✓ P4 | 主动出站（Webhook + 定时调度） | `internal/notify/` `schedule/` | 对话完成自动推送 |
| ✓ P5 | 成本治理（成本归因 + 语义缓存） | `internal/cost/` `cache/` | 同问题 23.8s → 21ms |
| ✓ P6 | 安全加固（SSRF/租户凭据/被遗忘权） | `safety/ssrf.go` `server/forget.go` | DELETE 后旧会话 401 |
| ✓ P7 | 第一轮收尾（docs） | README 能力地图 | 全量验证 |
| ✓ P8 | RAG 知识库（分块/向量/检索/注入/引用） | `internal/rag/` `docs/` | 基于资料准确回答，无幻觉 |
| ✓ P9 | 并行工具调用（fan-out/fan-in） | `internal/agent/agent.go` | 并发执行、结果有序 |
| ✓ P10 | 规划-执行编排（Plan-then-Execute） | `internal/agent/plan.go` | `mode=plan` 拆解→执行→汇总 |
| ✓ P11 | 第二轮收尾（docs） | README 更新 | 全量验证 |
| ✓ P12 | 异步长任务 + 检查点 | `internal/task/` | 提交即返 id，轮询到 done，断点续跑 |
| ✓ P13 | 多 Agent Supervisor | `internal/supervisor/` | 数据/知识/常规 worker 自动路由 |
| ✓ P14 | 前端 Web UI（零构建 SSE） | `internal/server/ui.go` | GET / 返回 HTML 200 |
| ✓ P15 | 第三轮收尾（docs） | README 更新 | 全量验证 |
| ✓ P16 | 用户反馈闭环 | `internal/feedback/` | counts {positive:1, negative:1} |
| ✓ P17 | 结构化输出强约束 | `internal/schema/` `provider/structured.go` | 强约束规划 JSON |
| ✓ P18 | 配置热更新 | `prompt.LoadDir` `server/reload.go` | 重载后 v2 生效 |
| ✓ P19 | 第五轮收尾（docs） | TODO/README 更新 | 全量验证 |
| ✓ P20 | RAG 混合检索（BM25 + 向量） | `internal/rag/bm25.go` | 专有名词精确命中 |
| ✓ P21 | 在线评测/影子模式 | `internal/eval/shadow.go` | verdict=candidate_better |
| ✓ P22 | 记忆画像/遗忘机制 | `memory/profile.go` `forget.go` | 画像可见可遗忘 |
| ✓ P23 | 文档/图表产出（docx/PDF/SVG） | `internal/docgen/` | 文件落盘可打开 |
| ✓ P24 | 第六轮收尾（docs） | TODO/README 更新 | 全量验证 |
| ✓ P25 | 语音交互（OpenAI 兼容 ASR/TTS） | `provider/voice.go` | 音频进→文本→Agent→音频出 |
| ✓ P26 | 影子评测看板 + 灰度切换 | `eval/stats.go` `router.Promote` | win-rate 统计 + 一键 promote |
| ✓ P27 | 画像 LLM 抽取升级 | `memory/extract.go` | LLM 失败自动回退规则 |
| ✓ P28 | 水平扩展骨架（RESP + Redis 会话） | `internal/redis/` `server/redis_session.go` | REDIS_URL 多副本共享会话 |
| ✓ P29 | 第七轮收尾（docs） | TODO/README 更新 | 全量验证 |
| ✓ P30 | 配置加载（零依赖 .env） | `internal/config/` | 启动自动加载 .env |
| ✓ P31 | 学习可观测（清单 + 执行痕迹） | `cmd/server/startup.go` `agent.OnTool/OnSkill` | 启动打印能力清单 |
| ✓ P32 | 入口装配一致（CLI/Server 共享） | `memory.SetupManager` `mcp.RegisterTools` | Zebra 与 server 同一套装配 |
| ✓ P33 | 终端配色（256 色，NO_COLOR 开关） | `internal/console/color.go` | TTY 着色、管道无色 |
| ✓ P34 | Zebra 诊断日志落盘 | `cmd/zebra/main.go` | 终端干净，降级原因可查日志 |
| ✓ P35 | server 日志双写落盘 | `cmd/server/main.go` `config.OpenLogFile` | stdout 与文件一致 |
| ✓ P36 | 启动清单统一（CLI/Server 共享渲染 + Zebra RAG） | `internal/observe/` `internal/rag/docs.go` | 两端清单行结构一致 |
| ✓ P37 | 终端启动 banner | `internal/observe/banner.go` | 首屏 ZEBRA ASCII 标题 |
| ✓ P38 | 命令行行编辑（raw + UTF-8 退格） | `internal/console/readline.go` | 中文输入删除不再卡 |
| ✓ P39 | MCP 子项展示 | `internal/observe/inventory.go` | 清单逐项列出 MCP 工具 |
| ✓ P40 | 反思/自一致性 | `internal/agent/reflect.go` | mode=reflect 自动改进 |
| ✓ P41 | RAG 重排（LLM 精排） | `internal/rag/rerank.go` | 二次精排提升 topK 质量 |
| ✓ P42 | 金丝雀自动回滚 | `internal/eval/stats.go` `server/shadow.go` | 胜率不达标自动切回 |
| ✓ P43 | Redis 任务队列 | `internal/task/redis_store.go` `internal/redistest/` | 任务存储切 Redis |
| ✓ P45 | ReAct 轨迹 | `internal/agent/react.go` | mode=react 思考→行动→观察 |
| ✓ P46 | 多 Agent 辩论 | `internal/agent/debate.go` | mode=debate 评审选优 |
| ✓ P47 | LLM 摘要压缩 | `internal/agent/summarize.go` | ZEBRA_SUMMARIZER=llm 启用 |
| ✓ P48 | 查询改写 | `internal/agent/rewrite.go` | ZEBRA_QUERY_REWRITE=1 启用 |
| ✓ P49 | 评测数据集管理 | `internal/eval/dataset.go` | LoadCases/RunCases/BaselineDiff |
| ✓ P51 | Redis 长期记忆 | `internal/memory/redis_mem.go` | 无 Qdrant 时 REDIS_URL 启用 |
| ✓ P52 | 知识图谱 | `internal/kg/` `server/knowledge.go` | GET /v1/knowledge |
| ✓ P53 | 插件动态加载 | `internal/plugin/` | JSON 即工具，reload 可卸载 |
| ✓ P55 | 反馈回流评测集 | `internal/eval/dataset.go` `server/feedback.go` | 踩→问答对进数据集 |
| ✓ P56 | 红队/对抗性评测 | `test/eval/cases/redteam.json` | 注入/越狱用例安全分门槛 |
| ✓ P57 | 画像冲突消解/合并 | `internal/memory/profile.go` `server/profile.go` | 冲突可查可裁决、自动合并 |
| ✓ P60 | 流式阶段轨迹（phase 事件：plan/ReAct 思考与步骤） | `internal/agent/stream.go` `plan.go` `react.go` | 流式端点按 mode 分发五种模式 |
| ✓ P61 | Web UI 多会话/导出/阶段展示/移动端抽屉 | `internal/server/ui.go` | 会话侧栏、TXT/JSON 导出、轨迹阶段行 |
| ✓ P62 | 结构化输出容错（围栏/多对象/单键包裹/顶层数组 + plan 字段归一化） | `internal/provider/structured.go` `internal/agent/plan.go` | 小模型不规范 JSON 也能通过强约束校验 |
| ✓ P63 | Web UI 活动轨迹（phase/skill/tool 统一活动块 + 工具去重计数 + 技能流式事件） | `internal/server/ui.go` `internal/agent/stream.go` | 一次回答内阶段/技能/工具优雅归集，工具按调用次数合并 |

---

## 9. 差距清单与生产化路径

### 9.1 待办差距项

#### A. 近期候选（纯标准库可落地，可直接继续）

| 优先级 | 项目 | 说明 |
|---|---|---|
| 高 | 语音流式 ASR / 实时语音对话（Streaming ASR / Realtime Voice） | 现为请求-响应式；实时对话需 WebSocket 半双工 |
| 中 | 浏览器自动化（Browser Automation） | 插件动态加载（P53）已落地；浏览器操作仍缺 |

#### B. 需决策项（与"零第三方依赖"约束冲突，或需外部工具链）

| 项目 | 现状与建议 |
|---|---|
| gRPC 化 | `net/rpc` 可零依赖落地；真 gRPC 需引入 protobuf 依赖，需拍板是否破例 |
| 前端工程化（React/Vue + WebSocket） | 现为零构建 SSE 单页；工程化需引入 Node 工具链 |
| OIDC/SSO 企业登录 | 现为 API Key + RBAC；接企业 SSO 需 OAuth2/OIDC 客户端 |
| 配置中心 / 特性开关 / 数据库迁移 | 现为环境变量 + 热更新 |
| 模型级注入检测 / 外部审核模型 | 现为关键词审核 |
| 密钥 KMS/Vault | 现为环境变量注入 |
| 精确 token 计费（tiktoken 级） | 现为启发式估算 + 单价表 |
| 评测平台化 / SLO 告警 | 现为 golden + 红队用例；平台化需看板与告警规则 |
| K8s 部署 / CDN 网关 | 现为 Docker/Compose |

### 9.2 生产化路径建议

| 阶段 | 重点 |
|---|---|
| 阶段一：单机可用（已完成） | 企业骨架 + Agent 能力补全，可演示、可学习 |
| 阶段二：质量与运营（已完成） | 评测闭环、反馈回流、影子/金丝雀、画像、热更新 |
| 阶段三：水平扩展（进行中） | Redis 会话/任务/记忆 → 无状态多副本 |
| 阶段四：商业化（待启动） | 精确计费、OIDC/SSO、SLO 告警、K8s 部署 |

---

## 10. 工程化与质量保障

- **单元测试**：67 个测试文件，`go test ./...` 全绿；每个新增功能强制配套测试。
- **静态检查**：`go vet ./...` 零警告；提交前 `gofmt` 全量格式化。
- **LLM 评测**：`test/eval/` 含 golden 回归与红队评测（`ZEBRA_EVAL=1` 开启真实模型）。
- **容器化与 CI**：Docker 多阶段构建 + distroless；GitHub Actions 提交自动 build+vet+test。
- **端到端验证**：每个里程碑以"真实运行 + 断言"收尾（如影子 verdict、语音音频回传、Redis 续期、冲突裁决回退）。

**启动清单符号说明**（Zebra CLI 与 server 启动时打印的能力清单，TTY 下按类别着色）：

| 符号 | 类别 | 颜色（TTY） |
|---|---|---|
| ── | 标题分隔（Title） | 浅灰 |
| ◆ | 模型（Model） | 天蓝 |
| ▲ | 工具（Tool） | 橙 |
| ■ | 技能（Skill） | 粉/品红 |
| ● | MCP（Model Context Protocol） | 绿 |
| ▣ | 记忆（Memory） | 紫 |
| ▤ | 知识库（RAG Knowledge Base） | 青 |
| ♪ | 语音（Voice：ASR / TTS） | 金 |
| ◐ | 影子评测（Shadow Evaluation） | 深灰 |
| ◎ | Redis（会话存储） | 红 |

> 颜色仅在 TTY 且未设置 `NO_COLOR` 时输出（256 色 ANSI，零宽度、不影响对齐）；管道/重定向/CI 自动无色。

示例（server）：

```
── Zebra 启动清单
├── ◆ 模型      : ollama
├── ▲ 工具      : 15 个
│   ├─ calculator   : 计算数学表达式
│   ├─ convert_units: 单位换算
│   └─ ...
├── ● MCP       : 未启用（MCP_MODE 未设置）
├── ■ 技能      : 2 个
│   ├─ data-check  : 当用户要求核对数据...时使用
│   └─ report-sop  : 当用户要求撰写研究报告...时使用
├── ▣ 记忆      : 工作记忆
├── ▤ 知识库    : 0 篇文档 / 0 块
├── ♪ 语音      : 已启用（ASR/TTS）
├── ◐ 影子评测  : 未启用（ZEBRA_SHADOW_MODEL 未设置）
└── ◎ Redis     : 未启用（内存会话，单机）
```

---

## 11. 开发规范

每个新增功能必须遵守：

1. **纯 Go 标准库**，零第三方运行时依赖。
2. **接口驱动 + 依赖注入**，可替换实现；生产演化方向用注释标注。
3. **详细中文注释**：先讲"为什么"，再讲"怎么做"。
4. **每个功能 ≤ 300 行**，超出则拆文件。
5. **以功能为单位提交**，commit message 标注功能名。
6. **新增代码必须有单元测试**。
7. **行首标识统一使用符号**（终端：◆▲■●▣▤♪◐◎ 等单字符几何符号，每类别唯一；Web UI 同步），不使用 emoji。

---

## 12. 许可证

[Apache License 2.0](LICENSE)
