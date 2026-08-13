package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// fakeTool 供测试的最小工具。
type fakeTool struct{}

func (fakeTool) Name() string        { return "fake" }
func (fakeTool) Description() string { return "test tool" }
func (fakeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"qty": map[string]interface{}{"type": "integer"},
		},
		"required": []interface{}{"qty"},
	}
}
func (fakeTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	return "executed:" + str(args["qty"]), nil
}

// riskyTool 高危工具。
type riskyTool struct{ fakeTool }

func (riskyTool) Name() string           { return "risky" }
func (riskyTool) RiskLevel() int         { return 2 }
func (riskyTool) AllowedRoles() []string { return []string{"admin"} }

func str(v interface{}) string { b, _ := json.Marshal(v); return string(b) }

func TestValidateArgs(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})
	// 缺必填
	if _, err := r.Execute(context.Background(), "fake", map[string]interface{}{}, "u", "admin", false); err == nil {
		t.Fatal("缺必填参数应报错")
	}
	// 类型不符
	if _, err := r.Execute(context.Background(), "fake", map[string]interface{}{"qty": "abc"}, "u", "admin", false); err == nil {
		t.Fatal("类型错误应报错")
	}
	// 正确调用
	res, err := r.Execute(context.Background(), "fake", map[string]interface{}{"qty": 5}, "u", "admin", false)
	if err != nil || res != "executed:5" {
		t.Fatalf("期望成功执行，got %q err=%v", res, err)
	}
}

func TestRoleAllowlist(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})
	r.Register(riskyTool{})

	// user 无白名单配置 → 全开（默认行为）
	if _, err := r.Execute(context.Background(), "fake", map[string]interface{}{"qty": 1}, "u", "user", false); err != nil {
		t.Fatalf("user 默认应可用，err=%v", err)
	}

	// 高危工具：user 不在 AllowedRoles，即使 confirm 也拒绝（D20）
	if _, err := r.Execute(context.Background(), "risky", map[string]interface{}{}, "u", "user", true); err == nil {
		t.Fatal("高危工具 user 应被拒绝")
	}
	// admin 高危未确认 → 拒绝
	if _, err := r.Execute(context.Background(), "risky", map[string]interface{}{}, "u", "admin", false); err == nil {
		t.Fatal("高危工具未二次确认应拒绝")
	}
	// admin + confirm → 通过（risky 继承 fake 的必填 qty）
	if _, err := r.Execute(context.Background(), "risky", map[string]interface{}{"qty": 1}, "u", "admin", true); err != nil {
		t.Fatalf("admin 二次确认后应通过，err=%v", err)
	}

	// 白名单收口：收回 user 的 fake
	r.DenyTool("user", "fake")
	if _, err := r.Execute(context.Background(), "fake", map[string]interface{}{"qty": 1}, "u", "user", false); err == nil {
		t.Fatal("白名单收回后 user 应被拒绝")
	}
}

func TestToolsForRoleFiltering(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})
	r.Register(riskyTool{})
	r.AllowTool("user", "fake")

	for _, role := range []string{"user", "admin"} {
		tools := r.ToolsFor(role)
		if len(tools) == 0 {
			t.Fatalf("%s 至少可见 fake", role)
		}
	}
}

// TestRegistryNames P31：启动清单需要"全部工具名"（排序）。
func TestRegistryNames(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})  // "fake"
	r.Register(riskyTool{}) // "risky"
	names := r.Names()
	want := []string{"fake", "risky"}
	if !reflect.DeepEqual(names, want) || !sort.StringsAreSorted(names) {
		t.Fatalf("Names 应排序且完整: %v", names)
	}
	if len(names) != 2 {
		t.Fatalf("Names 数量错误: %v", names)
	}
}

// TestRegistryDescriptions 启动清单需要工具名 → 描述映射。
func TestRegistryDescriptions(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})
	descs := r.Descriptions()
	if descs["fake"] != "test tool" {
		t.Fatalf("描述映射错误: %v", descs)
	}
	if len(descs) != 1 {
		t.Fatalf("描述数量错误: %v", descs)
	}
}
