// 向量工具函数（Cosine 余弦相似度），供语义缓存与 RAG复用。
package memory

// Cosine 计算两个向量的余弦相似度 [-1,1]。
// 零向量视为与任何向量不相似（返回 0），避免除零。
func Cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt(na) * sqrt(nb))
}

// sqrt 极简牛顿迭代开方（避免为单个函数引入额外依赖语义的负担）。
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	guess := x
	for i := 0; i < 32; i++ {
		next := (guess + x/guess) / 2
		if next == guess {
			break
		}
		guess = next
	}
	return guess
}
