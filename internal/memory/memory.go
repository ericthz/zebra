// Package memory 分层记忆系统。
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
	"fmt"
)

// Memory 长期记忆存储与检索接口。
type Memory interface {
	Store(ctx context.Context, content string, meta map[string]string) error
	// Retrieve 检索与 query 相关、且属于指定 user 的记忆（用户隔离）。
	// user 为空时表示不按用户过滤（仅内部工具用，如启动自检）。
	Retrieve(ctx context.Context, user, query string, limit int) ([]string, error)
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
// user 写进长期记忆元数据，供按用户删除（被遗忘权，见 ForgetUser）。
func (m *Manager) Remember(sessionID, user, userInput, assistant string) {
	if m.Working != nil {
		m.Working.Add(sessionID, userInput, assistant)
	}
	if m.Long == nil || assistant == "" {
		return
	}
	content := "用户: " + userInput + "\n助手: " + assistant
	go func() { // 异步落库，失败不阻塞对话（容错）
		ctx, cancel := context.WithTimeout(context.Background(), 5_000_000_000)
		defer cancel()
		_ = m.Long.Store(ctx, content, map[string]string{
			"type": "conversation", "session": sessionID, "user": user,
		})
	}()
}

// ReplaceLast 覆写某会话最近一条工作记忆（reflect 修订版替换原回答）。
// 只改工作记忆，不动长期记忆（修订版与原始回答的语义召回均可接受，避免
// 为同一轮追加两条长期记录造成膨胀）。
func (m *Manager) ReplaceLast(sessionID, userInput, assistant string) {
	if m.Working != nil {
		m.Working.ReplaceLast(sessionID, userInput, assistant)
	}
}

// TenantScoped 可选接口：长期记忆支持按租户切分（QdrantMemory 实现）。
type TenantScoped interface {
	ForTenant(tenant string) Memory
}

// UserScoped 可选接口：长期记忆支持按用户删除（被遗忘权）。
// 实现须保证只删除该用户的记忆，不误伤同租户其他用户。
type UserScoped interface {
	ForgetUser(ctx context.Context, user string) error
}

// ForgetTenant 被遗忘权（/GDPR）：删除某租户的全部长期记忆。
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

// ForgetUser 被遗忘权（/GDPR）：删除某用户的全部长期记忆。
// 优先走按用户删除（不会误删同租户其他用户）；存储不支持按用户删除时，
// 退化为整租户清空并返回错误说明，由调用方决定是否接受。
func (m *Manager) ForgetUser(ctx context.Context, user string) error {
	if m.Long == nil {
		return nil
	}
	if us, ok := m.Long.(UserScoped); ok {
		return us.ForgetUser(ctx, user)
	}
	if err := m.ForgetTenant(ctx, "default"); err != nil {
		return err
	}
	return fmt.Errorf("长期记忆不支持按用户删除，已整租户清空（可能误删其他用户）")
}

// ForgetSession 清空某会话的工作记忆（被遗忘权的一部分）。
func (m *Manager) ForgetSession(sessionID string) {
	if m.Working != nil {
		m.Working.Clear(sessionID)
	}
}

// Recall 检索相关记忆：先合并工作记忆的最近摘要，再叠加长期记忆语义检索。
// user 用于长期记忆按用户过滤（防止检索到同租户其他用户的对话）。
func (m *Manager) Recall(ctx context.Context, sessionID, user, query string, limit int) []string {
	var out []string
	if m.Working != nil {
		out = append(out, m.Working.Recent(sessionID, 2)...) // 最近两轮工作记忆
	}
	if m.Long != nil {
		if items, err := m.Long.Retrieve(ctx, user, query, limit); err == nil {
			out = append(out, items...)
		}
	}
	return out
}
