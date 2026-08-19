package tool

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocgenTools 文档/图表工具全链路：生成 → 校验文件内容 → 只读拦截。
func TestDocgenTools(t *testing.T) {
	sb, _ := newTestSandbox(t, false)
	ctx := context.Background()

	// 1. 生成 Word 文档
	dx := &GenerateDocxTool{Sandbox: sb}
	out, err := dx.Execute(ctx, map[string]interface{}{
		"path":       "report.docx",
		"title":      "周报",
		"paragraphs": []interface{}{"第一段：本周进展。", "第二段：下周计划。"},
	})
	if err != nil || !strings.Contains(out, "已生成 Word") {
		t.Fatalf("docx 工具失败: %v %s", err, out)
	}
	abs := filepath.Join(sb.WorkDir, "report.docx")
	zr, err := zip.OpenReader(abs)
	if err != nil {
		t.Fatalf("生成物不是合法 zip: %v", err)
	}
	zr.Close()

	// 2. 生成 SVG 柱状图
	ch := &GenerateChartTool{Sandbox: sb}
	out, err = ch.Execute(ctx, map[string]interface{}{
		"path":   "chart.svg",
		"type":   "bar",
		"title":  "周用量",
		"labels": []interface{}{"周一", "周二", "周三"},
		"values": []interface{}{float64(10), float64(25), float64(8)},
	})
	if err != nil || !strings.Contains(out, "已生成图表") {
		t.Fatalf("chart 工具失败: %v %s", err, out)
	}
	svgData, err := os.ReadFile(filepath.Join(sb.WorkDir, "chart.svg"))
	if err != nil || !strings.Contains(string(svgData), "<svg") || !strings.Contains(string(svgData), "周一") {
		t.Fatalf("SVG 内容异常: %v %s", err, string(svgData))
	}

	// 3. 只读模式拒绝生成
	ro, _ := newTestSandbox(t, true)
	roDx := &GenerateDocxTool{Sandbox: ro}
	if _, err := roDx.Execute(ctx, map[string]interface{}{"path": "x.docx", "paragraphs": []interface{}{"a"}}); err == nil {
		t.Fatal("只读模式应拒绝生成文件")
	}

	// 4. 参数错误：长度不一致
	if _, err := ch.Execute(ctx, map[string]interface{}{
		"path": "bad.svg", "type": "bar",
		"labels": []interface{}{"a"}, "values": []interface{}{float64(1), float64(2)},
	}); err == nil {
		t.Fatal("labels/values 长度不一致应报错")
	}

	// 5. 生成 PDF 摘要（最小引擎：合法 PDF 头 + xref 结构，中文被占位）
	pdf := &GeneratePDFTool{Sandbox: sb}
	out, err = pdf.Execute(ctx, map[string]interface{}{
		"path":  "report.pdf",
		"title": "Weekly Summary",
		"lines": []interface{}{"Line one: progress.", "中文行会被替换为占位符"},
	})
	if err != nil || !strings.Contains(out, "已生成 PDF") {
		t.Fatalf("pdf 工具失败: %v %s", err, out)
	}
	pdfData, err := os.ReadFile(filepath.Join(sb.WorkDir, "report.pdf"))
	if err != nil || !strings.HasPrefix(string(pdfData), "%PDF-1.4") || !strings.Contains(string(pdfData), "xref") {
		t.Fatalf("PDF 内容异常: %v %s", err, string(pdfData[:min(len(pdfData), 64)]))
	}

	// 6. 只读模式拒绝 PDF 生成
	roPdf := &GeneratePDFTool{Sandbox: ro}
	if _, err := roPdf.Execute(ctx, map[string]interface{}{"path": "x.pdf", "lines": []interface{}{"a"}}); err == nil {
		t.Fatal("只读模式应拒绝 PDF 生成")
	}
}
