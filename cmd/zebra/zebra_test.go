package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLogFile(t *testing.T) {
	// ZEBRA_LOG=off → stderr，不建文件
	w, closeFn, err := openLogFile("off")
	if err != nil {
		t.Fatal(err)
	}
	if w != os.Stderr {
		t.Fatal("off 应返回 stderr")
	}
	closeFn()

	// 指定路径 → 创建文件，可写
	path := filepath.Join(t.TempDir(), "zebra.log")
	w, closeFn, err = openLogFile(path)
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
	if _, _, err := openLogFile(filepath.Join(t.TempDir(), "no-such-dir", "x.log")); err == nil {
		t.Fatal("非法路径应报错")
	}
}
