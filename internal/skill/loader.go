// SKILL.md 加载器：扫描目录、解析 frontmatter、提取指令正文。
//
// SKILL.md 约定格式：
//
//	---
//	name: report-sop
//	description: 当用户要求撰写研究报告/周报/总结时使用
//	version: 1.0.0
//	---
//	（正文：给模型的 SOP 指令，支持 Markdown）
//
// 说明：本实现为手写极简 frontmatter 解析（单行键值，够教学用）；
// 生产可直接换用 yaml 库，解析函数签名不变。
package skill

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDir 递归扫描 root 下所有含 SKILL.md 的目录并解析为技能列表。
func LoadDir(root string) ([]*Skill, error) {
	var skills []*Skill
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		s, perr := parseSKILLFile(path)
		if perr != nil {
			return perr
		}
		skills = append(skills, s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return skills, nil
}

// parseSKILLFile 解析单个 SKILL.md：
//   - 读取 frontmatter（--- 之间的键值）
//   - 剩余部分作为 Instructions 正文
func parseSKILLFile(path string) (*Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	s := &Skill{Dir: filepath.Dir(path), Path: path}
	var body []string
	inFront := false
	done := false

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !done {
			if line == "---" {
				if !inFront {
					inFront = true // 进入 frontmatter
				} else {
					done = true // 结束 frontmatter，进入正文
				}
				continue
			}
			if inFront {
				if k, v, ok := strings.Cut(line, ":"); ok {
					switch strings.TrimSpace(k) {
					case "name":
						s.Name = strings.TrimSpace(v)
					case "description":
						s.Description = strings.TrimSpace(v)
					case "version":
						s.Version = strings.TrimSpace(v)
					}
				}
				continue
			}
		}
		body = append(body, sc.Text()) // 正文保留原始缩进
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	s.Instructions = strings.TrimSpace(strings.Join(body, "\n"))
	if s.Name == "" {
		return nil, os.ErrInvalid // 无 name 视为非法技能
	}
	return s, nil
}
