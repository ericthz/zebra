package console

import (
	"strings"
	"testing"
)

func TestPadAlignment(t *testing.T) {
	// CJK/emoji 计 2 格、ASCII 计 1 格：不同内容补到同一宽度后，冒号列应齐齐的
	a := Pad("🤖 模型", 12)
	b := Pad("🛠 工具", 12)
	c := Pad("🔌 MCP", 12)
	if displayWidth(a) != displayWidth(b) || displayWidth(a) != displayWidth(c) {
		t.Fatalf("补齐后宽度应一致: %q(%d) %q(%d) %q(%d)",
			a, displayWidth(a), b, displayWidth(b), c, displayWidth(c))
	}
	// 中文 2 格验证："ab" 宽 2，"中文" 宽 4
	if displayWidth("中文") != 4 || displayWidth("ab") != 2 {
		t.Fatalf("宽度计算错误: 中文=%d ab=%d", displayWidth("中文"), displayWidth("ab"))
	}
}

func TestColumns(t *testing.T) {
	names := []string{"calculator", "convert_units", "fetch_url", "get_ip_info", "list_dir"}
	rows := Columns(names, 3)
	if len(rows) != 2 {
		t.Fatalf("13→3 列应 2 行，实际 %d: %v", len(rows), rows)
	}
	// 行优先顺序：第一行是前 3 个
	if !strings.HasPrefix(rows[0], "calculator") || !strings.Contains(rows[0], "convert_units") || !strings.Contains(rows[0], "fetch_url") {
		t.Fatalf("行优先顺序错误: %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], "get_ip_info") || !strings.Contains(rows[1], "list_dir") {
		t.Fatalf("第二行顺序错误: %q", rows[1])
	}
	// 同一列对齐：第二列（convert_units / list_dir）的起点列号应一致
	c0 := strings.Index(rows[0], "convert_units")
	c1 := strings.Index(rows[1], "list_dir")
	if c0 != c1 || c0 < 2 {
		t.Fatalf("第二列起点不一致: %d vs %d\n%q\n%q", c0, c1, rows[0], rows[1])
	}

	// 空输入安全
	if Columns(nil, 4) != nil {
		t.Fatal("空输入应返回 nil")
	}
}
