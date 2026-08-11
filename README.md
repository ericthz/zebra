# aidemo

以下是三个平台在 **Function Calling（工具调用）** 场景中的核心 JSON 格式样例，涵盖工具定义、模型返回的工具调用请求、以及将工具执行结果发回的格式。

---

### 1. Ollama 原生 API (`/api/chat`)

**请求（携带工具定义）**
```json
{
  "model": "llama3.1",
  "messages": [
    { "role": "user", "content": "北京今天天气怎么样？" }
  ],
  "stream": false,
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_current_weather",
        "description": "获取指定城市的当前天气",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "城市名称，例如：北京"
            },
            "unit": {
              "type": "string",
              "enum": ["celsius", "fahrenheit"]
            }
          },
          "required": ["location"]
        }
      }
    }
  ]
}
```

**响应（模型请求调用工具）**
```json
{
  "model": "llama3.1",
  "created_at": "2024-01-01T00:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "get_current_weather",
          "arguments": { "location": "北京", "unit": "celsius" }
        }
      }
    ]
  },
  "done": true
}
```
> **注意**：`arguments` 可能是 JSON 对象（如上）或 JSON 字符串（如 `"{\"location\":\"北京\"}"`），取决于 Ollama 版本。

**将工具结果发回（二次请求）**
```json
{
  "model": "llama3.1",
  "messages": [
    { "role": "user", "content": "北京今天天气怎么样？" },
    {
      "role": "assistant",
      "content": "",
      "tool_calls": [ /* 上面的 tool_calls */ ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_abc123",
      "content": "地点：北京，当前温度：25度（单位：celsius），天气：晴朗"
    }
  ],
  "stream": false
}
```

---

### 2. OpenAI API (`/v1/chat/completions`)

**请求**
```json
{
  "model": "gpt-4o-mini",
  "messages": [
    { "role": "user", "content": "北京今天天气怎么样？" }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_current_weather",
        "description": "获取指定城市的当前天气",
        "parameters": {
          "type": "object",
          "properties": {
            "location": { "type": "string", "description": "城市名称" },
            "unit": { "type": "string", "enum": ["celsius", "fahrenheit"] }
          },
          "required": ["location"]
        }
      }
    }
  ]
}
```

**响应（有工具调用）**
```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1677652288,
  "model": "gpt-4o-mini",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "call_abc123",
            "type": "function",
            "function": {
              "name": "get_current_weather",
              "arguments": "{\"location\":\"北京\",\"unit\":\"celsius\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ]
}
```
> `arguments` 在 OpenAI 中始终是 **JSON 字符串**。

**返回工具结果**
```json
{
  "model": "gpt-4o-mini",
  "messages": [
    { "role": "user", "content": "北京今天天气怎么样？" },
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [ /* 上面的 tool_calls */ ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_abc123",
      "content": "地点：北京，当前温度：25度（单位：celsius），天气：晴朗"
    }
  ]
}
```

---

### 3. Anthropic API (`/v1/messages`)

Anthropic 的消息格式与 OpenAI 差异较大，使用 `content` 数组承载混合内容。

**请求**
```json
{
  "model": "claude-3-5-sonnet-20240620",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": "北京今天天气怎么样？"
    }
  ],
  "tools": [
    {
      "name": "get_current_weather",
      "description": "获取指定城市的当前天气",
      "input_schema": {
        "type": "object",
        "properties": {
          "location": { "type": "string", "description": "城市名称" },
          "unit": { "type": "string", "enum": ["celsius", "fahrenheit"] }
        },
        "required": ["location"]
      }
    }
  ]
}
```
> 工具定义：`type` 字段省略，顶层用 `name`、`description`、`input_schema`。

**响应（助手消息包含 `tool_use`）**
```json
{
  "id": "msg_123",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "tool_use",
      "id": "toolu_01A09q90...",
      "name": "get_current_weather",
      "input": {
        "location": "北京",
        "unit": "celsius"
      }
    }
  ],
  "stop_reason": "tool_use",
  "model": "claude-3-5-sonnet-20240620"
}
```
> 注意：`input` 是 **JSON 对象**，不是字符串。

**将工具结果发回（二次请求）**
```json
{
  "model": "claude-3-5-sonnet-20240620",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": "北京今天天气怎么样？"
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "toolu_01A09q90...",
          "name": "get_current_weather",
          "input": { "location": "北京", "unit": "celsius" }
        }
      ]
    },
    {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "tool_use_id": "toolu_01A09q90...",
          "content": "地点：北京，当前温度：25度（单位：celsius），天气：晴朗"
        }
      ]
    }
  ]
}
```
> **关键差异**：工具结果以 `role: "user"` 消息发送，且使用 `tool_result` 类型块。

---

### 对比总结

| 平台      | 工具定义位置                  | 工具调用字段                    | arguments 格式 | 工具结果消息角色                                | 特殊要求                                             |
| --------- | ----------------------------- | ------------------------------- | -------------- | ----------------------------------------------- | ---------------------------------------------------- |
| Ollama    | `tools` 数组，`function` 对象 | `message.tool_calls`            | 对象或字符串   | `role: "tool"`                                  | 需 `tool_call_id` 关联                               |
| OpenAI    | 同上                          | `choices[0].message.tool_calls` | 总是字符串     | `role: "tool"`                                  | 同上                                                 |
| Anthropic | `tools` 数组，扁平化          | `content[].type: "tool_use"`    | 总是对象       | `role: "user"`，`content` 中为 `tool_result` 块 | 必须使用 `max_tokens`，无 `stream`（流式需单独设置） |

这些样例可以直接用于调试或编写适配层，帮助你统一处理不同后端的函数调用。