// 本地执行能力（P2，Agentic 分水岭）—— 文件系统访问。
//
// 背景：zebra 此前的工具全部是"调用外部 API"，Agent 无法读写本地文件。
// 而成熟 Agent（Claude Code / Codex 等）的核心价值恰恰是"替你操作文件系统"。
// 本文件实现 3 个文件类工具，配合 exec_shell.go 的命令执行，构成最小 Agentic 能力。
//
// 安全设计（务必理解）：
//  1. 路径沙箱：所有读写被限制在 ExecSandbox.WorkDir 白名单目录内，
//     用 filepath.Clean + 前缀校验 防目录穿越（../ 逃逸）。
//  2. 只读模式：ExecSandbox.ReadOnly=true 时禁用写文件（默认开启只读更安全）。
//  3. 高危标记：写文件/执行命令声明 RiskLevel=2，配合 registry 的
//     二次确认 + 角色白名单（仅 admin）双保险（D20）。
//
// 生产演化方向：
//   - 多用户时按用户分配独立工作目录（/sandbox/<user>/），真正隔离
//   - 写文件前自动备份 / 生成 diff，支持回滚
//   - 文件类型/大小白名单（防病毒/超大文件）
package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExecSandbox 本地执行沙箱配置（所有本地工具共享）。
type ExecSandbox struct {
	WorkDir   string        // 允许读写的根目录（白名单）
	ReadOnly  bool          // true=只读模式，禁止写文件与执行命令
	Timeout   time.Duration // 命令执行超时
	MaxOutput int           // 命令输出最大字节（截断，防刷屏）
}

// NewExecSandbox 构造沙箱；workDir 自动绝对路径化并创建。
func NewExecSandbox(workDir string, readOnly bool) *ExecSandbox {
	abs, _ := filepath.Abs(workDir)
	return &ExecSandbox{
		WorkDir:   abs,
		ReadOnly:  readOnly,
		Timeout:   10 * time.Second,
		MaxOutput: 8 * 1024,
	}
}

// safePath 校验并解析"用户给的相对/绝对路径"是否落在 WorkDir 内。
// 返回安全的绝对路径；越界（目录穿越）返回错误。这是本地执行的第一道防线。
func (s *ExecSandbox) safePath(p string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.WorkDir, abs) // 相对路径一律锚定到工作目录
	}
	clean := filepath.Clean(abs)
	// 前缀校验：clean 必须等于或位于 WorkDir 之内
	if clean != s.WorkDir && !strings.HasPrefix(clean, s.WorkDir+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界（禁止访问工作目录之外）: %s", p)
	}
	return clean, nil
}

// ---------------- 工具：列出目录 ----------------

// ListDirTool 列出目录内容（只读，安全）。
type ListDirTool struct{ Sandbox *ExecSandbox }

func (t *ListDirTool) Name() string { return "list_dir" }
func (t *ListDirTool) Description() string {
	return "列出指定目录下的文件和子目录（被沙箱限制在工作目录内）。"
}
func (t *ListDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "目录路径（相对工作目录或绝对路径）"},
		},
		"required": []string{"path"},
	}
}
func (t *ListDirTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	dir, err := t.Sandbox.safePath(StringArg(args, "path"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		kind := "📄"
		if e.IsDir() {
			kind = "📁"
		}
		fmt.Fprintf(&b, "%s %s\n", kind, e.Name())
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ---------------- 工具：读文件 ----------------

// ReadFileTool 读文件内容（只读，安全）。
type ReadFileTool struct{ Sandbox *ExecSandbox }

func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "读取文本文件内容（限制在工作目录内，自动截断超长文件）。"
}
func (t *ReadFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "文件路径"},
		},
		"required": []string{"path"},
	}
}
func (t *ReadFileTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	p, err := t.Sandbox.safePath(StringArg(args, "path"))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	const cap = 8 * 1024
	if len(data) > cap {
		return string(data[:cap]) + "\n…（文件过长已截断）", nil
	}
	return string(data), nil
}

// ---------------- 工具：写文件 ----------------

// WriteFileTool 写文件（高危：需二次确认 + 仅 admin；只读模式禁用）。
type WriteFileTool struct{ Sandbox *ExecSandbox }

func (t *WriteFileTool) Name() string { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "写入文本文件（覆盖已存在内容）。高危操作，需要二次确认。"
}
func (t *WriteFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":    map[string]interface{}{"type": "string", "description": "目标文件路径"},
			"content": map[string]interface{}{"type": "string", "description": "要写入的内容"},
		},
		"required": []string{"path", "content"},
	}
}
func (t *WriteFileTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.Sandbox.ReadOnly {
		return "", fmt.Errorf("当前为只读模式，禁止写文件")
	}
	p, err := t.Sandbox.safePath(StringArg(args, "path"))
	if err != nil {
		return "", err
	}
	content := StringArg(args, "content")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %d 字节到 %s", len(content), p), nil
}

// RiskLevel 声明写文件为高危（2 级：必须二次确认）。
func (t *WriteFileTool) RiskLevel() int { return 2 }

// AllowedRoles 仅 admin 可用。
func (t *WriteFileTool) AllowedRoles() []string { return []string{"admin"} }

// 编译期断言：文件工具实现 Tool 接口。
var (
	_ Tool  = (*ListDirTool)(nil)
	_ Tool  = (*ReadFileTool)(nil)
	_ Tool  = (*WriteFileTool)(nil)
	_ Risky = (*WriteFileTool)(nil)
)
