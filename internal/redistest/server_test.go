package redistest

import (
	"bufio"
	"strings"
	"testing"
)

// TestReadCommandMalformed 验证：畸形 RESP 命令返回错误而非越界 panic。
func TestReadCommandMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"非数组前缀", "GET k\r\n"},
		{"负数组长度", "*-3\r\n"},
		{"超长数组", "*999999\r\n"},
		{"非法数组长度", "*abc\r\n"},
		{"参数非 bulk", "*1\r\n+OK\r\n"},
		{"负参数长度", "*1\r\n$-5\r\n"},
		{"参数长度非数字", "*1\r\n$x\r\n"},
		{"空行", "\r\n"},
		{"裸换行", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readCommand(bufio.NewReader(strings.NewReader(tc.in))); err == nil {
				t.Fatalf("畸形输入 %q 应报错", tc.in)
			}
		})
	}
}

// TestExecEmpty 验证：空命令不 panic。
func TestExecEmpty(t *testing.T) {
	s := &Server{}
	if got := s.exec(nil); !strings.Contains(got, "ERR") {
		t.Fatalf("空命令应返回错误，实际 %q", got)
	}
}
