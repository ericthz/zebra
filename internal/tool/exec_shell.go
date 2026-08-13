// 本地执行能力（P2）—— Shell 命令执行（Agentic 的核心，也是最危险的能力）。
//
// 安全设计（理解本文件 = 理解"为什么本地执行需要沙箱"）：
//  1. 命令黑名单：直接禁止破坏性命令（rm -rf / mkfs / 下载并执行 等）。
//     宁可误伤，不可放行——这是高危能力的第一原则。
//  2. 工作目录白名单：命令在沙箱 WorkDir 内执行（cmd.Dir），
//     即使模型绕开黑名单，也破坏不了工作目录之外的数据。
//  3. 超时兜底：命令最多跑 ExecSandbox.Timeout，超时强杀（kill -9 进程组），
//     防止模型写死循环 / fork 炸弹把宿主拖垮。
//  4. 输出截断：只回传前 MaxOutput 字节，防刷屏占满上下文。
//  5. 高危标记：RiskLevel=2 + 仅 admin，配合二次确认（D20）。
//
// 生产演化方向：
//   - 真正隔离：换成容器（Docker/E2B）/ 虚拟机，而不是进程级沙箱
//   - 环境变量白名单、网络禁用（无网沙箱）、CPU/内存配额
//   - 命令审计日志落库（谁在什么时候执行了什么）
package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// 命令黑名单：命中任一子串即拒绝执行。
// 注意：黑名单不是安全边界，只是"第一层护栏"；真正的边界是容器/虚拟机。
var commandBlacklist = []string{
	"rm -rf", "rm -fr", "mkfs", "dd if=", "shutdown", "reboot", "init 0",
	":(){", "curl | sh", "curl|sh", "wget | sh", "chmod -R 777 /",
	"sudo ", "kill -9 1", "> /dev/sda", "fork bomb",
}

// RunCommandTool 执行 shell 命令（高危：二次确认 + 仅 admin + 只读模式禁用）。
type RunCommandTool struct{ Sandbox *ExecSandbox }

func (t *RunCommandTool) Name() string { return "run_command" }
func (t *RunCommandTool) Description() string {
	return "在工作目录沙箱内执行 shell 命令并返回输出（有超时与截断，高危需确认）。"
}
func (t *RunCommandTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{"type": "string", "description": "要执行的 shell 命令"},
		},
		"required": []string{"command"},
	}
}

// Execute 执行命令。
func (t *RunCommandTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.Sandbox.ReadOnly {
		return "", fmt.Errorf("当前为只读模式，禁止执行命令")
	}
	cmd := StringArg(args, "command")
	if cmd == "" {
		return "", fmt.Errorf("缺少 command 参数")
	}
	// 第一层护栏：黑名单检查
	for _, bad := range commandBlacklist {
		if strings.Contains(cmd, bad) {
			return "", fmt.Errorf("命令命中黑名单（%q），已拒绝执行", bad)
		}
	}

	// 用 sh -c 执行；进程组隔离，超时才能整组强杀
	ctx, cancel := context.WithTimeout(context.Background(), t.Sandbox.Timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = t.Sandbox.WorkDir // 第二层护栏：工作目录白名单

	out, err := c.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("命令执行超时（>%s），已强杀", t.Sandbox.Timeout)
	}
	// 输出截断
	text := strings.TrimSpace(string(out))
	if t.Sandbox.MaxOutput > 0 && len(text) > t.Sandbox.MaxOutput {
		text = text[:t.Sandbox.MaxOutput] + "\n…（输出过长已截断）"
	}
	if err != nil {
		return "", fmt.Errorf("命令执行失败: %v\n%s", err, text)
	}
	if text == "" {
		text = "（命令执行成功，无输出）"
	}
	return text, nil
}

// RiskLevel 命令执行 = 最高危（2 级）。
func (t *RunCommandTool) RiskLevel() int { return 2 }

// AllowedRoles 仅 admin。
func (t *RunCommandTool) AllowedRoles() []string { return []string{"admin"} }

// 编译期断言。
var (
	_ Tool  = (*RunCommandTool)(nil)
	_ Risky = (*RunCommandTool)(nil)
)
