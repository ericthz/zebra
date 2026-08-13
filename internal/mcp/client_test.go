package mcp

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"
)

// newSilentStdio 构造一个"服务端只收不回"的 stdio 传输（模拟通知/无响应场景）。
func newSilentStdio() *stdioTransport {
	pr, pw := io.Pipe()
	// 服务端侧：持续消费请求行（否则管道写会阻塞），但从不响应
	go func() {
		rd := bufio.NewReader(pr)
		for {
			if _, err := rd.ReadBytes('\n'); err != nil {
				return
			}
		}
	}()
	return &stdioTransport{stdin: pw, reader: bufio.NewReader(pr), nextID: 1}
}

// TestStdioNotificationDoesNotBlock 通知类请求写后即返回（P34 stdio 竞态修复）。
func TestStdioNotificationDoesNotBlock(t *testing.T) {
	tr := newSilentStdio()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := tr.Send(ctx, &Request{Method: "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("通知不应阻塞等待响应，耗时 %v", elapsed)
	}
}

// TestStdioRequestWaitsForResponse 普通请求无响应时应等到超时（防误伤回归）。
func TestStdioRequestWaitsForResponse(t *testing.T) {
	tr := newSilentStdio()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := tr.Send(ctx, &Request{Method: "tools/list"}); err == nil {
		t.Fatal("无响应时普通请求应超时报错")
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("普通请求应等待响应，过早返回: %v", elapsed)
	}
}
