package agent

import (
	"context"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// TestUserMessagePlain 无图退化为纯文本消息（Content 非空、无 ContentParts）。
func TestUserMessagePlain(t *testing.T) {
	ag := newLoopAgent(&scriptedProvider{}, 3)
	// userMessage 不依赖 Bind；补一个空 Agent 直接测
	empty := &Agent{}
	m := empty.userMessage("你好", nil)
	if m.Role != "user" || m.Content != "你好" || len(m.ContentParts) != 0 {
		t.Fatalf("纯文本应走 Content: %+v", m)
	}
	_ = ag
}

// TestUserMessageImages：有图时构造 text+image_url 内容块，
// 且图片列表原样透传（URL/data URI 均由上层归一化）。
func TestUserMessageImages(t *testing.T) {
	empty := &Agent{}
	m := empty.userMessage("这张图是什么", []string{"https://x/a.png", "data:image/png;base64,AAAA"})
	if m.Role != "user" || m.Content != "" {
		t.Fatalf("有图时应走 ContentParts: %+v", m)
	}
	if len(m.ContentParts) != 3 {
		t.Fatalf("应有 text+2×image，实际 %d: %+v", len(m.ContentParts), m.ContentParts)
	}
	if m.ContentParts[0].Type != "text" || m.ContentParts[0].Text != "这张图是什么" {
		t.Fatalf("首块应为 text: %+v", m.ContentParts[0])
	}
	for i, p := range m.ContentParts[1:] {
		if p.Type != "image_url" || p.ImageURL == "" {
			t.Fatalf("第 %d 块应为 image_url: %+v", i+1, p)
		}
	}
}

// TestUserMessageImagesOnly 只有图没有文字时：不产生空 text 块。
func TestUserMessageImagesOnly(t *testing.T) {
	m := (&Agent{}).userMessage("", []string{"https://x/a.png"})
	if len(m.ContentParts) != 1 || m.ContentParts[0].Type != "image_url" {
		t.Fatalf("纯图应只有 image_url 块: %+v", m.ContentParts)
	}
}

// TestBuildMessagesWithImages：buildMessages 组装出的用户消息携带
// ContentParts（历史里仍只写文本——多模态图只进当前轮）。
func TestBuildMessagesWithImages(t *testing.T) {
	ag := newLoopAgent(&scriptedProvider{}, 3)
	msgs := ag.buildMessages(context.Background(), "看图", []string{"https://x/a.png"}, nil)
	last := msgs[len(msgs)-1]
	if len(last.ContentParts) != 2 {
		t.Fatalf("最后一条用户消息应含 text+image，实际 %+v", last)
	}
	if last.ContentParts[1].Type != "image_url" || last.ContentParts[1].ImageURL != "https://x/a.png" {
		t.Fatalf("image_url 块构造错误: %+v", last.ContentParts[1])
	}
}

// TestRunWithImages 端到端：opts.Images 到达 provider 消息（captureProvider 校验）。
func TestRunWithImages(t *testing.T) {
	cap := &captureProvider{}
	ag := newLoopAgent(cap, 3)
	if _, err := ag.Run(context.Background(), "看图", RunOptions{Images: []string{"https://x/a.png"}}); err != nil {
		t.Fatal(err)
	}
	for _, msgs := range cap.received {
		for _, m := range msgs {
			if m.Role == "user" && len(m.ContentParts) > 0 {
				return // 端到端命中 ContentParts
			}
		}
	}
	t.Fatalf("Run 收到的消息中无 ContentParts（图片未到达 provider）: %+v", cap.received)
}

// captureProvider 记录每次收到的消息（供断言 provider 实际收到图片）。
type captureProvider struct {
	received [][]provider.Message
}

func (p *captureProvider) Name() string { return "capture" }
func (p *captureProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	p.received = append(p.received, msgs)
	return provider.Message{Content: "看到了"}, nil
}
func (p *captureProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}
