package memory

import (
	"testing"
	"time"
)

func TestExtractFacts(t *testing.T) {
	// 自我介绍 + 偏好 + 工作，一条话抽多条事实
	facts := ExtractFacts("我叫小明，我在字节跳动工作，我喜欢吃火锅，今年 28 岁。")
	got := map[string]string{}
	for _, f := range facts {
		got[f.Key] = f.Value
	}
	if got["name"] != "小明" {
		t.Fatalf("名字抽取错误: %+v", got)
	}
	if got["work"] != "字节跳动" {
		t.Fatalf("工作抽取错误: %+v", got)
	}
	if got["preference"] != "火锅" {
		t.Fatalf("偏好抽取错误: %+v", got)
	}
	if got["age"] != "28" {
		t.Fatalf("年龄抽取错误: %+v", got)
	}

	// 单独验证"住在"类地点抽取
	if facts := ExtractFacts("我住在北京朝阳区。"); len(facts) != 1 || facts[0].Key != "location" || facts[0].Value != "北京朝阳区" {
		t.Fatalf("地点抽取错误: %+v", facts)
	}

	// 反例："我是程序员"不应误抽为名字（规则只认显式自我介绍）
	if facts := ExtractFacts("我是程序员，负责后端开发。"); len(facts) != 0 {
		t.Fatalf("不应抽到名字: %+v", facts)
	}
}

func TestProfileLearnTTLAndMerge(t *testing.T) {
	s := NewProfileStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	s.Learn("alice", ExtractFacts("我叫小明，我喜欢吃火锅。"), now)
	if s.Count("alice") != 2 {
		t.Fatalf("应学到 2 条事实，实际 %d", s.Count("alice"))
	}

	// TTL 内可见
	if got := s.FactsFor("alice", now.Add(30*time.Second), time.Minute); len(got) != 2 {
		t.Fatalf("TTL 内应返回 2 条，实际 %d", len(got))
	}
	// 超过 TTL 惰性隐藏
	if got := s.FactsFor("alice", now.Add(2*time.Minute), time.Minute); len(got) != 0 {
		t.Fatalf("超 TTL 应隐藏，实际 %d", len(got))
	}
	// ttl<=0 永不过期
	if got := s.FactsFor("alice", now.Add(24*time.Hour), 0); len(got) != 2 {
		t.Fatalf("ttl=0 应永不过期，实际 %d", len(got))
	}

	// 同 key 更新：新值 + 更高置信度覆盖；低置信度不覆盖
	s.Learn("alice", []Fact{{Key: "name", Value: "大明", Confidence: 0.99}}, now.Add(time.Minute))
	fs := s.FactsFor("alice", now.Add(time.Minute), time.Hour)
	name := findFact(fs, "name")
	if name.Value != "大明" {
		t.Fatalf("高置信度新值应覆盖: %+v", name)
	}
	s.Learn("alice", []Fact{{Key: "name", Value: "小名", Confidence: 0.3}}, now.Add(2*time.Minute))
	fs = s.FactsFor("alice", now.Add(2*time.Minute), time.Hour)
	if findFact(fs, "name").Value != "大明" {
		t.Fatalf("低置信度不应覆盖高置信度")
	}
}

func TestProfileForgetAndSweep(t *testing.T) {
	s := NewProfileStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s.Learn("alice", ExtractFacts("我叫小明，我喜欢吃火锅。"), now)
	s.Learn("bob", ExtractFacts("我叫小红。"), now)

	if !s.ForgetKey("alice", "name") {
		t.Fatal("应能删除 name 事实")
	}
	if s.ForgetKey("alice", "nonexistent") {
		t.Fatal("不存在的 key 不应返回 true")
	}
	if n := s.Sweep(now.Add(2*time.Minute), time.Minute); n != 2 {
		t.Fatalf("应清理 2 条过期事实（alice 剩 1 条 + bob 1 条），实际 %d", n)
	}
	if s.Count("alice") != 0 || s.Count("bob") != 0 {
		t.Fatal("过期清理后应为 0")
	}

	// 被遗忘权：删整个用户
	s.Learn("carol", ExtractFacts("我叫小王。"), now)
	if n := s.ForgetUser("carol"); n != 1 {
		t.Fatalf("应删除 carol 1 条，实际 %d", n)
	}
}

func TestForgetPolicyCapacity(t *testing.T) {
	s := NewProfileStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s.Learn("alice", []Fact{
		{Key: "a", Value: "1", Confidence: 0.9},
		{Key: "b", Value: "2", Confidence: 0.9},
		{Key: "c", Value: "3", Confidence: 0.9},
	}, now)
	s.Learn("alice", []Fact{{Key: "d", Value: "4", Confidence: 0.9}}, now.Add(time.Minute))

	forgotten := 0
	p := &ForgetPolicy{TTL: time.Hour, MaxFactsPerUser: 2, OnForget: func(_, _ string) { forgotten++ }}
	n := p.Apply(s, now.Add(2*time.Minute))
	if n != 2 { // 4 条 → 保留 2 条，丢 2 条
		t.Fatalf("应丢 2 条，实际 %d", n)
	}
	if forgotten != 1 {
		t.Fatalf("容量裁剪钩子应按用户触发 1 次，实际 %d", forgotten)
	}
	if s.Count("alice") != 2 {
		t.Fatalf("容量裁剪后应剩 2 条，实际 %d", s.Count("alice"))
	}
}

// TestProfileConflictAndResolve P57：同 key 不同取值记录冲突，可裁决回退。
func TestProfileConflictAndResolve(t *testing.T) {
	s := NewProfileStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s.Learn("alice", []Fact{{Key: "preference", Value: "火锅", Confidence: 0.85}}, now)
	// 新值 0.9 ≥ 0.85*0.8 → 记录冲突并覆盖
	s.Learn("alice", []Fact{{Key: "preference", Value: "烧烤", Confidence: 0.9}}, now.Add(time.Minute))

	cs := s.ConflictsFor("alice")
	if len(cs) != 1 || cs[0].OldValue != "火锅" || cs[0].NewValue != "烧烤" {
		t.Fatalf("冲突记录异常: %+v", cs)
	}
	if fs := s.FactsFor("alice", now.Add(time.Minute), time.Hour); findFact(fs, "preference").Value != "烧烤" {
		t.Fatal("覆盖后当前值应为新值")
	}
	// 裁决：回退旧值并清除冲突
	if !s.ResolveConflict("alice", "preference", true) {
		t.Fatal("应能裁决冲突")
	}
	if fs := s.FactsFor("alice", time.Now(), time.Hour); findFact(fs, "preference").Value != "火锅" {
		t.Fatalf("回退后应为旧值: %+v", fs)
	}
	if len(s.ConflictsFor("alice")) != 0 {
		t.Fatal("裁决后冲突应清空")
	}
	if s.ResolveConflict("alice", "name", false) {
		t.Fatal("不存在冲突应返回 false")
	}
}

// TestProfileConsolidate P57：同分类下取值归一化相同的事实合并，保留高置信度。
func TestProfileConsolidate(t *testing.T) {
	s := NewProfileStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s.Learn("alice", []Fact{{Key: "preference", Value: "火锅", Confidence: 0.7}}, now)
	s.Learn("alice", []Fact{{Key: "hobby", Value: "吃火锅", Confidence: 0.9}}, now.Add(time.Minute))
	if s.Count("alice") != 2 {
		t.Fatalf("前置应 2 条，实际 %d", s.Count("alice"))
	}
	if n := s.Consolidate("alice"); n != 1 {
		t.Fatalf("应合并 1 条，实际 %d", n)
	}
	fs := s.FactsFor("alice", now.Add(time.Minute), time.Hour)
	if len(fs) != 1 || fs[0].Key != "hobby" || fs[0].Value != "吃火锅" {
		t.Fatalf("合并后应保留高置信度 hobby=吃火锅: %+v", fs)
	}
}

func findFact(fs []Fact, key string) Fact {
	for _, f := range fs {
		if f.Key == key {
			return f
		}
	}
	return Fact{}
}
