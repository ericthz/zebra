package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDir 验证 SKILL.md 目录扫描 + frontmatter 解析 + 正文抽取。
func TestLoadDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "report", "SKILL.md"), `---
name: report-sop
description: 当用户要求写报告时使用
version: 1.0.0
---
# 研究报告
第一步：收集事实
第二步：起草结构`)
	writeFile(t, filepath.Join(root, "data", "SKILL.md"), `---
name: data-check
description: 当用户要求核对数据时使用
---
# 数据核对
永远用工具重新计算。`)
	// 目录里混一个无 SKILL.md 的子目录，应被忽略
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)

	skills, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("期望 2 个技能，实际 %d", len(skills))
	}

	byName := map[string]*Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}

	report := byName["report-sop"]
	if report == nil {
		t.Fatal("缺 report-sop")
	}
	if report.Description == "" || report.Version != "1.0.0" {
		t.Fatalf("frontmatter 解析错误: %+v", report)
	}
	if !contains(report.Instructions, "第一步：收集事实") {
		t.Fatalf("正文抽取错误: %q", report.Instructions)
	}
	if report.Dir != filepath.Join(root, "report") {
		t.Fatalf("Dir 应为技能源目录: %s", report.Dir)
	}
}

// TestMatch 验证关键词检索排序（懒加载：命中才返回）。
func TestMatch(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{Name: "report-sop", Description: "写研究报告、周报、总结"})
	r.Register(&Skill{Name: "data-check", Description: "核对数据、校验计算"})
	r.Register(&Skill{Name: "translate", Description: "文本翻译"})

	hits := r.Match("帮我写一份研究报告", 1)
	if len(hits) != 1 || hits[0].Name != "report-sop" {
		t.Fatalf("应命中 report-sop，实际 %+v", hits)
	}

	hits = r.Match("核对计算", 2)
	if len(hits) < 1 || hits[0].Name != "data-check" {
		t.Fatalf("应优先命中 data-check，实际 %+v", hits)
	}

	if len(r.Match("随便聊聊", 3)) != 0 {
		t.Fatal("无关查询不应命中技能")
	}
}

// TestRegistryConcurrent 并发读写安全性。
func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			r.Register(&Skill{Name: "s", Description: "x"})
			r.Match("x", 1)
			r.List()
		}
	}()
	for i := 0; i < 100; i++ {
		r.Get("s")
		r.List()
	}
	<-done
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
