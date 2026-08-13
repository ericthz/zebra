package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	data := []byte(`
# 注释行
KEY1=value1
   KEY2   =   value with spaces
export KEY3=exported
KEY4='单引号值'
KEY5="双引号值"
KEY6=

`)
	vars, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"KEY1": "value1",
		"KEY2": "value with spaces",
		"KEY3": "exported",
		"KEY4": "单引号值",
		"KEY5": "双引号值",
		"KEY6": "",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Fatalf("Parse[%s] = %q, want %q（全部: %v）", k, vars[k], v, vars)
		}
	}
}

func TestParseErrors(t *testing.T) {
	// 缺 '=' 的行应报错并带行号
	if _, err := Parse([]byte("GOOD=1\nBAD LINE\n")); err == nil || !strings.Contains(err.Error(), "2") {
		t.Fatalf("缺 = 的行应报错并带行号: %v", err)
	}
	// 空 key 应报错
	if _, err := Parse([]byte("=value\n")); err == nil {
		t.Fatal("空 key 应报错")
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	// 文件里定义两个变量
	if err := os.WriteFile(envFile, []byte("FROM_FILE=yes\nFROM_BOTH=file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 系统环境变量已占用 FROM_BOTH → 优先级更高，不被 .env 覆盖
	t.Setenv("FROM_BOTH", "env")

	n, err := Load(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 { // 只有 FROM_FILE 被设置
		t.Fatalf("应设置 1 个变量，实际 %d", n)
	}
	if os.Getenv("FROM_FILE") != "yes" {
		t.Fatalf("FROM_FILE 应来自 .env: %q", os.Getenv("FROM_FILE"))
	}
	if os.Getenv("FROM_BOTH") != "env" {
		t.Fatalf("已存在的环境变量不应被覆盖: %q", os.Getenv("FROM_BOTH"))
	}
}

func TestLoadDefaultMissing(t *testing.T) {
	// internal/config 目录下没有 .env → 静默返回 0，不算错误
	n, err := LoadDefault()
	if err != nil {
		t.Fatalf("缺 .env 不应报错: %v", err)
	}
	if n != 0 {
		t.Fatalf("缺 .env 应返回 0，实际 %d", n)
	}
}

func TestLoadParseErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, ".env")
	if err := os.WriteFile(bad, []byte("OK=1\nBAD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Fatal("解析错误应向上传播")
	}
}
