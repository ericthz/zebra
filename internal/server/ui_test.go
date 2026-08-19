package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUIHasNoDefaultKey 验证：Web UI 页面不再内置默认密钥（此前硬编码 'admin-key'，
// 会让公开页面的访问者静默获得管理员凭据，等于公开鉴权形同虚设）。
func TestUIHasNoDefaultKey(t *testing.T) {
	h := uiHandler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 {
		t.Fatalf("UI 应返回 200，实际 %d", rr.Code)
	}
	html := rr.Body.String()
	if strings.Contains(html, "admin-key") {
		t.Fatal("UI 不应内置默认密钥 admin-key")
	}
	if strings.Contains(html, "localStorage.getItem('zebra_key') || 'admin-key'") {
		t.Fatal("密钥回退逻辑仍硬编码 admin-key")
	}
}
