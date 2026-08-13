// Package console 终端排版小工具（P31 增强）：解决 emoji/CJK/ASCII 混排
// 时的对齐问题，并提供多列换行，让启动清单与执行痕迹更易读。
//
// 为什么需要：fmt 的 %-Ns 按"字节/runes"补齐，但终端里 CJK 字符占 2 格、
// emoji 占 2 格、ASCII 占 1 格。直接混排（如 "⚙️ 模型" vs "🛠 工具"）
// 会让标签列参差不齐。本包按"近似显示宽度"补齐与排版。
package console

import (
	"strings"
)

// displayWidth 近似终端显示宽度：CJK/全角/emoji 计 2，其余计 1。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1F000: // emoji（U+1F000+，按 2 格估算）
			w += 2
		case r >= 0x1100 && (r <= 0x115F || // 谚文字母
			r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) || // CJK 等
			(r >= 0xAC00 && r <= 0xD7A3) || // 谚文音节
			(r >= 0xF900 && r <= 0xFAFF) || // CJK 兼容
			(r >= 0xFE30 && r <= 0xFE4F) || // CJK 兼容形式
			(r >= 0xFF00 && r <= 0xFF60) || // 全角
			(r >= 0xFFE0 && r <= 0xFFE6)):
			w += 2
		default:
			w++
		}
	}
	return w
}

// Pad 按显示宽度补齐空格（不足则原样返回）。
func Pad(s string, width int) string {
	if w := displayWidth(s); w >= width {
		return s
	} else {
		return s + strings.Repeat(" ", width-w)
	}
}

// Columns 把 items 排成 cols 列的多行文本（每行一个字符串，已按显示宽度对齐）。
// 典型用途：工具名列表避免"一行长串"，改为网格布局。
func Columns(items []string, cols int) []string {
	if len(items) == 0 {
		return nil
	}
	if cols <= 0 {
		cols = 4
	}
	max := 0
	for _, it := range items {
		if w := displayWidth(it); w > max {
			max = w
		}
	}
	cell := max + 2
	rows := (len(items) + cols - 1) / cols
	out := make([]string, rows)
	for i, it := range items {
		out[i/cols] += Pad(it, cell) // 行优先填充，按显示宽度对齐
	}
	for i := range out {
		out[i] = strings.TrimRight(out[i], " ")
	}
	return out
}
