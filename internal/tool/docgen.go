// 文档/图表产出工具（P23）：Agent 把聊天结果落地成"可交付文件"。
//
// 背景：企业 Agent 的产出常是 Word 报告、图表、PDF 摘要——如果只能输出
// 文本，用户还得自己复制粘贴。本文件把 internal/docgen 的能力暴露为
// 工具（generate_docx / generate_chart），模型按 schema 传参即可生成文件。
//
// 安全：与写文件工具一致——沙箱路径白名单 + 只读模式禁用 + 高危二次确认。
package tool

import (
	"context"
	"fmt"
	"os"

	"github.com/ericthz/zebra/internal/docgen"
)

// GenerateDocxTool 生成 Word 文档。
type GenerateDocxTool struct{ Sandbox *ExecSandbox }

func (t *GenerateDocxTool) Name() string { return "generate_docx" }
func (t *GenerateDocxTool) Description() string {
	return "生成 Word(.docx) 报告文档：指定输出路径、标题、段落列表。"
}
func (t *GenerateDocxTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":       map[string]interface{}{"type": "string", "description": "输出文件路径（相对工作目录，如 report.docx）"},
			"title":      map[string]interface{}{"type": "string", "description": "文档标题（可选）"},
			"paragraphs": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "正文段落列表"},
		},
		"required": []string{"path", "paragraphs"},
	}
}
func (t *GenerateDocxTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.Sandbox.ReadOnly {
		return "", fmt.Errorf("当前为只读模式，禁止生成文件")
	}
	path := StringArg(args, "path")
	paragraphs := StringSliceArg(args, "paragraphs")
	if path == "" || len(paragraphs) == 0 {
		return "", fmt.Errorf("缺少 path 或 paragraphs")
	}
	abs, err := t.Sandbox.safePath(path)
	if err != nil {
		return "", err
	}
	if err := docgen.WriteDocx(abs, StringArg(args, "title"), paragraphs); err != nil {
		return "", err
	}
	return ToResult(fmt.Sprintf("已生成 Word 文档 %s（标题: %s，段落: %d 段）", path, StringArg(args, "title"), len(paragraphs))), nil
}

// GenerateChartTool 生成 SVG 柱状图/折线图。
type GenerateChartTool struct{ Sandbox *ExecSandbox }

func (t *GenerateChartTool) Name() string { return "generate_chart" }
func (t *GenerateChartTool) Description() string {
	return "生成图表 SVG 文件（柱状图 bar / 折线图 line）：指定输出路径、标题、标签与数值。"
}
func (t *GenerateChartTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":   map[string]interface{}{"type": "string", "description": "输出文件路径（相对工作目录，如 chart.svg）"},
			"type":   map[string]interface{}{"type": "string", "description": "图表类型：bar 柱状图 / line 折线图"},
			"title":  map[string]interface{}{"type": "string", "description": "图表标题（可选）"},
			"labels": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "X 轴标签列表"},
			"values": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}, "description": "数值列表（与 labels 等长）"},
		},
		"required": []string{"path", "type", "labels", "values"},
	}
}
func (t *GenerateChartTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.Sandbox.ReadOnly {
		return "", fmt.Errorf("当前为只读模式，禁止生成文件")
	}
	path := StringArg(args, "path")
	labels := StringSliceArg(args, "labels")
	values := FloatSliceArg(args, "values")
	if path == "" || len(labels) == 0 || len(values) == 0 {
		return "", fmt.Errorf("缺少 path/labels/values")
	}
	abs, err := t.Sandbox.safePath(path)
	if err != nil {
		return "", err
	}
	var svg string
	switch StringArg(args, "type") {
	case "bar":
		svg, err = docgen.BarChartSVG(StringArg(args, "title"), labels, values)
	case "line":
		svg, err = docgen.LineChartSVG(StringArg(args, "title"), labels, values)
	default:
		return "", fmt.Errorf("type 仅支持 bar/line，实际 %q", StringArg(args, "type"))
	}
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(svg), 0o644); err != nil {
		return "", err
	}
	return ToResult(fmt.Sprintf("已生成图表 %s（%d 个数据点）", path, len(values))), nil
}

// RiskLevel 生成文件与写文件同级：高危（需二次确认）。
func (t *GenerateDocxTool) RiskLevel() int  { return 2 }
func (t *GenerateChartTool) RiskLevel() int { return 2 }

// AllowedRoles 仅 admin 可用（与本地写文件一致）。
func (t *GenerateDocxTool) AllowedRoles() []string  { return []string{"admin"} }
func (t *GenerateChartTool) AllowedRoles() []string { return []string{"admin"} }

// 编译期断言。
var (
	_ Tool  = (*GenerateDocxTool)(nil)
	_ Tool  = (*GenerateChartTool)(nil)
	_ Risky = (*GenerateDocxTool)(nil)
	_ Risky = (*GenerateChartTool)(nil)
)
