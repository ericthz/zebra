package console

import (
	"testing"
)

func TestPadAlignment(t *testing.T) {
	// CJK/emoji 计 2 格、ASCII 计 1 格：不同内容补到同一宽度后，冒号列应齐齐的
	a := Pad("◆ 模型", 12)
	b := Pad("▲ 工具", 12)
	c := Pad("● MCP", 12)
	if displayWidth(a) != displayWidth(b) || displayWidth(a) != displayWidth(c) {
		t.Fatalf("补齐后宽度应一致: %q(%d) %q(%d) %q(%d)",
			a, displayWidth(a), b, displayWidth(b), c, displayWidth(c))
	}
	// 中文 2 格验证："ab" 宽 2，"中文" 宽 4
	if displayWidth("中文") != 4 || displayWidth("ab") != 2 {
		t.Fatalf("宽度计算错误: 中文=%d ab=%d", displayWidth("中文"), displayWidth("ab"))
	}
	// emoji 分支仍按 2 格（保留对 U+1F000+ 的处理覆盖）
	if displayWidth("🤖") != 2 {
		t.Fatalf("emoji 应按 2 格: %d", displayWidth("🤖"))
	}
}
