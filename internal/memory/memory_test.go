package memory

import (
	"context"
	"testing"
)

// fakeLongMemory 实现 Memory + TenantScoped，用于测试分层管理。
type fakeLongMemory struct {
	tenant  string
	cleared bool
}

func (f *fakeLongMemory) Store(context.Context, string, map[string]string) error { return nil }
func (f *fakeLongMemory) Retrieve(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (f *fakeLongMemory) Clear(context.Context) error { f.cleared = true; return nil }
func (f *fakeLongMemory) ForTenant(tenant string) Memory {
	f.tenant = tenant
	return f
}

func TestManagerForget(t *testing.T) {
	w := NewWorkingMemory(10)
	long := &fakeLongMemory{}
	m := NewManager(w, long)

	ctx := context.Background()
	m.Remember("sess-1", "你好", "你好呀")
	m.Remember("sess-2", "在吗", "在的")

	// 清空某会话工作记忆
	m.ForgetSession("sess-1")
	if got := w.Recent("sess-1", 5); len(got) != 0 {
		t.Fatal("sess-1 工作记忆应清空")
	}
	if got := w.Recent("sess-2", 5); len(got) == 0 {
		t.Fatal("sess-2 工作记忆应保留")
	}

	// 清空某租户长期记忆（应调用 ForTenant(tenant).Clear）
	if err := m.ForgetTenant(ctx, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if long.tenant != "tenant-a" || !long.cleared {
		t.Fatal("应按租户清空长期记忆")
	}
}
