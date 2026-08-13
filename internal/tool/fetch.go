// fetch_url 工具：让 Agent 主动抓取网页（P6 SSRF 防护的真实落点）。
//
// 为什么它比内置网络工具更危险：weather/search 等工具的 URL 主机名是
// 写死的公网域名，SSRF 风险低；而 fetch_url 的 URL 由模型/用户完全控制，
// 必须做 SSRF 校验，否则 Agent 可能被诱导访问内网（云元数据、内网服务）。
//
// 安全设计：
//  1. CheckSSRF：协议白名单 + 内网 IP 拦截 + 可选域名白名单（P6）
//  2. 大小限制：只读前 MaxBytes，防下载巨文件撑爆内存
//  3. 超时：单次请求带超时，防挂起
//  4. 内容类型：默认仅文本类（HTML/JSON/纯文本），防二进制
//
// 生产演化方向：抓取结果先过内容审核（D18）与注入防护（D17）再交给模型。
package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/safety"
)

// FetchURLTool 抓取 URL 内容。
type FetchURLTool struct {
	AllowHosts []string // 域名白名单（空 = 不限域名，但拦截内网）
	MaxBytes   int      // 响应体大小上限
}

func (t *FetchURLTool) Name() string { return "fetch_url" }
func (t *FetchURLTool) Description() string {
	return "抓取指定 URL 的文本内容（受 SSRF 防护限制，仅限公网地址）。"
}
func (t *FetchURLTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "要抓取的 URL（http/https）"},
		},
		"required": []string{"url"},
	}
}

// Execute 抓取 URL。
func (t *FetchURLTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	raw := StringArg(args, "url")
	if raw == "" {
		return "", fmt.Errorf("缺少 url 参数")
	}
	// P6 SSRF 第一道防线：协议 + 内网 + 白名单校验
	if err := safety.CheckSSRF(raw, t.AllowHosts); err != nil {
		return "", err
	}

	maxBytes := t.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "zebra-agent/0.1")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 大小上限读取
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxBytes {
		return "", fmt.Errorf("内容超过大小上限 %d 字节，已拒绝", maxBytes)
	}

	ct := resp.Header.Get("Content-Type")
	// 仅接受文本类内容（防二进制/恶意文件）
	if !strings.HasPrefix(ct, "text/") && !strings.Contains(ct, "json") && !strings.Contains(ct, "html") && ct != "" {
		return "", fmt.Errorf("非文本内容类型 %q，已拒绝", ct)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "（页面无内容）", nil
	}
	return text, nil
}
