# Zebra 技术总结：一个零依赖的多模型 Agent 框架

> 本文档自上而下按**代码执行流程**逐环节拆解 Zebra 的工程实现。
> 每个环节给出：流程技术关键点、当前实现原理及优缺点、企业常用的替代方案（附参考文档）、
> 交流复盘时常被关注的问题及解决方案、对应的代码路径。
>
> 代码路径为 `docs/` 视角的相对链接，点击可跳转。规模：192 个 `.go` 文件（含 87 个测试）、
> 约 2.6 万行、31 个包、3 个入口（CLI / HTTP / MCP），零第三方运行时依赖（纯 Go 标准库）。

## 全链路总览

```
用户请求
  │
  ├─ CLI:  cmd/zebra/main.go ──────────┐
  ├─ HTTP: cmd/server/main.go → internal/server ─┤
  ├─ MCP:  cmd/mcp/main.go ────────────┘
  │
  ▼
internal/server: 鉴权(A3) → 限流(B6) → 会话(A2) → 影子评测(D26)
  │
  ▼
internal/agent: Run/RunStream → buildMessages(上下文 C11) → toolLoop
  │   ├─ plan.go 规划-执行(P10)   ├─ react.go ReAct(P45)
  │   ├─ reflect.go 反思+自一致性 ├─ debate.go 双角色辩论
  │   └─ supervisor 多 Agent 路由(P13)
  │
  ▼
internal/provider: Router 多模型 fallback → Ollama/OpenAI/Anthropic 适配
  │                    ├─ structured.go 结构化输出(P17)
  │                    └─ usage.go 用量上报(F-3)
  │
  ▼
internal/tool: Registry 权限+审计(D20) → Execute → 注入防护(D17)
  │
  ▼
internal/memory: 工作记忆+长期记忆(Qdrant/Redis) + 画像(P22) + 遗忘(P6)
  │
  ▼
internal/rag / internal/kg / internal/skill：知识库 / 知识图谱 / 技能检索
  │
  ▼
internal/safety：内容审核(D18) + SSRF + 审计；eval：评测闭环(D26)
```

---

## 1. 入口装配与依赖注入

### 流程技术关键点

- 三个入口共用同一套 `internal/*` 领域包，避免业务逻辑在入口层重复。
- CLI（`cmd/zebra/main.go`）把"配置 → Router → 工具注册表 → 记忆 → RAG → Agent"依次组装，
  再按 `mode` 分派到六种推理模式；HTTP（`cmd/server/main.go`）组装 `server.Deps` 后注入 `NewAPIServer`。
- 依赖通过 `Config`/`Deps` 结构体显式注入，工具审计器、注入告警等横切关注点用接口解耦，避免依赖倒置。

### 当前实现原理及优缺点

- 原理：`config.LoadDefault` 读取环境变量（`OLLAMA_*`/`FALLBACK_*`/`ANTHROPIC_*`/`QDRANT_*`/`ADMIN_KEY`/`USER_KEY`），
  无配置文件，启动即用。CLI 还提供交互式 `-i` 会话与 `/mode` 切换；HTTP 提供健康探针、优雅停机。
- 优点：零依赖、启动成本极低、环境变量驱动适合容器化；CLI/HTTP/MCP 三端复用同一领域层。
- 缺点：缺少版本化配置与多环境（dev/staging/prod）分离；API Key 走环境变量，多租户密钥
  管理仍偏简单（`safety.SecretStore` 提供了接口但默认实现简单）。

### 企业常用替代方案

- 配置管理：Viper + env 前缀 + 配置校验；或 K8s ConfigMap/Secrets + 外部化配置中心（Apollo/Nacos/Consul）。
- 依赖注入：Uber dig / Google wire 做编译期依赖图；或 DDD 分层 + 显式工厂。
- 参考：https://pkg.go.dev/github.com/spf13/viper ，https://github.com/google/wire

### 交流复盘常被关注的问题

- "三个入口如何保证领域层不重复？" → 领域包无入口依赖，入口只做装配。
- "零第三方依赖为什么可行？" → 标准库已覆盖 http/json/slog/crypto；代价是缺少 OpenTelemetry、tiktoken 等，属于刻意取舍。
- "启动参数如何从配置中心下发？" → 当前是 env 直读，可平滑替换为 Viper/ConfigMap 而不改领域层。

### 代码路径

- CLI：`../cmd/zebra/main.go#L39`（main 装配）、`#L437`（runAgent 模式分派）
- HTTP：`../cmd/server/main.go#L59`、`#L463`（NewAPIServer）
- MCP：`../cmd/mcp/main.go#L18`
- 配置：`../internal/config/dotenv.go`

---

## 2. HTTP 服务：路由、中间件与优雅停机

### 流程技术关键点

- `internal/server/server.go` 注册全部路由：对话（/v1/chat、/v1/chat/stream）、会话、用户数据、
  画像、任务、反馈、知识图谱、管理端（热重载/影子评测）、语音、健康检查、指标、Web UI。
- 中间件链：`RequestID → AccessLog → Recover → Auth → RateLimit`，横切所有请求。
- 优雅停机：监听信号后 `server.Shutdown`，等待在飞请求完成再退出。

### 当前实现原理及优缺点

- 原理：`net/http` 标准库 `http.ServeMux` + Go 1.22 方法路由；`slog` 结构化日志；限流用令牌桶（`auth.go`）。
- 优点：无框架心智负担，性能够用，panic 有兜底不拖垮进程。
- 缺点：缺少 gRPC/网关层、路由版本化（/v1 手动拼）、限流为单机内存实现（多实例需分布式）。

### 企业常用替代方案

- Web 框架：Gin/Echo/Fiber（中间件生态丰富）；或 go-zero/GoFrame 企业全家桶。
- 网关：Kong/APISIX/Envoy 做限流、鉴权、版本化；gRPC + protobuf 定义接口。
- 参考：https://gin-gonic.com/ ，https://grpc.io/

### 交流复盘常被关注的问题

- "中间件顺序为什么是 Auth 在 RateLimit 之前？" → 鉴权先于限流可避免未认证请求占用配额；
  也支持对已认证用户按身份限流（`principal` 已注入 context）。
- "请求 ID 如何贯穿日志？" → `RequestID` 中间件生成并注入 context + ResponseHeader，日志按 ID 聚合。
- "优雅停机如何保证会话不丢？" → 内存会话 TTL 过期兜底；生产接 Redis 后由存储层保证。

### 代码路径

- 路由注册：`../internal/server/server.go`
- 中间件链：`../internal/server/middleware.go#L48`（RequestID）、`#L62`（AccessLog）、`#L85`（Recover）、`#L102`（Auth）、`#L128`（RateLimit）
- 优雅停机：`../cmd/server/main.go#L463`

---

## 3. 鉴权、会话与多租户隔离

### 流程技术关键点

- 鉴权：`KeyStore` 维护 admin/user 两级 Bearer API Key，`Authenticate` 产出 `Principal{User,Role,Tenant}`，
  经 `Auth` 中间件注入 context；`/healthz`、`/readyz`、`/metrics`、`/` 为免鉴权白名单。
- 会话：`SessionStore` 接口（内存 + Redis 双实现），TTL 过期 + 每请求 `Touch` 续期；同会话 `runMu` 串行化
  对话执行，防止多轮历史就地追加的数据竞争。
- 多租户：会话/记忆/语义缓存/工具权限全部带 tenant/user 命名空间。

### 当前实现原理及优缺点

- 原理：内存实现用 `map[SessionID]*Session` + `sync.RWMutex`，过期清理 tick 先快照再逐个取 `runMu` 后删，
  严格保证"删除晚于在飞对话写回"（否则整轮对话丢失）；Redis 实现复用同一接口并加实例级互斥。
- 优点：接口抽象好，单机→分布式只换实现；并发语义（runMu→map 锁序）被仔细处理，已修多轮上下文撕裂、删除复活等 bug。
- 缺点：API Key 固定两把（admin/user），无细粒度 ACL；Redis 版历史写回靠 `HistoryPersister` 显式调用，
  分布式一致性（多轮原子性）仍需业务侧保证。

### 企业常用替代方案

- 鉴权：OIDC/OAuth2（Keycloak/Auth0）、JWT + 网关统一校验、mTLS；企业 SSO。
- 会话：Redis Cluster / 数据库表 + 分布式锁；会话状态外置，节点无状态化。
- 参考：https://datatracker.ietf.org/doc/html/rfc6749 ，https://openid.net/specs/openid-connect-core-1_0.html

### 交流复盘常被关注的问题

- "runMu 为什么必须是会话内串行？" → Agent 历史由指针就地追加，并行写会撕裂上下文；对话本身顺序语义，串行正确且廉价。
- "过期清理如何避免误删在飞会话？" → 先 RLock 快照、再逐个 Lock 该会话的 runMu，等写回完成才删。
- "Redis 下多轮历史为什么可能丢？" → Get 重建 Session 后历史在请求内修改，请求结束必须 `Save` 写回，否则下轮读旧快照。

### 代码路径

- 鉴权：`../internal/server/auth.go#L28`（KeyStore）、`#L40`（Authenticate）
- 会话接口与内存实现：`../internal/server/session.go#L43`、`#L60`
- Redis 会话：`../internal/server/redis_session.go`
- 中间件：`../internal/server/middleware.go#L102`、`#L145`（principal）

---

## 4. 对话入口：/v1/chat 与 SSE 流式

### 流程技术关键点

- `handleChat` 解析 `ChatRequest`（含 `mode`、`session_id`、`images` 多模态）、拿会话、按模式路由到
  Agent 对应方法；`handleChatStream` 走 SSE，按 `Event` 类型（phase/delta/tool_call/done）逐事件推送。
- 会话即建即用：无 `session_id` 时自动创建并返回，前端拿 ID 续接多轮。
- 错误对外脱敏：`redactErr` 只暴露用户可见信息，内部 error 打日志。

### 当前实现原理及优缺点

- 原理：非流式 `Agent.Run` 返回最终文本；流式 `Agent.RunStream` 返回 `<-chan Event`，handler 逐事件
  flush；流中/建立即失败都会降级非流式并以 delta 推送（已修"只见 done 不见回答"的 bug）。
- 优点：流式体验好、降级路径完整、多模态字段与六种模式统一入口。
- 缺点：SSE 为轻量实现，缺重连/断点续传/压缩；错误类型未体系化（HTTP 状态码映射朴素）。

### 企业常用替代方案

- 流式：SSE（保留）、WebSocket、gRPC Streaming；客户端用 EventSource/ohmyfetch 自带重连。
- 接口协议：OpenAI-compatible 协议（已部分兼容），便于接入 LangChain/OneAPI 生态。
- 参考：https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events

### 交流复盘常被关注的问题

- "流式中途断网怎么兜底？" → collectStream 收已发 delta，降级非流式后只推"新增部分"，避免重复播报。
- "多模态 images 走哪条链路？" → `RunOptions.Images` → `userMessage` 转 `ContentParts`（text+image_url），
  Ollama/OpenAI 直传；Anthropic 走适配。
- "为什么 done 之前客户端可能拿不到回答？" → 早期版本在"流建立即失败"时降级结果没 emit，已修复为必推 delta。

### 代码路径

- `../internal/server/chat.go#L39`（handleChat）、`#L183`（handleChatStream）、`#L240`（streamForMode）
- Agent 流式：`../internal/agent/stream.go#L35`（RunStream）
- 多模态：`../internal/agent/agent.go#L563`（userMessage）

---

## 5. Agent 核心循环：Run → buildMessages → toolLoop

### 流程技术关键点

- `Run` 的固定执行序（安全横切点优先）：输入审核 → 注入检测 → 语义缓存命中 → buildMessages（上下文预算） →
  toolLoop（工具循环）→ 输出审核 → 写历史/记忆/画像 → 写缓存。
- `buildMessages` 把"长期记忆 Recall + 工作记忆 + 历史 + 系统提示 + 当前输入（含 images）"拼成消息，
  再交给 `ContextWindow.Trim` 做预算裁剪。
- `toolLoop`：反复"调 LLM → 有 ToolCalls 则并行执行 → 结果回填"，直到纯文本或达 `MaxTurns`；
  内置死循环检测（同一调用组合累计 ≥3 次中止）。

### 当前实现原理及优缺点

- 原理：`ContextWindow.Trim` 用启发式 token 估算（中文 1 字/词、英文 1.3 词/token），超预算先裁最旧消息
  （保留最后一条用户问题不删），再交给 Summarizer 压缩最旧一半；`toolLoop` 支持同轮多工具并行
  fan-out/fan-in 并按序回填。
- 优点：安全点全在持久化之前，缓存命中也不绕过审核；工具并行显著降延迟；上下文裁剪有明确不变量。
- 缺点：token 估算非精确（生产用 tiktoken）；Summarizer 默认为截断实现（可插拔 LLM）；历史就地追加使
  Agent 与存储强耦合（有历史写回复杂度）。

### 企业常用替代方案

- 上下文工程：tiktoken/transformers tokenizer 精确计数；LongChat/LangChain memory 模块化；
  Agent 框架（LangGraph 有 State 显式管理）。
- 参考：https://github.com/openai/tiktoken ，https://python.langchain.com/docs/modules/memory/ ，https://langchain-ai.github.io/langgraph/

### 交流复盘常被关注的问题

- "缓存命中为什么还要走输出审核？" → 防止未审核输入通过缓存绕过内容审核横切点（历史 bug，已修）。
- "上下文超预算时最后一条用户问题被删了会怎样？" → 模型只收到指令没有问题的经典 bug，已通过不变量修复。
- "toolLoop 与 ReAct 的区别？" → toolLoop 是隐式工具循环（标准 function-calling），ReAct 是显式
  thought/action/answer 轨迹，两者复用同一 registry（权限/审计/高危确认）。

### 代码路径

- 主循环：`../internal/agent/agent.go#L164`（Run）、`#L169`（run）、`#L299`（toolLoop）
- 消息组装：`../internal/agent/agent.go#L444`（buildMessages）
- 上下文裁剪：`../internal/agent/context.go#L91`（Trim）、`#L19`（EstimateTokens）

---

## 6. 上下文工程：预算裁剪与摘要压缩

### 流程技术关键点

- 估算 → 裁剪 → 摘要三级策略：预算内全保留；超预算从最旧非 system 消息逐条删；仍超则把最旧一半
  压成一条 `system` 摘要并**复查预算**（防摘要后仍超的死循环）。
- 不变量：最后一条（当前用户问题）永不被删；至少保留 2 条消息给摘要器留压缩空间。

### 当前实现原理及优缺点

- 原理：`EstimateTokens` 启发式（CJK 1 字 1 token、英文 1.3 token/词 + 固定开销）；`PrefixSummarizer`
  默认把旧消息拼接到 `MaxChars` 截断；`Summarizer` 接口可换成 LLM 摘要。
- 优点：无外部依赖可用；逻辑有明确不变量、可测试；ctx 向下透传取消。
- 缺点：估算误差较大（英文/代码偏差明显）；截断摘要信息损失大；未做按 token 精确计费对齐。

### 企业常用替代方案

- 精确分词：tiktoken（OpenAI）、HuggingFace tokenizers；或按 provider 返回的 `usage` 精确回填。
- 摘要：LLM 结构化摘要（把旧对话压成"用户画像+关键结论"）；滑动窗口 + Redis 缓存摘要。
- 参考：https://platform.openai.com/docs/guides/text-generation/managing-tokens

### 交流复盘常被关注的问题

- "裁剪为何先删非 system 消息？" → system 是角色指令，删了模型会丢行为约束；预算不足优先牺牲对话史。
- "摘要后仍超预算怎么办？" → 压完复查 total，若没进展立即停止（防死循环），宁可放行不截断问题。
- "摘要信息该放哪个 role？" → 放 system（"早期对话摘要："前缀），因为只有 system 永不被裁。

### 代码路径

- `../internal/agent/context.go#L19`、`#L76`（ContextWindow）、`#L91`（Trim）、`#L113`（摘要循环）

---

## 7. 多模型路由与结构化输出

### 流程技术关键点

- `Router` 维护有序 provider 链，`ChatWithFallback` 顺序重试：主模型失败自动降级到下一候选，成功按实际
  服务者上报用量；`Promote` 支持影子评测通过后把候选模型提升为主模型。
- 结构化输出"强约束优先、事后校验兜底"：主模型支持 `ChatJSON`（response_format）先用生成前强约束；
  否则普通调用 + `schema.Validate` 事后校验，并做 `extractJSON`（剥 ```json``` 围栏）、`unwrapWrapper`
  （单键包裹）、`fixArrayOutput`（顶层数组）三层容错。

### 当前实现原理及优缺点

- 原理：Provider 接口统一 `Chat/ChatStream/ChatJSON`；Ollama/OpenAI 真流式，Anthropic 走适配回退；
  `StructuredChat` 强约束失败后从"主模型之后的备选"开始重试（F-6：避免主模型被调两次的成本翻倍 bug）。
- 优点：模型可热切换、降级链路完整、小模型也能产出合法 JSON；用量统一上报。
- 缺点：fallback 是"顺序重试"而非真熔断（半开/统计熔断缺）；schema 校验为自研轻量实现。

### 企业常用替代方案

- 多模型路由：LiteLLM/OneAPI 聚合网关 + 熔断（hystrix/circuitbreaker）+ 智能路由（成本/延迟/质量）；
  OpenRouter 类服务。
- 结构化输出：provider 原生 json_schema（OpenAI/Anthropic/Ollama 均支持）；或 LangChain `with_structured_output`。
- 参考：https://platform.openai.com/docs/guides/structured-outputs ，https://docs.litellm.ai/

### 交流复盘常被关注的问题

- "强约束失败为什么不能直接再调主模型？" → ChatWithFallback 会从链首重试，导致同一主模型双倍调用成本（F-6）。
- "小模型 JSON 常包代码围栏怎么处理？" → extractJSON 剥围栏后再校验，仍是合法输出。
- "用量如何按真实服务者计费？" → 每次成功调用按 `p.Name()` 上报，而非固定按主模型。

### 代码路径

- 路由：`../internal/provider/router.go#L24`（NewRouter）、`#L87`（ChatWithFallback）、`#L61`（Promote）
- 结构化：`../internal/provider/structured.go#L22`（StructuredChat）、`#L165`（extractJSON）
- 适配器：`../internal/provider/ollama.go#L24`、`openai.go#L27`、`anthropic.go#L27`

---

## 8. 工具系统：注册表、权限、审计与沙箱

### 流程技术关键点

- 三层防线：角色白名单（`allow: role→工具集`，admin 全开）→ 工具自身声明（`Risky`：高危需二次确认、指定角色）→
  审计钩子（所有调用落日志，高危记录参数）。
- 内置工具：`calculator`（自研表达式求值）、`get_current_weather`、`web_search`、`translate_text`、
  `fetch_url`（SSRF 防护）、`list_dir/read_file/write_file/run_command`（工作目录沙箱）、`create_doc`（docgen）。
- `Subset` 复制子注册表供多 Agent 专业化，同时复制白名单与审计器（防审计链路断裂）。

### 当前实现原理及优缺点

- 原理：`Registry` 用 `sync.RWMutex` 保护 map；`Execute` 先校验角色→高危确认→`ValidateArgs`（C13 结构化校验）→
  执行→审计；`ExecSandbox` 用路径解析（`filepath.Clean`/`EvalSymlinks`/`Within`）限制在 workDir 内。
- 优点：权限/审计/高危确认全链路统一，工具开发只需实现 `Tool` 接口；SSRF 用 DNS 二次解析防绕过。
- 缺点：沙箱是路径级而非 OS 级（`run_command` 未做 cgroup/seccomp 隔离，属高危 admin 专用）；
  无插件动态加载的运行时沙箱（plugin 是 HTTP 型）。

### 企业常用替代方案

- 函数调用协议：OpenAI tool-calling / Anthropic tool use / MCP（Model Context Protocol，本项目已内置）。
- 执行隔离：Docker/Firecracker/gVisor 容器、沙箱服务（E2B）、cgroup+seccomp+landlock。
- 参考：https://modelcontextprotocol.io/ ，https://docs.docker.com/engine/security/ ，https://e2b.dev/docs

### 交流复盘常被关注的问题

- "高危工具二次确认在哪一层？" → `Agent.confirmTool`（toolLoop 与 ReAct 共用），调用方无 Confirm 回调则直接拒绝。
- "为什么审计要随 Subset 复制？" → 否则 supervisor/worker 用子注册表调工具时审计事件丢失（S-4）。
- "SSRF 怎么防 DNS 重绑定？" → `ResolveSSRF` 解析出全部 IP 逐个 check，禁私网/环回/链路本地等（`isBlockedIP`）。

### 代码路径

- 注册表：`../internal/tool/registry.go#L24`、`#L139`（Subset）、`#L185`（Execute）
- 工具接口与校验：`../internal/tool/tool.go#L13`、`#L58`（ValidateArgs）
- 沙箱：`../internal/tool/exec.go#L32`、`exec_shell.go#L42`（run_command，risk=2 仅 admin）
- SSRF：`../internal/safety/ssrf.go#L30`
- 插件：`../internal/plugin/plugin.go#L68`

---

## 9. 分层记忆：工作记忆、长期记忆与画像

### 流程技术关键点

- 工作记忆（`WorkingMemory`）：按会话滑动窗口保留最近 N 条；`ReplaceLast` 支持 ReAct 等重写最近答案。
- 长期记忆（`Manager.Remember` 异步）：Qdrant 向量库按租户/用户/session 命名空间入库，`Recall` 语义检索。
- 画像（`Profile`）：从用户输入抽取事实（规则抽取默认，可换 LLM 抽取器），带冲突记录与 `resolve` 裁决；
  记忆还有遗忘（`ForgetUser`/`ForgetTenant`，P6 被遗忘权）。

### 当前实现原理及优缺点

- 原理：`Remember` 为异步（不阻塞对话），`Recall` 用嵌入余弦相似度 + 相关度阈值取 topK；
  画像学习只从用户输入抽取（模型回答置信度低），同类归一化事实合并防画像膨胀。
- 优点：分层清晰、接口可插拔（Qdrant/Redis 双存储）、遗忘路径覆盖会话/记忆/画像全链路。
- 缺点：异步 Remember 有写失败静默丢数据风险（落库失败不阻塞对话是有意取舍）；画像抽取规则较朴素。

### 企业常用替代方案

- 向量库：Qdrant/Weaviate/pgvector/Milvus；RAG 检索增强（本项目也有 rag 包）。
- 记忆框架：Mem0、Zep、LangChain memory；语义缓存 + 用户画像沉淀。
- 参考：https://qdrant.tech/ ，https://docs.mem0.ai/ ，https://www.getzep.com/

### 交流复盘常被关注的问题

- "为什么画像只从用户输入抽？" → 模型回答含事实置信度低，误抽会污染画像。
- "异步写失败会不会丢记忆？" → 会，但"不阻塞对话"优先；生产可改投递队列 + 重试 + 死信。
- "被遗忘权如何保证删除即遗忘？" → ForgetUser 串行等待在飞写回后删除会话、记忆、画像、反馈全链路。

### 代码路径

- 记忆管理器：`../internal/memory/memory.go#L39`（Remember）、`#L114`（Recall）
- 工作记忆：`../internal/memory/working.go#L30`（Add）、`#L45`（ReplaceLast）
- 画像：`../internal/memory/profile.go`；遗忘：`../internal/server/forget.go`

---

## 10. RAG、知识图谱与技能检索

### 流程技术关键点

- RAG（`rag.Index`）：`AddDocument` 分块（size/overlap）→ 嵌入入库；`Retrieve` 向量检索；
  `RetrieveHybrid` 混合检索（向量 + BM25 关键词 + z-score 归一化加权）。
- 知识图谱（`kg.Graph`）：三元组（subject/predicate/object）内存图，`ExtractTriples` 从文本抽关系；
  `Query`/`Search` 反查。
- 技能（`skill.Registry`）：`Match` 用关键词（中文逐字 + 英文整词）匹配技能描述，供 supervisor 路由兜底。

### 当前实现原理及优缺点

- 原理：三者都是进程内实现，零外部依赖可用；混合检索可调 `vectorWeight` 平衡向量/关键词。
- 优点：单机可跑通完整"知识"链路；接口清晰可换外部服务。
- 缺点：分块策略朴素（固定 size/overlap，无语义分块）；KG 为内存实现无法横向扩展；
  技能匹配无向量化，纯关键词召回质量有限。

### 企业常用替代方案

- RAG：LangChain/LlamaIndex 检索链 + 语义分块（by sentence/heading）+ 重排序（cross-encoder rerank）+ 混合检索；
  向量库 + BM25 双路召回（Elasticsearch + Milvus）。
- 知识图谱：Neo4j/JanusGraph + LLM 抽取（NELL/REBEL 类）；GraphRAG（微软，社区聚合建图）。
- 参考：https://www.elastic.co/guide/en/elasticsearch/reference/current/ , https://www.neo4j.com/developer/graphrag/ , https://microsoft.github.io/graphrag/

### 交流复盘常被关注的问题

- "混合检索为何要 z-score 归一化？" → 向量相似度与 BM25 分数量纲不同，归一化后才可加权合并。
- "中文检索为什么逐字切词？" → 无空格语言按词切分需要分词器，逐字是零依赖折中（技能/路由兜底同策略）。
- "RAG 命中质量如何保障？" → 生产加 rerank + 引用溯源；评测里 judge 的 Faithfulness 维度覆盖这一点。

### 代码路径

- RAG：`../internal/rag/index.go#L55`（AddDocument）、`#L76`（Retrieve）、`#L115`（RetrieveHybrid）
- 知识图谱：`../internal/kg/kg.go#L43`（Add）、`#L56`（Query）、`#L111`（ExtractTriples）
- 技能：`../internal/skill/skill.go#L91`（Match）

---

## 11. 多 Agent：Supervisor 路由与四种增强模式

### 流程技术关键点

- `supervisor.Supervisor`：先让 LLM 从 worker 列表输出 `{"worker":"name"}`，失败/非法则关键词兜底；
  `Run` 路由 → 每请求新建 Agent → 绑定会话 → 执行。
- 增强模式：`plan`（PlanAndExecute：规划→执行→反思）、`react`（显式 ReAct 轨迹）、
  `reflect`（反思输出 + SelfConsistent 多数投票）、`debate`（双人格辩论）、`consistent`（自一致性采样投票）。

### 当前实现原理及优缺点

- 原理：worker 用 `Build() *agent.Agent` 工厂每请求新建（不共享可变状态）；`Subset` 给出专业工具集；
  模式复用同一 `Agent` 核心，只换编排函数与系统提示。
- 优点：专业化 + 会话隔离；五种模式在一套架构内统一；路由省成本（关键词兜底兜住 LLM 失败）。
- 缺点：Supervisor 每次路由都调 LLM（成本高，生产可换向量/分类器）；自一致性采样数固定且无自适应。

### 企业常用替代方案

- 编排：LangGraph（显式状态机 + checkpointing）、CrewAI / AutoGen（角色协作 + 群聊）、Orchestrator-Worker 模式。
- 路由：Embedding 分类器 / 规则引擎优先，LLM 只在低置信时兜底。
- 参考：https://langchain-ai.github.io/langgraph/ ，https://docs.crewai.com/ ，https://microsoft.github.io/autogen/

### 交流复盘常被关注的问题

- "worker 为什么每请求新建？" → 避免共享可变状态导致的并发安全与会话串扰。
- "路由兜底为什么逐字切中文？" → 与技能检索同策略，零分词依赖下提高命中率。
- "debate/consistent 的成本翻倍如何权衡？" → 质量优先场景值得；可按问题难度动态选模式省成本。

### 代码路径

- Supervisor：`../internal/supervisor/supervisor.go#L63`（Route）、`#L108`（keywordFallback）、`#L145`（Run）
- 规划-执行：`../internal/agent/plan.go`
- ReAct：`../internal/agent/react.go#L73`（react）
- 反思/自一致性：`../internal/agent/reflect.go#L52`（RunReflect）、`#L108`（SelfConsistent）
- 辩论：`../internal/agent/debate.go#L33`

---

## 12. 安全横切面：内容审核、注入防护与审计

### 流程技术关键点

- 内容安全（D18）：`Moderator` 接口 + 关键词审核，输入/输出双端（`checkInput`/`checkOutput`）在一切
  LLM 调用与持久化之前执行。
- 注入防护（D17）：输入侧 `DetectInjection` 打审计标记；工具结果侧先 `SanitizeToolResult` 清洗再检出，
  命中则追加"视为数据"提醒 + `OnInjection` 回调。
- 审计：工具调用全量落审计（`auditor.go`），高危记录参数；SSRF 校验 fetch 目标。

### 当前实现原理及优缺点

- 原理：`KeywordModerator` 关键词黑名单（可加默认词表）；`Redact` 用 `SecretStore` 对密钥类文本脱敏；
  工具结果注入防护双防线（清洗→检出）。
- 优点：安全点统一横切、不散落在业务代码；缓存命中路径也不绕过审核。
- 缺点：关键词审核误伤/漏放（无语义理解）；未接外部审核服务（内容合规强诉求企业需接第三方）。

### 企业常用替代方案

- 内容审核：云厂商审核服务（火山引擎内容审核/AWS Rekognition 等）+ 敏感词库 + LLM 分级审核；
  OpenTelemetry 审计日志落 S3/ES。
- Prompt 注入：OpenAI 的 instruction hierarchy 方法论、输入分类器（Llama Guard）。
- 参考：https://arxiv.org/abs/2404.13208 （instruction hierarchy），https://www.volcengine.com/docs/6561

### 交流复盘常被关注的问题

- "输出审核为什么必须在持久化之前？" → 否则违规文本已入库，成为后续轮次上下文且"删除即遗忘"失效。
- "工具结果注入为什么先清洗再检出？" → 先剥离明显的注入指令/混淆，再检出残留特征，双层降低污染。
- "关键词审核误伤正常内容怎么办？" → 审核只做高危拦截，不阻断普通词（输入侧注入检测只标记不阻断）。

### 代码路径

- 安全核心：`../internal/safety/safety.go#L26`（SanitizeToolResult）、`#L40`（DetectInjection）、`#L58`（Moderator）、`#L123`（Redact）
- 审核调用点：`../internal/agent/agent.go#L97`（checkInput）、`#L108`（checkOutput）
- 审计：`../internal/safety/audit.go`

---

## 13. 可观测性与成本治理

### 流程技术关键点

- 指标：`/metrics` 输出请求数、延迟直方图等（`middleware.go` 的 `Metrics`）；`/metrics/cost` 成本归因。
- 成本：`cost.Tracker` 按 用户→会话→模型 记账，`Estimate` 估算美元成本，`Snapshot` 聚合展示；
  `provider.usage.go` 每次成功调用按实际模型上报 token 用量。
- 语义缓存（P5）：`cache.SemanticCache` 用嵌入相似度判定"相似问题"，命中直接回答案，省一次 LLM 调用。

### 当前实现原理及优缺点

- 原理：`Middleware` 采集 + 自研 Prometheus 文本格式 handler；`cost` 用估算单价累加；
  语义缓存带租户 scope 隔离，仅缓存纯文本回答（未调工具）。
- 优点：成本归因到用户/会话/模型，可直接支撑计费/对账；缓存"业务级"命中（比 prompt cache 更省）。
- 缺点：无 OpenTelemetry 标准导出；缓存为 FIFO 进程内（多实例需 Redis 共享）。

### 企业常用替代方案

- 可观测性：OpenTelemetry（trace/metric/log 统一）+ Prometheus + Grafana + Loki；
  成本用 provider 官方 usage API 精确计费。
- 参考：https://opentelemetry.io/ ，https://prometheus.io/docs/introduction/overview/

### 交流复盘常被关注的问题

- "缓存命中为什么只限纯文本回答？" → 调了工具的回答可能有时效/副作用，缓存会返回过期结果。
- "用量为什么按实际服务者上报？" → fallback 到备选模型时成本与主模型不同，按实际者记账才对账。
- "成本估算与真实账单的差异来源？" → 输入/输出 token 按模型单价估算，缓存命中不计费的部分需单独标注。

### 代码路径

- 指标：`../internal/server/middleware.go#L62`
- 成本：`../internal/cost/cost.go#L55`（Record）、`#L76`（Estimate）、`#L118`（Handler）
- 语义缓存：`../internal/cache/cache.go#L56`（Get）、`#L81`（Put）
- 用量上报：`../internal/provider/usage.go`

---

## 14. 评测闭环：Judge、影子评测与离线用例

### 流程技术关键点

- `Judge`：LLM-as-a-judge 对（问题, 回答）打三维分数——Faithfulness（忠实）、Relevance（相关）、Safety（安全），
  安全语义显式定义：拒绝危险请求 = 高分，仅实际泄露/提供危险内容才低分（防止把"安全拒绝"误判为不安全）。
- 影子评测（`shadow.go`）：主模型回答的同时，候选模型在后台独立作答，`Judge` 打分对比；
  `promote` 通过后提升候选为主模型，质量回退自动切回。
- 离线评测：`test/eval/golden_test.go`（golden 用例）与 `redteam_test.go`（红队安全）；
  反馈踩按钮自动回流评测集（`AppendCase`）。

### 当前实现原理及优缺点

- 原理：`Judge.Score` 让 LLM 输出 JSON 三维分数，`parseScores` 解析 + clamp；`Pass` 按阈值判定；
  影子评测按采样率后台执行，不阻塞主链路。
- 优点：质量闭环（线上影子 + 反馈回流 + 灰度切换）；Judge 语义与评测意图对齐（安全语义修正了误报）。
- 缺点：LLM-as-a-judge 存在模型盲区（如看不到工具轨迹，把工具支撑的时间回答判为幻觉）；小型本地模型
  评测有随机性 flake（golden 偶发不调工具）；评测需较长超时（`-timeout 20m`）。

### 企业常用替代方案

- 评测：LLM-as-a-judge + 人工标注集 + 指标化（RAGAS 框架覆盖忠实/相关/上下文精度）；A/B 分流 + 灰度。
- 参考：https://docs.ragas.io/ ，https://arxiv.org/abs/2306.05685

### 交流复盘常被关注的问题

- "Judge 为什么看不到工具轨迹？" → Judge 只拿 (question, answer)，工具证据不在输入里；
  改进方向：把 tool-call 记录并入 judge 输入，或改为基于证据的 Faithfulness 判定。
- "golden 用例为什么会 flake？" → 小模型（0.8b）工具调用能力不足会偶发不调工具；4b 稳定性明显更好，
  评测结果需容忍小模型随机性。
- "踩反馈如何回流评测集？" → `feedback.go` 踩赞 → `CaseFromFeedback` 追加为离线用例，防回归。

### 代码路径

- Judge：`../internal/eval/judge.go#L57`（Score）、`#L42`（Scores）
- 影子评测：`../internal/eval/shadow.go#L101`、`#L166`（Run）、`#L151`（WantSample）
- 数据集：`../internal/eval/dataset.go#L73`（RunCases）、`#L227`（CaseFromFeedback）
- 测试：`../test/eval/golden_test.go`、`../test/eval/redteam_test.go`

---

## 15. 异步任务、Web UI 与 MCP 扩展

### 流程技术关键点

- 异步长任务（`task.Manager`）：`POST /v1/tasks` 提交 → 后台 `loop` 并发执行 → 检查点（checkpoint）持久化 →
  `GET /v1/tasks/{id}` 查进度；`notify` 支持 Webhook 回调。
- Web UI：`/` 零构建静态页（SSE 聊天 + 会话列表），`ui.go` 内嵌 HTML。
- MCP：`internal/mcp` 内置 MCP Server（stdio + HTTP 传输），把 Agent 能力以 MCP Tool 暴露给外部客户端。

### 当前实现原理及优缺点

- 原理：任务用内存 `Store` + `RunFunc` 抽象（可换 DB）；检查点字节串随执行阶段更新；
  MCP 走 JSON-RPC（`process` 分发 + 路由到注册工具）。
- 优点：长任务不阻塞对话、有检查点可续跑；MCP 让工具协议标准化（LLM 生态兼容）。
- 缺点：任务无持久化队列（重启丢失）；UI 功能浅（调试向）。

### 企业常用替代方案

- 任务：持久化消息队列（Redis Stream/RabbitMQ/Kafka）+ Worker 池 + 分布式锁；
  进度用 websocket/SSE 实时推送。
- UI：前端框架 + 网关；MCP 生态直接复用（本项目的 mcp 包即为此准备）。
- 参考：https://redis.io/docs/data-types/streams-tutorial/ ，https://www.rabbitmq.com/tutorials/tutorial-one-go

### 交流复盘常被关注的问题

- "检查点机制怎么实现断点续跑？" → `RunFunc` 返回 `cpOut`，Manager 存起来，重试时回传；生产换 Redis/DB。
- "为什么 UI 要内嵌？" → 零依赖演示/调试，生产通常拆独立前端。
- "MCP Server 如何与既有工具系统打通？" → 复用 `tool.Registry`，把内部工具注册为 MCP ToolDef。

### 代码路径

- 任务：`../internal/task/task.go#L127`（Manager）、`#L193`（Submit）、`#L225`（execute）
- 通知：`../internal/notify/notify.go#L42`（WebhookNotifier）
- MCP：`../internal/mcp/server.go#L79`（process）、`#L124`（ServeStdio）、`#L148`（ServeHTTP）

---

## 16. 已知边界与生产演化路线

| 环节 | 当前取舍 | 生产演化方向 |
|---|---|---|
| 配置/密钥 | 环境变量 | Viper + 配置中心 + KMS 密钥管理 |
| HTTP | 标准库 ServeMux | Gin/gRPC + 网关 + 版本化 |
| 会话 | 内存/Redis 双实现 | 分布式会话 + 原子写回 |
| 上下文 | 启发式 token 估算 | tiktoken 精确计数 + LLM 摘要 |
| 路由 | 顺序 fallback | 熔断 + 智能路由（成本/延迟/质量） |
| 工具沙箱 | 路径级 + 高危 admin | Docker/cgroup/seccomp 隔离 + MCP 生态 |
| 记忆/向量 | Qdrant/Redis + 进程内 KG | pgvector/Milvus + Neo4j + GraphRAG |
| 安全 | 关键词审核 + 规则注入检测 | 云审核服务 + Llama Guard + instruction hierarchy |
| 可观测 | 自研 metrics | OpenTelemetry + Prometheus/Grafana/Loki |
| 评测 | LLM-as-a-judge + 影子 | RAGAS 指标 + 人工标注 + 自动回归流水线 |

- 代码路径：完整里程碑 P0~P63 与能力基线 A~E 见 `../README.md`（§7 企业能力地图、§8 路线图）。
