package console

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
)

// TestReadLineRawMultibyteBackspace 中文（3 字节/字符）按字符退格删除，
// 不应残留字节残片（修复的核心回归用例）。
func TestReadLineRawMultibyteBackspace(t *testing.T) {
	input := "你好世界" + "\x7f\x7f\x7f\x7f" + "exit\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "exit" {
		t.Fatalf("行内容应为 exit，实际 %q", line)
	}
}

func TestReadLineRawAscii(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("hello\r")), &out, "> ", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Fatalf("got %q", line)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatal("应回显 hello")
	}
}

func TestReadLineRawBackspaceAscii(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("abc\x7f\x7fxyz\n")), &out, "> ", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "axyz" { // 退格删掉 bc，剩 axyz
		t.Fatalf("got %q", line)
	}
}

func TestReadLineRawEmptyAndEOF(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("\r")), &out, "> ", nil, nil)
	if err != nil || line != "" {
		t.Fatalf("空行: %q %v", line, err)
	}
	// EOF：已输入内容返回内容 + io.EOF（调用方据此结束）
	line, err = readLineRaw(bufio.NewReader(strings.NewReader("abc")), &out, "> ", nil, nil)
	if line != "abc" || err != io.EOF {
		t.Fatalf("EOF 场景: %q %v", line, err)
	}
}

// TestReadLineRawTabCompletion 输入 "/mod" 后按 Tab 刷新候选列表（/ 前缀
// 已实时进入候选模式）；随后回车提交高亮选中的命令。
func TestReadLineRawTabCompletion(t *testing.T) {
	input := "/mod" + "\t" + "\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", []string{
		"/mode", "/modes", "/model", "/provider", "/tools",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "/mode" { // 候选模式下 Enter 提交首个高亮项
		t.Fatalf("Tab 后 Enter 应提交首个候选 /mode，got %q", line)
	}
	if got := out.String(); !strings.Contains(got, "/mode") || !strings.Contains(got, "/modes") {
		t.Fatalf("Tab 应提示匹配候选命令:\n%s", got)
	}
}

// TestReadLineRawAutoHint 输入 "/" 前缀时实时提示候选命令，无需按 Tab。
func TestReadLineRawAutoHint(t *testing.T) {
	input := "/mo\r" // 输入到 /mo 即应提示，无需 Tab；Enter 选中首个候选
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", []string{
		"/mode", "/modes", "/model", "/provider",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "/mode" { // 候选模式下 Enter 提交高亮项（首个）
		t.Fatalf("got %q", line)
	}
	if got := out.String(); !strings.Contains(got, "/mode") || !strings.Contains(got, "/modes") || !strings.Contains(got, "/model") {
		t.Fatalf("输入 /mo 应实时提示匹配命令:\n%s", got)
	}
}

// TestReadLineRawCandidateArrows ↑/↓ 在候选命令中移动高亮，Enter 提交选中项。
func TestReadLineRawCandidateArrows(t *testing.T) {
	// 输入 /mo 后 ↓ 一次（选中 /modes，跳过 /mode），Enter 提交
	input := "/mo\x1b[B\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", []string{
		"/mode", "/modes", "/model",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "/modes" {
		t.Fatalf("↓ 后 Enter 应提交第二个候选 /modes，实际 %q", line)
	}
}

// TestReadLineRawCandidateUp ↑ 在候选命令中回退选择；↑ 越过首项后停在首项。
func TestReadLineRawCandidateUp(t *testing.T) {
	// 输入 /mo 后 ↓↓↓（到底 /model）再 ↑（回到 /modes），Enter 提交
	input := "/mo\x1b[B\x1b[B\x1b[B\x1b[A\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", []string{
		"/mode", "/modes", "/model",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "/modes" {
		t.Fatalf("↓到底后 ↑ 应回到 /modes，实际 %q", line)
	}
}

// TestReadLineRawCandidateHighlight 选中项渲染为反色高亮（\x1b[7m...\x1b[0m），
// 未选中项为普通 "  " 前缀。
func TestReadLineRawCandidateHighlight(t *testing.T) {
	var out bytes.Buffer
	_, err := readLineRaw(bufio.NewReader(strings.NewReader("/mo\x1b[B\r")), &out, "> ", []string{
		"/mode", "/modes", "/model",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "\x1b[7m> /modes\x1b[0m") {
		t.Fatalf("选中项应反色高亮并带 > 前缀:\n%q", got)
	}
	if !strings.Contains(got, "\r\n  /mode") {
		t.Fatalf("未选中项应普通前缀:\n%q", got)
	}
}

// TestReadLineRawHintCRLF 提示行必须以 \r\n 分隔（raw 模式关闭 OPOST 后
// \n 只换行不回车，若用 \n 会导致各行从上一行末尾累积、递增缩进）。
func TestReadLineRawHintCRLF(t *testing.T) {
	var out bytes.Buffer
	_, err := readLineRaw(bufio.NewReader(strings.NewReader("/\r")), &out, "> ", []string{"/help", "/mode", "/modes"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "\r\n\x1b[7m> /help\x1b[0m\r\n  /mode\r\n  /modes") {
		t.Fatalf("提示行应用 \\r\\n 分隔且首项高亮:\n%q", got)
	}
}

// TestReadLineRawTabNoPrefix 非 "/" 前缀按 Tab 无动作（避免普通输入被干扰）。
func TestReadLineRawTabNoPrefix(t *testing.T) {
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader("hello\t\r")), &out, "> ", []string{"/help", "/mode"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Fatalf("got %q", line)
	}
}

// TestReadLineRawHistoryUpDown ↑/↓ 在历史记录中前后选择。
func TestReadLineRawHistoryUpDown(t *testing.T) {
	history := []string{"第一句", "第二句", "第三句"}
	// ↑↑↑：回溯到最早一条；随后 ↓：前进一条
	input := "\x1b[A\x1b[A\x1b[A\x1b[B\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", nil, history)
	if err != nil {
		t.Fatal(err)
	}
	if line != "第二句" { // ↑↑↑ 到第 0 条，↓ 到第 1 条
		t.Fatalf("历史选择结果应为 第二句，实际 %q", line)
	}
}

// TestReadLineRawHistoryDownPastEnd ↓ 越过最晚一条后回到空输入行。
func TestReadLineRawHistoryDownPastEnd(t *testing.T) {
	history := []string{"第一句"}
	input := "\x1b[A\x1b[B\r" // ↑ 选中，↓ 越过末尾回到空
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", nil, history)
	if err != nil {
		t.Fatal(err)
	}
	if line != "" {
		t.Fatalf("↓ 越过末尾应回到空输入行，实际 %q", line)
	}
}

// TestReadLineRawHistoryEmpty 无历史时方向键无动作。
func TestReadLineRawHistoryEmpty(t *testing.T) {
	input := "\x1b[A\x1b[A\x1b[B\r"
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line != "" {
		t.Fatalf("无历史时方向键应无动作，实际 %q", line)
	}
}

// TestReadLineRawHistoryThenEdit 选中历史后输入字符即脱离历史选择。
func TestReadLineRawHistoryThenEdit(t *testing.T) {
	history := []string{"旧问题"}
	input := "\x1b[A" + "x\r" // 选中"旧问题"后追加 x
	var out bytes.Buffer
	line, err := readLineRaw(bufio.NewReader(strings.NewReader(input)), &out, "> ", nil, history)
	if err != nil {
		t.Fatal(err)
	}
	if line != "旧问题x" {
		t.Fatalf("应基于历史项继续编辑，实际 %q", line)
	}
}

// TestReadLineRawHintClearOrder 重绘提示区时必须"先清当前行再上移"（\x1b[2K\x1b[A）。
// 若写成"先上移再清行"（\x1b[A\x1b[2K）会漏清光标所在行，导致上一帧提示残留。
// 输入 / 后输入 m 触发两次重绘，每处清除应为连续 3 组，且无旧顺序残留。
func TestReadLineRawHintClearOrder(t *testing.T) {
	var out bytes.Buffer
	_, err := readLineRaw(bufio.NewReader(strings.NewReader("/m\r")), &out, "> ", []string{"/mode", "/modes", "/model"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	re := regexp.MustCompile(`(?:\x1b\[2K\x1b\[A){3}`)
	if leftovers := strings.Count(re.ReplaceAllString(got, ""), "\x1b[2K\x1b[A"); leftovers != 0 {
		t.Fatalf("清除序列应为连续 3 组 \\x1b[2K\\x1b[A，残留 %d 组:\n%q", leftovers, got)
	}
}
