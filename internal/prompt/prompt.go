// Package prompt Prompt 工程管理（C16）：模板化 + 版本化 + 灰度切换。
//
// 生产演进方向：模板仓库 + A/B 测试 + 线上效果回流，本包保持最简骨架。
package prompt

import (
	"fmt"
	"strings"
	"sync"
)

// Template 一个版本的提示词模板。
type Template struct {
	Name    string
	Version string
	Text    string
}

// Registry 模板注册表：name → 各版本 → 当前生效版本。
type Registry struct {
	mu     sync.RWMutex
	name   string
	active map[string]string // name → active version
	byVer  map[string]map[string]*Template
}

// NewRegistry 构造。
func NewRegistry(name string) *Registry {
	return &Registry{name: name, active: make(map[string]string), byVer: make(map[string]map[string]*Template)}
}

// Register 注册模板版本；首个版本自动成为 active。
func (r *Registry) Register(tmpl *Template) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byVer[tmpl.Version] == nil {
		r.byVer[tmpl.Version] = make(map[string]*Template)
	}
	r.byVer[tmpl.Version][tmpl.Name] = tmpl
	if _, ok := r.active[tmpl.Name]; !ok {
		r.active[tmpl.Name] = tmpl.Version
	}
}

// Activate 切换某模板的生效版本（灰度时先小流量切）。
func (r *Registry) Activate(name, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byVer[version][name] == nil {
		return fmt.Errorf("版本不存在: %s@%s", name, version)
	}
	r.active[name] = version
	return nil
}

// Render 按生效版本渲染模板，用 {key} 占位符替换。
func (r *Registry) Render(name string, vars map[string]string) (string, error) {
	r.mu.RLock()
	version := r.active[name]
	tmpl := r.byVer[version][name]
	r.mu.RUnlock()
	if tmpl == nil {
		return "", fmt.Errorf("模板不存在: %s", name)
	}
	out := tmpl.Text
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out, nil
}

// LoadAll 批量注册模板（供 LoadDir 后调用；P18 热更新用）。
func (r *Registry) LoadAll(templates []*Template) {
	for _, t := range templates {
		r.Register(t)
	}
}

// Active 返回某模板当前版本号。
func (r *Registry) Active(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active[name]
}
