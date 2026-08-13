// 终端启动 banner（P37）：Zebra CLI 与 server 启动时打印的 ASCII 标题。
//
// 用 6 行高的块状字母拼出 ZEBRA，下面跟一行副标题（各入口自定）；
// 纯文本输出到终端（stdout），不进入结构化日志文件。
package observe

import (
	"fmt"
	"io"
)

// zebraLetters 6 行块状 ASCII 字母（figlet block 风格）。
var zebraLetters = map[rune][]string{
	'Z': {"███████╗", "   ███╔╝", "  ███╔╝ ", " ███╔╝  ", "███████╗", "╚══════╝"},
	'E': {"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "███████╗", "╚══════╝"},
	'B': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔══██╗", "██████╔╝", "╚═════╝ "},
	'R': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔══██╗", "██║  ██║", "╚═╝  ╚═╝"},
	'A': {" █████╗ ", "██╔══██╗", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
}

// PrintBanner 打印 ZEBRA ASCII banner + 副标题（副标题为空则省略）。
func PrintBanner(w io.Writer, subtitle string) {
	for i := 0; i < 6; i++ {
		for _, ch := range "ZEBRA" {
			fmt.Fprint(w, zebraLetters[ch][i]+" ")
		}
		fmt.Fprintln(w)
	}
	if subtitle != "" {
		fmt.Fprintln(w, subtitle)
	}
	fmt.Fprintln(w)
}
