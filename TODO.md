# TODO — zebra 全功能路线图（对标成熟 AI Agent）

> **整合说明**：本路线图已整合进 `README.md`（§8 交付路线图、§9 差距清单与生产化路径），
> README 为对外官方文档；本文件保留为**开发过程中的工作清单**（勾选验收细节），
> 完成项以 README 为准，未完成项以本文件为准推进。

> 本文件是三轮差距分析的**汇总纲领**，也是后续实现的**验收清单**。
> 原则：**以功能为单位实现，完成一个提交一个**；保持纯 Go 标准库、详细中文注释、精简可读。

---

## 0. 现状基线（已具备，重构于 commit d1ef43f）

42 个 .go 文件 ~4850 行，纯 Go 标准库，`go build/vet` 零警告、`go test ./...` 全绿。

| 层 | 已具备 |
|---|---|
| 协议 | Ollama/OpenAI/Anthropic 三协议适配 + 流式 + 多模态消息 |
| 编排 | 单 Agent 工具循环 + 循环检测 + 参数纠错 + 高危二次确认 |
| 工具 | 8 个内置工具 + RBAC 白名单 + schema 校验 + 审计 |
| MCP | 自研协议栈（initialize 握手 + tools + stdio/HTTP 双传输） |
| 记忆 | 分层：工作记忆 + Qdrant 长期记忆（租户隔离 collection） |
| 上下文 | token 估算 + 滑动窗口 + 摘要压缩接口 |
| 路由 | 多模型顺序 fallback + 超时/重试/熔断 |
| 服务化 | HTTP API + SSE + 会话(TTL/隔离) + API Key/RBAC + 限流 |
| 可观测 | slog 结构化日志 + /metrics + /healthz + /readyz + 优雅停机 |
| 安全 | 注入防护 + 关键词审核 + 脱敏 + 审计 + 工具权限 |
| Prompt | 模板注册表 + 版本化 + 灰度切换 |
| 工程化 | Docker/Compose/Makefile/CI + 8 个单测 + LLM golden 评测骨架 |

---

## 1. 差距分析汇总（三轮结论浓缩）

### 1.1 第一轮：10 大缺失类（单 Agent → 多 Agent/产品级）
1. **编排层**：多 Agent/图状态机/Plan-Execute/反思/并行工具/HITL
2. **长程任务**：异步队列/检查点/定时调度/持久化
3. **RAG**：文档摄取/分块/混合检索/重排/引用溯源/知识图谱
4. **LLM 推理深度**：结构化输出强约束/CoT/自一致性/采样治理
5. **工具生态**：MCP 全协议/插件动态加载/代码执行沙箱/浏览器自动化
6. **安全治理**：模型级注入检测/策略引擎/成本治理/合规
7. **可观测**：LLM 专用平台/Langfuse 类/在线评测/影子模式/SLO
8. **记忆升级**：遗忘机制/画像/精确计费/长上下文
9. **工程架构**：水平扩展/Redis/消息队列/gRPC/数据库/前端
10. **评测体系**：Evals 平台化/基准集/输出质量指标

### 1.2 第二轮：Skill 技能体系（核心缺失）
- Skill = 目录（`SKILL.md` + `scripts/` + `resources/`），本质是**"怎么做"的程序性知识包**，区别于 Tool（原子操作）/ MCP（外部协议）/ Prompt（一段文本）。
- 缺：SKILL.md 解析、技能注册/发现、技能检索、注入机制、Skill↔Tool 编排、脚本沙箱。

### 1.3 第三轮：穷举补充（质变级 + 细化级）
- **🔴 本地执行**：文件读写/编辑/diff、Shell 命令、代码库操作、沙箱权限边界（Agentic 分水岭）
- **🔴 主动出站**：Webhook/邮件推送、写回外部系统(幂等+OAuth)、定时/触发式运行
- **🔴 生成输出**：文档(Word/PDF/Excel)、图表、代码落地(PR)、语音(ASR/TTS)
- **🔴 质量闭环**：LLM-as-Judge、工具成功率指标、模型漂移监控、红队评测
- **🟡 成本治理**：成本归因(按会话/用户)、语义缓存、预算告警/超支熔断
- **🟡 安全**：SSRF 防护、文件上传安全、被遗忘权(GDPR)、系统提示泄漏防护、租户级凭据
- **🟡 运营**：反馈闭环(点赞/踩→回流)、热更新(不重启)、延迟 SLO、金丝雀/回滚
- **🟡 工程/产品**：前端 UI、个性化画像、OIDC/SSO、配置中心/特性开关、数据库迁移

---

## 2. 实施路线图（按功能单位提交）

> 每项含：**设计 / 落点 / 验收标准**。勾选 = 已完成并提交。

### ✅ P0 — 基线
- [x] 汇总差距分析到 TODO.md
- [x] 提交基线

### ✅ P1 — Skill 技能体系
- [x] `internal/skill/`：Skill 结构 + SKILL.md(frontmatter) 解析 + 注册表 + 检索 + 注入
- [x] 示例技能：`skills/` 目录下 2 个（研究报告 SOP / 数据核对 SOP）
- [x] Agent 集成：buildMessages 时检索并注入相关技能指令
- [x] 单元测试 + 提交

### ✅ P2 — 本地执行能力（Agentic 分水岭）
- [x] `tool/exec`：文件读写/列目录 + 只读/可写模式
- [x] `tool/exec`：Shell 命令执行 + **沙箱**（工作目录白名单/命令黑名单/超时/输出截断）
- [x] 高危工具标记（RiskLevel=2 + 二次确认）联动 D20
- [x] 单元测试 + 提交（端到端：Agent 真实建文件 + 执行命令）

### ✅ P3 — LLM 质量闭环
- [x] `internal/eval/`：LLM-as-Judge 自动评分器（忠实度/相关性/安全性）
- [x] 工具调用成功率指标（挂 /metrics，tool_call:<name>:ok/fail）
- [x] 评测增强：TestJudgeClosedLoop 自动打分汇总
- [x] 单元测试 + 提交

### ✅ P4 — 主动出站能力
- [x] `internal/notify/`：Webhook 通知器（HMAC 签名 + 幂等键 + 重试）
- [x] 对话完成自动推送 task.complete（端到端验证）
- [x] `internal/schedule/`：最小定时调度器（Every/Once + panic 隔离）
- [x] 单元测试 + 提交

### ✅ P5 — 成本与资源治理
- [x] 成本归因：/metrics/cost 按 用户×会话×模型 归因 token + 单价估算
- [x] `internal/cache/`：语义缓存（端到端：23.8s → 21ms）
- [x] 单元测试 + 提交

### ✅ P6 — 安全加固
- [x] SSRF 防护（协议白名单/内网拦截/域名白名单）接入 fetch_url + 网络工具
- [x] 租户级凭据（TenantSecretStore）
- [x] 被遗忘权：DELETE /v1/user/data（端到端验证 401 失效）
- [x] 单元测试 + 提交

### P7 — 收尾
- [ ] README 更新（新增能力地图）
- [ ] 全量 build/vet/test 验证
- [ ] 最终总结

---

### ✅ P8 — RAG 文档库（知识接入，防幻觉）
- [x] `internal/rag/`：分块器(段落优先+重叠) + 向量索引(AddDocument/Retrieve)
- [x] Agent 注入：检索命中片段带【来源】标记（接地/引用）
- [x] server 加载 docs/ 目录；示例文档 zebra.md / agent-design.md
- [x] 单测 + 端到端（'zebra 用什么语言写'→基于知识库准确回答，无幻觉）

### ✅ P9 — 并行工具调用（fan-out / fan-in）
- [x] 工具循环改为并发执行（goroutine + WaitGroup），结果按序回填不失序
- [x] execTool 抽为并行执行单元（C13/D20/D17 全链路）
- [x] 单测：最大并发=2、总耗时显著低于串行、调用顺序保持

### ✅ P10 — 规划-执行编排（Plan-then-Execute）
- [x] agent/plan.go：plan(拆解 JSON) → executeStep(逐部执行，复用 toolLoop) → 汇总
- [x] 子步骤不写历史/记忆（不污染对话）
- [x] API：mode="plan" 触发；HTTP_TIMEOUT 可配置
- [x] 单测 + 端到端（规划 2 步：计算 123*7=861 + 时间 → 汇总）

### ✅ P12 — 异步长任务 + 检查点
- [x] `internal/task/`：任务状态机 + 消费者队列（信号量限并发）+ 进度 + 检查点
- [x] API：POST /v1/tasks（立即返回 id）、GET /v1/tasks、GET /v1/tasks/{id}
- [x] 检查点：会话历史快照断点续跑；完成 Webhook（复用 P4）
- [x] 单测 + 端到端（后台执行 calculator 45*32=1440 + 时间，轮询 done）

### ✅ P13 — 多 Agent Supervisor（路由到专业 Worker）
- [x] `internal/supervisor/`：Worker(工厂) + Route(LLM 路由+关键词兜底) + Run
- [x] `tool.Registry.Subset`：按名复制工具子集（worker 能力聚焦）
- [x] 3 个专业 worker：数据 / 知识 / 常规（persona + 工具子集）
- [x] API：mode="supervisor" 自动路由；单测 + 端到端（计算→data worker）

### ✅ P14 — 前端 Web UI
- [x] 零构建单页 SSE 聊天界面（/ 公开返回）
- [x] API Key / 多轮会话续接 / 三模式切换 / 流式渲染 / 工具展示
- [x] 验证：GET / 返回 HTML 200 免鉴权

### ✅ P16 — 用户反馈闭环
- [x] `internal/feedback/`：点赞/踩 + 评论 + 正负计数
- [x] API：POST/GET /v1/feedback（落库 + /metrics + 审计）；回流评测真值
- [x] 单测 + 端到端（counts {positive:1,negative:1}）

### ✅ P17 — 结构化输出强约束
- [x] `internal/schema/`：通用 JSON Schema 校验器（type/required/enum/items）
- [x] provider：OpenAI ChatJSON（response_format=json_schema 生成前强约束）
- [x] `provider.StructuredChat`：强约束→回退→校验 双保险；集成进 P10 规划器
- [x] 单测（schema 多场景 / response_format 注入 / 回退路径）

### ✅ P18 — 配置热更新
- [x] prompt.LoadDir：prompts/ 文件化模板；rag.Index.Reset 重建
- [x] API：POST /v1/admin/reload（仅 admin，RBAC）
- [x] 重载 技能/提示词/知识库，不重启；单测 + 端到端（v2 生效）
- [x] 样本模板 prompts/*.md

### ✅ P20 — RAG 混合检索（BM25 关键词 + 向量融合）
- [x] `internal/rag/bm25.go`：BM25 打分（f(t,d) 文档词频 + IDF + 长度归一）
- [x] `RetrieveHybrid`：向量/BM25 z-score 归一融合（默认 0.7/0.3）；Agent 默认切换
- [x] 单测（tokenize / BM25 命中 / 专有名词混合命中 / 空索引安全）

### ✅ P21 — 在线评测/影子模式（shadow traffic）
- [x] `internal/eval/shadow.go`：候选模型独立回答 + Judge 双评 + 胜负对比
- [x] ShadowStore 记录 + 指标（runs / better / worse / error）+ 采样率
- [x] API：POST/GET /v1/eval/shadow（仅 admin）；chat 流量自动采样（异步不阻塞）
- [x] 单测 + 端到端（verdict=candidate_better；user 403）

### ✅ P22 — 记忆画像/遗忘机制深化
- [x] `internal/memory/profile.go`：Fact + 规则抽取器 + ProfileStore（Learn / FactsFor / ForgetKey / ForgetUser / Sweep / TrimUser）
- [x] `internal/memory/forget.go`：ForgetPolicy（TTL 保鲜 + MaxFactsPerUser 容量治理）
- [x] Agent 画像注入 + 对话自动学习；API：GET /v1/user/profile、POST /v1/user/profile/forget
- [x] 被遗忘权扩展：DELETE /v1/user/data 同时清除画像
- [x] 单测 + 端到端（学习→查看→精细遗忘→租户隔离）

### ✅ P23 — 文档/图表产出
- [x] `internal/docgen/`：WriteDocx（zip+OOXML）、WritePDF（对象/xref/trailer）、BarChartSVG / LineChartSVG
- [x] 工具：generate_docx / generate_chart（沙箱落盘 + 高危二次确认 + admin-only）
- [x] 单测 + 端到端（docx 可解包、PDF 头/xref/startxref、SVG 元素校验）

### ✅ P25 — 语音交互（ASR/TTS）
- [x] `internal/provider/voice.go`：OpenAI 兼容 Transcribe（multipart）+ Synthesize（JSON→音频流）
- [x] API：POST /v1/voice/transcribe、/v1/voice/synthesize、/v1/voice/chat（音频→Agent→音频 base64）
- [x] 单测 + 端到端（mock 语音网关断言请求格式；全链路音频回传）

### ✅ P26 — 影子评测看板 + 灰度切换
- [x] `internal/eval/stats.go`：ShadowStats 聚合 + RecommendSwitch（样本数/胜率阈值）
- [x] `router.Promote`：候选模型提升为主模型（即时生效）
- [x] API：GET /v1/eval/shadow/stats（看板+建议）、POST /v1/eval/shadow/promote（仅 admin，审计留痕）
- [x] 单测 + 端到端（win-rate 统计；promote 后 primary 切换）

### ✅ P27 — 画像 LLM 抽取升级
- [x] `internal/memory/extract.go`：Extractor 接口 + LLMExtractor（P17 结构化输出）+ RuleExtractor 回退
- [x] Agent/Server 接线：Extractor 可注入，默认 LLM 抽取（PROFILE_LLM=0 退回纯规则）
- [x] 单测 + 端到端（LLM 抽取入库；失败/空结果自动回退规则）

### ✅ P28 — 水平扩展骨架（Redis 会话存储）
- [x] `internal/redis/`：纯标准库 RESP 客户端（SET/GET/DEL/EXPIRE/KEYS + AUTH/SELECT）
- [x] `RedisSessionStore`：SessionStore + HistoryPersister（历史写回），TTL 由 Redis 原生负责
- [x] 接线：REDIS_URL 开启；chat 请求结束自动写回历史（多轮不丢）
- [x] 单测 + 端到端（假 Redis 服务器验证协议；创建/续期/遗忘/历史写回）

## 3. 设计基调（每个功能都必须遵守）

1. **纯 Go 标准库**，零第三方运行时依赖
2. **接口驱动 + 依赖注入**，可替换实现（生产演化方向用注释标注）
3. **详细中文注释**：先讲"为什么"，再讲"怎么做"
4. **每个功能 ≤ 300 行**，超出则拆文件
5. **以功能为单位提交**，commit message 标注功能名
6. 新增代码必须有单元测试
