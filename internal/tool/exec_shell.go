// 本地执行能力—— Shell 命令执行（Agentic 的核心，也是最危险的能力）。
//
// 安全设计（理解本文件 = 理解"为什么本地执行需要沙箱"）：
//  1. 命令黑名单：直接禁止破坏性命令（rm -rf / mkfs / 下载并执行 等）。
//     宁可误伤，不可放行——这是高危能力的第一原则。
//  2. 工作目录白名单：命令以沙箱 WorkDir 作为启动目录（cmd.Dir）。
//     注意：cmd.Dir 只改变工作目录，不是文件系统隔离——命令仍可读写
//     工作目录之外（如 cat /etc/passwd、ls /）。真正的隔离需容器/虚拟机，
//     见"生产演化方向"。当前沙箱仅限本地可信环境 + admin 白名单使用。
//  3. 超时兜底：命令最多跑 ExecSandbox.Timeout，超时强杀（kill -9 进程组），
//     防止模型写死循环 / fork 炸弹把宿主拖垮。
//  4. 输出截断：只回传前 MaxOutput 字节，防刷屏占满上下文。
//  5. 高危标记：RiskLevel=2 + 仅 admin，配合二次确认。
//
// 生产演化方向：
//   - 真正隔离：换成容器（Docker/E2B）/ 虚拟机，而不是进程级沙箱
//   - 环境变量白名单、网络禁用（无网沙箱）、CPU/内存配额
//   - 命令审计日志落库（谁在什么时候执行了什么）
package tool

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

// 命令黑名单：命中任一子串即拒绝执行。
// 注意：黑名单不是安全边界，只是"第一层护栏"。`rm -r -f`、`$()`/“ ` “、
// $IFS、别名、符号链接等都能绕过子串匹配——真正的边界是容器/虚拟机。
// 本工具仅限本地可信环境 + admin 白名单使用（见文件头注释）。
var commandBlacklist = []string{
	"rm -rf", "rm -fr", "rm -r -f", "rm -f -r", "rm --recursive", "rm --force",
	"mkfs", "dd if=", "shutdown", "reboot", "init 0", "poweroff", "halt",
	":(){", "curl | sh", "curl|sh", "wget | sh", "chmod -R 777 /",
	"sudo ", "su -", "kill -9 1", "> /dev/sda", "fork bomb",
}

// RunCommandTool 执行 shell 命令（高危：二次确认 + 仅 admin + 只读模式禁用）。
type RunCommandTool struct{ Sandbox *ExecSandbox }

func (t *RunCommandTool) Name() string { return "run_command" }
func (t *RunCommandTool) Description() string {
	return "在工作目录沙箱内执行 shell 命令并返回输出（有超时与截断，高危需确认；仅限可信环境）。"
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

	// 用 sh -c 执行；进程组隔离，超时才能整组强杀。
	// 六9：不加 Setpgid 时只有 shell 进程被杀，其后台子进程（sleep 5 等）
	// 成为孤儿继续存活，注释里"强杀进程组"名不副实。Setpgid=true 让 sh 成为
	// 独立进程组组长，超时/输出超限时 kill(-pgid) 可整组终止。
	ctx, cancel := context.WithTimeout(context.Background(), t.Sandbox.Timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = t.Sandbox.WorkDir // 第二层护栏：工作目录白名单
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// 六6：输出必须流式限量。CombinedOutput 会等命令全量输出完才截断，
	// `cat /dev/zero` / `yes` 等可无限刷输出直接 OOM。改用管道 +
	// LimitReader，读满 cap 即杀进程，绝不全量缓冲。
	maxOut := t.Sandbox.MaxOutput
	if maxOut <= 0 {
		maxOut = 8 * 1024
	}
	outP, err := c.StdoutPipe()
	if err != nil {
		return "", err
	}
	errP, err := c.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := c.Start(); err != nil {
		return "", err
	}

	// killGroup 整组强杀（六9）：Setpgid 后 pgid==pid，kill(-pgid) 终止
	// shell 与其所有子进程，孤儿进程无法存活。
	killGroup := func() {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
	// 六9：超时/取消必须立即整组强杀。若等到读管道/ Wait 之后再杀就太晚——
	// 后台子进程持有管道写端，ReadAll 会一直阻塞到它退出；而它早已趁机
	// 写完了 marker。故 ctx 一到期立刻 kill(-pgid)。
	groupDone := make(chan struct{})
	defer close(groupDone)
	go func() {
		select {
		case <-ctx.Done():
			killGroup()
		case <-groupDone:
		}
	}()

	// 读满 cap 后立即强杀（进程组），避免管道写满阻塞在写端
	limit := maxOut + 1
	var outBuf, errBuf []byte
	outBuf, _ = io.ReadAll(io.LimitReader(outP, int64(limit)))
	if len(outBuf) > maxOut {
		killGroup()
	}
	if len(outBuf) <= maxOut {
		errBuf, _ = io.ReadAll(io.LimitReader(errP, int64(limit)))
	}
	if len(errBuf) > maxOut {
		killGroup()
	}
	waitErr := c.Wait()

	// 六9：ctx 已过期时（goroutine 已在到期瞬间杀过整组）直接报超时；
	// 若没超时但被 killGroup 强杀（输出超限），走 waitErr 分支。
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("命令执行超时（>%s），已强杀", t.Sandbox.Timeout)
	}

	// 输出合并 + 截断
	out := strings.TrimSpace(string(outBuf) + string(errBuf))
	if len(out) > maxOut {
		out = out[:maxOut] + "\n…（输出过长已截断）"
	}
	if waitErr != nil {
		if len(out) == 0 {
			out = "（无输出）"
		}
		return "", fmt.Errorf("命令执行失败: %v\n%s", waitErr, out)
	}
	if out == "" {
		out = "（命令执行成功，无输出）"
	}
	return out, nil
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
