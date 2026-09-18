package observe

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintBanner(t *testing.T) {
	var buf bytes.Buffer
	PrintBanner(&buf, "Zebra server — AI Agent API")
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 8 { // 1 行前置空行 + 6 行字母 + 1 行副标题
		t.Fatalf("banner 行数错误: %d\n%s", len(lines), out)
	}
	if lines[0] != "" {
		t.Fatalf("首行应为空白行: %q", lines[0])
	}
	for i, l := range lines[1:7] {
		if !strings.ContainsAny(l, "█╔╗╚╝═") { // 块状字母由这些像素组成
			t.Fatalf("第 %d 行缺少字母像素: %q", i+1, l)
		}
	}
	if !strings.Contains(lines[7], "Zebra server") {
		t.Fatalf("副标题缺失: %q", lines[7])
	}

	// 空副标题：前置空行 + 只有 6 行字母
	buf.Reset()
	PrintBanner(&buf, "")
	if n := strings.Count(buf.String(), "\n"); n != 8 { // 前置空行 + 6 行 + 结尾空行
		t.Fatalf("空副标题时行数错误: %d", n)
	}
	if !strings.HasPrefix(buf.String(), "\n") {
		t.Fatalf("空副标题时首行应为空白行")
	}
}
