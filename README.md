# zebra

> 基于 Ollama 本地 LLM 构建的 AI Agent 演示项目，手写实现 **Function Calling**、**MCP 协议** 和 **语义记忆** 三大基础设施。

25 个 `.go` 文件，约 3274 行代码，除测试外仅依赖 Go 标准库。项目经历了 **协议适配 → MCP 集成 → 记忆系统** 三个阶段的演进，是一个典型的自底向上构建 AI Agent 的学习参考。

---

## 目录

- [快速开始](#快速开始)
- [架构总览](#架构总览)
- [核心模块深度解析](#核心模块深度解析)
  - [provider/ — 多协议 LLM 适配层](#1-provider--多协议-llm-适配层)
  - [mcp/ — 自研 MCP 协议栈](#2-mcp--自研-mcp-协议栈)
  - [tool/ — 内置工具系统](#3-tool--内置工具系统)
  - [memory/ — 语义记忆系统](#4-memory--语义记忆系统)
  - [agent/ — Agent 核心引擎](#5-agent--agent-核心引擎)
- [数据流全链路](#数据流全链路)
- [配置说明](#配置说明)
- [项目演进](#项目演进)
- [代码质量评估](#代码质量评估)
- [综合评价](#综合评价)

---

## 快速开始

### 前置依赖

| 组件 | 用途 | 必需 |
|---|---|---|
| [Ollama](https://ollama.com) | LLM 推理 + Embedding 生成 | ✅ |
| [Qdrant](https://qdrant.tech) | 向量数据库（长期记忆） | 可选 |
| MCP Server | 独立工具服务进程 | 可选 |

```bash
# 拉取模型
ollama pull qwen3.5:0.8b-mlx
ollama pull nomic-embed-text:v1.5

# 启动 Qdrant（可选）
docker run -p 6333:6333 -p 6334:6334 qdrant/qdrant
```

### 运行

```bash
# 完整交互式 Agent（含 MCP + 记忆，推荐）
go run cmd/memory/main.go

# 单次对话示例
go run cmd/agent/main.go

# 启动 MCP 工具服务器（HTTP 模式）
go run cmd/mcp_server/main.go -http :9000

# MCP 客户端示例
go run cmd/mcp_client/main.go
```

---

## 架构总览

```
┌──────────────────────────────────────────────────────────────┐
│                     cmd/ (4 个入口程序)                         │
│        交互式对话循环 / 单次对话 / MCP Server / MCP Client      │
└──────────────┬──────────────────┬────────────────────────────┘
               │                  │
    ┌──────────▼──────┐  ┌────────▼────────────────────────────┐
    │    agent/       │  │            mcp/                      │
    │  Agent 核心引擎  │  │  MCP Server / Client / Adapter       │
    │  ┌───────────┐  │  │  ┌──────────────────────────────┐   │
    │  │ 对话管理   │  │  │  │ JSON-RPC 2.0 协议栈          │   │
    │  │ 工具调用循环│  │  │  │ stdio + HTTP 双传输层        │   │
    │  │ 记忆检索存储│  │  │  │ MCPToolAdapter 桥接器       │   │
    │  └───────────┘  │  │  └──────────────────────────────┘   │
    └──┬───────┬──────┘  └─────────────────────────────────────┘
       │       │
  ┌────▼──┐ ┌──▼────────────────────────────────┐
  │provider│ │              tool/                │
  │  ┌───┐ │ │  ┌─────────────────────────────┐ │
  │  │ 3 │ │ │  │ 8 个内置工具                  │ │
  │  │ 种 │ │ │  │ Registry (注册/执行/转换)    │ │
  │  │ 协 │ │ │  │ MCP 适配器 → tool.Tool 桥接  │ │
  │  │ 议 │ │ │  └─────────────────────────────┘ │
  │  └───┘ │ └──────────────────┬───────────────┘
  └────────┘                    │
                     ┌──────────▼──────────┐
                     │      memory/        │
                     │  Qdrant 向量存储     │
                     │  Ollama / OpenAI    │
                     │  Embedding 双后端    │
                     └─────────────────────┘
```

### 目录结构

```
.
├── cmd/
│   ├── agent/main.go       # 单次 Agent 对话示例
│   ├── memory/main.go      # 全功能交互式 Agent（含 MCP + 记忆）
│   ├── mcp_server/main.go  # MCP 工具服务器（8 个工具）
│   └── mcp_client/main.go  # MCP 客户端示例
├── agent/agent.go          # Agent 核心：对话管理 + 工具调用循环
├── provider/               # LLM 协议适配层
│   ├── provider.go         # 公共接口与数据结构
│   ├── ollama_native.go    # Ollama 原生 API
│   ├── ollama_openai.go    # OpenAI 兼容 API
│   └── ollama_anthropic.go # Anthropic 兼容 API
├── tool/                   # 工具系统
│   ├── tool.go             # 接口定义 + Registry + ParseArguments
│   ├── weather.go          # 天气查询（wttr.in）
│   ├── calculator.go       # 数学计算（手写 Shunting-yard）
│   ├── datetime.go         # 日期时间
│   ├── random.go           # 随机数
│   ├── search.go           # 网页搜索（DuckDuckGo）
│   ├── unit_converter.go   # 单位换算（长度/重量/温度）
│   ├── translate.go        # 文本翻译（MyMemory）
│   └── ip_info.go          # IP 信息查询（ipify + ip-api）
├── mcp/                    # MCP 协议实现
│   ├── types.go            # JSON-RPC 2.0 + MCP 结构体
│   ├── server.go           # MCP Server (stdio + HTTP)
│   ├── client.go           # MCP Client (stdio + HTTP)
│   └── adapter.go          # MCP 工具 → tool.Tool 适配器
├── memory/memory.go        # Qdrant 语义记忆存储
├── config/config.go        # .env 配置加载
├── test/qdrant_test.go     # Qdrant gRPC 集成测试
└── .env                    # 环境变量配置
```

### 核心设计原则

项目以 **三个核心接口** 为骨架，实现高度解耦：

```go
// provider/provider.go
type Provider interface {
    Chat(messages []Message, tools []Tool) (Message, error)
}

// tool/tool.go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(args map[string]interface{}) (string, error)
}

// memory/memory.go
type Memory interface {
    Store(content string, metadata map[string]string) error
    Retrieve(query string, limit int) ([]string, error)
    Clear() error
}
```

替换任何一个实现都不影响其他模块——这是项目在架构层面最大的价值。

---

## 核心模块深度解析

### 1. `provider/` — 多协议 LLM 适配层

项目最出彩的模块。三个 Provider 共享同一套 OpenAI 风格的 `Message`/`Tool`/`ToolCall` 内部数据结构，在各适配器内部完成协议转换。

#### 协议差异对比

| 维度 | Ollama Native | OpenAI 兼容 | Anthropic 兼容 |
|------|:---:|:---:|:---:|
| API 端点 | `/api/chat` | `/v1/chat/completions` | `/v1/messages` |
| 工具定义格式 | `{type, function}` | `{type, function}` | `{name, description, input_schema}` 扁平化 |
| 工具调用位置 | `message.tool_calls` | `choices[0].message.tool_calls` | `content[].type: "tool_use"` |
| arguments 格式 | 对象或 JSON 字符串 | 总是 JSON 字符串 | 总是 JSON 对象 |
| 工具结果消息角色 | `role: "tool"` | `role: "tool"` | `role: "user"`，`content` 为 `tool_result` 块 |
| 特殊要求 | 需 `tool_call_id` 关联 | 需 `tool_call_id` 关联 | 必须 `max_tokens`，无 `stream` |

#### Anthropic 适配器转换逻辑（关键实现）

Anthropic 的适配是最复杂的，核心转换包括：

1. **消息转换**：`assistant` 消息中的 `tool_use` content block → `Message.ToolCalls`；`tool` 角色消息 → `user` 角色包裹 `tool_result` content block（Anthropic API 要求工具结果必须放在 `user` 消息中）
2. **工具定义转换**：`{type: "function", function: {name, description, parameters}}` → `{name, description, input_schema}`
3. **响应解析**：遍历 `content[]` 数组，`type: "text"` 拼接为 `Content`，`type: "tool_use"` 转换为 `ToolCall`

> ⚠️ 当前 `max_tokens` 硬编码为 1024，可能截断长回复。生产环境建议改为可配置参数。

#### Ollama 工具调用参数兼容

不同 LLM 返回的 `arguments` 格式不一致——Ollama 有时返回 JSON 字符串，OpenAI 返回对象。`ParseArguments` 同时兼容两种格式：

```go
func ParseArguments(raw json.RawMessage) (map[string]interface{}, error) {
    var args map[string]interface{}
    if err := json.Unmarshal(raw, &args); err == nil {
        return args, nil  // 对象格式
    }
    var str string
    if err := json.Unmarshal(raw, &str); err != nil {
        return nil, fmt.Errorf("无法解析 arguments: %v", err)
    }
    if err := json.Unmarshal([]byte(str), &args); err != nil {
        return nil, fmt.Errorf("arguments 字符串内容无效: %v", err)
    }
    return args, nil  // JSON 字符串格式
}
```

---

### 2. `mcp/` — 自研 MCP 协议栈

这是项目 **最具学习价值** 的模块——从零手写 JSON-RPC 2.0 + MCP 协议栈，仅使用 Go 标准库。

#### 协议实现覆盖

| MCP 方法 | 状态 | 说明 |
|----------|:----:|------|
| `tools/list` | ✅ | 完整实现，返回工具定义列表 |
| `tools/call` | ✅ | 完整实现，调用工具并返回结果 |
| `initialize` | ❌ | 标准 MCP 握手，当前缺失 |
| `resources/*` | ❌ | 资源管理方法 |
| `prompts/*` | ❌ | 提示模板方法 |

#### 传输层设计

| 传输层 | 实现方式 | 适用场景 |
|--------|---------|---------|
| **stdio** | 启动子进程，通过 stdin/stdout 传递 JSON-RPC 行协议 | 本地进程通信，进程生命周期绑定 |
| **HTTP** | POST JSON-RPC，标准 HTTP 请求/响应 | 远程服务调用，独立部署 |

#### MCPToolAdapter — 关键桥接层

```go
type MCPToolAdapter struct {
    client *Client
    def    ToolDef
}

func (t *MCPToolAdapter) Execute(args map[string]interface{}) (string, error) {
    result, err := t.client.CallTool(t.def.Name, args)
    // 合并所有 content blocks 的文本
    ...
}
```

将 MCP 远程工具无缝适配为 `tool.Tool` 接口，同名工具后注册的覆盖先注册的（MCP 覆盖本地），Agent 无需区分工具来源。

> ⚠️ 安全提示：`mcp/adapter.go:59` 存在 `fmt.Errorf(output)` 非字面量格式字符串问题，当 `output` 包含 `%` 时会产生意外行为。

---

### 3. `tool/` — 内置工具系统

#### 8 个内置工具一览

| 工具 | 名称 | 实现方式 | 外部依赖 |
|------|------|---------|---------|
| 天气查询 | `get_current_weather` | wttr.in API | 免费 HTTP |
| 数学计算 | `calculator` | **手写 Shunting-yard 表达式求值器** | 无 |
| 日期时间 | `get_current_datetime` | `time.Now()` | 标准库 |
| 随机数 | `generate_random_number` | `math/rand` | 标准库 |
| 网页搜索 | `web_search` | DuckDuckGo Instant Answer API | 免费 HTTP |
| 单位换算 | `convert_units` | 手动映射表（长度/重量/温度） | 无 |
| 文本翻译 | `translate_text` | MyMemory API | 免费 HTTP |
| IP 查询 | `get_ip_info` | ipify + ip-api.com | 免费 HTTP |

#### calculator 工具 — Shunting-yard 算法

`calculator` 工具手写了完整的 Shunting-yard 表达式求值器，支持加减乘除、括号、小数。这是项目中 **算法实现最深入** 的部分，展示了从 tokenize → 中缀转后缀 → 后缀求值的完整流程。

```go
// 核心流程
tokenize(expr) → 中缀 token 流
shuntingYard(tokens) → 后缀表达式（RPN）
evaluateRPN(rpn) → 最终结果
```

#### Registry 设计

```go
type Registry struct {
    tools map[string]Tool  // 工具名 → 工具实例
}

func (r *Registry) Register(t Tool)        // 注册工具
func (r *Registry) ToProviderTools() []provider.Tool  // 转换为 Provider 工具定义
func (r *Registry) Execute(name string, args map[string]interface{}) (string, error)  // 执行工具
```

简单直接的 map 注册表，配合 `MCPToolAdapter` 可以无缝混合本地工具和远程 MCP 工具。

---

### 4. `memory/` — 语义记忆系统

#### 架构设计

```
Store(content, metadata)
  │
  ├─► generateEmbedding(content)          ← Ollama /api/embeddings 或 OpenAI /v1/embeddings
  │
  ├─► upsertPoints(qdrantPoint)           ← Qdrant HTTP API (PUT /collections/{name}/points)
  │
  └─► 异步 goroutine，不阻塞对话

Retrieve(query, limit)
  │
  ├─► generateEmbedding(query)
  │
  ├─► searchPoints(vector, limit)         ← Qdrant HTTP API (POST /collections/{name}/points/search)
  │
  └─► 返回 payload["content"] 列表
```

#### 双 Embedding 后端

| 后端 | 端点 | 配置 |
|------|------|------|
| Ollama | `/api/embeddings` | `EMBED_PROVIDER=ollama`（默认） |
| OpenAI | `/v1/embeddings` | `EMBED_PROVIDER=openai` + `OPENAI_API_KEY` |

#### 设计要点

- **HTTP 超时**：唯一设置了 10 秒超时的模块（`http.Client{Timeout: 10 * time.Second}`）
- **集合懒初始化**：首次 `Store` 时自动创建 Qdrant 集合，无需手动初始化
- **异步存储**：`go func()` 异步写入，失败只打日志不影响主流程
- **随机 ID**：`crypto/rand` 生成 16 字节十六进制 ID

> ⚠️ 并发注意：`collectionInited` 的检查-设置不是原子操作，高并发下可能重复创建集合。`sync.RWMutex` 已引入，修复成本低。

---

### 5. `agent/` — Agent 核心引擎

#### 工具调用循环

```
for turn := 0; turn < maxTurns; turn++ {
    respMsg := provider.Chat(messages, tools)
    if len(respMsg.ToolCalls) == 0 {
        return respMsg.Content  // 无工具调用，结束
    }
    for _, tc := range respMsg.ToolCalls {
        result := registry.Execute(tc.Function.Name, args)
        messages = append(messages, toolResultMessage)
    }
}
```

每次 LLM 推理后检查是否返回 `tool_calls`，有则执行工具并将结果注入消息列表继续推理，最多 `maxTurns`（默认 5）轮。

#### 历史管理策略

```
当前轮 messages：
  system + memory + history(user/assistant) + 当前 user + tool_calls + tool_results

持久化 history：
  只保留 user + assistant 最终回答，丢弃中间 tool 消息
```

**设计取舍**：简化了历史管理，但多轮对话中如果用户追问工具结果，可能丢失上下文。这是出于 Demo 简洁性的考虑。

#### 记忆集成

- **检索**：`buildMemoryQuery` 直接使用用户输入作为检索查询（可扩展为拼接历史上下文）
- **存储**：异步 goroutine，失败只打日志

---

## 数据流全链路

以"北京今天天气怎么样？"为例，展示完整的数据流：

```
用户输入: "北京今天天气怎么样？"
  │
  ▼
agent.Run("北京今天天气怎么样？")
  │
  ├─► memory.Retrieve(query, 3)          ← Qdrant 向量搜索
  │     └─► ollamaEmbed(query)           ← Ollama /api/embeddings
  │
  ├─► buildMessages()                    ← 组合 system + memory + history + user
  │
  ├─► provider.Chat(messages, tools)     ← 根据 OLLAMA_PROVIDER 选择协议适配器
  │     └─► Ollama/OpenAI/Anthropic API  ← 携带 8 个工具定义
  │
  ├─► 响应: tool_calls=[{name:"get_current_weather", args:{location:"北京"}}]
  │
  ├─► registry.Execute("get_current_weather", args)
  │     └─► WeatherTool.Execute()        ← HTTP GET wttr.in
  │
  ├─► 注入 tool 结果消息 → 再次 Chat
  │
  ├─► 响应: content="北京今天晴，25°C"
  │
  ├─► 更新 history (user + assistant)
  └─► 异步: memory.Store(对话摘要)        ← Qdrant upsert
```

---

## 配置说明

```bash
# .env 关键配置

# --- LLM 配置 ---
OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_MODEL=qwen3.5:0.8b-mlx       # 模型名称
OLLAMA_PROVIDER=openai               # 协议适配：ollama / openai / anthropic
OLLAMA_API_KEY=                      # API Key（通常 Ollama 不需要）

# --- MCP 配置 ---
MCP_MODE=http                        # stdio 或 http
MCP_HTTP_URL=http://localhost:9000   # HTTP 模式下的服务地址
# MCP_COMMAND=go run cmd/mcp_server/main.go  # stdio 模式下的启动命令

# --- Qdrant 向量数据库 ---
QDRANT_URL=http://localhost:6333
QDRANT_COLLECTION=agent_memory

# --- Embedding 配置 ---
EMBED_MODEL=nomic-embed-text:v1.5
EMBED_VECTOR_SIZE=768               # nomic-embed-text 为 768，all-minilm 为 384
# EMBED_PROVIDER=ollama              # 可选 ollama 或 openai
# OPENAI_API_KEY=                    # OpenAI 嵌入时必需
# OPENAI_BASE_URL=https://api.openai.com/v1
```

---

## 项目演进

```
b42232f  Initial commit
6bf0cfb  三大 LLM API Function Calling 协议适配器
5fdf689  重构项目结构，添加 MCP 服务器框架和 cmd 目录
56068b8  重构项目结构，添加 MCP 服务器框架和 cmd 目录
df194e9  基于 Qdrant 的语义记忆模块，Agent 支持长期记忆
```

5 个 commit，三阶段演进：**协议适配 → MCP 集成 → 记忆系统**，自底向上构建 AI Agent 基础设施。

---

## 代码质量评估

### 优点

| 维度 | 评价 |
|---|---|
| **架构设计** | 接口驱动（Provider/Tool/Memory/Transport），SOLID 原则良好，替换任何一层不影响其他模块 |
| **协议适配** | 三大 LLM 协议完整适配，Anthropic 的复杂转换逻辑展示了深厚的 API 协议理解 |
| **MCP 自研** | 从零手写 JSON-RPC 2.0 + MCP 协议栈，stdio/HTTP 双传输层，教育价值极高 |
| **零外部 SDK** | 除 Qdrant gRPC 测试外，所有 HTTP 调用手工实现，展示底层原理 |
| **工具丰富度** | 8 个内置工具，含手写 Shunting-yard 表达式求值器 |
| **记忆系统** | 语义向量检索 + 双 Embedding 后端（Ollama / OpenAI） |
| **代码可读性** | 中文注释 + 英文代码，结构清晰，命名规范 |

### 需要改进

| 问题 | 严重程度 | 说明 |
|------|:---:|------|
| **流式输出缺失** | 🟡 中 | 所有 Provider 都是非流式，无法实现逐字输出体验 |
| **错误处理薄弱** | 🔴 高 | 多处 `_` 忽略错误（`json.Marshal`、`http.NewRequest` 等），生产环境不可接受 |
| **HTTP 无超时/重试** | 🔴 高 | Provider 使用 `http.DefaultClient` 无超时，可能永久阻塞；无重试机制 |
| **测试覆盖极低** | 🔴 高 | 仅 `test/qdrant_test.go` 一个集成测试，无单元测试 |
| **MCP 握手缺失** | 🟡 中 | 缺少 `initialize` 握手，无法与标准 MCP 客户端/服务器互操作 |
| **并发控制不足** | 🟡 中 | `Agent.history` 无锁保护，`Registry.tools` 无锁，`collectionInited` 非原子 |
| **历史管理简化** | 🟢 低 | tool 消息被丢弃，多轮工具调用场景可能丢失上下文 |
| **`max_tokens` 硬编码** | 🟢 低 | Anthropic 适配器固定 1024，可能截断长回复 |
| **`go vet` 警告** | 🟢 低 | `mcp/adapter.go:59` 使用非字面量格式字符串 |

### 代码风险点速查

```go
// provider/ollama_native.go — 忽略关键错误
body, _ := json.Marshal(reqBody)
httpReq, _ := http.NewRequest("POST", p.BaseURL+"/api/chat", bytes.NewReader(body))

// provider/ollama_native.go — 无超时控制
resp, err := http.DefaultClient.Do(httpReq)

// agent/agent.go — 异步 goroutine 无 panic 恢复
go func() {
    if err := a.mem.Store(summary, map[string]string{"type": "conversation"}); err != nil {
        log.Printf("⚠️ 记忆存储失败: %v", err)
    }
}()
```

---

## 综合评价

| 维度 | 评分 | 说明 |
|------|:---:|------|
| **架构设计** | ⭐⭐⭐⭐⭐ | 接口驱动、分层清晰、低耦合，替换任何一层不影响其他模块 |
| **代码质量** | ⭐⭐⭐ | 协议实现优秀，但错误处理、测试覆盖不足，生产环境需补强 |
| **学习价值** | ⭐⭐⭐⭐⭐ | 手写 MCP 协议栈 + 三协议适配 + Shunting-yard 算法，教育价值极高 |
| **生产就绪** | ⭐⭐ | 缺少流式输出、错误处理、测试、并发保护，当前适合 Demo 演示 |
| **扩展性** | ⭐⭐⭐⭐ | 接口设计良好，添加新 Provider/Tool/Memory 实现成本低 |

### 适用场景

- ✅ 学习 AI Agent 底层原理（MCP 协议、Function Calling、向量记忆）
- ✅ 理解 LLM 协议差异（Ollama / OpenAI / Anthropic）
- ✅ 快速搭建本地 AI Agent 原型
- ⚠️ 生产环境部署（需补强错误处理、流式输出、测试覆盖）

### 改进路线图

1. **短期**：修复错误处理（替换 `_` 忽略）、添加 HTTP 超时和重试、修复 `go vet` 警告
2. **中期**：实现流式输出、补充 MCP 初始化和握手、添加单元测试
3. **长期**：完善 MCP 协议（resources/prompts）、添加并发控制、性能优化

---

**总结**：这是一个设计精良的 AI Agent 教学项目。最大价值在于**自研实现了 MCP 协议栈**和**三大 LLM 协议适配**，且全部代码使用 Go 标准库 + HTTP 手工实现，非常适合作为学习 AI Agent 底层原理的参考。当前状态适合 Demo 演示，若要用于生产环境，需要在流式输出、错误处理、测试覆盖等方面补强。
