package provider

import (
	"context"
	"testing"
)

// namedProvider 带名字的静态 Provider（Promote 测试用）。
type namedProvider struct {
	name string
}

func (n *namedProvider) Name() string { return n.name }
func (n *namedProvider) Chat(context.Context, []Message, []Tool) (Message, error) {
	return Message{Role: "assistant", Content: n.name}, nil
}
func (n *namedProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

func TestRouterPromote(t *testing.T) {
	r := NewRouter(&namedProvider{name: "primary"}, &namedProvider{name: "candidate"}, &namedProvider{name: "backup"})
	if r.Primary().Name() != "primary" {
		t.Fatal("初始主模型应为 primary")
	}

	// 提升候选 → 变为新主，返回被顶替的原主；顺序保持其余不变
	prev, ok := r.Promote("candidate")
	if !ok || prev != "primary" {
		t.Fatal("promote 应成功")
	}
	if r.Primary().Name() != "candidate" {
		t.Fatalf("promote 后主模型应为 candidate，实际 %s", r.Primary().Name())
	}
	chain := r.Chain()
	if chain[1].Name() != "primary" || chain[2].Name() != "backup" {
		t.Fatalf("promote 后顺序错误: %s %s %s", chain[0].Name(), chain[1].Name(), chain[2].Name())
	}

	// 未找到 → ok=false；已为主 → prev 为空
	if _, ok := r.Promote("ghost"); ok {
		t.Fatal("不存在的模型不应 promote 成功")
	}
	if prev, ok := r.Promote("candidate"); !ok || prev != "" {
		t.Fatalf("已为主模型应返回 (\"\", true): prev=%q ok=%v", prev, ok)
	}
}
