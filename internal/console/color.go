// 终端配色：给启动清单的行首符号加 256 色，提升辨识度。
//
// 设计要点：
//   - 使用 ANSI CSI SGR 转义（\x1b[38;5;Nm...\x1b[0m），零依赖、零宽度
//     （不会破坏 console.Pad 的对齐，宽度计算已忽略转义）。
//   - 自动开关：遵循 NO_COLOR 约定；非 TTY 输出（管道/重定向/CI）不上色，
//     避免日志文件被控制字符污染。
//   - 每个类别一种颜色，色相互不混淆；配套 256 色码见下方常量。
package console

import (
	"os"
	"strconv"
)

// 符号配色（256 色，xterm palette）。
const (
	ColorTitle  = 245 // ── 标题分隔：浅灰
	ColorModel  = 45  // ◆ 模型：天蓝
	ColorTool   = 208 // ▲ 工具：橙
	ColorSkill  = 205 // ■ 技能：粉/品红
	ColorMCP    = 82  // ● MCP：绿
	ColorMemory = 99  // ▣ 记忆：紫
	ColorKB     = 51  // ▤ 知识库：青
	ColorVoice  = 214 // ♪ 语音：金
	ColorShadow = 239 // ◐ 影子评测：深灰
	ColorRedis  = 196 // ◎ Redis：红
)

// ColorEnabled 是否输出颜色：NO_COLOR / TERM=dumb / 非 TTY 时关闭。
func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Symbol 返回行首符号：颜色开启时包 256 色转义，否则原样返回。
func Symbol(sym string, code int) string {
	if !ColorEnabled() {
		return sym
	}
	return wrapColor(sym, code)
}

// wrapColor 总是包上 256 色转义（供测试直接验证转义序列）。
func wrapColor(sym string, code int) string {
	return "\x1b[38;5;" + strconv.Itoa(code) + "m" + sym + "\x1b[0m"
}
