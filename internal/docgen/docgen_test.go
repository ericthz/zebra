package docgen

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDocx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.docx")
	if err := WriteDocx(path, "周报", []string{"本周完成了影子评测。", "下周计划做文档产出。"}); err != nil {
		t.Fatal(err)
	}

	// 1. 是合法 zip
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("docx 必须是合法 zip: %v", err)
	}
	defer zr.Close()

	// 2. 必备部件齐全
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		if !names[want] {
			t.Fatalf("docx 缺少部件 %s，实际 %v", want, names)
		}
	}

	// 3. 正文包含标题与段落
	var doc string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			buf := make([]byte, f.UncompressedSize64)
			rc.Read(buf)
			rc.Close()
			doc = string(buf)
		}
	}
	if !strings.Contains(doc, "周报") || !strings.Contains(doc, "影子评测") || !strings.Contains(doc, "下周计划") {
		t.Fatalf("正文缺失内容: %s", doc)
	}

	// 4. XML 转义：特殊字符不破坏文档
	path2 := filepath.Join(t.TempDir(), "esc.docx")
	if err := WriteDocx(path2, "a<b&c", []string{`5 < 10 && x > 1`}); err != nil {
		t.Fatal(err)
	}
	zr2, err := zip.OpenReader(path2)
	if err != nil {
		t.Fatal(err)
	}
	defer zr2.Close()
	escDoc := ""
	for _, f := range zr2.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			buf := make([]byte, f.UncompressedSize64)
			rc.Read(buf)
			rc.Close()
			escDoc = string(buf)
		}
	}
	if !strings.Contains(escDoc, "a&lt;b&amp;c") || strings.Contains(escDoc, "a<b&c") {
		t.Fatalf("XML 转义失败: %s", escDoc)
	}
}

func TestWritePDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.pdf")
	if err := WritePDF(path, "Summary", []string{"line one", "line two (with parens)"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "%PDF-1.4") {
		t.Fatalf("PDF 头错误: %q", s[:20])
	}
	for _, want := range []string{"/Type /Catalog", "/Type /Page", "xref", "trailer", "startxref", "%%EOF", "Summary", "line one", "line two \\(with parens\\)"} {
		if !strings.Contains(s, want) {
			t.Fatalf("PDF 缺少 %q", want)
		}
	}
}

// TestWritePDFASCIISanitize 最小 PDF 引擎不支持 CID 字体：中文应被替换为占位符，
// 且字节仍是合法 ASCII（不产生乱码字节流）。
func TestWritePDFASCIISanitize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cjk.pdf")
	if err := WritePDF(path, "周报", []string{"本周进展良好", "下周计划：完成报告"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "周报") || strings.Contains(s, "本周进展") {
		t.Fatal("中文不应原样进入 PDF 内容流（无 CID 字体会乱码）")
	}
	// 替换后仍为合法 PDF：结构标记齐全
	for _, want := range []string{"xref", "trailer", "startxref", "%%EOF"} {
		if !strings.Contains(s, want) {
			t.Fatalf("替换中文后 PDF 结构被破坏，缺少 %q", want)
		}
	}
}

func TestCharts(t *testing.T) {
	labels := []string{"周一", "周二", "周三"}
	values := []float64{10, 25, 8}

	bar, err := BarChartSVG("周用量", labels, values)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bar, `<svg`) || !strings.HasSuffix(bar, `</svg>`) {
		t.Fatalf("SVG 结构错误: %s", bar)
	}
	if strings.Count(bar, "<rect") != 3 || !strings.Contains(bar, "周一") || !strings.Contains(bar, "周用量") {
		t.Fatalf("柱状图元素缺失: %s", bar)
	}

	line, err := LineChartSVG("趋势", labels, values)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(line, "<circle") != 3 || strings.Count(line, "<line") < 2 {
		t.Fatalf("折线图元素缺失: %s", line)
	}

	// 错误场景
	if _, err := BarChartSVG("", nil, nil); err == nil {
		t.Fatal("空数据应报错")
	}
	if _, err := BarChartSVG("", []string{"a"}, []float64{1, 2}); err == nil {
		t.Fatal("长度不一致应报错")
	}
}
