package main

import "testing"

// TestBindLocalOnly 验证：默认密钥的安全门槛——只有绑定本机回环才放行。
func TestBindLocalOnly(t *testing.T) {
	// 绑定回环：允许使用默认密钥（bindLocalOnly == true）
	local := []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.1:0"}
	for _, a := range local {
		if !bindLocalOnly(a) {
			t.Fatalf("%q 应判定为仅本机回环", a)
		}
	}
	// 空 host（绑定全部接口）或公网/通配地址：对外暴露 → 拒绝默认密钥
	remote := []string{":8080", "0.0.0.0:8080", "10.0.0.5:8080", "[::]:8080", ""}
	for _, a := range remote {
		if bindLocalOnly(a) {
			t.Fatalf("%q 应判定为对外暴露", a)
		}
	}
}

// TestDefaultKeysRejectedOnExposedBind：非回环绑定时，配置的密钥等于
// 仓库自带默认值（admin-key/user-key）必须等同未配置、拒绝启动，否则
// 攻击者可用公开已知凭据接管服务。
func TestDefaultKeysRejectedOnExposedBind(t *testing.T) {
	if !isDefaultKey("admin-key") || !isDefaultKey("user-key") {
		t.Fatal("默认值应被识别")
	}
	if isDefaultKey("my-secret-key") || isDefaultKey("") {
		t.Fatal("非默认值不应被误判")
	}
}
