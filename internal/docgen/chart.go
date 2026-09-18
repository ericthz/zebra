// 图表产出：生成 SVG 柱状图/折线图。
//
// 为什么选 SVG：SVG 是纯文本 XML，浏览器/Word/Markdown 原生可预览，
// 无需字体渲染引擎；数据驱动生成最简单，完全符合"零第三方依赖"约束。
// 生产演化方向：加主题色/图例/坐标轴刻度、导出 PNG（Go image 可做像素
// 光栅化）、接图表库做交互式看板。
package docgen

import (
	"fmt"
	"math"
	"strings"
)

// BarChartSVG 生成柱状图 SVG。
// labels 与 values 等长；返回完整 <svg> 字符串，可直接写文件或嵌 HTML。
func BarChartSVG(title string, labels []string, values []float64) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("values 不能为空")
	}
	if len(labels) != len(values) {
		return "", fmt.Errorf("labels 与 values 长度不一致: %d vs %d", len(labels), len(values))
	}
	n := len(values)
	width := 120 + n*70
	height := 320
	maxVal := maxValue(values)
	if maxVal <= 0 {
		maxVal = 1 // 全 0 数据避免除零
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height)
	if title != "" {
		fmt.Fprintf(&b, `<text x="%d" y="28" font-size="18" font-weight="bold" text-anchor="middle">%s</text>`, width/2, svgEscape(title))
	}
	// 基线 + 柱体 + 数值标签 + X 轴标签
	baseY := 270
	fmt.Fprintf(&b, `<line x1="60" y1="%d" x2="%d" y2="%d" stroke="#888"/>`, baseY, width-40, baseY)
	for i, v := range values {
		x := 80 + i*70
		h := int(math.Round(v / maxVal * 180))
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="40" height="%d" fill="#4f8cff"/>`, x, baseY-h, h)
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" text-anchor="middle">%.2f</text>`, x+20, baseY-h-6, v)
		if i < len(labels) {
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" text-anchor="middle">%s</text>`, x+20, baseY+18, svgEscape(labels[i]))
		}
	}
	b.WriteString(`</svg>`)
	return b.String(), nil
}

// LineChartSVG 生成折线图 SVG（数据点 + 连线）。
func LineChartSVG(title string, labels []string, values []float64) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("values 不能为空")
	}
	if len(labels) != len(values) {
		return "", fmt.Errorf("labels 与 values 长度不一致")
	}
	n := len(values)
	width := 120 + n*70
	height := 320
	maxVal := maxValue(values)
	if maxVal <= 0 {
		maxVal = 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height)
	if title != "" {
		fmt.Fprintf(&b, `<text x="%d" y="28" font-size="18" font-weight="bold" text-anchor="middle">%s</text>`, width/2, svgEscape(title))
	}
	baseY := 270
	fmt.Fprintf(&b, `<line x1="60" y1="%d" x2="%d" y2="%d" stroke="#888"/>`, baseY, width-40, baseY)

	// 折线：把每个点映射到画布坐标
	pts := make([][2]int, n)
	for i, v := range values {
		x := 80 + i*70
		y := baseY - int(math.Round(v/maxVal*180))
		pts[i] = [2]int{x, y}
	}
	for i := 0; i+1 < n; i++ {
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#ff7a59" stroke-width="2"/>`,
			pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1])
	}
	for i, p := range pts {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="4" fill="#ff7a59"/>`, p[0], p[1])
		if i < len(labels) {
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" text-anchor="middle">%s</text>`, p[0], baseY+18, svgEscape(labels[i]))
		}
	}
	b.WriteString(`</svg>`)
	return b.String(), nil
}

func maxValue(values []float64) float64 {
	m := values[0]
	for _, v := range values {
		if v > m {
			m = v
		}
	}
	return m
}

// svgEscape 转义 XML 特殊字符（SVG 内嵌文本安全）。
func svgEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
