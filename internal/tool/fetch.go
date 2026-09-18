// fetch_url 工具：让 Agent 主动抓取网页（SSRF 防护的真实落点）。
//
// 为什么它比内置网络工具更危险：weather/search 等工具的 URL 主机名是
// 写死的公网域名，SSRF 风险低；而 fetch_url 的 URL 由模型/用户完全控制，
// 必须做 SSRF 校验，否则 Agent 可能被诱导访问内网（云元数据、内网服务）。
//
// 安全设计：
//  1. CheckSSRF：协议白名单 + 内网 IP 拦截 + 可选域名白名单+ DNS 解析后校验
//  2. 大小限制：只读前 MaxBytes，防下载巨文件撑爆内存
//  3. 超时：单次请求带超时，防挂起
//  4. 内容类型：默认仅文本类（HTML/JSON/纯文本），防二进制
//  5. 抓取结果先过内容审核与注入防护再交给模型
package tool

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/safety"
)

// FetchURLTool 抓取 URL 内容。
type FetchURLTool struct {
	AllowHosts []string         // 域名白名单（空 = 不限域名，但拦截内网）
	MaxBytes   int              // 响应体大小上限
	Moderator  safety.Moderator // 内容审核器（nil = 跳过，但注入检测始终生效）
	BlockFetch bool             // 命中敏感词/注入时拒绝返回内容（false = 返回摘要说明）
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
	// SSRF 第一道防线：协议 + 内网 + 白名单校验。
	// 返回解析后的安全 IP，供 DialContext 绑定，消除"校验时解析公网 IP、
	// 连接时重绑内网 IP"的 DNS 重绑定 TOCTOU 窗口。
	safeIPs, err := safety.ResolveSSRF(raw, t.AllowHosts)
	if err != nil {
		return "", err
	}

	maxBytes := t.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	// 连接绑定解析出的安全 IP（保留 Host 头与 TLS SNI 为原主机名）：
	// 只有 ResolveSSRF 解析/校验过的目标才能被连接，杜绝重绑定。
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// 白名单放行（safeIPs==nil）时按标准解析走；否则必须命中安全 IP
			if len(safeIPs) > 0 {
				dest := net.JoinHostPort(safeIPs[0].String(), port)
				return dialer.DialContext(dctx, network, dest)
			}
			// 白名单域名：直接按原地址连接（可信域名）
			return dialer.DialContext(dctx, network, addr)
		},
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		// 重定向也必须过 SSRF：初始 URL 校验通过后，恶意服务器可通过 302
		// 把请求重定向到内网（云元数据/内网服务），故对每个跳转目标再次校验。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			if err := safety.CheckSSRF(req.URL.String(), t.AllowHosts); err != nil {
				return err
			}
			return nil
		},
	}
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

	// 内容审核：抓取结果先过审核器，命中敏感词则拒绝（防把违规内容喂给模型）
	if t.Moderator != nil {
		if allowed, reason := t.Moderator.Check(text); !allowed {
			if t.BlockFetch {
				return "", fmt.Errorf("抓取内容未通过内容审核：%s", reason)
			}
			return "（抓取内容命中敏感内容，已拦截不返回原文：" + reason + "）", nil
		}
	}

	// 注入检测：外部网页可能夹带"忽略以上指令"类恶意文本，检测后拒收
	if hit, _ := safety.DetectInjection(text); hit {
		if t.BlockFetch {
			return "", fmt.Errorf("抓取内容疑似夹带指令注入，已拒绝")
		}
		return "（抓取内容疑似夹带指令注入，已拦截不返回原文）", nil
	}

	return text, nil
}
