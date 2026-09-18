// 提示词模板文件加载器（热更新基础）。
//
// 约定：prompts/ 目录下每个 .md 文件是一个模板：
//
//	---
//	name: assistant
//	version: v1
//	---
//	（正文即模板文本，支持 {var} 占位符）
//
// 这样模板可以"改文件 → 热重载 → 不重启服务"，比硬编码在代码里更接近生产。
// 生产演化方向：模板仓库 + 版本灰度 + A/B，本实现保持本地文件。
package prompt

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDir 扫描 dir 下所有 .md 文件并解析为模板列表。
func LoadDir(dir string) ([]*Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Template
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		t, perr := parseTemplateFile(filepath.Join(dir, e.Name()))
		if perr != nil {
			return nil, perr
		}
		out = append(out, t)
	}
	return out, nil
}

// parseTemplateFile 解析单个模板文件（frontmatter + 正文）。
func parseTemplateFile(path string) (*Template, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	t := &Template{}
	var body []string
	inFront, done := false, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !done {
			if line == "---" {
				if !inFront {
					inFront = true
				} else {
					done = true
				}
				continue
			}
			if inFront {
				if k, v, ok := strings.Cut(line, ":"); ok {
					switch strings.TrimSpace(k) {
					case "name":
						t.Name = strings.TrimSpace(v)
					case "version":
						t.Version = strings.TrimSpace(v)
					}
				}
				continue
			}
		}
		body = append(body, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	t.Text = strings.TrimSpace(strings.Join(body, "\n"))
	if t.Name == "" || t.Version == "" {
		return nil, os.ErrInvalid
	}
	return t, nil
}
