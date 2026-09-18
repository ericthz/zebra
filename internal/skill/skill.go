// Package skill 技能体系（对标 Claude Skills / OpenAI AgentKit Skills）。
//
// 概念澄清 —— Skill 与其他概念的本质区别：
//
//	Tool     ：原子操作（入参→出参，无状态），Agent 通过 Function Calling"调用"
//	MCP      ：连接外部能力/工具的"协议"，工具经握手后调用
//	Prompt   ：一段提示词文本，每次注入
//	Skill    ：一段"怎么做一件事"的【程序性知识包】
//	          （多步 SOP + 配套脚本 + 参考资源），Agent"读取并遵循"，而非"调用"
//
// 类比：Tool 是"螺丝刀"，MCP 是"接外设的标准接口"，Prompt 是"一句叮嘱"，
// Skill 是"操作手册 + 整套工具箱 + 步骤 SOP"。
//
// 本包职责：
//   - Skill 结构定义（含 SKILL.md 元数据）
//   - 注册表（并发安全，供 Agent 查询）
//   - 关键词检索（检索到才注入，避免全量塞上下文 —— 技能懒加载）
//
// 生产演化方向：
//   - 检索从"关键词打分"升级为"向量检索"（复用 memory 包嵌入器）
//   - SKILL.md 支持多行 description、依赖声明、内置自测
//   - scripts/ 在隔离沙箱执行（见本地执行）
package skill

import (
	"strings"
	"sync"
)

// Skill 一个技能：元数据 + 指令正文。
// Instructions 是注入给模型的"怎么做"说明；Dir/Path 指向源目录以便
// 按需读取 scripts/ 与 resources/（懒加载）。
type Skill struct {
	Name         string // 技能名（唯一，用于注册与引用）
	Description  string // 一句话说明"什么时候该用本技能"（检索用）
	Version      string // 版本号，用于技能更新与审计
	Instructions string // SKILL.md 正文：给模型的 SOP 指令
	Dir          string // 技能源目录（可含 scripts/、resources/）
	Path         string // SKILL.md 完整路径
}

// Registry 技能注册表（并发安全）。
// 生产可扩展为"技能市场"：从共享仓库/对象存储拉取，本实现保持本地目录。
type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill
}

// NewRegistry 构造技能注册表。
func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]*Skill)}
}

// Register 注册一个技能（同名覆盖，支持热更新技能）。
func (r *Registry) Register(s *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Name] = s
}

// Get 按名取技能。
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// List 返回全部技能（副本，避免外部篡改内部 map）。
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	return out
}

// LoadAll 从一批 Skill 批量注册（由 loader 扫描后调用）。
func (r *Registry) LoadAll(skills []*Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range skills {
		r.skills[s.Name] = s
	}
}

// Match 按用户输入检索最相关技能（技能懒加载：命中才注入）。
// 打分逻辑见 retrieve.go；返回按相关度降序的 limit 个。
func (r *Registry) Match(query string, limit int) []*Skill {
	r.mu.RLock()
	all := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		all = append(all, s)
	}
	r.mu.RUnlock()
	return rankSkills(all, query, limit)
}

// keywordTokens 把查询切成检索词：
// 英文按空格分词，中文按单字切分（中文无空格，字即最小检索单元）。
func keywordTokens(s string) []string {
	var tokens []string
	for _, field := range strings.Fields(strings.ToLower(s)) {
		for _, r := range field {
			if r >= 0x4e00 && r <= 0x9fff { // CJK 汉字逐字
				tokens = append(tokens, string(r))
			} else {
				tokens = append(tokens, field) // 英文整体作为一个词
				break
			}
		}
	}
	return tokens
}
