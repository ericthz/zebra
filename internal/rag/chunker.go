// 文本分块器（RAG 管道第一步：把长文档切成可检索的块）。
//
// 为什么必须分块：
//  1. 嵌入模型有输入长度上限，超长直接截断丢失语义
//  2. 检索是"按块相似度"打分，块越小越精准（但太小会丢上下文）
//  3. 注入上下文时只塞命中的块，省 token
//
// 策略（教学用，够用且好懂）：
//   - 按段落（空行）切分，再按目标 size（rune 数）打包
//   - 块间保留 overlap 长度重叠，避免上下文在边界处断裂
//
// 生产演化方向：
//   - 按语义切分（句子向量变化点 / 标题层级 / 代码块）而非纯长度
//   - 块附带结构化元数据（标题、章节、URL）供引用溯源
package rag

import "strings"

// ChunkText 把长文本切成若干块，每块约 size 个 rune，块间重叠 overlap。
func ChunkText(text string, size, overlap int) []string {
	if size <= 0 {
		size = 1000
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size / 2
	}

	// 1. 按段落切分（保留自然语义边界）
	paras := strings.Split(text, "\n")

	var chunks []string
	var cur []rune // 当前正在组装的块

	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, string(cur))
		}
		// 块间重叠：把上一块尾部 overlap 个 rune 留作下一块开头
		if overlap > 0 && len(cur) > overlap {
			cur = cur[len(cur)-overlap:]
		} else {
			cur = nil
		}
	}

	for _, p := range paras {
		pt := strings.TrimSpace(p)
		if pt == "" {
			continue
		}
		pr := []rune(pt)
		for len(pr) > 0 {
			take := size - len(cur)
			if take <= 0 { // 当前块满了
				flush()
				take = size - len(cur)
			}
			if take >= len(pr) {
				cur = append(cur, pr...)
				pr = nil
			} else {
				cur = append(cur, pr[:take]...)
				pr = pr[take:]
			}
		}
	}
	flush()
	return chunks
}
