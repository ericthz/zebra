package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 构造一个临时沙箱：root 作为 WorkDir，root/secret.txt 放在工作目录外。
func newTestSandbox(t *testing.T, readOnly bool) (*ExecSandbox, string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// 工作目录外的敏感文件（验证路径越界拦截）
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewExecSandbox(work, readOnly), root
}

// TestPathTraversal 目录穿越必须被拦截。
func TestPathTraversal(t *testing.T) {
	sb, root := newTestSandbox(t, false)

	// 相对路径逃逸
	if _, err := sb.safePath("../secret.txt"); err == nil {
		t.Fatal("相对路径穿越应被拦截")
	}
	// 绝对路径逃逸
	if _, err := sb.safePath(filepath.Join(root, "secret.txt")); err == nil {
		t.Fatal("绝对路径越界应被拦截")
	}
	// 合法路径应通过
	ok, err := sb.safePath("a/b.txt")
	if err != nil {
		t.Fatalf("合法路径应通过: %v", err)
	}
	if !strings.HasPrefix(ok, sb.WorkDir) {
		t.Fatalf("合法路径应落在工作目录内: %s", ok)
	}
}

// TestFileTools 写文件 → 读文件 → 列目录 全链路。
func TestFileTools(t *testing.T) {
	sb, _ := newTestSandbox(t, false)

	write := &WriteFileTool{Sandbox: sb}
	if _, err := write.Execute(context.Background(), map[string]interface{}{
		"path": "hello.txt", "content": "hello zebra",
	}); err != nil {
		t.Fatal(err)
	}

	read := &ReadFileTool{Sandbox: sb}
	out, err := read.Execute(context.Background(), map[string]interface{}{"path": "hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello zebra") {
		t.Fatalf("读回内容不符: %q", out)
	}

	list := &ListDirTool{Sandbox: sb}
	out, _ = list.Execute(context.Background(), map[string]interface{}{"path": "."})
	if !strings.Contains(out, "hello.txt") {
		t.Fatalf("目录应包含 hello.txt: %q", out)
	}
}

// TestReadOnlyMode 只读模式应拒绝写文件。
func TestReadOnlyMode(t *testing.T) {
	sb, _ := newTestSandbox(t, true)
	w := &WriteFileTool{Sandbox: sb}
	if _, err := w.Execute(context.Background(), map[string]interface{}{"path": "x.txt", "content": "x"}); err == nil {
		t.Fatal("只读模式应拒绝写文件")
	}
}

// TestRunCommand 命令执行：正常、输出截断、黑名单、超时。
func TestRunCommand(t *testing.T) {
	sb, _ := newTestSandbox(t, false)
	rc := &RunCommandTool{Sandbox: sb}

	// 正常执行
	out, err := rc.Execute(context.Background(), map[string]interface{}{"command": "echo hello"})
	if err != nil || !strings.Contains(out, "hello") {
		t.Fatalf("echo 应成功，got %q err=%v", out, err)
	}

	// 黑名单拦截
	if _, err := rc.Execute(context.Background(), map[string]interface{}{"command": "rm -rf /"}); err == nil {
		t.Fatal("黑名单命令应被拒绝")
	}

	// 只读模式拒绝
	ro, _ := newTestSandbox(t, true)
	rcRO := &RunCommandTool{Sandbox: ro}
	if _, err := rcRO.Execute(context.Background(), map[string]interface{}{"command": "ls"}); err == nil {
		t.Fatal("只读模式应拒绝执行命令")
	}

	// 超时强杀
	sbTimeout := NewExecSandbox(sb.WorkDir, false)
	sbTimeout.Timeout = 300 * time.Millisecond
	rcT := &RunCommandTool{Sandbox: sbTimeout}
	if _, err := rcT.Execute(context.Background(), map[string]interface{}{"command": "sleep 5"}); err == nil {
		t.Fatal("超时命令应报错")
	}
}
