package kg

import "testing"

func TestExtractTriples(t *testing.T) {
	triples := ExtractTriples("zebra 支持 工具调用。北京位于中国。猫是动物。")
	got := map[string]bool{}
	for _, tr := range triples {
		got[tr.Subject+"|"+tr.Predicate+"|"+tr.Object] = true
	}
	for _, want := range []string{
		"zebra|支持|工具调用",
		"北京|位于|中国",
		"猫|是|动物",
	} {
		if !got[want] {
			t.Fatalf("缺少三元组 %s，实际 %v", want, triples)
		}
	}
}

func TestGraphQuery(t *testing.T) {
	g := NewGraph()
	g.Add(Triple{Subject: "zebra", Predicate: "支持", Object: "工具调用"})
	g.Add(Triple{Subject: "工具调用", Predicate: "属于", Object: "Agent能力"})
	if g.Size() != 2 {
		t.Fatalf("Size 应为 2，实际 %d", g.Size())
	}
	// "工具调用" 既作为客体（zebra 支持）又作为主体（属于 Agent能力）
	q := g.Query("工具调用")
	if len(q) != 2 {
		t.Fatalf("Query(工具调用) 应为 2 条，实际 %v", q)
	}
	// 无关实体 → 空
	if len(g.Query("不存在")) != 0 {
		t.Fatal("无关实体应返回空")
	}
}

func TestGraphSearch(t *testing.T) {
	g := NewGraph()
	g.Add(Triple{Subject: "zebra", Predicate: "支持", Object: "工具调用"})
	g.Add(Triple{Subject: "工具调用", Predicate: "属于", Object: "Agent能力"})

	// 查询文本含实体"zebra"，应命中其出边
	out := g.Search("zebra 有哪些能力？")
	if len(out) != 1 || out[0].Object != "工具调用" {
		t.Fatalf("Search(zebra) 应命中 1 条关系，实际 %v", out)
	}

	// 查询含两个实体（zebra 与 工具调用），应去重返回两条
	out = g.Search("zebra 与 工具调用 什么关系？")
	if len(out) != 2 {
		t.Fatalf("Search 应返回 2 条去重关系，实际 %v", out)
	}

	// 无命中 → 空
	if len(g.Search("你好")) != 0 {
		t.Fatal("无关查询应返回空")
	}
}
