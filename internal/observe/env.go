// 环境变量配置清单（/env）：把代码里读取的全部配置键汇总成可展示的清单，
// 供 REPL /env 命令列出"生效值/默认值"，排查"配了没生效"类问题。
//
// 设计要点：
//   - 与 .env.example 同源：键、默认值、说明在此维护一份，终端展示与文档一致。
//   - 只列"代码真正读取"的键（避免列了却不生效的假配置）。
//   - 生效值 = 环境变量已设置则用环境值，否则用默认值；默认值空表示"未配置=功能关闭"。
package observe

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ericthz/zebra/internal/console"
)

// EnvSpec 一个配置键的展示说明。
type EnvSpec struct {
	Key     string // 环境变量名
	Default string // 默认值（空=未配置即关闭）
	Group   string // 分组名（.env.example 分区）
	Desc    string // 一句话说明
}

// envSpecs 全部可展示配置键（与 internal 各装配点的 os.Getenv 一一对应）。
// 注意：值以进程实际环境为准，这里只提供默认值与说明。
var envSpecs = []EnvSpec{
	// ---- LLM / 备选模型 ----
	{"OLLAMA_BASE_URL", "http://localhost:11434", "LLM", "主模型 Ollama 服务地址"},
	{"OLLAMA_MODEL", "qwen3.5:0.8b-mlx", "LLM", "主模型名"},
	{"FALLBACK_BASE_URL", "", "LLM", "备选模型 OpenAI 兼容地址（主模型故障降级）"},
	{"FALLBACK_MODEL", "", "LLM", "备选模型名"},
	{"ANTHROPIC_API_KEY", "", "LLM", "Anthropic 备选模型 API Key"},
	{"ANTHROPIC_MODEL", "", "LLM", "Anthropic 备选模型名"},
	{"ANTHROPIC_BASE_URL", "", "LLM", "Anthropic 备选服务地址"},
	{"OPENAI_API_KEY", "", "LLM", "OpenAI 兼容 API Key（备选/嵌入/语音共用）"},
	{"OPENAI_BASE_URL", "", "LLM", "OpenAI 兼容服务地址"},
	{"EMBED_MODEL", "nomic-embed-text:v1.5", "LLM", "本地嵌入模型名"},

	// ---- 记忆 / 知识库 ----
	{"QDRANT_URL", "", "记忆", "Qdrant 服务地址（设置即启用长期记忆）"},
	{"QDRANT_COLLECTION", "zebra_mem", "记忆", "Qdrant 集合名"},
	{"EMBED_VECTOR_SIZE", "768", "记忆", "嵌入向量维度（与嵌入模型匹配）"},
	{"OPENAI_EMBED_MODEL", "text-embedding-3-small", "记忆", "OpenAI 兼容嵌入模型名"},
	{"REDIS_URL", "", "记忆", "Redis 地址（设置即启用 Redis 记忆/会话）"},
	{"REDIS_PASSWORD", "", "记忆", "Redis 密码"},
	{"REDIS_DB", "0", "记忆", "Redis 库号"},
	{"RAG_CHUNK_SIZE", "600", "知识库", "RAG 文档分块大小（字符）"},
	{"RAG_CHUNK_OVERLAP", "100", "知识库", "RAG 分块重叠（字符）"},
	{"ZEBRA_RAG_RERANK", "0", "知识库", "检索后重排序（1=开启）"},

	// ---- 服务 / 鉴权 ----
	{"ADDR", ":8080", "服务", "server 监听地址"},
	{"ADMIN_KEY", "admin-key", "服务", "管理员 API Key（RBAC）"},
	{"USER_KEY", "user-key", "服务", "普通用户 API Key（RBAC）"},
	{"EXEC_WORKDIR", "workspace", "沙箱", "本地执行沙箱工作目录白名单"},
	{"EXEC_READONLY", "1", "沙箱", "沙箱只读模式（1=禁止写文件/执行命令）"},
	{"MCP_MODE", "", "MCP", "MCP 模式：stdio/http（设置即启用）"},
	{"MCP_COMMAND", "", "MCP", "MCP stdio 子进程命令"},
	{"MCP_HTTP_URL", "", "MCP", "MCP HTTP 远端服务地址"},
	{"MCP_HTTP_TOKEN", "", "MCP", "MCP HTTP 访问令牌（设置即要求 Bearer）"},
	{"WEBHOOK_URL", "", "通知", "Webhook 主动出站地址（对话完成推送）"},
	{"WEBHOOK_SECRET", "", "通知", "Webhook 签名密钥"},

	// ---- Agent 执行参数 ----
	{"REACT_MAX_STEPS", "6", "执行", "ReAct 推理-行动最大步数"},
	{"SELF_CONSISTENT_SAMPLES", "3", "执行", "自一致性采样份数"},
	{"CONTEXT_MAX_TOKENS", "4000", "执行", "上下文窗口 token 预算"},
	{"SUMMARY_MAX_CHARS", "600", "执行", "对话摘要最大字符数"},
	{"MAX_TOOL_TURNS", "5", "执行", "最大工具调用轮数"},
	{"ZEBRA_QUERY_REWRITE", "0", "执行", "RAG 查询改写（1=开启）"},
	{"ZEBRA_SUMMARIZER", "truncate", "执行", "摘要压缩器：truncate/llm"},

	// ---- 缓存 / 容错 ----
	{"CACHE_MAX_ENTRIES", "200", "缓存", "语义缓存最大条目数"},
	{"CACHE_THRESHOLD", "0.92", "缓存", "语义缓存命中阈值（0~1）"},
	{"HTTP_TIMEOUT", "60", "容错", "LLM 请求超时（秒）"},
	{"HTTP_RETRIES", "1", "容错", "失败重试次数"},
	{"HTTP_BACKOFF_MS", "300", "容错", "重试退避基数（毫秒）"},
	{"CIRCUIT_THRESHOLD", "5", "容错", "熔断连续失败阈值"},
	{"CIRCUIT_COOLDOWN_SEC", "30", "容错", "熔断恢复冷却（秒）"},

	// ---- 日志 ----
	{"ZEBRA_LOG", "zebra.log", "日志", "CLI 诊断日志路径（off=stderr）"},
	{"LOG_FILE", "server.log", "日志", "server JSON 日志双写文件（off=仅 stdout）"},

	// ---- 语音 ----
	{"VOICE_BASE_URL", "", "语音", "语音服务 OpenAI 兼容地址（设置即启用 ASR/TTS）"},
	{"VOICE_API_KEY", "", "语音", "语音服务 API Key"},
	{"VOICE_ASR_MODEL", "whisper-1", "语音", "ASR 转写模型名"},
	{"VOICE_TTS_MODEL", "tts-1", "语音", "TTS 合成模型名"},
	{"VOICE_TONE", "alloy", "语音", "TTS 音色"},

	// ---- 评测 / 画像 ----
	{"ZEBRA_SHADOW_MODEL", "", "评测", "影子评测候选模型（设置即启用）"},
	{"ZEBRA_SHADOW_OPENAI", "0", "评测", "候选走 OpenAI 兼容后端（1=开启）"},
	{"ZEBRA_SHADOW_BASE_URL", "", "评测", "候选服务地址（默认同主后端）"},
	{"ZEBRA_SHADOW_SAMPLE", "10", "评测", "影子评测自动采样率 %（0=仅显式触发）"},
	{"SHADOW_STORE_MAX", "200", "评测", "影子评测记录上限"},
	{"JUDGE_BASE_URL", "", "评测", "独立评审模型地址（空=用生产 router）"},
	{"JUDGE_OPENAI", "0", "评测", "评审走 OpenAI 兼容（1=开启）"},
	{"JUDGE_MODEL", "", "评测", "评审模型名"},
	{"JUDGE_API_KEY", "", "评测", "评审模型 API Key"},
	{"EVAL_CASES_DIR", "test/eval/cases", "评测", "离线评测用例目录"},
	{"PROFILE_TTL_HOURS", "720", "画像", "画像事实保鲜期（小时，30 天）"},
	{"PROFILE_LLM", "1", "画像", "画像 LLM 语义抽取（0=纯规则）"},
	{"PROFILE_SWEEP_MINUTES", "60", "画像", "画像过期清扫周期（分钟）"},
}

// EnvSpecs 返回全部可展示配置键（调用方可直接传给 PrintEnv）。
func EnvSpecs() []EnvSpec {
	return envSpecs
}

// envValue 取键的生效值：环境已设置用环境值，否则默认值。
func envValue(spec EnvSpec) string {
	if v := os.Getenv(spec.Key); v != "" {
		return v
	}
	return spec.Default
}

// envSet 键是否被进程显式设置（区别于走默认值）。
func envSet(key string) bool { return os.Getenv(key) != "" }

// PrintEnv 打印配置清单：按分组输出，已显式设置的键标 *。
func PrintEnv(w io.Writer, specs []EnvSpec) {
	if len(specs) == 0 {
		return
	}
	var group string
	for _, s := range specs {
		if s.Group != group {
			group = s.Group
			fmt.Fprintf(w, "%s %s\n", console.Symbol("──", console.ColorTitle), group)
		}
		value := envValue(s)
		mark := "  "
		if envSet(s.Key) {
			mark = " *"
		}
		val := value
		if value == "" {
			val = "（未配置=关闭）"
		}
		fmt.Fprintf(w, "  %-24s = %s%s %s\n", s.Key, val, mark, s.Desc)
	}
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 44))
	fmt.Fprintf(w, "  * 表示已显式设置（环境变量/命令行），未标 * 走默认值或关闭状态\n")
}
