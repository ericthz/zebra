// 知识库文档加载：读取 docs/ 目录下的 .md / .txt 文档作为 RAG 摄取源
// 供 cmd/server 与 cmd/zebra 共用（保证两个入口的知识库装配一致）。
package rag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadDocs 读取 dir 目录下的 .md / .txt 文档，返回 文件名 → 内容。
func LoadDocs(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		out[name] = string(data)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no docs found")
	}
	return out, nil
}
