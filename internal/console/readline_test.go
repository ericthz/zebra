package console

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestReadLineRawMultibyteBackspace 中文（3 字节/字符）按字符退格删除，
// 不应残留字节残片（P38 修复的核心回归用例）。
func TestReadLineRawMultibyteBackspace(t *testing.T) {
	input := "你好世界" + "\x7f\x7f\x7f\x7f" + "exit\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out)
	if err != nil {
		t.Fatal(err)
	}
	if line != "exit" {
		t.Fatalf("行内容应为 exit，实际 %q", line)
	}
	// 每个中文字符宽 2 格，退格应做 4 次 2 格抹除（\b\b 空格 \b\b）
	if got := strings.Count(out.String(), "\b\b  \b\b"); got != 4 {
		t.Fatalf("中文退格应出现 4 次 2 格抹除，实际 %d\n%s", got, out.String())
	}
}

func TestReadLineRawAscii(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("hello\r")), &out)
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Fatalf("got %q", line)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatal("应回显 hello")
	}
}

func TestReadLineRawBackspaceAscii(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("abc\x7f\x7fxyz\n")), &out)
	if err != nil {
		t.Fatal(err)
	}
	if line != "axyz" { // 退格删掉 bc，剩 axyz
		t.Fatalf("got %q", line)
	}
}

func TestReadLineRawEmptyAndEOF(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("\r")), &out)
	if err != nil || line != "" {
		t.Fatalf("空行: %q %v", line, err)
	}
	// EOF：已输入内容返回内容 + io.EOF（调用方据此结束）
	line, err = readLineRaw(bufio.NewReader(strings.NewReader("abc")), &out)
	if line != "abc" || err != io.EOF {
		t.Fatalf("EOF 场景: %q %v", line, err)
	}
}
