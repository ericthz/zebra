package safety

import (
	"context"
	"net"
	"testing"
)

// fakeResolver 可配置的解析桩（模拟 DNS 重绑定：域名解析到内网 IP）。
type fakeResolver struct{ ips map[string][]net.IP }

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]net.IP, error) {
	return f.ips[host], nil
}

func TestCheckSSRF(t *testing.T) {
	// 内网/环回 IP 必须拒绝
	for _, bad := range []string{
		"http://127.0.0.1:8080/",        // 环回
		"http://10.0.0.1/x",             // RFC1918
		"http://192.168.1.1/x",          // RFC1918
		"http://169.254.169.254/latest", // 云元数据
		"http://[::1]/",                 // IPv6 环回
		"ftp://example.com/x",           // 非 http/https
	} {
		if err := CheckSSRF(bad, nil); err == nil {
			t.Errorf("应拒绝 %q", bad)
		}
	}

	// 公网 IP 应放行
	for _, good := range []string{
		"http://8.8.8.8/",
	} {
		if err := CheckSSRF(good, nil); err != nil {
			t.Errorf("应放行 %q: %v", good, err)
		}
	}

	// 白名单模式：非白名单域名拒绝，白名单放行
	if err := CheckSSRF("https://evil.com/", []string{"api.duckduckgo.com"}); err == nil {
		t.Fatal("白名单外域名应拒绝")
	}
	if err := CheckSSRF("https://api.duckduckgo.com/", []string{"api.duckduckgo.com"}); err != nil {
		t.Fatalf("白名单内域名应放行: %v", err)
	}
}

// TestCheckSSRFHostname 验证：主机名经 DNS 解析后逐 IP 校验（防重绑定）。
func TestCheckSSRFHostname(t *testing.T) {
	old := resolver
	defer func() { resolver = old }()

	// 域名解析到公网 IP → 放行
	resolver = fakeResolver{ips: map[string][]net.IP{
		"public.example.com": {net.ParseIP("93.184.216.34")},
		"rebind.example.com": {net.ParseIP("127.0.0.1")},       // 重绑定 → 内网
		"evil.example.com":   {net.ParseIP("169.254.169.254")}, // 云元数据
	}}
	for _, good := range []string{"http://public.example.com/"} {
		if err := CheckSSRF(good, nil); err != nil {
			t.Errorf("公网域名应放行 %q: %v", good, err)
		}
	}
	for _, bad := range []string{
		"http://rebind.example.com/",
		"http://evil.example.com/",
	} {
		if err := CheckSSRF(bad, nil); err == nil {
			t.Errorf("解析到内网的域名应拒绝 %q", bad)
		}
	}
	// 解析失败 → 拒绝（不可信）
	resolver = fakeResolver{ips: nil}
	if err := CheckSSRF("http://nonexistent.invalid/", nil); err == nil {
		t.Fatal("解析失败的域名应拒绝")
	}
}

// TestResolveSSRF 验证：返回可绑定的安全 IP（防 DNS 重绑定 TOCTOU 的契约）。
func TestResolveSSRF(t *testing.T) {
	// IP 字面量：返回该 IP
	ips, err := ResolveSSRF("http://8.8.8.8/", nil)
	if err != nil || len(ips) != 1 || ips[0].String() != "8.8.8.8" {
		t.Fatalf("IP 字面量应返回自身供绑定: %v %v", ips, err)
	}
	// 内网 IP：拒绝（无返回）
	if _, err := ResolveSSRF("http://127.0.0.1/", nil); err == nil {
		t.Fatal("内网 IP 应拒绝")
	}

	// 白名单命中：返回 nil（放行交给标准解析）
	ips, err = ResolveSSRF("https://api.duckduckgo.com/", []string{"api.duckduckgo.com"})
	if err != nil || ips != nil {
		t.Fatalf("白名单命中应返回 nil: %v %v", ips, err)
	}

	// 主机名：返回解析出的公网 IP 供绑定
	old := resolver
	defer func() { resolver = old }()
	resolver = fakeResolver{ips: map[string][]net.IP{
		"public.example.com": {net.ParseIP("93.184.216.34")},
	}}
	ips, err = ResolveSSRF("http://public.example.com/", nil)
	if err != nil || len(ips) != 1 || ips[0].String() != "93.184.216.34" {
		t.Fatalf("应返回解析出的公网 IP: %v %v", ips, err)
	}
}
