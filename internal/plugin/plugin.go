// 插件动态加载：从目录加载 JSON 定义的外部 HTTP 工具，运行时注册。
//
// 背景：工具目前是编译期注册（改一个工具就要改代码重编译）。本包让
// "新增一个外部服务工具"变成"放一个 JSON 文件"——配合热更新
// 运行时即可加载/卸载插件工具。
//
// 协议：插件工具 Execute 时 POST 到 def.url，body 为 {"args":{...}}，
// 响应文本作为工具结果返回。生产演化方向：插件鉴权、超时/重试、
// SSRF 校验、插件市场/版本管理。
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/tool"
)

// Def 插件工具定义。
type Def struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	URL         string                 `json:"url"`                   // POST，body {"args":{...}}
	Parameters  map[string]interface{} `json:"parameters"`            // JSON Schema 子集
	AllowHosts  []string               `json:"allow_hosts,omitempty"` // SSRF 域名白名单（空=不限域名但拦截内网 IP）
}

// Load 从 dir 读取 *.json 插件定义（文件结构 {"plugins":[...]}）。
func Load(dir string) ([]Def, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Def
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var file struct {
			Plugins []Def `json:"plugins"`
		}
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, file.Plugins...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no plugins in %s", dir)
	}
	return out, nil
}

// HTTPPluginTool 通过 HTTP POST 调用的插件工具。
type HTTPPluginTool struct {
	def Def
}

func (t *HTTPPluginTool) Name() string        { return t.def.Name }
func (t *HTTPPluginTool) Description() string { return t.def.Description }
func (t *HTTPPluginTool) Parameters() map[string]interface{} {
	return t.def.Parameters
}

// Execute POST {"args":{...}} 到插件 URL，响应文本即工具结果。
func (t *HTTPPluginTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	// SSRF 校验：插件 URL 若被篡改指向内网，Agent 会成为内网代理。
	// AllowHosts 白名单命中则放行（供内部插件显式声明），否则拦截内网/本地地址。
	// 返回解析后的安全 IP，供 DialContext 绑定，消除 DNS 重绑定 TOCTOU 窗口（六8）。
	safeIPs, err := safety.ResolveSSRF(t.def.URL, t.def.AllowHosts)
	if err != nil {
		return "", fmt.Errorf("plugin %s: %w", t.def.Name, err)
	}
	body, _ := json.Marshal(map[string]interface{}{"args": args})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.def.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	// 连接绑定解析出的安全 IP（保留 Host 头/TLS SNI 为原主机名），
	// 只有 ResolveSSRF 解析并校验过的目标才能被连接，杜绝重绑定。
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if len(safeIPs) > 0 {
				return dialer.DialContext(dctx, network, net.JoinHostPort(safeIPs[0].String(), port))
			}
			return dialer.DialContext(dctx, network, addr) // 白名单域名：标准解析
		},
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		// 重定向也必须过 SSRF（六8）：初始 URL 校验通过后，恶意服务器可借 302
		// 把请求转向内网（云元数据/内网服务），对每个跳转目标再次校验。
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			if err := safety.CheckSSRF(r.URL.String(), t.def.AllowHosts); err != nil {
				return err
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("plugin %s: %d %s", t.def.Name, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

// Register 把插件定义注册为工具，返回已注册的工具名（供热重载移除）。
func Register(reg *tool.Registry, defs []Def) []string {
	var names []string
	for _, d := range defs {
		if d.Name == "" || d.URL == "" {
			continue
		}
		reg.Register(&HTTPPluginTool{def: d})
		names = append(names, d.Name)
	}
	return names
}
