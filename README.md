# zebra — AI Agent 企业架构参考实现

> 版本：v0.7（第七轮交付）· 许可：Apache-2.0 · 语言：纯 Go 标准库（零第三方运行时依赖）

**zebra** 是一套把**企业级 AI Agent 的横向能力**落到可运行代码中的参考实现：服务化、可靠性、安全合规、可观测、智能体编排、运营治理与规模化，全部以纯 Go 标准库实现，每个能力都附带最小可运行代码、详细中文注释与端到端验证。

> 这不是一个可直接上线的产品，而是一张**可对照学习的架构地图**：每个模块既实现了最小可运行代码，也标注了生产演化方向。按目录逐文件阅读，等于上一堂企业 AI 架构课。

---

## 目录

- [1. 项目简介](#1-项目简介)
- [2. 功能与里程碑总览](#2-功能与里程碑总览)
- [3. 架构设计](#3-架构设计)
- [4. 快速开始](#4-快速开始)
- [5. 配置与环境变量](#5-配置与环境变量)
- [6. API 参考](#6-api-参考)
- [7. 企业能力地图](#7-企业能力地图)
- [8. 交付路线图](#8-交付路线图)
- [9. 差距清单与生产化路径](#9-差距清单与生产化路径)
- [10. 工程化与质量保障](#10-工程化与质量保障)
- [11. 开发规范](#11-开发规范)
- [12. 许可证](#12-许可证)

---

## 1. 项目简介

### 1.1 定位

本项目面向两类读者：

- **架构学习者**：把 29 项企业 AI Agent 能力（P0~P28）各自用最小可读的代码落地，`go build` / `go vet` 零警告、`go test ./...` 全绿，可编译、可测试、可运行。
- **工程实践者**：以接口驱动 + 依赖注入的方式组织，每个组件（会话存储、记忆、模型路由、审核器）都可替换，注释中标注了生产演化方向。

### 1.2 现状基线

| 维度 | 现状 |
|---|---|
| 代码规模 | 151 个 `.go` 文件（含 56 个测试），约 1.6 万行 |
| 包数量 | 29 个（`cmd/` 3 个入口 + `internal/` 25 个 + `test/` 评测） |
| 运行时依赖 | 零第三方，纯 Go 标准库 |
| 质量门禁 | `go build` / `go vet` 零警告，`go test ./...` 全绿 |
| 交付轮次 | 七轮（A~E 企业骨架 → P1~P28 能力里程碑） |

### 1.3 七轮交付概览

| 轮次 | 主题 | 覆盖里程碑 |
|---|---|---|
| 第一轮 | 企业骨架（Enterprise Skeleton） | A~E（服务化/可靠性/安全/可观测） |
| 第二轮 | Agent 能力补全（Agent Capabilities） | P1~P6（技能/本地执行/质量闭环/主动出站/成本治理/安全加固） |
| 第三轮 | 智能体纵深（Agent Depth） | P8~P10（RAG/并行工具/规划-执行） |
| 第四轮 | 规模化与体验（Scale & Experience） | P12~P14（异步长任务/多 Agent/Web UI） |
| 第五轮 | 运营与工程纵深（Operations & Engineering） | P16~P18（反馈闭环/结构化输出/配置热更新） |
| 第六轮 | 检索与交付纵深（Retrieval & Delivery） | P20~P23（混合检索/影子评测/记忆画像/文档图表产出） |
| 第七轮 | 交互与规模纵深（Interaction & Scale） | P25~P28（语音交互/影子灰度切换/画像 LLM 抽取/Redis 水平扩展） |

---

## 2. 功能与里程碑总览

按能力域汇总已交付的里程碑：

| 能力域 | 已交付能力（里程碑） |
|---|---|
| 服务化与访问（Service & Access） | HTTP API/SSE、会话管理、API Key+RBAC、多租户隔离（A1~A4） |
| 可靠性工程（Reliability Engineering） | 可观测、限流配额、熔断降级、健康检查/优雅停机、错误恢复（B5~B9） |
| Agent 能力（Agent Capabilities） | 流式输出、上下文工程、分层记忆、结构化输出、多模态、多模型路由、Prompt 管理（C10~C16） |
| 安全与合规（Security & Compliance） | 注入防护、内容审核、敏感数据治理、工具安全边界（D17~D20） |
| 技能与执行（Skills & Execution） | 技能体系（P1）、本地执行沙箱（P2）、文档/图表产出（P23） |
| 质量与评测（Quality & Evaluation） | LLM-as-Judge（P3）、反馈闭环（P16）、影子评测与灰度切换（P21/P26） |
| 知识接入（Knowledge & RAG） | RAG 知识库（P8）、BM25+向量混合检索（P20） |
| 编排与协作（Orchestration & Collaboration） | 并行工具（P9）、规划-执行（P10）、多 Agent Supervisor（P13） |
| 规模化与体验（Scale & Experience） | 异步长任务（P12）、Web UI（P14）、Redis 水平扩展骨架（P28） |
| 运营治理（Operations & Governance） | 成本归因与语义缓存（P5）、安全加固（P6）、结构化输出强约束（P17）、配置热更新（P18）、记忆画像与遗忘（P22/P27） |
| 多模态交互（Multimodal Interaction） | 语音 ASR/TTS 与语音对话链路（P25） |
| 学习与可观测（Observability & Learning） | 启动能力清单 + 执行痕迹（工具/技能调用日志，P31） |
| 入口一致性（Entry Consistency） | zebra CLI 与 server 共享装配：.env / MCP / 长期记忆（P32） |

详细里程碑见 [8. 交付路线图](#8-交付路线图)。

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
│  internal/server —— A. 服务化与访问层                            │
│  HTTP API / SSE / 会话管理 / API Key+RBAC / 限流 / 日志 / 指标    │
│  /healthz /readyz /metrics / 优雅停机                            │
└───────┬──────────────┬───────────────┬───────────────┬─────────┘
        │              │               │               │
┌───────▼─────┐ ┌──────▼─────┐ ┌───────▼─────┐ ┌──────▼───────┐
│ agent       │ │ tool       │ │ memory      │ │ mcp          │
│ 编排/上下文   │ │ 工具+权限    │ │ 分层记忆/画像 │ │ MCP 协议栈    │
│ 规划/流式    │ │ 校验/审计    │ │ 遗忘策略      │ │ 握手/双传输   │
└───────┬─────┘ └────────────┘ └─────────────┘ └──────────────┘
        │
┌───────▼─────────────┐      ┌─────────────────┐      ┌───────────┐
│ provider            │      │ safety          │      │ prompt    │
│ 多协议LLM/语音适配    │      │ 注入防护/审核/脱敏  │      │ 模板版本化  │
│ 路由/流式/熔断/重试    │      │ 审计             │      │           │
└─────────────────────┘      └─────────────────┘      └───────────┘

水平扩展：internal/redis（RESP 客户端）→ 会话存储可插拔
评测纵深：internal/eval（Judge / 影子评测 / 灰度统计）
交付产出：internal/docgen（Word / PDF / SVG 图表）
```

### 3.2 设计原则

- **分层依赖倒置（Layered Dependency Inversion）**：下层不依赖上层，上层通过接口依赖下层；`cmd/server/main.go` 是唯一做装配的地方，每个组件均可替换。
- **接口驱动（Interface-Driven Design）**：`SessionStore`、`Memory`、`Moderator`、`Embedder`、`Extractor` 等均为接口，注释标注生产实现方向。
- **横切集中（Cross-cutting Concerns）**：鉴权、限流、日志、恢复、审计作为中间件/回调统一挂载，业务代码不感知。

### 3.3 目录结构

```
├── cmd/                3 个入口（server / zebra / mcp）
├── internal/
│   ├── agent/          核心编排 + 上下文工程 + 规划-执行 + 画像注入
│   ├── provider/       LLM 多协议适配 + 路由 + 结构化输出 + 语音 ASR/TTS
│   ├── tool/           工具 + 权限 + 校验 + 审计 + 本地执行沙箱 + 文档工具
│   ├── memory/         分层记忆 + 用户画像 + 遗忘策略 + LLM/规则抽取
│   ├── docgen/         Word / PDF / SVG 图表产出（零依赖文件生成）
│   ├── rag/            分块 + BM25 关键词/向量混合检索
│   ├── eval/           LLM-as-Judge + 影子评测 + 灰度统计
│   ├── redis/          纯标准库 RESP 客户端（水平扩展）
│   ├── config/         零依赖 .env 配置加载
│   ├── observe/        启动清单共享渲染（CLI/Server 一致）
│   ├── mcp/            MCP 协议栈（客户端/服务端/握手）
│   ├── server/         HTTP API + 会话 + 鉴权 + 限流 + 可观测 + Web UI
│   ├── safety/         注入防护 + 审核 + 脱敏 + 审计
│   ├── prompt/         模板版本化 + 热更新
│   ├── feedback/       反馈闭环
│   ├── task/           异步长任务 + 检查点
│   ├── supervisor/     多 Agent 路由
│   └── notify/ schedule/ cost/ cache/ schema/   出站/调度/成本/缓存/校验
├── skills/             技能包示例（SKILL.md）
├── prompts/            文件化提示词模板（热更新）
├── docs/               RAG 知识库示例文档
├── test/eval/          LLM 黄金评测骨架
├── workspace/          本地执行沙箱工作目录
└── Dockerfile / docker-compose.yml / Makefile / .github/workflows/ci.yml
```

---

## 4. 快速开始

### 4.1 前置条件

- Go 1.21+（纯标准库，无第三方依赖）
- Ollama（本地 LLM 与嵌入，可选；也可用 OpenAI 兼容网关）

### 4.2 本地 CLI（Agent 命令行客户端）

```bash
ollama pull qwen3.5:0.8b-mlx
go run ./cmd/zebra
# 输入：北京今天天气怎么样？ → 观察工具调用循环
```

> zebra CLI 与 server 使用**同一套装配逻辑**（P32）：自动加载 `.env`，配置了
> `MCP_MODE` 则挂载 MCP 工具，配置了可用的 `QDRANT_URL` 则启用长期记忆；
> 启动清单会如实显示这些状态（未就绪时自动降级，不阻断使用）。MCP/Qdrant 等
> 依赖的探测细节写入诊断日志 `zebra.log`（`ZEBRA_LOG=off` 可退回 stderr），
> 终端保持干净，只显示清单与对话。P36 起 zebra 也加载 `docs/` 知识库（RAG），
> 且与 server 共用同一套清单渲染（`internal/observe`），行结构完全一致。

### 4.3 企业版 HTTP 服务

```bash
go run ./cmd/server
# 另开终端：
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer admin-key" \
  -H "Content-Type: application/json" \
  -d '{"message":"北京今天天气怎么样？","confirm_risky":true}'
```

> server 的 JSON 日志**双写**：stdout 与本地文件 `server.log`（P35，`LOG_FILE` 可改路径，`LOG_FILE=off` 关闭落盘）。

### 4.4 一键起全套依赖（Ollama + Qdrant + 服务）

```bash
docker compose up --build
```

### 4.5 独立 MCP 服务器

```bash
go run ./cmd/mcp -http :9000
```

> 也支持 stdio 模式：`make build` 产出 `bin/zebra-mcp` 后，在 `.env` 配
> `MCP_MODE=stdio` + `MCP_COMMAND=bin/zebra-mcp`，zebra CLI/server 启动时
> 自动拉起并挂载其工具（避免 `go run` 每次编译拖慢启动）。

### 4.6 快速自检

```bash
go build ./... && go vet ./... && go test ./...
curl :8080/healthz   # ok
curl :8080/readyz    # ready
```

---

## 5. 配置与环境变量

全部配置通过环境变量注入（`.env.example` 为模板）。服务启动时**自动加载工作目录下的 `.env`**（P30，零依赖）：真实环境变量优先，`.env` 只填充尚未设置的项，作为本地开发默认值；生产环境仍建议以 KMS/Vault 注入真实密钥。

### 5.1 模型与协议

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OLLAMA_BASE_URL` / `OLLAMA_MODEL` | `http://localhost:11434` / `qwen3.5:0.8b-mlx` | 主模型（Ollama，支持流式） |
| `FALLBACK_BASE_URL` / `FALLBACK_MODEL` | 空 | OpenAI 兼容备选模型（C15 降级） |
| `ANTHROPIC_API_KEY` / `ANTHROPIC_MODEL` | 空 | 可选 Anthropic 备选 |
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` | 空 | 嵌入模型（OpenAI 兼容） |
| `EMBED_MODEL` / `OPENAI_EMBED_MODEL` | `nomic-embed-text:v1.5` / `text-embedding-3-small` | 嵌入模型名 |
| `HTTP_TIMEOUT` | `60` | LLM 请求超时秒数（本地大模型首 token 慢） |

### 5.2 服务与安全

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADDR` | `:8080` | 服务监听地址 |
| `ADMIN_KEY` / `USER_KEY` | `admin-key` / `user-key` | RBAC 两级 API Key（演示默认值） |
| `ZEBRA_LOG` | `zebra.log` | zebra CLI 诊断日志路径（置 `off` 输出到 stderr） |
| `LOG_FILE` | `server.log` | server JSON 日志双写文件路径（置 `off` 仅输出 stdout） |
| `MCP_MODE` / `MCP_COMMAND` / `MCP_HTTP_URL` | 空 | MCP 远端工具（stdio/http）；stdio 建议指向预编译 `bin/zebra-mcp`（先 `make build`） |

### 5.3 本地执行沙箱

| 变量 | 默认值 | 说明 |
|---|---|---|
| `EXEC_WORKDIR` | `workspace` | 沙箱工作目录白名单 |
| `EXEC_READONLY` | `1` | 1=只读模式（禁止写文件/执行命令，更安全） |

### 5.4 主动出站与缓存

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WEBHOOK_URL` / `WEBHOOK_SECRET` | 空 | Webhook 通知（HMAC 签名） |
| `CACHE_MAX_ENTRIES` | `200` | 语义缓存最大条目数 |

### 5.5 记忆画像与影子评测

| 变量 | 默认值 | 说明 |
|---|---|---|
| `QDRANT_URL` | 空 | 长期记忆向量库（不可用自动降级） |
| `PROFILE_TTL_HOURS` | `720` | 画像事实保鲜期（小时，默认 30 天） |
| `PROFILE_LLM` | `1` | 画像抽取方式：1=LLM+规则回退，0=纯规则 |
| `ZEBRA_SHADOW_MODEL` | 空 | 影子评测候选模型（设置即开启） |
| `ZEBRA_SHADOW_OPENAI` | `0` | 1=候选走 OpenAI 兼容后端 |
| `ZEBRA_SHADOW_BASE_URL` | 空 | 候选模型网关地址 |
| `ZEBRA_SHADOW_SAMPLE` | `10` | 影子自动采样率百分比（0=仅显式触发） |

### 5.6 语音交互与水平扩展

| 变量 | 默认值 | 说明 |
|---|---|---|
| `VOICE_BASE_URL` | 空 | OpenAI 兼容语音网关（设置即开启语音 API） |
| `VOICE_API_KEY` | 空 | 语音网关鉴权 |
| `VOICE_ASR_MODEL` / `VOICE_TTS_MODEL` | `whisper-1` / `tts-1` | 转写/合成模型 |
| `VOICE_TONE` | `alloy` | 合成音色 |
| `REDIS_URL` / `REDIS_PASSWORD` / `REDIS_DB` | 空 | Redis 会话存储（设置后多副本共享状态） |

---

## 6. API 参考

### 6.1 端点一览

| 方法 | 路径 | 说明 | 鉴权 |
|---|---|---|---|
| POST | `/v1/chat` | 非流式对话（支持 `mode=plan/supervisor`） | 用户 |
| POST | `/v1/chat/stream` | SSE 流式对话 | 用户 |
| DELETE | `/v1/user/data` | 被遗忘权：删除当前用户全链路数据 | 用户 |
| GET | `/v1/user/profile` | 查看自己的画像事实 | 用户 |
| POST | `/v1/user/profile/forget` | 删除一条画像事实 | 用户 |
| POST | `/v1/tasks` | 提交异步长任务（立即返回 id） | 用户 |
| GET | `/v1/tasks` | 任务列表 | 用户 |
| GET | `/v1/tasks/{id}` | 任务详情/进度/检查点 | 用户 |
| POST | `/v1/feedback` | 提交反馈（赞/踩 + 评论） | 用户 |
| GET | `/v1/feedback` | 我的反馈列表 + 正负计数 | 用户 |
| POST | `/v1/admin/reload` | 热更新技能/提示词/知识库 | admin |
| POST | `/v1/eval/shadow` | 触发一次影子评测（同步返回对比结论） | admin |
| GET | `/v1/eval/shadow` | 影子评测记录 | admin |
| GET | `/v1/eval/shadow/stats` | 影子看板 + 灰度切换建议 | admin |
| POST | `/v1/eval/shadow/promote` | 候选模型提升为主模型 | admin |
| POST | `/v1/voice/chat` | 语音对话全链路（音频→文本→Agent→音频 base64） | 用户 |
| POST | `/v1/voice/transcribe` | 语音转写（multipart 上传） | 用户 |
| POST | `/v1/voice/synthesize` | 文本合成语音（返回音频字节流） | 用户 |
| GET | `/healthz` `/readyz` | 存活/就绪探针 | 免鉴权 |
| GET | `/metrics` | Prometheus 指标 | 免鉴权 |
| GET | `/metrics/cost` | 成本归因（用户×会话×模型） | 免鉴权 |
| GET | `/` | 零构建 Web UI（SSE 聊天） | 免鉴权 |

### 6.2 对话示例

```bash
# 非流式对话（user 角色仅开放部分工具）
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
# → 返回 session_id，下一轮带上：
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"session_id":"<上一步返回>","message":"我叫什么名字？"}'
```

### 6.3 高级能力示例

```bash
# 影子评测（仅 admin）：主模型回答 + 候选模型双评对比
curl -X POST :8080/v1/eval/shadow \
  -H "Authorization: Bearer admin-key" -H "Content-Type: application/json" \
  -d '{"message":"北京天气怎么样？"}'

# 影子看板与灰度切换（仅 admin）
curl :8080/v1/eval/shadow/stats -H "Authorization: Bearer admin-key"
curl -X POST :8080/v1/eval/shadow/promote -H "Authorization: Bearer admin-key"

# 语音对话（multipart file = 音频；返回文本 + 回复音频 base64）
curl -X POST :8080/v1/voice/chat \
  -H "Authorization: Bearer user-key" -F "file=@voice.wav"

# 画像：查看与精细遗忘
curl :8080/v1/user/profile -H "Authorization: Bearer user-key"
curl -X POST :8080/v1/user/profile/forget -H "Authorization: Bearer user-key" \
  -d '{"key":"preference"}'
```

**鉴权与限流体验**：不带 `Authorization` → 401；高频调用 → 429；
`user-key` 调用 `web_search` → 工具执行错误（白名单收回权限）。

---

## 7. 企业能力地图

> 每一项标注：**现实现 / 生产演化方向**。这是本项目文档的核心价值。

### 7.1 服务化与访问层

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| A1 | HTTP API 服务（HTTP API Server） | `internal/server/server.go` `chat.go` | `/v1/chat`(JSON) + `/v1/chat/stream`(SSE)；生产加 gRPC/网关/版本化路由 |
| A2 | 会话管理（Session Management） | `internal/server/session.go` `redis_session.go` | 内存 + Redis 双实现：TTL 过期 + `Touch` 续期；`SessionStore` 接口可插拔 |
| A3 | 认证鉴权（Authentication & RBAC） | `internal/server/auth.go` `middleware.go` | Bearer API Key + admin/user 两级 RBAC；生产接 OAuth2/OIDC/企业 SSO |
| A4 | 多租户隔离（Multi-tenancy Isolation） | `session.go` `memory/qdrant.go` `tool/registry.go` | 每会话独立历史；记忆按租户分 collection；工具按角色白名单 |

### 7.2 可靠性工程

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| B5 | 可观测性（Observability） | `middleware.go` `health.go` | slog 结构化日志 + 请求 ID + `/metrics`(Prometheus 文本)；生产加 OTel 链路追踪 |
| B6 | 限流与配额（Rate Limiting & Quota） | `auth.go`(TokenBucket/RateLimiter) | 按用户分桶限流，429 拒绝；配额计量挂在 Metrics |
| B7 | 熔断/降级/容错（Circuit Breaker / Fallback / Fault Tolerance） | `provider/http.go` `router.go` `agent.go` | 超时→指数退避重试→熔断；多模型 fallback；Qdrant 不可用自动降级 |
| B8 | 健康检查/优雅停机（Health Check / Graceful Shutdown） | `health.go` `server.go` | `/healthz` `/readyz`；`signal.NotifyContext` + `srv.Shutdown` 平滑退出 |
| B9 | 错误恢复（Error Recovery） | `middleware.go`(panic恢复) `agent.go`(ctx传播) | 请求级 panic 兜底；LLM 调用全程可取消；记忆落库失败不阻塞对话 |

### 7.3 Agent 能力补全

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| C10 | 流式输出（Streaming） | `provider/*.go` `agent/stream.go` `server/chat.go` | Ollama/OpenAI 真流式；Anthropic 非流式回退；SSE 逐字推送 |
| C11 | 上下文工程（Context Engineering） | `agent/context.go` | 启发式 token 估算 + 滑动窗口裁剪 + `Summarizer` 摘要压缩接口 |
| C12 | 记忆系统升级（Layered Memory） | `memory/*.go` | 分层：工作记忆 + 长期记忆(Qdrant)；画像 + 遗忘策略（P22） |
| C13 | 结构化输出（Structured Output） | `tool/tool.go` `schema/` | 工具参数 schema 校验 + 通用 JSON Schema 校验器（P17） |
| C14 | 多模态（Multimodal） | `provider/provider.go` `openai.go` `anthropic.go` | `ContentParts` 支持 text/image_url；语音链路（P25） |
| C15 | 多模型路由（Multi-Model Routing） | `provider/router.go` | 顺序 fallback + `Promote` 灰度切换（P26） |
| C16 | Prompt 管理（Prompt Management） | `prompt/prompt.go` | 模板注册表 + 版本化 + 灰度切换 + 文件化热更新（P18） |

### 7.4 安全与合规

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| D17 | Prompt 注入防护（Prompt Injection Defense） | `safety/safety.go` | 工具结果强制包隔离标记 `【工具数据】` + 注入特征检测 |
| D18 | 内容安全审核（Content Moderation） | `safety/safety.go` | `Moderator` 接口：输入/输出双端审核；生产接外部审核模型 |
| D19 | 敏感数据治理（Sensitive Data Governance） | `safety/safety.go` | 日志/审计强制 `Redact`；`SecretStore` 接口替代 `.env` 明文 |
| D20 | 工具安全边界（Tool Safety Boundary） | `tool/registry.go` `safety/audit.go` `server/auditor.go` | 角色白名单 + 高危工具二次确认 + 全量调用审计 |

### 7.5 工程化与测试

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| E | 单元测试 | `internal/*/*_test.go` | 51 个测试文件，覆盖限流/会话/脱敏/权限/上下文/prompt/RAG/影子/画像等 |
| E | LLM 评测 | `test/eval/golden_test.go` | 黄金用例回归（`ZEBRA_EVAL=1` 开启），换模型/改 prompt 必跑 |
| E | 容器化 | `Dockerfile` `docker-compose.yml` | 多阶段构建 + distroless 最小镜像 + 一键依赖编排 |
| E | CI/CD | `.github/workflows/ci.yml` `Makefile` | 提交自动 build+vet+test；`make eval` 触发真实模型评测 |

---

## 8. 交付路线图

> 实施原则：**以功能为单位实现，完成一个提交一个**。
> ✓ = 已完成并提交（含对应收尾 docs 提交）；每个里程碑均含实现 + 单元测试 + 端到端验证。

| 里程碑 | 能力 | 关键落点 | 端到端验收 |
|---|---|---|---|
| ✓ P0 | 基线：差距分析汇总 + 基线提交 | `README.md` | 可编译、可测试、可运行 |
| ✓ P1 | 技能体系 Skill（程序性知识包） | `internal/skill/` `skills/` | Agent 命中技能自动注入 SOP |
| ✓ P2 | 本地执行（Local Execution：文件读写 + 命令沙箱） | `internal/tool/exec*.go` | Agent 真实建文件 + 执行命令 |
| ✓ P3 | LLM 质量闭环（Quality Loop：LLM-as-a-Judge + 工具成功率指标） | `internal/eval/` | Judge 自动打分 + `/metrics` |
| ✓ P4 | 主动出站（Outbound Actions：Webhook 通知 + 定时调度） | `internal/notify/` `schedule/` | 对话完成自动推送 |
| ✓ P5 | 成本治理（Cost Governance：成本归因 + 语义缓存） | `internal/cost/` `cache/` | 同问题 23.8s → 21ms |
| ✓ P6 | 安全加固（Security Hardening：SSRF/租户凭据/被遗忘权） | `safety/ssrf.go` `tool/fetch.go` `server/forget.go` | DELETE 后旧会话 401 |
| ✓ P7 | 第一轮收尾（docs） | README 能力地图 | 全量验证 |
| ✓ P8 | RAG 知识库（RAG Knowledge Base：分块/向量/检索/注入/引用） | `internal/rag/` `docs/` | 基于资料准确回答，无幻觉 |
| ✓ P9 | 并行工具调用（Parallel Tool Calls：fan-out/fan-in） | `internal/agent/agent.go` | 并发执行、结果有序 |
| ✓ P10 | 规划-执行编排（Plan-then-Execute） | `internal/agent/plan.go` | `mode=plan` 拆解→执行→汇总 |
| ✓ P11 | 第二轮收尾（docs） | README 第二轮章节 | 全量验证 |
| ✓ P12 | 异步长任务 + 检查点（Async Tasks & Checkpoints） | `internal/task/` `server/tasks.go` | 提交即返 id，轮询到 done，断点续跑 |
| ✓ P13 | 多 Agent Supervisor | `internal/supervisor/` `cmd/server/workers.go` | 数据/知识/常规 worker 自动路由 |
| ✓ P14 | 前端 Web UI（零构建 SSE 聊天） | `internal/server/ui.go` | GET / 返回 HTML 200 |
| ✓ P15 | 第三轮收尾（docs） | README 更新 | 全量验证 |
| ✓ P16 | 用户反馈闭环（User Feedback Loop：赞/踩→存储+指标+审计+回流） | `internal/feedback/` `server/feedback.go` | counts {positive:1, negative:1} |
| ✓ P17 | 结构化输出强约束（Structured Output：schema 校验 + response_format） | `internal/schema/` `provider/structured.go` | 强约束规划 JSON |
| ✓ P18 | 配置热更新（Hot Reload：技能/提示词/知识库不重启） | `prompt.LoadDir` `server/reload.go` | 重载后 v2 生效 |
| ✓ P19 | 第五轮收尾（docs） | TODO/README 更新 | 全量验证 |
| ✓ P20 | RAG 混合检索（Hybrid Retrieval：BM25 + 向量 z-score 融合） | `internal/rag/bm25.go` `index.go` | 专有名词精确命中 |
| ✓ P21 | 在线评测/影子模式（Shadow Evaluation：shadow traffic 双评） | `internal/eval/shadow.go` `server/shadow.go` | verdict=candidate_better |
| ✓ P22 | 记忆画像/遗忘机制（User Profile & Forgetting：对话学习 + TTL + 容量治理） | `memory/profile.go` `forget.go` | 画像可见可遗忘，TTL 自动隐藏 |
| ✓ P23 | 文档/图表产出（Document & Chart Generation：docx/PDF/SVG） | `internal/docgen/` `tool/docgen.go` | 文件落盘可打开 |
| ✓ P24 | 第六轮收尾（docs） | TODO/README 更新 | 全量验证 |
| ✓ P25 | 语音交互（Voice Interaction：OpenAI 兼容 ASR/TTS） | `provider/voice.go` `server/voice.go` | 音频进→文本→Agent→音频出 |
| ✓ P26 | 影子评测看板 + 灰度切换（Shadow Dashboard & Canary Switch） | `eval/stats.go` `router.Promote` | win-rate 统计 + 一键 promote |
| ✓ P27 | 画像 LLM 抽取升级（LLM-based Profile Extraction：语义抽取 + 规则回退） | `memory/extract.go` | LLM 失败自动回退规则 |
| ✓ P28 | 水平扩展骨架（Horizontal Scaling：RESP 客户端 + Redis 会话存储） | `internal/redis/` `server/redis_session.go` | REDIS_URL 后多副本共享会话 |
| ✓ P29 | 第七轮收尾（docs） | TODO/README 更新 | 全量验证 |
| ✓ P30 | 配置加载（Config Loading：零依赖 .env 加载器） | `internal/config/` `cmd/server/main.go` | 启动自动加载 .env，真实环境变量优先 |
| ✓ P31 | 学习可观测（Observability for Learning：启动能力清单 + 执行痕迹） | `cmd/server/startup.go` `agent.OnTool/OnSkill` `tool.Registry.Names` | 启动打印工具/MCP/技能清单；执行打印工具调用与技能注入 |
| ✓ P32 | 入口装配一致（CLI/Server 共享 .env/MCP/记忆） | `memory.SetupManager` `mcp.RegisterTools` | zebra CLI 与 server 同一套装配逻辑，状态如实显示 |
| ✓ P33 | 终端配色（256 色符号，TTY/NO_COLOR 自动开关） | `internal/console/color.go` | TTY 下符号按类别着色；管道/CI 自动无色、对齐不变 |
| ✓ P34 | zebra 诊断日志落盘（默认 zebra.log，ZEBRA_LOG=off 回退 stderr） | `cmd/zebra/main.go` | 终端无探测告警刷屏；依赖降级原因可查日志 |
| ✓ P35 | server 日志双写落盘（默认 server.log，LOG_FILE=off 仅 stdout） | `cmd/server/main.go` `config.OpenLogFile` | JSON 日志同时输出 stdout 与文件，采集与排查两不误 |
| ✓ P36 | 启动清单统一（CLI/Server 共享渲染 + zebra 支持 RAG 知识库） | `internal/observe/` `internal/rag/docs.go` | zebra 与 server 清单行结构一致；zebra 真实加载 docs/ |
| ✓ P37 | 终端启动 banner（ZEBRA ASCII 标题 + 副标题） | `internal/observe/banner.go` | zebra CLI 与 server 启动首屏打印 banner |
| ✓ P38 | 命令行行编辑（raw 模式 + UTF-8 感知退格） | `internal/console/readline.go` `raw_*.go` | 中文输入删除不再残留字节残片；非 TTY 自动回退 |
| ✓ P39 | MCP 子项展示（与工具一致：树形分支 + 名称: 描述） | `internal/observe/inventory.go` `mcp.RegisterTools` | 启动清单逐项列出已连接的 MCP 工具 |
| ✓ P40 | 反思/自一致性（Reflect 批判改进 + SelfConsistent 采样择优） | `internal/agent/reflect.go` | `mode=reflect` 回答后自动改进一轮 |
| ✓ P41 | RAG 重排（LLM 精排候选片段，失败回退原序） | `internal/rag/rerank.go` | RetrieveReranked 二次精排提升 topK 质量 |
| ✓ P42 | 金丝雀自动回滚（promote 后胜率不达标自动切回原主） | `internal/eval/stats.go` `server/shadow.go` | 回滚含审计与指标，观察期自动清空 |
| ✓ P43 | Redis 任务队列（RedisTaskStore + 共享 redistest） | `internal/task/redis_store.go` `internal/redistest/` | REDIS_URL 时任务存储切 Redis，多副本共享 |

**内置工具**：`calculator` / `get_current_datetime` / `generate_random_number` / `convert_units` / `translate_text` /
`web_search` / `fetch_url`（SSRF 防护） / `list_dir` / `read_file` / `write_file` / `run_command` /
`generate_docx` / `generate_chart`。

---

## 9. 差距清单与生产化路径

### 9.1 待办差距项

以下为差距分析（早期三轮 + 历轮交付后复盘）中**尚未落地**的项目，按实施建议分组：

#### A. 近期候选（纯标准库可落地，可直接继续）

| 优先级 | 项目 | 说明 |
|---|---|---|
| 高 | 语音流式 ASR / 实时语音对话（Streaming ASR / Realtime Voice） | 现为请求-响应式；实时对话需 WebSocket 半双工 |
| 中 | 知识图谱（Knowledge Graph） | RAG 重排（P41）已落地；实体关系检索仍缺 |
| 中 | Redis 长期记忆存储深化（Memory Store） | 会话与任务队列已迁 Redis（P28/P43）；长期记忆仍为 Qdrant |
| 中 | 插件动态加载 / 浏览器自动化（Plugin Loading / Browser Automation） | 工具生态扩展（现为编译期注册） |

> 本轮已闭环：反思/自一致性（P40）、RAG 重排（P41）、金丝雀自动回滚（P42）、
> Redis 任务队列（P43）——详见 [8. 交付路线图](#8-交付路线图)。

#### B. 需决策项（与"零第三方依赖"约束冲突，或需外部工具链）

| 项目 | 现状与建议 |
|---|---|
| gRPC 化 | `net/rpc` 可零依赖落地；真 gRPC 需引入 protobuf 依赖，需拍板是否破例 |
| 前端工程化（React/Vue + WebSocket） | 现为零构建 SSE 单页；工程化需引入 Node 工具链 |
| OIDC/SSO 企业登录 | 现为 API Key + RBAC；接企业 SSO 需 OAuth2/OIDC 客户端 |
| 配置中心 / 特性开关 / 数据库迁移 | 现为环境变量 + 热更新；规模化需配置中心与 DB 迁移 |
| 模型级注入检测（Model-level Injection Detection）/ 外部审核模型（External Moderation Model） | 现为关键词审核；生产接外部审核模型 |
| 密钥 KMS/Vault | 现为环境变量注入；生产接密钥管理服务 |
| 精确 token 计费（tiktoken 级） | 现为启发式估算 + 单价表 |
| 评测平台化 / SLO 告警 | 现为 golden 用例 + 指标；平台化需看板与告警规则 |
| K8s 部署 / CDN 网关 | 现为 Docker/Compose；云原生编排未落地 |

### 9.2 生产化路径建议

| 阶段 | 重点 |
|---|---|
| 阶段一：单机可用（Standalone，已完成） | 企业骨架 + Agent 能力补全，可演示、可学习 |
| 阶段二：质量与运营（Quality & Operations，已完成） | 评测闭环、反馈、结构化输出、热更新、画像、影子评测 |
| 阶段三：水平扩展（Horizontal Scaling，进行中） | Redis 会话共享（P28）→ 任务队列 → 记忆存储 → 无状态多副本 |
| 阶段四：商业化（Commercialization，待启动） | 精确计费、额度账单、OIDC/SSO、SLO 告警、K8s 部署 |

---

## 10. 工程化与质量保障

- **单元测试**：51 个测试文件，`go test ./...` 全绿；每个新增功能强制配套测试。
- **静态检查**：`go vet ./...` 零警告；提交前 `gofmt` 全量格式化。
- **学习可观测（P31）**：启动打印能力清单（模型/工具/MCP/技能/记忆/知识库/语音/影子/Redis）；执行阶段打印 `skill.inject` 与 `tool.call` 痕迹（zebra CLI 终端友好输出，server 结构化日志）。
- **LLM 评测**：`test/eval/golden_test.go` 黄金用例回归（`ZEBRA_EVAL=1` 开启真实模型），换模型/改 prompt 必跑。
- **容器化与 CI**：Docker 多阶段构建 + distroless 最小镜像；GitHub Actions 提交自动 build+vet+test。
- **端到端验证**：每个里程碑都以"真实运行 + 断言"收尾（如影子 verdict、语音音频回传、Redis 会话续期）。

**启动清单符号说明**：zebra CLI 与 server 启动时打印的能力清单使用以下单字符几何符号作为行首标识（每类别唯一，均为 1 格宽、无 emoji 呈现歧义）：

| 符号 | 类别 | 颜色（TTY） |
|---|---|---|
| ── | 标题分隔（Title） | 浅灰 |
| ◆ | 模型（Model） | 天蓝 |
| ▲ | 工具（Tool） | 橙 |
| ■ | 技能（Skill） | 粉/品红 |
| ● | MCP（Model Context Protocol，模型上下文协议） | 绿 |
| ▣ | 记忆（Memory） | 紫 |
| ▤ | 知识库（RAG Knowledge Base） | 青 |
| ♪ | 语音（Voice：ASR / TTS） | 金 |
| ◐ | 影子评测（Shadow Evaluation） | 深灰 |
| ◎ | Redis（会话存储） | 红 |

> 颜色仅在 TTY 且未设置 `NO_COLOR` 时输出（256 色 ANSI，零宽度、不影响对齐）；管道/重定向/CI 自动无色。

执行痕迹与对话循环的符号：

| 符号 | 含义 |
|---|---|
| `>` / `»` | 对话输入提示 / 回复前缀（CLI） |
| `▲ 工具调用` / `■ 技能注入` | 执行阶段痕迹 |
| `✓` / `✗` | 工具调用成功 / 失败 |

示例（server）：

```
── zebra 启动清单
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

每个新增功能必须遵守以下设计基调：

1. **纯 Go 标准库**，零第三方运行时依赖。
2. **接口驱动 + 依赖注入**，可替换实现；生产演化方向用注释标注。
3. **详细中文注释**：先讲"为什么"，再讲"怎么做"。
4. **每个功能 ≤ 300 行**，超出则拆文件。
5. **以功能为单位提交**，commit message 标注功能名。
6. **新增代码必须有单元测试**。
7. **行首标识统一使用符号**（终端：◆▲■●▣▤♪◐◎ 等单字符几何符号，每类别唯一；Web UI 同步使用同一套符号），不使用 emoji。

---

## 12. 许可证

[Apache License 2.0](LICENSE)
