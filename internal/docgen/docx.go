// 文档产出：生成 .docx（Office Open XML）。
//
// 背景：Agent 产出不能只停在聊天文本，要能交付"文件"。docx 本质是
// zip 包 + 一组 XML（OOXML），Go 标准库的 archive/zip + 手写 XML 即可生成，
// Word/WPS/预览都能打开——零第三方依赖。
//
// 最小 docx 只需要三个部件：
//
//	[Content_Types].xml   声明内容类型（哪个部件是什么 MIME）
//	_rels/.rels           根关系（指向 word/document.xml）
//	word/document.xml     正文（段落 w:p + 文本 w:t）
//
// 生产演化方向：表格/图片/样式表（styles.xml）、模板填充、批量生成。
package docgen

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
)

// WriteDocx 生成一个最小 Word 文档：标题（加粗居中）+ 若干段落。
// path 为输出文件路径（调用方保证目录可写）。
func WriteDocx(path, title string, paragraphs []string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// 1. 内容类型声明
	if err := writeZip(zw, "[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`); err != nil {
		return err
	}

	// 2. 根关系：默认文档部件
	if err := writeZip(zw, "_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`); err != nil {
		return err
	}

	// 3. 正文：标题 + 段落
	var body strings.Builder
	if title != "" {
		// 标题：居中加粗
		body.WriteString(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>`)
		body.WriteString(xmlEscape(title))
		body.WriteString(`</w:t></w:r></w:p>`)
	}
	for _, p := range paragraphs {
		if p == "" {
			continue
		}
		body.WriteString(`<w:p><w:r><w:t>`)
		body.WriteString(xmlEscape(p))
		body.WriteString(`</w:t></w:r></w:p>`)
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>` + body.String() + `<w:sectPr/>
  </w:body>
</w:document>`
	if err := writeZip(zw, "word/document.xml", doc); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// writeZip 向 zip 写入一个文本部件。
func writeZip(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(content))
	return err
}

// xmlEscape 转义 XML 特殊字符（文本内容里出现 & < > 时防止文档损坏/注入）。
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
