package cost

import "testing"

func TestRecordAndSnapshot(t *testing.T) {
	tr := NewTracker()
	tr.Record("alice", "s1", "gpt-4o-mini", 1000, 2000)
	tr.Record("alice", "s2", "gpt-4o-mini", 500, 500)
	tr.Record("bob", "s3", "qwen3.5:0.8b", 100, 100)

	snap := tr.Snapshot()
	for _, want := range []string{
		`zebra_cost_user_tokens_in{user="alice"} 1500`,
		`zebra_cost_user_tokens_in{user="bob"} 100`,
		`zebra_cost_session_tokens_in{session="s1"`,
		`zebra_cost_session_tokens_out{session="s1"`,
	} {
		if !contains(snap, want) {
			t.Fatalf("快照缺 %q\n%s", want, snap)
		}
	}
}

func TestEstimate(t *testing.T) {
	// gpt-4o-mini: in=1M*0.15 + out=1M*0.60 = 0.75 美元
	got := Estimate("gpt-4o-mini", 1_000_000, 1_000_000)
	if got < 0.74 || got > 0.76 {
		t.Fatalf("估算错误: %v", got)
	}
	// 未知模型应有默认价（>0）
	if Estimate("unknown-model", 1000, 1000) <= 0 {
		t.Fatal("未知模型应有默认价")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
