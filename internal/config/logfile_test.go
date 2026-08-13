package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLogFile(t *testing.T) {
	// "off" → 禁用文件日志（nil writer），不建文件
	w, closeFn, err := OpenLogFile("off")
	if err != nil {
		t.Fatal(err)
	}
	if w != nil {
		t.Fatal("off 应返回 nil writer")
	}
	closeFn()

	// 指定路径 → 创建文件、可写
	path := filepath.Join(t.TempDir(), "server.log")
	w, closeFn, err = OpenLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello log\n")); err != nil {
		t.Fatal(err)
	}
	closeFn()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello log\n" {
		t.Fatalf("日志内容异常: %q %v", data, err)
	}

	// 非法路径 → 报错
	if _, _, err := OpenLogFile(filepath.Join(t.TempDir(), "no-such-dir", "x.log")); err == nil {
		t.Fatal("非法路径应报错")
	}
}
