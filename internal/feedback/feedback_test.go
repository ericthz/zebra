package feedback

import (
	"testing"
	"time"
)

func TestAddAndCounts(t *testing.T) {
	s := NewInMemoryStore()
	if err := s.Add(&Feedback{User: "u1", Session: "s1", Rating: RatingUp}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(&Feedback{User: "u1", Session: "s1", Rating: RatingDown, Comment: "答错了"}); err != nil {
		t.Fatal(err)
	}
	// 无效评分
	if err := s.Add(&Feedback{User: "u1", Session: "s1", Rating: 99}); err == nil {
		t.Fatal("无效评分应报错")
	}

	pos, neg := s.Counts()
	if pos != 1 || neg != 1 {
		t.Fatalf("计数错误: pos=%d neg=%d", pos, neg)
	}

	list := s.List("u1")
	if len(list) != 2 {
		t.Fatalf("应列出 2 条反馈，实际 %d", len(list))
	}
	if list[0].ID == "" || list[0].Created.IsZero() || list[0].Created.After(time.Now()) {
		t.Fatalf("反馈元数据错误: %+v", list[0])
	}
}
