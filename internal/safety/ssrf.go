// SSRF 防护（P6 安全加固）。
//
// 背景：Agent 会主动拉取 URL（搜索、天气、抓取网页）。若 URL 由用户/外部
// 内容控制，恶意输入可能让 Agent 访问内网（如 http://169.254.169.254 云元数据、
// http://127.0.0.1:6379 内网 Redis），这就是 SSRF（服务端请求伪造）。
//
// 防护策略（层层设防）：
//  1. 协议白名单：仅 http/https
//  2. 可选域名白名单：allowHosts 非空时，只放行白名单域名
//  3. IP 黑名单：IP 字面量命中内网/环回/链路本地/未指定 → 拒绝
//  4. DNS 解析后校验：主机名先解析再逐 IP 检查（防 DNS 重绑定）——
//     恶意域名可先解析到公网 IP 骗过检查、随后的请求再绑定到内网 IP；
//     主动解析一次并在连接前校验能挡住最常见的重绑定手法。
//
// 生产演化方向：解析结果与连接目标绑定（连接用解析出的 IP，防 TOCTOU）、
// 内网网段 CIDR 可配置、出站代理统一管控。
package safety

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// CheckSSRF 校验目标 URL 是否可安全访问；不安全返回错误。
// allowHosts 为空表示不限制域名（但仍拦截内网 IP）。
func CheckSSRF(rawURL string, allowHosts []string) error {
	_, err := ResolveSSRF(rawURL, allowHosts)
	return err
}

// ResolveSSRF 校验目标 URL 并返回应连接的安全 IP（防 DNS 重绑定 TOCTOU）。
//
// 返回语义：
//   - URL 不安全 → 返回错误
//   - IP 字面量 → 返回该 IP（校验通过后）
//   - 主机名 → 解析后逐 IP 校验，全部通过则返回全部解析 IP
//   - 命中 allowHosts 白名单（直接放行）→ 返回 nil（由标准网络栈自行解析，
//     白名单域名视为可信，不参与 IP 校验）
//
// 调用方应把返回的 IP 与连接绑定（DialContext 用该 IP + 保留 Host/SNI），
// 消除"校验时解析公网 IP、连接时重绑内网 IP"的窗口。
func ResolveSSRF(rawURL string, allowHosts []string) ([]net.IP, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL 解析失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("SSRF 防护：仅允许 http/https，收到 %q", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("SSRF 防护：URL 缺少主机名")
	}

	// 域名白名单：非空则只放行白名单内域名（放行后不绑定 IP，交给标准栈）
	if len(allowHosts) > 0 {
		for _, h := range allowHosts {
			if strings.EqualFold(host, h) {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("SSRF 防护：域名 %q 不在白名单内", host)
	}

	// IP 字面量：命中内网/环回等直接拒绝；通过则返回该 IP 供连接绑定
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("SSRF 防护：禁止访问内网/本地地址 %s", host)
		}
		return []net.IP{ip}, nil
	}

	// 主机名：DNS 解析后逐 IP 校验（防 DNS 重绑定），返回解析出的安全 IP。
	// 解析失败视为不可信 → 拒绝（宁可误杀不可放行内网）。
	return checkHostname(host)
}

// ssrfResolver 解析接口（测试可替换，避免依赖真实 DNS）。
type ssrfResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// netResolver 适配 *net.Resolver → ssrfResolver（LookupNetIP 返回 netip.Addr）。
type netResolver struct{ r *net.Resolver }

func (n netResolver) LookupNetIP(ctx context.Context, network, host string) ([]net.IP, error) {
	addrs, err := n.r.LookupNetIP(ctx, network, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, net.IP(a.AsSlice()))
	}
	return out, nil
}

// resolver 可注入的解析器；nil 时用系统默认解析器。
var resolver ssrfResolver = netResolver{net.DefaultResolver}

// checkHostname 解析主机名并对每个解析结果做 IP 校验，返回全部安全 IP。
// 任意一个解析 IP 命中内网/本地段都拒绝；解析超时/失败也拒绝。
func checkHostname(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := resolver
	if r == nil {
		r = netResolver{net.DefaultResolver}
	}
	ips, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("SSRF 防护：域名 %q 解析失败（视为不可信，拒绝访问）: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("SSRF 防护：域名 %q 无解析结果，拒绝访问", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("SSRF 防护：域名 %q 解析到内网/本地地址 %s（疑似 DNS 重绑定），拒绝访问", host, ip)
		}
	}
	return ips, nil
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
