package schema

import "testing"

func TestValidateObject(t *testing.T) {
	sch := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"age":  map[string]interface{}{"type": "integer"},
		},
		"required": []interface{}{"name"},
	}

	// 合法
	if err := Validate([]byte(`{"name":"zebra","age":3}`), sch); err != nil {
		t.Fatalf("合法输入应通过: %v", err)
	}
	// 缺必填
	if err := Validate([]byte(`{"age":3}`), sch); err == nil {
		t.Fatal("缺必填字段应报错")
	}
	// 类型错
	if err := Validate([]byte(`{"name":"z","age":"old"}`), sch); err == nil {
		t.Fatal("类型错误应报错")
	}
	// 非 JSON
	if err := Validate([]byte(`not json`), sch); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestValidateEnumAndArray(t *testing.T) {
	sch := map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{
			"type": "string",
			"enum": []interface{}{"red", "green", "blue"},
		},
	}
	if err := Validate([]byte(`["red","blue"]`), sch); err != nil {
		t.Fatalf("合法数组应通过: %v", err)
	}
	if err := Validate([]byte(`["red","yellow"]`), sch); err == nil {
		t.Fatal("enum 外值应报错")
	}
	if err := Validate([]byte(`[1,2]`), sch); err == nil {
		t.Fatal("数组元素类型错误应报错")
	}
}

// TestValidateEnumStringSlice：enum 用 []string 声明时也必须生效
// （历史 bug：只断言 []interface{}，[]string 声明的 enum 被静默跳过）。
func TestValidateEnumStringSlice(t *testing.T) {
	sch := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"winner": map[string]interface{}{
				"type": "string",
				"enum": []string{"left", "right", "tie"},
			},
		},
	}
	// 合法
	if err := Validate([]byte(`{"winner":"right"}`), sch); err != nil {
		t.Fatalf("合法 enum 值应通过: %v", err)
	}
	// enum 外值必须报错（旧实现会静默通过）
	if err := Validate([]byte(`{"winner":"admin"}`), sch); err == nil {
		t.Fatal("[]string 声明的 enum 外值应报错")
	}
}
