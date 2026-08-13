package console

import "testing"

func TestWrapColor(t *testing.T) {
	got := wrapColor("◆", ColorModel)
	want := "\x1b[38;5;45m◆\x1b[0m"
	if got != want {
		t.Fatalf("wrapColor = %q, want %q", got, want)
	}
}

func TestStripANSI(t *testing.T) {
	s := "\x1b[38;5;45m◆\x1b[0m 模型"
	if got := stripANSI(s); got != "◆ 模型" {
		t.Fatalf("stripANSI = %q", got)
	}
	if stripANSI("plain") != "plain" {
		t.Fatal("无转义应原样返回")
	}
}

func TestDisplayWidthIgnoresANSI(t *testing.T) {
	plain := "◆ 模型"
	colored := wrapColor("◆", ColorModel) + " 模型"
	if displayWidth(plain) != displayWidth(colored) {
		t.Fatalf("ANSI 不应影响宽度: %d vs %d", displayWidth(plain), displayWidth(colored))
	}
	if displayWidth(colored) != 6 { // ◆(1) + 空格(1) + 模型(4)
		t.Fatalf("宽度错误: %d", displayWidth(colored))
	}
	// Pad 对齐不受颜色影响：彩色标签与纯文本标签补到同一宽度
	if displayWidth(Pad(colored, 12)) != displayWidth(Pad("▲ 工具", 12)) {
		t.Fatal("彩色标签补齐后宽度应与纯文本一致")
	}
}

func TestSymbolPlainWhenDisabled(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if ColorEnabled() {
		t.Fatal("NO_COLOR 应关闭颜色")
	}
	if got := Symbol("◆", ColorModel); got != "◆" {
		t.Fatalf("颜色关闭时应返回原符号: %q", got)
	}
}
