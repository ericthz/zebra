package memory

import (
	"context"
	"testing"
)

// fakeLongMemory 实现 Memory + TenantScoped + UserScoped，用于测试分层管理。
type fakeLongMemory struct {
	tenant        string
	cleared       bool
	forgottenUser string
}

func (f *fakeLongMemory) Store(context.Context, string, map[string]string) error { return nil }
func (f *fakeLongMemory) Retrieve(context.Context, string, string, int) ([]string, error) {
	return nil, nil
}
func (f *fakeLongMemory) Clear(context.Context) error { f.cleared = true; return nil }
func (f *fakeLongMemory) ForTenant(tenant string) Memory {
	f.tenant = tenant
	return f
}
func (f *fakeLongMemory) ForgetUser(_ context.Context, user string) error {
	f.forgottenUser = user
	return nil
}

func TestManagerForget(t *testing.T) {
	w := NewWorkingMemory(10)
	long := &fakeLongMemory{}
	m := NewManager(w, long)

	ctx := context.Background()
	m.Remember("sess-1", "alice", "你好", "你好呀")
	m.Remember("sess-2", "bob", "在吗", "在的")

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

	// 按用户删除长期记忆（应只删该用户，不误删同租户其他用户）
	long.cleared = false
	if err := m.ForgetUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if long.forgottenUser != "alice" || long.cleared {
		t.Fatal("应按用户删除，不应整租户清空")
	}
}

// TestManagerForgetUserFallback 存储不支持按用户删除时：退化为整租户清空并返回错误说明。
func TestManagerForgetUserFallback(t *testing.T) {
	w := NewWorkingMemory(10)
	long := &tenantOnlyLongMemory{}
	m := NewManager(w, long)
	if err := m.ForgetUser(context.Background(), "alice"); err == nil {
		t.Fatal("不支持按用户删除时应返回错误说明")
	}
	if !long.cleared {
		t.Fatal("退化为整租户清空")
	}
}

// tenantOnlyLongMemory 仅实现 TenantScoped（无 UserScoped），验证退化路径。
type tenantOnlyLongMemory struct {
	cleared bool
}

func (f *tenantOnlyLongMemory) Store(context.Context, string, map[string]string) error { return nil }
func (f *tenantOnlyLongMemory) Retrieve(context.Context, string, string, int) ([]string, error) {
	return nil, nil
}
func (f *tenantOnlyLongMemory) Clear(context.Context) error    { f.cleared = true; return nil }
func (f *tenantOnlyLongMemory) ForTenant(tenant string) Memory { return f }
