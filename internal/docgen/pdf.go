// 文档产出（P23）：生成最小 PDF（Portable Document Format）。
//
// PDF 文件结构（理解它 = 会造 PDF）：
//
//	%PDF-1.4 头
//	对象：Catalog → Pages → Page → Font → Content stream
//	xref 交叉引用表（每个对象在文件中的字节偏移）
//	trailer + startxref + %%EOF
//
// 本实现为单页、Helvetica（Type1 标准字体，无需嵌入）、ASCII 文本。
// 中文需嵌入 CID 字体（超最小实现范围），生产演化方向：接 gofpdf/weasyprint
// 类引擎做复杂排版；这里保留"能验证、能打开"的最小骨架。
package docgen

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// WritePDF 生成单页 PDF：标题（18pt）+ 逐行文本（11pt）。
// 仅支持 ASCII 可打印字符；path 为输出文件路径。
func WritePDF(path, title string, lines []string) error {
	content := pdfContent(title, lines)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	// offsets[i] = 对象 i 的文件偏移（0 号对象为 null，占位）
	offsets := make([]int, 1, 6)
	offsets[0] = 0

	writeObj := func(s string) {
		offsets = append(offsets, buf.Len())
		buf.WriteString(s)
	}

	writeObj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	writeObj("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	writeObj("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842]" +
		" /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n")
	writeObj("4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	writeObj(fmt.Sprintf("5 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content))

	xrefPos := buf.Len()
	buf.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, off := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n%%%%EOF\n", xrefPos)

	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// pdfContent 构造内容流：BT/ET 块内用文本矩阵 Tm 逐行定位。
func pdfContent(title string, lines []string) string {
	var b strings.Builder
	b.WriteString("BT\n/F1 18 Tf\n1 0 0 1 72 800 Tm\n")
	if title != "" {
		b.WriteString("(" + pdfEscape(title) + ") Tj\n")
	}
	y := 770
	for _, l := range lines {
		if y < 60 {
			break // 超出页面底部停止（单页）
		}
		fmt.Fprintf(&b, "1 0 0 1 72 %d Tm\n/F1 11 Tf\n(%s) Tj\n", y, pdfEscape(l))
		y -= 22
	}
	b.WriteString("ET\n")
	return b.String()
}

// pdfEscape 转义 PDF 字符串中的特殊字符（括号/反斜杠）。
func pdfEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}
