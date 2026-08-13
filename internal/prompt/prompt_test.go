package prompt

import "testing"

func TestRegistryVersioning(t *testing.T) {
	r := NewRegistry("z")
	r.Register(&Template{Name: "sys", Version: "v1", Text: "v1 提示 {role}"})
	r.Register(&Template{Name: "sys", Version: "v2", Text: "v2 提示 {role}"})

	out, err := r.Render("sys", map[string]string{"role": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "v1 提示 admin" {
		t.Fatalf("首个版本应默认生效，got %q", out)
	}

	if err := r.Activate("sys", "v2"); err != nil {
		t.Fatal(err)
	}
	out, _ = r.Render("sys", map[string]string{"role": "user"})
	if out != "v2 提示 user" {
		t.Fatalf("切换版本后应生效，got %q", out)
	}

	if err := r.Activate("sys", "v9"); err == nil {
		t.Fatal("不存在的版本应报错")
	}
}
