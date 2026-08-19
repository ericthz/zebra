// Package kg 最小知识图谱（P52）：三元组 实体-关系-实体。
//
// 背景：RAG 擅长"找片段"，但不擅长"实体间关系"（如 A 依赖 B、B 属于 C）。
// 知识图谱用 三元组(主体, 谓词, 客体) 显式表达关系，支持按实体反查。
//
// 本实现：
//   - Triple / Graph：内存图存储（并发安全）+ 按实体查询
//   - ExtractTriples：规则抽取（常见中文关系句式），零成本、确定性
//
// 生产演化方向：LLM 抽取实体关系（复用 P17 结构化输出）、图数据库
// 持久化、与 RAG 检索结果联合注入上下文。
package kg

import (
	"regexp"
	"strings"
	"sync"
)

// Triple 一条关系三元组。
type Triple struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// Graph 内存知识图谱（并发安全）。
type Graph struct {
	mu        sync.RWMutex
	bySubject map[string][]Triple
	byObject  map[string][]Triple
}

// NewGraph 构造空图。
func NewGraph() *Graph {
	return &Graph{
		bySubject: make(map[string][]Triple),
		byObject:  make(map[string][]Triple),
	}
}

// Add 添加一条三元组（双向索引）。
func (g *Graph) Add(t Triple) {
	t.Subject = strings.TrimSpace(t.Subject)
	t.Object = strings.TrimSpace(t.Object)
	if t.Subject == "" || t.Object == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bySubject[t.Subject] = append(g.bySubject[t.Subject], t)
	g.byObject[t.Object] = append(g.byObject[t.Object], t)
}

// Query 查询与 entity 相关的全部三元组（作为主体或客体命中）。
func (g *Graph) Query(entity string) []Triple {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Triple
	out = append(out, g.bySubject[entity]...)
	out = append(out, g.byObject[entity]...)
	return out
}

// entityRule 从自然语言查询里粗提候选实体（中文/英文单词片段）。
var entityRule = regexp.MustCompile(`[\p{Han}A-Za-z0-9]{1,24}`)

// Search 从查询文本中提取候选实体并反查图谱，返回去重后的相关三元组。
// 用于把知识图谱接入 Agent 检索：问题里的实体（如"zebra"）在图谱中
// 有出/入边时，这些关系能直接服务关系类问题（A 依赖谁 / 谁属于 B）。
// 无命中返回空切片（不影响正常注入）。
func (g *Graph) Search(query string) []Triple {
	seen := map[string]bool{}
	var out []Triple
	for _, m := range entityRule.FindAllString(query, -1) {
		for _, t := range g.Query(m) {
			k := t.Subject + "\x00" + t.Predicate + "\x00" + t.Object
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, t)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}

// Size 三元组总数。
func (g *Graph) Size() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n := 0
	for _, v := range g.bySubject {
		n += len(v)
	}
	return n
}

// ---- 规则抽取（常见中文关系句式）----

var (
	// 谓词两侧允许空白（"zebra 支持 工具调用" 这类带空格的句式）
	verbRule = regexp.MustCompile(`([\p{Han}A-Za-z0-9]{1,16})\s*(?:位于|包含|包括|属于|支持|擅长|依赖|使用)\s*([\p{Han}A-Za-z0-9]{1,24})`)
	isRule   = regexp.MustCompile(`([\p{Han}A-Za-z0-9]{1,16})\s*是\s*([\p{Han}A-Za-z0-9]{1,24})`)
)

// ExtractTriples 从文本中规则抽取三元组（零成本、确定性；生产可换 LLM 抽取）。
func ExtractTriples(text string) []Triple {
	var out []Triple
	for _, m := range verbRule.FindAllStringSubmatch(text, -1) {
		out = append(out, Triple{Subject: m[1], Predicate: verbOf(text, m[0]), Object: m[2]})
	}
	for _, m := range isRule.FindAllStringSubmatch(text, -1) {
		out = append(out, Triple{Subject: m[1], Predicate: "是", Object: m[2]})
	}
	return out
}

// verbOf 从命中串中还原谓词动词（位于/包含/…）。
func verbOf(text, matched string) string {
	for _, v := range []string{"位于", "包含", "包括", "属于", "支持", "擅长", "依赖", "使用"} {
		if strings.Contains(matched, v) {
			return v
		}
	}
	return "关系"
}
