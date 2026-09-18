// 命令行行编辑：raw 模式下 UTF-8 感知的输入读取。
//
// 背景：终端默认"规范模式"（canonical）的退格按【字节】删除，中文等
// 多字节字符会被删掉一部分，留下不可见残片——连续"输入→删除"几轮后
// 行缓冲被污染，表现为"前面的字删不掉、像卡住"（实测复现）。
// 本文件自实现最小行编辑：raw 模式下逐字符回显、退格按 rune 删除，
// 彻底规避多字节问题；非 TTY（管道/重定向）自动回退标准行读取。
//
// 交互扩展：
//   - 命令提示：输入以 "/" 开头时实时列出匹配的候选命令（前缀过滤），
//     输入/退格都会刷新提示，无需额外按键。
//   - 历史选择：↑/↓ 方向键在历史聊天记录中前后选择，选中即替换当前行。
package console

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// ErrInterrupted Ctrl-C 中断输入。
var ErrInterrupted = errors.New("interrupted")

// fallbackReader 非 TTY 回退读取器：保持缓冲，跨多次 ReadLine 不丢行。
var fallbackReader *bufio.Reader

// ReadLine 输出 prompt 后读取一行（不含换行）。
// stdin 为 TTY 且 raw 模式可用 → UTF-8 感知编辑；否则回退标准行读取。
func ReadLine(prompt string) (string, error) {
	return ReadLineFull(prompt, nil, nil)
}

// ReadLineWithCompletions 同 ReadLine，支持命令补全提示与历史选择。
// 保留旧签名（仅补全、无历史），行为与 ReadLineFull(prompt, candidates, nil) 一致。
func ReadLineWithCompletions(prompt string, candidates []string) (string, error) {
	return ReadLineFull(prompt, candidates, nil)
}

// ReadLineFull 输出 prompt 后读取一行，支持命令提示（candidates）与
// 历史选择（history）。history 为只读；调用方负责维护并追加已提交的行。
// 非 TTY 回退标准行读取（提示/历史不可用）。
func ReadLineFull(prompt string, candidates, history []string) (string, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if restore, err := makeRaw(fd); err == nil {
		defer restore()
		return readLineRaw(bufio.NewReader(os.Stdin), os.Stdout, prompt, candidates, history)
	}
	if fallbackReader == nil {
		fallbackReader = bufio.NewReader(os.Stdin)
	}
	line, err := fallbackReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readLineRaw raw 模式行编辑核心（注入 reader/writer 便于测试）。
// 支持：Enter 提交、退格按字符删除、Ctrl-C 中断、Ctrl-D 结束、
// ↑/↓ 历史选择（history 非空时）、"/" 前缀实时命令提示（candidates 非空时）。
// 输入以 "/" 开头时进入候选模式：提示区列出匹配命令，↑/↓ 移动高亮，
// Enter 提交高亮选中的命令（候选模式下优先于历史选择）。
func readLineRaw(r *bufio.Reader, w io.Writer, prompt string, candidates, history []string) (string, error) {
	var buf []rune
	histIdx := -1            // -1=正在输入新行；否则指向 history 中被选中的项
	hintRows := 0            // 当前屏幕下方提示区占用的行数
	var cand *candidateState // 非 nil 表示处于候选模式（/ 前缀命令选择）

	for {
		b, err := r.ReadByte()
		if err != nil {
			return string(buf), err
		}
		switch {
		case b == '\r' || b == '\n': // Enter 提交
			clearHint(w, hintRows)
			fmt.Fprint(w, "\r\n")
			if cand != nil && cand.sel >= 0 && cand.sel < len(cand.matches) {
				return cand.matches[cand.sel], nil // 候选模式下提交高亮项
			}
			return string(buf), nil
		case b == 0x7f || b == 0x08: // 退格：按"字符"删，而非按字节
			if n := len(buf); n > 0 {
				buf = buf[:n-1]
				histIdx = -1 // 手动编辑后脱离历史选择
			}
			hintRows, cand = syncLine(w, prompt, buf, hintRows, candidates)
		case b == 0x09: // Tab：手动刷新命令提示（/ 前缀已实时提示）
			hintRows, cand = syncLine(w, prompt, buf, hintRows, candidates)
		case b == 0x1b: // ESC 序列：方向键（候选模式移动选择，否则历史选择）
			hintRows = readEscape(r, w, prompt, &buf, &histIdx, hintRows, history, cand)
		case b == 0x03: // Ctrl-C
			clearHint(w, hintRows)
			return string(buf), ErrInterrupted
		case b == 0x04: // Ctrl-D（空行时表示结束）
			clearHint(w, hintRows)
			return string(buf), io.EOF
		case b < 0x20: // 其余控制字符暂不处理
			continue
		default:
			r_, _ := readRune(r, b)
			if r_ == utf8.RuneError {
				continue
			}
			buf = append(buf, r_)
			histIdx = -1 // 手动输入后脱离历史选择
			hintRows, cand = syncLine(w, prompt, buf, hintRows, candidates)
		}
	}
}

// candidateState 候选模式状态：输入以 "/" 开头时进入，matches 为匹配命令，
// sel 为当前高亮项索引（0..len-1）。
type candidateState struct {
	matches []string
	sel     int
}

// readEscape 处理 ESC 开头的按键序列。支持 ↑/↓：
//   - 候选模式下移动选择高亮（/ 前缀命令）；
//   - 否则在历史聊天记录中前后选择，选中项替换当前行。
//
// 返回新的提示区行数。
func readEscape(r *bufio.Reader, w io.Writer, prompt string, buf *[]rune, histIdx *int, hintRows int, history []string, cand *candidateState) int {
	b2, err := r.ReadByte()
	if err != nil || b2 != '[' {
		return hintRows
	}
	b3, err := r.ReadByte()
	if err != nil {
		return hintRows
	}
	switch b3 {
	case 'A': // ↑
		if cand != nil && len(cand.matches) > 0 { // 候选模式：向上移动选择
			if cand.sel > 0 {
				cand.sel--
			}
			return renderLine(w, prompt, *buf, hintRows, cand)
		}
		if len(history) == 0 {
			return hintRows
		}
		if *histIdx < 0 {
			*histIdx = len(history) - 1
		} else if *histIdx > 0 {
			*histIdx--
		} else {
			return hintRows
		}
		*buf = []rune(history[*histIdx])
	case 'B': // ↓
		if cand != nil && len(cand.matches) > 0 { // 候选模式：向下移动选择
			if cand.sel < len(cand.matches)-1 {
				cand.sel++
			}
			return renderLine(w, prompt, *buf, hintRows, cand)
		}
		if *histIdx < 0 {
			return hintRows
		}
		if *histIdx < len(history)-1 {
			*histIdx++
			*buf = []rune(history[*histIdx])
		} else {
			*histIdx = -1
			*buf = nil
		}
	default:
		return hintRows // 左右键等暂不处理
	}
	return renderLine(w, prompt, *buf, hintRows, nil)
}

// syncLine 普通输入后刷新提示区：按 "/" 前缀计算匹配候选并渲染，
// 返回新提示区行数与候选状态（供方向键选择使用）。非候选模式返回 nil。
func syncLine(w io.Writer, prompt string, buf []rune, hintRows int, candidates []string) (int, *candidateState) {
	if candidates == nil || !strings.HasPrefix(string(buf), "/") {
		return renderLine(w, prompt, buf, hintRows, nil), nil
	}
	cand := &candidateState{matches: matchCandidates(string(buf), candidates)}
	return renderLine(w, prompt, buf, hintRows, cand), cand
}

// renderLine 重绘终端：清掉旧提示区与输入行，重绘 prompt+已输入内容，
// 再按需打印命令候选（cand 非 nil 时）。返回新提示区行数。
// 选中项用反色高亮（\x1b[7m）并加 "> " 前缀，未选中项 "  " 前缀对齐。
// 注意：raw 模式关闭了 OPOST，\n 只换行不回车——提示区换行必须用 \r\n，
// 否则各行光标从上一行末尾继续累积，形成递增缩进。
func renderLine(w io.Writer, prompt string, buf []rune, hintRows int, cand *candidateState) int {
	clearHint(w, hintRows)
	fmt.Fprint(w, "\r\x1b[2K")
	fmt.Fprint(w, prompt)
	fmt.Fprint(w, string(buf))
	if cand == nil || len(cand.matches) == 0 {
		return 0
	}
	for i, m := range cand.matches {
		if i == cand.sel {
			fmt.Fprintf(w, "\r\n\x1b[7m> %s\x1b[0m", m)
		} else {
			fmt.Fprintf(w, "\r\n  %s", m)
		}
	}
	return len(cand.matches)
}

// matchCandidates 返回以 prefix 开头的候选命令（按候选原始顺序）。
func matchCandidates(prefix string, candidates []string) []string {
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// clearHint 清除屏幕下方 hintRows 行提示区。光标当前位于最后一行提示上：
// 先清当前行再上移，循环结束后光标落在输入行（列位置不变，syncLine 会用
// \r 回行首重绘输入行）。若先上移再清行会漏清光标所在行、残留旧内容。
func clearHint(w io.Writer, hintRows int) {
	for i := 0; i < hintRows; i++ {
		fmt.Fprint(w, "\x1b[2K")
		fmt.Fprint(w, "\x1b[A")
	}
}

// readRune 从首字节 + 续字节解码一个完整 UTF-8 rune。
func readRune(r *bufio.Reader, first byte) (rune, int) {
	seq := make([]byte, utf8ByteCount(first))
	seq[0] = first
	for i := 1; i < len(seq); i++ {
		b, err := r.ReadByte()
		if err != nil {
			return utf8.RuneError, 1
		}
		seq[i] = b
	}
	return utf8.DecodeRune(seq)
}

func utf8ByteCount(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}
