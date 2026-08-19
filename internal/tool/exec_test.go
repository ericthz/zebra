package tool

import (
	"bytes"
	"context"
	"fmt"
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

// TestPathSymlinkEscape 工作目录内的 symlink 指向外部 → 必须被拦截（五8）。
func TestPathSymlinkEscape(t *testing.T) {
	sb, root := newTestSandbox(t, false)

	// 工作目录外建一个敏感文件，再在工作目录内建指向它的 symlink
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sb.WorkDir, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}

	// 读 symlink 应被拦截（EvalSymlinks 发现真实路径在工作目录外）
	if _, err := sb.safePath("leak.txt"); err == nil {
		t.Fatal("symlink 指向工作目录外应被拦截")
	}

	// 工作目录内的 symlink 指向内部文件 → 应放行
	inner := filepath.Join(sb.WorkDir, "inner.txt")
	if err := os.WriteFile(inner, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inner.txt", filepath.Join(sb.WorkDir, "link-inner.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.safePath("link-inner.txt"); err != nil {
		t.Fatalf("指向工作目录内部的 symlink 应放行: %v", err)
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

// TestRunCommandKillsChildGroup 六9：超时强杀必须连 shell 的后台子进程一起
// 终止（Setpgid + kill(-pgid)）。若只杀 shell，后台 sleep 会成为孤儿存活，
// 违背"超时整组强杀"的承诺。
func TestRunCommandKillsChildGroup(t *testing.T) {
	if os.Getenv("ZEBRA_SKIP_PROCGROUP") != "" {
		t.Skip("跳过进程组强杀验证")
	}
	// 用写文件探针验证子进程已死：后台子进程先睡眠，超时后被强杀；
	// 若存活，会在其醒来后写入 marker。等待超过子进程睡眠时长后检查。
	sb := NewExecSandbox(t.TempDir(), false)
	sb.Timeout = 200 * time.Millisecond
	rc := &RunCommandTool{Sandbox: sb}
	marker := filepath.Join(sb.WorkDir, "child-survived")
	// 前台 sleep 5 负责触发超时；后台子进程 sleep 1 后在 shell 被杀时
	// 应随进程组一并终止，不会醒来写 marker。
	cmd := fmt.Sprintf("sh -c '(sleep 1 && touch %s) & sleep 5'", marker)
	if _, err := rc.Execute(context.Background(), map[string]interface{}{"command": cmd}); err == nil {
		t.Fatal("超时命令应报错")
	}
	// 等 1.5s（> 子进程 1s 睡眠）：若子进程没被杀，marker 会出现
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("超时后后台子进程仍存活（进程组未强杀，六9）")
	}
}

// TestReadFileHugeNoOOM 六6：read_file 读取超大文件不得占满内存——
// LimitReader 只读 cap+1 字节即返回截断结果。
func TestReadFileHugeNoOOM(t *testing.T) {
	sb, _ := newTestSandbox(t, false)
	read := &ReadFileTool{Sandbox: sb}

	// 在沙箱内造一个 32MB 的大文件
	huge := filepath.Join(sb.WorkDir, "huge.bin")
	if err := os.WriteFile(huge, bytes.Repeat([]byte{'x'}, 32<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var out string
	var err error
	go func() {
		defer close(done)
		out, err = read.Execute(context.Background(), map[string]interface{}{"path": "huge.bin"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("read_file 大文件卡死（应被 LimitReader 截断）")
	}
	if err != nil {
		t.Fatalf("读取应成功截断返回: %v", err)
	}
	if len(out) > 8*1024+64 {
		t.Fatalf("输出应被截断到 ~8KB，实际 %d 字节", len(out))
	}
	if !strings.Contains(out, "已截断") {
		t.Fatalf("大文件应标记截断: %q", out)
	}
}

// TestRunCommandOutputCapNoOOM 六6：命令无限刷输出（yes）不得 OOM——
// 读满 MaxOutput 即杀进程并返回截断结果。
func TestRunCommandOutputCapNoOOM(t *testing.T) {
	sb, _ := newTestSandbox(t, false)
	sb.MaxOutput = 1024
	rc := &RunCommandTool{Sandbox: sb}

	done := make(chan struct{})
	var out string
	var err error
	go func() {
		defer close(done)
		out, err = rc.Execute(context.Background(), map[string]interface{}{"command": "yes x"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run_command `yes` 卡死（输出应被限量并杀进程）")
	}
	if err == nil {
		t.Fatalf("被强制终止的命令应报错，实际 out=%q", out)
	}
	if len(out) > 1024+64 {
		t.Fatalf("输出应被截断到 ~1KB，实际 %d 字节", len(out))
	}
}
