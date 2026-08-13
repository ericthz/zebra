package safety

import "testing"

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

	// 公网域名/IP 应放行
	for _, good := range []string{
		"https://api.duckduckgo.com/",
		"https://wttr.in/Beijing",
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
