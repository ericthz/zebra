package redis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReadLineEdgeCases 验证：畸形行（缺 \r）不越界 panic。
func TestReadLineEdgeCases(t *testing.T) {
	for _, in := range []string{"\n", "abc\n", "a\r\n", "\r\n"} {
		s, err := readLine(bufio.NewReader(strings.NewReader(in)))
		if err != nil {
			t.Fatalf("输入 %q 不应报错: %v", in, err)
		}
		if strings.Contains(s, "\n") || strings.Contains(s, "\r") {
			t.Fatalf("输入 %q 应去除行尾分隔符，实际 %q", in, s)
		}
	}
}

// TestReadLineBounded 六11：超长行必须报错，不能无限缓冲占满内存。
func TestReadLineBounded(t *testing.T) {
	// maxLineBytes+1 个字符且无换行 → 应报"行超过上限"
	in := strings.Repeat("x", maxLineBytes+1)
	if _, err := readLine(bufio.NewReader(strings.NewReader(in))); err == nil {
		t.Fatal("超长行应报错（六11）")
	}
	// 恰好低于上限且有换行 → 正常返回
	ok := strings.Repeat("y", maxLineBytes-1) + "\r\n"
	s, err := readLine(bufio.NewReader(strings.NewReader(ok)))
	if err != nil || len(s) != maxLineBytes-1 {
		t.Fatalf("正常长度行应成功，实际 len=%d err=%v", len(s), err)
	}
}

// TestReadReplyMalformed 验证：非法 RESP 长度/前缀返回错误而非 panic/越界。
func TestReadReplyMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"负 bulk 长度", "$-3\r\n"},
		{"负数组长度", "*-2\r\n"},
		{"数组元素不完整", "*2\r\n$3\r\nfoo\r\n"},
		{"空响应行", "\r\n"},
		{"未知前缀", "%3\r\n"},
		{"非法数字", "$abc\r\n"},
		{"超大 bulk 长度", "$99999999999\r\n"},
		{"超大数组长度", "*99999999999\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readReply(bufio.NewReader(strings.NewReader(tc.in))); err == nil {
				t.Fatalf("畸形输入 %q 应报错", tc.in)
			}
		})
	}
}

// TestDelExpireTypeGuard 验证：返回类型异常时返回错误而非 panic。
func TestDelExpireTypeGuard(t *testing.T) {
	// 构造服务器：对 DEL/EXPIRE 返回字符串（异常协议应答）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				io.ReadFull(c, buf[:3]) // 吃 *N
				c.Write([]byte("+not-an-int\r\n"))
			}(conn)
		}
	}()

	c := &Client{Addr: ln.Addr().String()}
	if _, err := c.Del(context.Background(), "k"); err == nil {
		t.Fatal("DEL 返回非整数应报错")
	}
	if _, err := c.Expire(context.Background(), "k", time.Minute); err == nil {
		t.Fatal("EXPIRE 返回非整数应报错")
	}
}

// fakeRedisServer 内存版假 Redis：解析 RESP 命令并按语义应答。
// 它同时是客户端协议正确性的"反向验证"（两边都严格按 RESP 编解码）。
type fakeRedisServer struct {
	ln      net.Listener
	mu      sync.Mutex
	data    map[string]string
	expires map[string]time.Time
}

func startFakeRedis(t *testing.T) *fakeRedisServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeRedisServer{ln: ln, data: map[string]string{}, expires: map[string]time.Time{}}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeRedisServer) addr() string { return s.ln.Addr().String() }

func (s *fakeRedisServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeRedisServer) handle(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	for {
		cmd, err := parseCommand(rd)
		if err != nil {
			return
		}
		conn.Write([]byte(s.exec(cmd)))
	}
}

// parseCommand 解析 RESP 数组命令（*N + N 个 $len...）。
func parseCommand(rd *bufio.Reader) ([]string, error) {
	line, err := readLine(rd)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("非法命令: %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		l, err := readLine(rd)
		if err != nil {
			return nil, err
		}
		ln, err := strconv.Atoi(l[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, ln+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:ln]))
	}
	return args, nil
}

func (s *fakeRedisServer) exec(cmd []string) string {
	if len(cmd) == 0 {
		return "-ERR empty command\r\n"
	}
	switch strings.ToUpper(cmd[0]) {
	case "AUTH", "SELECT":
		return "+OK\r\n"
	case "SET":
		s.mu.Lock()
		s.data[cmd[1]] = cmd[2]
		if len(cmd) >= 5 && (cmd[3] == "EX" || cmd[3] == "PX") {
			if n, err := strconv.Atoi(cmd[4]); err == nil {
				if cmd[3] == "EX" {
					s.expires[cmd[1]] = time.Now().Add(time.Duration(n) * time.Second)
				} else {
					s.expires[cmd[1]] = time.Now().Add(time.Duration(n) * time.Millisecond)
				}
			}
		}
		s.mu.Unlock()
		return "+OK\r\n"
	case "GET":
		s.mu.Lock()
		s.purgeLocked()
		v, ok := s.data[cmd[1]]
		s.mu.Unlock()
		if !ok {
			return "$-1\r\n"
		}
		return "$" + strconv.Itoa(len(v)) + "\r\n" + v + "\r\n"
	case "DEL":
		s.mu.Lock()
		n := 0
		for _, k := range cmd[1:] {
			if _, ok := s.data[k]; ok {
				delete(s.data, k)
				delete(s.expires, k)
				n++
			}
		}
		s.mu.Unlock()
		return ":" + strconv.Itoa(n) + "\r\n"
	case "EXPIRE":
		sec, _ := strconv.Atoi(cmd[2])
		s.mu.Lock()
		if _, ok := s.data[cmd[1]]; ok {
			s.expires[cmd[1]] = time.Now().Add(time.Duration(sec) * time.Second)
		}
		exists := false
		if _, ok := s.data[cmd[1]]; ok {
			exists = true
		}
		s.mu.Unlock()
		if exists {
			return ":1\r\n"
		}
		return ":0\r\n"
	case "KEYS":
		s.mu.Lock()
		s.purgeLocked()
		var keys []string
		pattern := cmd[1]
		for k := range s.data {
			if matchPattern(k, pattern) {
				keys = append(keys, k)
			}
		}
		s.mu.Unlock()
		var b strings.Builder
		b.WriteString("*" + strconv.Itoa(len(keys)) + "\r\n")
		for _, k := range keys {
			b.WriteString("$" + strconv.Itoa(len(k)) + "\r\n" + k + "\r\n")
		}
		return b.String()
	default:
		return "-ERR unknown command '" + cmd[0] + "'\r\n"
	}
}

func (s *fakeRedisServer) purgeLocked() {
	for k, exp := range s.expires {
		if time.Now().After(exp) {
			delete(s.data, k)
			delete(s.expires, k)
		}
	}
}

func matchPattern(key, pattern string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(key, strings.TrimSuffix(pattern, "*"))
	}
	return key == pattern
}

func TestClientSetGetDel(t *testing.T) {
	s := startFakeRedis(t)
	c := &Client{Addr: s.addr()}
	ctx := context.Background()

	if err := c.Set(ctx, "k1", "v1", 0); err != nil {
		t.Fatal(err)
	}
	v, ok, err := c.Get(ctx, "k1")
	if err != nil || !ok || v != "v1" {
		t.Fatalf("Get 异常: %q %v %v", v, ok, err)
	}
	// 不存在的 key → ok=false
	if _, ok, _ := c.Get(ctx, "ghost"); ok {
		t.Fatal("不存在的 key 应返回 ok=false")
	}
	// 删除
	if n, err := c.Del(ctx, "k1"); err != nil || n != 1 {
		t.Fatalf("Del 异常: %d %v", n, err)
	}
	if _, ok, _ := c.Get(ctx, "k1"); ok {
		t.Fatal("删除后不应再可取")
	}
}

func TestClientExpireAndKeys(t *testing.T) {
	s := startFakeRedis(t)
	c := &Client{Addr: s.addr(), Timeout: 2 * time.Second}
	ctx := context.Background()

	if err := c.Set(ctx, "zebra:sess:a:1", "x", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok, _ := c.Get(ctx, "zebra:sess:a:1"); ok {
		t.Fatal("TTL 过期后 key 应消失")
	}

	c.Set(ctx, "zebra:sess:a:2", "y", 0)
	c.Set(ctx, "zebra:other:3", "z", 0)
	keys, err := c.Keys(ctx, "zebra:sess:*")
	if err != nil || len(keys) != 1 || keys[0] != "zebra:sess:a:2" {
		t.Fatalf("KEYS 异常: %v %v", keys, err)
	}
}

func TestClientAuthSelectAndError(t *testing.T) {
	s := startFakeRedis(t)
	c := &Client{Addr: s.addr(), Password: "pw", DB: 2}
	ctx := context.Background()
	if err := c.Set(ctx, "k", "v", 0); err != nil {
		t.Fatalf("带 AUTH/SELECT 的命令失败: %v", err)
	}

	// 未知命令 → 错误返回
	bad := &Client{Addr: s.addr()}
	if _, err := bad.Do(ctx, "NOSUCHCMD"); err == nil {
		t.Fatal("未知命令应返回错误")
	}
}
