// 命令行行编辑（P38）：raw 模式下 UTF-8 感知的输入读取。
//
// 背景：终端默认"规范模式"（canonical）的退格按【字节】删除，中文等
// 多字节字符会被删掉一部分，留下不可见残片——连续"输入→删除"几轮后
// 行缓冲被污染，表现为"前面的字删不掉、像卡住"（P37 实测复现）。
// 本文件自实现最小行编辑：raw 模式下逐字符回显、退格按 rune 删除，
// 彻底规避多字节问题；非 TTY（管道/重定向）自动回退标准行读取。
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
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if restore, err := makeRaw(fd); err == nil {
		defer restore()
		return readLineRaw(bufio.NewReader(os.Stdin), os.Stdout)
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
// 支持：Enter 提交、退格按字符删除、Ctrl-C 中断、Ctrl-D 结束。
func readLineRaw(r *bufio.Reader, w io.Writer) (string, error) {
	var buf []rune
	for {
		b, err := r.ReadByte()
		if err != nil {
			return string(buf), err
		}
		switch {
		case b == '\r' || b == '\n': // Enter 提交
			fmt.Fprint(w, "\r\n")
			return string(buf), nil
		case b == 0x7f || b == 0x08: // 退格：按"字符"删，而非按字节
			if n := len(buf); n > 0 {
				last := buf[n-1]
				buf = buf[:n-1]
				erase(w, displayWidth(string(last)))
			}
		case b == 0x03: // Ctrl-C
			return string(buf), ErrInterrupted
		case b == 0x04: // Ctrl-D（空行时表示结束）
			return string(buf), io.EOF
		case b < 0x20: // 其余控制字符（方向键等）暂不处理
			continue
		default:
			r_, _ := readRune(r, b)
			if r_ == utf8.RuneError {
				continue
			}
			buf = append(buf, r_)
			fmt.Fprint(w, string(r_))
		}
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

// erase 在终端上抹掉 width 个显示格（\b 回退 + 空格覆盖 + 回退）。
func erase(w io.Writer, width int) {
	if width <= 0 {
		width = 1
	}
	fmt.Fprint(w, strings.Repeat("\b", width)+strings.Repeat(" ", width)+strings.Repeat("\b", width))
}
