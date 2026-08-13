package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// fakeJudgeProvider 返回一段固定 JSON 打分（模拟评审模型）。
type fakeJudgeProvider struct{ reply string }

func (f *fakeJudgeProvider) Name() string { return "fake-judge" }
func (f *fakeJudgeProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: f.reply}, nil
}
func (f *fakeJudgeProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestParseScores(t *testing.T) {
	// 纯 JSON
	s, err := parseScores(`{"faithfulness":0.9,"relevance":0.8,"safety":1.0,"comment":"不错"}`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Faithfulness != 0.9 || s.Relevance != 0.8 || s.Safety != 1.0 {
		t.Fatalf("解析错误: %+v", s)
	}
	if !s.Pass(0.7) {
		t.Fatal("应通过 0.7 阈值")
	}

	// Markdown 代码块包裹
	s2, err := parseScores("```json\n{\"faithfulness\":0.5,\"relevance\":0.6,\"safety\":0.9}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Pass(0.7) {
		t.Fatal("低分不应通过")
	}

	// 越界钳制
	s3, _ := parseScores(`{"faithfulness":1.5,"relevance":-1,"safety":0.5}`)
	if s3.Faithfulness != 1 || s3.Relevance != 0 {
		t.Fatalf("应钳制到 [0,1]: %+v", s3)
	}

	// 非法输入
	if _, err := parseScores("完全不是 JSON"); err == nil {
		t.Fatal("非法输入应报错")
	}
}

func TestJudgeScore(t *testing.T) {
	j := NewJudge(provider.NewRouter(&fakeJudgeProvider{
		reply: `{"faithfulness":0.85,"relevance":0.75,"safety":0.95,"comment":"基本忠实"}`,
	}))
	s, err := j.Score(context.Background(), "北京天气怎么样？", "北京今天 25 度，晴朗。")
	if err != nil {
		t.Fatal(err)
	}
	if s.Faithfulness != 0.85 || s.Relevance != 0.75 {
		t.Fatalf("打分错误: %+v", s)
	}
	if !strings.Contains(s.Comment, "忠实") {
		t.Fatalf("评审意见缺失: %q", s.Comment)
	}
}
