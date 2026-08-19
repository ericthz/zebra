package tool

import (
	"context"
	"strconv"
	"testing"
)

// TestRandomToolRange 验证：正常区间返回范围内整数。
func TestRandomToolRange(t *testing.T) {
	r := &RandomTool{}
	for i := 0; i < 50; i++ {
		out, err := r.Execute(context.Background(), map[string]interface{}{"min": 5.0, "max": 7.0})
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.Atoi(out)
		if n < 5 || n > 7 {
			t.Fatalf("随机数应在 [5,7] 内，实际 %d", n)
		}
	}
}

// TestRandomToolEdge 验证：min==max、min>max、极端边界不 panic（五6 模零修复）。
func TestRandomToolEdge(t *testing.T) {
	r := &RandomTool{}
	// min == max → 返回该值
	out, err := r.Execute(context.Background(), map[string]interface{}{"min": 42.0, "max": 42.0})
	if err != nil {
		t.Fatal(err)
	}
	if out != "42" {
		t.Fatalf("min==max 应返回该值，实际 %q", out)
	}
	// min > max → 报错
	if _, err := r.Execute(context.Background(), map[string]interface{}{"min": 9.0, "max": 3.0}); err == nil {
		t.Fatal("min>max 应报错")
	}
	// 极端边界：max-min+1 溢出为 0/负数，不得 panic（须返回结果）
	for _, args := range []map[string]interface{}{
		{"min": float64(-1 << 62), "max": float64(1<<62 - 1)},
		{"min": float64(-1 << 30), "max": float64(1 << 30)},
	} {
		if _, err := r.Execute(context.Background(), args); err != nil {
			t.Fatalf("极端边界应可执行（不 panic）: %v", err)
		}
	}
}
