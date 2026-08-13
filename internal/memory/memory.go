// Package memory 分层记忆系统（C12）。
//
// 分层模型：
//
//	工作记忆（WorkingMemory）——会话内、进程内存、TTL 短，用于当前对话的临时上下文
//	长期记忆（QdrantMemory） ——持久化向量库、按租户隔离，跨会话复用
//
// 生产演进方向：记忆压缩与遗忘策略、用户画像、RAG 文档库（只需在 Manager
// 上再加一层检索源，接口不变）。
package memory

import (
	"context"
)

// Memory 长期记忆存储与检索接口。
type Memory interface {
	Store(ctx context.Context, content string, meta map[string]string) error
	Retrieve(ctx context.Context, query string, limit int) ([]string, error)
	Clear(ctx context.Context) error
}

// Manager 分层记忆管理器：工作记忆 + 可选长期记忆。
type Manager struct {
	Working *WorkingMemory
	Long    Memory // nil 表示禁用长期记忆（如 Qdrant 不可用）
}

// NewManager 构造分层记忆管理器。
func NewManager(working *WorkingMemory, long Memory) *Manager {
	return &Manager{Working: working, Long: long}
}

// Remember 记录一轮对话：写入工作记忆；异步写入长期记忆（不阻塞主流程）。
func (m *Manager) Remember(sessionID, userInput, assistant string) {
	if m.Working != nil {
		m.Working.Add(sessionID, userInput, assistant)
	}
	if m.Long == nil || assistant == "" {
		return
	}
	content := "用户: " + userInput + "\n助手: " + assistant
	go func() { // 异步落库，失败不阻塞对话（B9 容错）
		ctx, cancel := context.WithTimeout(context.Background(), 5_000_000_000)
		defer cancel()
		_ = m.Long.Store(ctx, content, map[string]string{"type": "conversation", "session": sessionID})
	}()
}

// TenantScoped 可选接口：长期记忆支持按租户切分（QdrantMemory 实现，A4）。
type TenantScoped interface {
	ForTenant(tenant string) Memory
}

// ForgetTenant 被遗忘权（P6/GDPR）：删除某租户的全部长期记忆。
// 生产还需：删除会话、日志、审计中的该租户数据（全链路）。
func (m *Manager) ForgetTenant(ctx context.Context, tenant string) error {
	if m.Long == nil {
		return nil
	}
	if ts, ok := m.Long.(TenantScoped); ok {
		return ts.ForTenant(tenant).Clear(ctx)
	}
	// 非租户切分的长期记忆：仅当只有一个租户时清空
	return m.Long.Clear(ctx)
}

// ForgetSession 清空某会话的工作记忆（被遗忘权的一部分）。
func (m *Manager) ForgetSession(sessionID string) {
	if m.Working != nil {
		m.Working.Clear(sessionID)
	}
}

// Recall 检索相关记忆：先合并工作记忆的最近摘要，再叠加长期记忆语义检索。
func (m *Manager) Recall(ctx context.Context, sessionID, query string, limit int) []string {
	var out []string
	if m.Working != nil {
		out = append(out, m.Working.Recent(sessionID, 2)...) // 最近两轮工作记忆
	}
	if m.Long != nil {
		if items, err := m.Long.Retrieve(ctx, query, limit); err == nil {
			out = append(out, items...)
		}
	}
	return out
}
