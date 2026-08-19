// Package redistest 内存版假 Redis 服务器，供各包测试复用（RESP 协议子集）。
//
// 支持：AUTH / SELECT / GET / SET(EX/PX) / DEL / EXPIRE / KEYS。
// 用法：srv := redistest.New(t)；client := &redis.Client{Addr: srv.Addr()}。
package redistest

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
)

// Server 内存版假 Redis。
type Server struct {
	ln      net.Listener
	mu      sync.Mutex
	data    map[string]string
	expires map[string]time.Time
}

// New 启动假 Redis，测试结束自动关闭。
func New(t *testing.T) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ln: ln, data: map[string]string{}, expires: map[string]time.Time{}}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

// Addr 返回监听地址（host:port）。
func (s *Server) Addr() string { return s.ln.Addr().String() }

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			rd := bufio.NewReader(c)
			for {
				cmd, err := readCommand(rd)
				if err != nil {
					return
				}
				c.Write([]byte(s.exec(cmd)))
			}
		}(conn)
	}
}

func readCommand(rd *bufio.Reader) ([]string, error) {
	line, err := readLine(rd)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("非法命令 %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 || n > 1024 {
		return nil, fmt.Errorf("非法命令长度 %q", line)
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		l, err := readLine(rd)
		if err != nil {
			return nil, err
		}
		if len(l) == 0 || l[0] != '$' {
			return nil, fmt.Errorf("非法参数行 %q", l)
		}
		ln, err := strconv.Atoi(l[1:])
		if err != nil || ln < 0 || ln > 512*1024 {
			return nil, fmt.Errorf("非法参数长度 %q", l)
		}
		buf := make([]byte, ln+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:ln]))
	}
	return args, nil
}

func readLine(rd *bufio.Reader) (string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) >= 2 && line[len(line)-2] == '\r' {
		return line[:len(line)-2], nil
	}
	if len(line) >= 1 && line[len(line)-1] == '\n' {
		return line[:len(line)-1], nil
	}
	return line, nil
}

func (s *Server) exec(cmd []string) string {
	if len(cmd) == 0 {
		return "-ERR empty\r\n"
	}
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

func (s *Server) purge() {
	for k, exp := range s.expires {
		if time.Now().After(exp) {
			delete(s.data, k)
			delete(s.expires, k)
		}
	}
}
