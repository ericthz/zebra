# zebra — AI Agent 企业架构参考实现

> 一个把 **企业级 AI Agent 的全部横切能力** 落到代码里的学习项目。
> 纯 Go 标准库（零第三方运行时依赖），覆盖**三轮架构**：
> 第一轮 A~E 企业骨架（服务化/可靠性/安全/可观测）；
> 第二轮 P1~P6 对标成熟 Agent 的能力补全（技能/本地执行/质量闭环/主动出站/成本治理/安全加固）；
> 第三轮 P8~P10 智能体纵深（RAG 知识库 / 并行工具调用 / 规划-执行编排）；
> 第四轮 P12~P14 规模化与体验（异步长任务 / 多 Agent 协作 / 前端 Web UI）。
> **每个能力都有真实可运行的最小实现 + 详细中文注释 + 端到端验证**，刻意保持精简、可逐行读懂。

这不是一个"能直接上线的产品"，而是一张**可对照学习的架构地图**：
每个 `A~E` 模块既实现了最小可运行代码，也标注了**生产演化方向**。

---

## 目录

- [快速开始](#快速开始)
- [架构总览](#架构总览)
- [企业能力地图（A~E → 代码）](#企业能力地图ae--代码)
  - [A. 服务化与访问层](#a-服务化与访问层)
  - [B. 可靠性工程](#b-可靠性工程)
  - [C. Agent 能力补全](#c-agent-能力补全)
  - [D. 安全与合规](#d-安全与合规)
  - [E. 工程化与测试](#e-工程化与测试)
- [API 示例](#api-示例)
- [代码规模](#代码规模)
- [从 Demo 到上线的差距清单](#从-demo-到上线的差距清单)
- [第二轮：对标成熟 Agent 的能力补全（P1~P6）](#第二轮对标成熟-agent-的能力补全p1p6)
- [API 端点一览](#api-端点一览)

---

## 快速开始

### 1. 单机 CLI（最快体验 Agent 本体）

```bash
ollama pull qwen3.5:0.8b-mlx
go run ./cmd/demo
# 输入：北京今天天气怎么样？ → 观察工具调用循环
```

### 2. 企业版 HTTP 服务

```bash
go run ./cmd/server
# 另开终端：
curl -X POST :8080/v1/chat \
  -H "Authorization: Bearer admin-key" \
  -H "Content-Type: application/json" \
  -d '{"message":"北京今天天气怎么样？","confirm_risky":true}'
```

### 3. 一键起全套依赖（Ollama + Qdrant + 服务）

```bash
docker compose up --build
```

### 4. 独立 MCP 服务器

```bash
go run ./cmd/mcp -http :9000
```

---

## 架构总览

```
                    ┌─────────────────────────────────────────────┐
                    │                 cmd/（3 个入口）              │
                    │   server(企业API)   demo(CLI)   mcp(独立服务)  │
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
│ 编排/上下文   │ │ 工具+权限    │ │ 分层记忆      │ │ MCP 协议栈    │
│ 工程/流式    │ │ 校验/审计    │ │ 租户隔离      │ │ 握手/双传输   │
└───────┬─────┘ └────────────┘ └─────────────┘ └──────────────┘
        │
┌───────▼─────────────┐      ┌─────────────────┐      ┌───────────┐
│ provider            │      │ safety          │      │ prompt    │
│ 多协议LLM适配         │      │ 注入防护/审核/脱敏  │      │ 模板版本化  │
│ 路由/流式/熔断/重试    │      │ 审计             │      │           │
└─────────────────────┘      └─────────────────┘      └───────────┘
```

**分层原则**：下层不依赖上层；上层通过接口依赖下层（依赖倒置）。
`server` 是唯一做"装配"的地方（`cmd/server/main.go`），每个组件都可替换。

---

## 企业能力地图（A~E → 代码）

> 每一项都标注：**现实现 / 生产演化方向**。这是本文档的核心价值。

### A. 服务化与访问层

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| A1 | HTTP API Server | `internal/server/server.go` `chat.go` | `/v1/chat`(JSON) + `/v1/chat/stream`(SSE)；生产加 gRPC/网关/版本化路由 |
| A2 | 会话管理 | `internal/server/session.go` | 内存实现：TTL 过期 + 后台清理 + `Touch` 续期；`SessionStore` 接口可换 Redis/DB |
| A3 | 认证鉴权 | `internal/server/auth.go` `middleware.go` | Bearer API Key + admin/user 两级 RBAC；生产接 OAuth2/OIDC/企业 SSO |
| A4 | 多租户隔离 | `session.go` `memory/qdrant.go` `tool/registry.go` | 每会话独立历史；记忆按租户分 collection（`ForTenant`）；工具按角色白名单 |

### B. 可靠性工程

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| B5 | 可观测性 | `middleware.go` `health.go` | slog 结构化日志 + 请求 ID + `/metrics`(Prometheus 文本)；生产加 OTel 链路追踪 |
| B6 | 限流与配额 | `auth.go`(TokenBucket/RateLimiter) | 按用户分桶限流，429 拒绝；配额计量挂在 Metrics；生产做额度充值/超支告警 |
| B7 | 熔断/降级/容错 | `provider/http.go` `router.go` `agent.go` | 超时→指数退避重试→熔断；多模型 fallback；Qdrant 不可用自动降级为工作记忆 |
| B8 | 健康检查/优雅停机 | `health.go` `server.go` | `/healthz` `/readyz`；`signal.NotifyContext` + `srv.Shutdown` 平滑退出 |
| B9 | 错误恢复 | `middleware.go`(panic恢复) `agent.go`(ctx传播) | 请求级 panic 兜底；LLM 调用全程可取消；记忆落库失败不阻塞对话 |

### C. Agent 能力补全

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| C10 | 流式输出 | `provider/*.go` `agent/stream.go` `server/chat.go` | Ollama/OpenAI 真流式；Anthropic 非流式回退；SSE 逐字推送 |
| C11 | 上下文工程 | `agent/context.go` | 启发式 token 估算 + 滑动窗口裁剪 + `Summarizer` 摘要压缩接口 |
| C12 | 记忆系统升级 | `memory/*.go` | 分层：工作记忆(会话内)+ 长期记忆(Qdrant 向量库)；生产加遗忘策略/用户画像/RAG |
| C13 | 结构化输出 | `tool/tool.go` | 工具参数 schema 校验；解析失败自动反馈模型纠错重试 |
| C14 | 多模态 | `provider/provider.go` `openai.go` `anthropic.go` | `Message.ContentParts` 支持 text/image_url；OpenAI/Anthropic 已做转换 |
| C15 | 多模型路由 | `provider/router.go` | 顺序 fallback（主备切换）；生产扩展任务路由/成本路由/健康度权重 |
| C16 | Prompt 管理 | `prompt/prompt.go` | 模板注册表 + 版本化 + `Activate` 灰度切换 |

### D. 安全与合规

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| D17 | Prompt 注入防护 | `safety/safety.go` | 工具结果强制包隔离标记 `【工具数据】` + 注入特征检测 |
| D18 | 内容安全审核 | `safety/safety.go` | `Moderator` 接口：输入/输出双端审核；生产接外部审核模型 |
| D19 | 敏感数据治理 | `safety/safety.go` | 日志/审计强制 `Redact`（APIKey/手机号/邮箱）；`SecretStore` 接口替代 `.env` 明文 |
| D20 | 工具安全边界 | `tool/registry.go` `safety/audit.go` `server/auditor.go` | 角色白名单 + 高危工具二次确认(`confirm_risky`) + 全量调用审计 |

### E. 工程化与测试

| # | 能力 | 代码 | 说明 |
|---|---|---|---|
| E | 单元测试 | `internal/*/*_test.go` | 限流/会话/脱敏/权限/上下文/prompt 全覆盖，`go test ./...` 全绿 |
| E | LLM 评测 | `test/eval/golden_test.go` | 黄金用例回归（`ZEBRA_EVAL=1` 开启），换模型/改 prompt 必跑 |
| E | 容器化 | `Dockerfile` `docker-compose.yml` | 多阶段构建 + distroless 最小镜像 + 一键依赖编排 |
| E | CI/CD | `.github/workflows/ci.yml` `Makefile` | 提交自动 build+vet+test；`make eval` 触发真实模型评测 |

---

## API 示例

```bash
# 非流式对话（user 角色仅开放部分工具，见 cmd/server 白名单）
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

# 运维端点
curl :8080/healthz
curl :8080/readyz
curl :8080/metrics
```

**限流 / 认证体验**：不带 `Authorization` 头 → 401；短时间高频调用 → 429；
`user-key` 调 `web_search` → 工具执行错误（权限被白名单收回）。

---

## 代码规模

```
42 个 .go 文件（含 8 个测试），约 4850 行，纯 Go 标准库
├── cmd/         3 个入口（server / demo / mcp）
├── internal/
│   ├── agent/    核心编排 + 上下文工程 + 流式 + 循环检测
│   ├── provider/ LLM 多协议适配 + 路由 + 超时重试熔断
│   ├── tool/     工具 + 权限 + 校验 + 审计
│   ├── memory/   分层记忆（工作/长期/租户隔离）
│   ├── mcp/      MCP 协议栈（客户端/服务端/握手）
│   ├── server/   HTTP API + 会话 + 鉴权 + 限流 + 可观测
│   ├── safety/   注入防护 + 审核 + 脱敏 + 审计
│   └── prompt/   模板版本化
└── test/eval/    LLM 黄金评测骨架
```

对比：重构前 25 个文件 3274 行，覆盖的却是"单机 CLI Demo"能力。
**相近量级的代码，现在覆盖了 21 项企业能力** —— 这就是"架构设计"的杠杆。
并且：`go build` / `go vet` 零警告，`go test ./...` 全绿。

---

## 从 Demo 到上线的差距清单

本项目的定位是**架构骨架 + 每点最小实现**。真上线还需：

| 类别 | 还需补强 |
|---|---|
| 基础设施 | Redis/PostgreSQL 化会话与记忆、K8s 部署、CDN/网关 |
| 规模化 | 水平扩展（无状态化）、任务队列/长任务异步、Webhook 通知 |
| 商业化 | 精确 token 计费（tiktoken 级）、额度/账单、多语言 |
| 安全 | 外部审核模型接入、密钥 KMS/Vault、prompt 注入的模型级检测 |
| 质量 | LLM 评测集扩充、模糊测试、压测、SLO 告警 |

---

**一句话**：这个仓库的价值不是"代码能用"，而是**把企业 AI Agent 的 21 个架构要点，
各自用最小可读的代码落在一个文件里，并且能编译、能测试、能跑起来** ——
按 `README` 的能力地图逐文件读一遍，就等于上了一堂企业 AI 架构课。

---

## 第二轮：对标成熟 Agent 的能力补全（P1~P6）

> 第一轮是"企业骨架"，第二轮补的是**"Agent 能实际做事 + 商用好"**的能力。
> 每个 P 项都有：实现 + 单元测试 + 真实模型端到端验证 + 独立提交。

| 里程碑 | 能力 | 代码 | 端到端验证 |
|---|---|---|---|
| ✅ P1 | **技能体系 Skill**（程序性知识包，区别于 Tool/Prompt/MCP） | `internal/skill/` `skills/` | Agent 命中技能时自动注入 SOP |
| ✅ P2 | **本地执行**（文件读写 + 命令沙箱，Agentic 分水岭） | `internal/tool/exec*.go` | Agent 真实创建文件 + 执行 ls |
| ✅ P3 | **质量闭环**（LLM-as-Judge + 工具成功率指标） | `internal/eval/` | Judge 自动打分 + /metrics |
| ✅ P4 | **主动出站**（Webhook 通知 + 定时调度） | `internal/notify/` `internal/schedule/` | 对话完成自动推送 task.complete |
| ✅ P5 | **成本治理**（成本归因 + 语义缓存） | `internal/cost/` `internal/cache/` | 同问题 23.8s(LLM) → 21ms(缓存) |
| ✅ P6 | **安全加固**（SSRF + 租户凭据 + 被遗忘权） | `internal/safety/ssrf.go` `internal/tool/fetch.go` `internal/server/forget.go` | DELETE 后旧会话 401 失效 |
| ✅ P8 | **RAG 知识库**（分块/向量索引/检索/注入/引用） | `internal/rag/` `docs/` | 问知识库问题→基于资料准确回答，无幻觉 |
| ✅ P9 | **并行工具调用**（fan-out/fan-in） | `internal/agent/agent.go` | 双工具并发执行，耗时减半、结果有序 |
| ✅ P10 | **规划-执行编排**（Plan-then-Execute） | `internal/agent/plan.go` | mode=plan 自动拆解→逐步执行→汇总 |
| ✅ P12 | **异步长任务+检查点** | `internal/task/` `internal/server/tasks.go` | 提交即返回 id，后台执行可轮询，断点续跑 |
| ✅ P13 | **多 Agent Supervisor** | `internal/supervisor/` `cmd/server/workers.go` | 数据/知识/常规 worker，自动路由 |
| ✅ P14 | **前端 Web UI** | `internal/server/ui.go` | 零构建 SSE 聊天界面，浏览器直接用 |

**新增工具**：`list_dir` / `read_file` / `write_file` / `run_command`（本地执行，P2）、`fetch_url`（SSRF 防护，P6）。

**新增环境变量**：见 `.env.example`（`EXEC_WORKDIR` / `EXEC_READONLY` / `WEBHOOK_URL` / `WEBHOOK_SECRET` 等）。

---

## API 端点一览

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /v1/chat | 非流式对话 |
| POST | /v1/chat/stream | SSE 流式对话 |
| DELETE | /v1/user/data | 被遗忘权：删除当前用户全链路数据（P6） |
| GET | /healthz /readyz | 存活/就绪探针（免鉴权） |
| GET | /metrics | 通用指标（免鉴权） |
| GET | /metrics/cost | 成本归因：按用户/会话 token 用量（P5，免鉴权） |
