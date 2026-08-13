package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "---\nname: assistant\nversion: v1\n---\n你是 {role} 助手")
	write("b.md", "---\nname: data\nversion: v2\n---\n你是数据专家")
	write("ignore.txt", "---\nname: x\n---\nnot a template")

	templates, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 2 {
		t.Fatalf("应加载 2 个模板（忽略 .txt），实际 %d", len(templates))
	}

	reg := NewRegistry("z")
	reg.LoadAll(templates)
	out, err := reg.Render("assistant", map[string]string{"role": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "你是 admin 助手" {
		t.Fatalf("渲染结果错误: %q", out)
	}
	if reg.Active("data") != "v2" {
		t.Fatalf("首个版本应生效: %s", reg.Active("data"))
	}
}
