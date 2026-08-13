package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/redis"
)

// testRedisServer 精简假 Redis（支持本测试用到的命令子集）。
type testRedisServer struct {
	ln      net.Listener
	mu      sync.Mutex
	data    map[string]string
	expires map[string]time.Time
}

func startTestRedis(t *testing.T) *testRedisServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testRedisServer{ln: ln, data: map[string]string{}, expires: map[string]time.Time{}}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *testRedisServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			rd := bufio.NewReader(c)
			for {
				cmd, err := s.readCommand(rd)
				if err != nil {
					return
				}
				c.Write([]byte(s.exec(cmd)))
			}
		}(conn)
	}
}

func (s *testRedisServer) readCommand(rd *bufio.Reader) ([]string, error) {
	line, err := readRESPLine(rd)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("非法命令 %q", line)
	}
	n, _ := strconv.Atoi(line[1:])
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		l, err := readRESPLine(rd)
		if err != nil {
			return nil, err
		}
		ln, _ := strconv.Atoi(l[1:])
		buf := make([]byte, ln+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:ln]))
	}
	return args, nil
}

func readRESPLine(rd *bufio.Reader) (string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	return line[:len(line)-2], nil
}

func (s *testRedisServer) exec(cmd []string) string {
	switch strings.ToUpper(cmd[0]) {
	case "AUTH", "SELECT":
		return "+OK\r\n"
	case "SET":
		s.mu.Lock()
		s.data[cmd[1]] = cmd[2]
		if len(cmd) >= 5 && (cmd[3] == "EX" || cmd[3] == "PX") {
			n, _ := strconv.Atoi(cmd[4])
			if cmd[3] == "EX" {
				s.expires[cmd[1]] = time.Now().Add(time.Duration(n) * time.Second)
			} else {
				s.expires[cmd[1]] = time.Now().Add(time.Duration(n) * time.Millisecond)
			}
		}
		s.mu.Unlock()
		return "+OK\r\n"
	case "GET":
		s.mu.Lock()
		s.purge()
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
		n, _ := strconv.Atoi(cmd[2])
		s.mu.Lock()
		_, ok := s.data[cmd[1]]
		if ok {
			s.expires[cmd[1]] = time.Now().Add(time.Duration(n) * time.Second)
		}
		s.mu.Unlock()
		if ok {
			return ":1\r\n"
		}
		return ":0\r\n"
	case "KEYS":
		s.mu.Lock()
		s.purge()
		var keys []string
		prefix := strings.TrimSuffix(cmd[1], "*")
		for k := range s.data {
			if strings.HasPrefix(k, prefix) {
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
		return "-ERR unknown\r\n"
	}
}

func (s *testRedisServer) purge() {
	for k, exp := range s.expires {
		if time.Now().After(exp) {
			delete(s.data, k)
			delete(s.expires, k)
		}
	}
}

func TestRedisSessionStore(t *testing.T) {
	fs := startTestRedis(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.ln.Addr().String(), Timeout: 2 * time.Second}, 30*time.Minute)

	// 创建 → 读取
	sess, err := store.Create("alice", "default", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.User != "alice" {
		t.Fatalf("会话元数据错误: %+v", sess)
	}
	got, ok := store.Get(sess.ID)
	if !ok || got.User != "alice" {
		t.Fatalf("应从 Redis 读到会话: %+v %v", got, ok)
	}

	// 历史写回（HistoryPersister）：修改后 Save → 再读仍保留（多轮不丢）
	*got.History() = append(*got.History(), provider.Message{Role: "user", Content: "第一轮"})
	if err := store.Save(got); err != nil {
		t.Fatal(err)
	}
	got2, ok := store.Get(sess.ID)
	if !ok || len(*got2.History()) != 1 || (*got2.History())[0].Content != "第一轮" {
		t.Fatalf("历史写回失败: %+v %v", got2, ok)
	}

	// Touch 续期：60ms 会过期，Touch 后延长到 30min
	short, _ := store.Create("bob", "default", "user", 60*time.Millisecond)
	if ok := store.Touch(short.ID); !ok {
		t.Fatal("Touch 应成功")
	}
	time.Sleep(120 * time.Millisecond)
	if _, ok := store.Get(short.ID); !ok {
		t.Fatal("Touch 续期后不应过期")
	}

	// 不存在的会话 Touch → false
	if store.Touch("ghost-session") {
		t.Fatal("不存在会话 Touch 应返回 false")
	}
}

func TestRedisSessionForgetUser(t *testing.T) {
	fs := startTestRedis(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.ln.Addr().String()}, time.Minute)

	a1, _ := store.Create("alice", "default", "user", time.Minute)
	a2, _ := store.Create("alice", "default", "user", time.Minute)
	b1, _ := store.Create("bob", "default", "user", time.Minute)

	deleted := store.ForgetUser("alice")
	if len(deleted) != 2 {
		t.Fatalf("应删除 alice 的 2 个会话，实际 %d: %v", len(deleted), deleted)
	}
	if _, ok := store.Get(a1.ID); ok {
		t.Fatal("alice 会话应被删除")
	}
	if _, ok := store.Get(a2.ID); ok {
		t.Fatal("alice 会话应被删除")
	}
	if _, ok := store.Get(b1.ID); !ok {
		t.Fatal("bob 会话不应被误删")
	}
}
