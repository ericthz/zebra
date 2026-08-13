// SSRF 防护（P6 安全加固）。
//
// 背景：Agent 会主动拉取 URL（搜索、天气、抓取网页）。若 URL 由用户/外部
// 内容控制，恶意输入可能让 Agent 访问内网（如 http://169.254.169.254 云元数据、
// http://127.0.0.1:6379 内网 Redis），这就是 SSRF（服务端请求伪造）。
//
// 防护策略（层层设防，本文件实现第一层）：
//   1. 协议白名单：仅 http/https
//   2. 可选域名白名单：allowHosts 非空时，只放行白名单域名
//   3. IP 黑名单：IP 字面量命中内网/环回/链路本地/未指定 → 拒绝
//   4. 主机名：生产应做 DNS 解析后递归校验（防 DNS 重绑定）；本实现默认放行公开域名
//
// 生产演化方向：DNS 解析后校验、内网网段 CIDR 可配置、出站代理统一管控。
package safety

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CheckSSRF 校验目标 URL 是否可安全访问；不安全返回错误。
// allowHosts 为空表示不限制域名（但仍拦截内网 IP）。
func CheckSSRF(rawURL string, allowHosts []string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("URL 解析失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("SSRF 防护：仅允许 http/https，收到 %q", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("SSRF 防护：URL 缺少主机名")
	}

	// 域名白名单：非空则只放行白名单内域名
	if len(allowHosts) > 0 {
		for _, h := range allowHosts {
			if strings.EqualFold(host, h) {
				return nil
			}
		}
		return fmt.Errorf("SSRF 防护：域名 %q 不在白名单内", host)
	}

	// IP 字面量：命中内网/环回等直接拒绝
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("SSRF 防护：禁止访问内网/本地地址 %s", host)
		}
		return nil
	}
	// 主机名（默认放行公开域名；生产加 DNS 解析 + 递归校验）
	return nil
}

// isBlockedIP 判断是否命中需拦截的地址段。
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || // 127.0.0.0/8, ::1
		ip.IsPrivate() || // RFC1918: 10/8, 172.16/12, 192.168/16
		ip.IsLinkLocalUnicast() || // 169.254/16（含云元数据 169.254.169.254）
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || // 0.0.0.0, ::
		ip.IsMulticast()
}
