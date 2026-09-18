// Package console 终端排版小工具（增强）：解决 emoji/CJK/ASCII 混排
// 时的对齐问题，让启动清单与执行痕迹更易读。
//
// 为什么需要：fmt 的 %-Ns 按"字节/runes"补齐，但终端里 CJK 字符占 2 格、
// emoji 占 2 格、ASCII 占 1 格。直接混排 emoji、CJK 与 ASCII 会让标签列
// 参差不齐。本包按"近似显示宽度"补齐与排版。
//
// 使用约定：行首图标统一用"单字符几何符号"（◆▲■●▣▤♪◐◎），它们是
// 1 格宽的纯文本符号、无 emoji 呈现歧义，且每个类别用不同符号避免混淆；
// 若确需 emoji，请选 Unicode `Emoji_Presentation=Yes` 的字符（2 格），
// 避免使用"默认文本呈现"（Emoji_Presentation=No）的 emoji——它们可能被
// 终端渲染成 1 格导致错位。
package console

import (
	"strings"
)

// displayWidth 近似终端显示宽度：CJK/全角/emoji 计 2，其余计 1；
// 自动忽略 ANSI 颜色转义（它们是零宽度控制序列，不应计入占位）。
func displayWidth(s string) int {
	return displayWidthRunes(stripANSI(s))
}

// Width 返回字符串的近似终端显示宽度（CJK/全角/emoji 计 2，其余计 1；
// 忽略 ANSI 转义）。供外部做"字节列 vs 显示列"换算。
func Width(s string) int {
	return displayWidth(s)
}

func displayWidthRunes(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1F000: // emoji（U+1F000+，按 2 格估算；见包注释的使用约定）
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

// stripANSI 去除 ANSI 转义序列（本包只产生 CSI SGR：ESC [ ... m）。
// 用于宽度计算与测试断言，避免把控制字节当成可显示字符。
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				for j < len(s) && s[j] != 'm' {
					j++
				}
				if j < len(s) {
					j++ // 跳过结尾 'm'
				}
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
