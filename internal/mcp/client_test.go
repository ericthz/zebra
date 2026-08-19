package mcp

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"
)

// newSilentStdio 构造一个"服务端只收不回"的 stdio 传输（模拟通知/无响应场景）。
// 请求管道：服务端持续消费防写阻塞；响应管道：无人写入 → 客户端读永远阻塞。
func newSilentStdio() *stdioTransport {
	reqPr, reqPw := io.Pipe()
	go func() {
		rd := bufio.NewReader(reqPr)
		for {
			if _, err := rd.ReadBytes('\n'); err != nil {
				return
			}
		}
	}()
	respPr, _ := io.Pipe()
	tr := &stdioTransport{stdin: reqPw, lines: make(chan []byte, 64), readErr: make(chan error, 1), nextID: 1}
	go tr.readLoop(bufio.NewReader(respPr))
	return tr
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

// TestStdioNoGoroutineLeakOnCancel 验证：ctx 取消后读 goroutine 不泄漏。
// 旧实现每次 Send 起一个阻塞读的 goroutine，取消时泄漏并可能吞掉下一轮响应；
// 新实现是"一传输一个常驻读 goroutine"，取消只退出 select。
func TestStdioNoGoroutineLeakOnCancel(t *testing.T) {
	tr := newSilentStdio()
	// 第一次：无响应 → 超时取消，send 返回错误
	ctx1, cancel1 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	start := time.Now()
	if _, err := tr.Send(ctx1, &Request{Method: "tools/list"}); err == nil {
		t.Fatal("无响应请求应报错")
	}
	cancel1()
	// 第二次：重新发起，验证传输仍可用（未因泄漏的 goroutine 抢读而错乱）
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if _, err := tr.Send(ctx2, &Request{Method: "tools/list"}); err == nil {
		t.Fatal("第二次无响应请求也应超时报错")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("取消不应拖慢后续请求，耗时 %v", elapsed)
	}
}

// TestStdioReadsResponse 验证：正常响应行能被读取并解出。
func TestStdioReadsResponse(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"ok\"}\n"))
		pw.Close()
	}()
	tr := &stdioTransport{stdin: &nopCloser{io.Discard}, lines: make(chan []byte, 64), readErr: make(chan error, 1), nextID: 1}
	go tr.readLoop(bufio.NewReader(pr))

	resp, err := tr.Send(context.Background(), &Request{Method: "tools/list"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result == nil || string(resp.Result) != `"ok"` {
		t.Fatalf("响应解析异常: %+v", resp)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
