package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/tool"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.json"), []byte(`{"plugins":[
		{"name":"echo","description":"echo service","url":"http://x/echo","parameters":{"type":"object"}}
	]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	defs, err := Load(dir)
	if err != nil || len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("Load 异常: %v %v", defs, err)
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("空目录应报错")
	}
}

func TestHTTPPluginTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Args map[string]interface{} `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Args["text"] != "hi" {
			http.Error(w, "bad args", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("echo:hi"))
	}))
	defer ts.Close()

	toolDef := Def{Name: "echo", Description: "echo", URL: ts.URL}
	pt := &HTTPPluginTool{def: toolDef, client: ts.Client()}
	out, err := pt.Execute(context.Background(), map[string]interface{}{"text": "hi"})
	if err != nil || out != "echo:hi" {
		t.Fatalf("插件执行异常: %q %v", out, err)
	}
	// 参数不匹配 → 服务端 400 → 报错
	if _, err := pt.Execute(context.Background(), map[string]interface{}{"text": "x"}); err == nil {
		t.Fatal("服务端 400 应报错")
	}
}

func TestRegister(t *testing.T) {
	reg := tool.NewRegistry()
	defs := []Def{
		{Name: "p1", URL: "http://x"},
		{Name: "", URL: "http://y"}, // 非法定义跳过
	}
	names := Register(reg, defs, nil)
	if len(names) != 1 || names[0] != "p1" {
		t.Fatalf("Register 异常: %v", names)
	}
	if reg.Names()[0] != "p1" {
		t.Fatalf("工具未注册: %v", reg.Names())
	}
	// Remove（热重载卸载）
	if !reg.Remove("p1") || len(reg.Names()) != 0 {
		t.Fatalf("Remove 异常: %v", reg.Names())
	}
	if strings.Contains(strings.Join(reg.Names(), ","), "p1") {
		t.Fatal("移除后不应存在")
	}
}
