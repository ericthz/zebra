// 最小 Redis 客户端（水平扩展骨架）：纯标准库实现 RESP 协议。
//
// 背景：水平扩展的第一件事是"会话/状态不绑单机内存"。zebra 的会话存储
// 是 SessionStore 接口（可插拔），缺一个 Redis 实现。本项目坚持零第三方
// 依赖，所以按 RESP 协议手写最小客户端——这也是一堂很好的协议课：
//
// RESP 请求：*N\r\n  +  N 个 $len\r\nvalue\r\n（bulk string 数组）
// RESP 响应：+OK（简单串）/ -ERR（错误）/ :N（整数）/ $len\r\n...（bulk）/
//
//	*N（数组）/ $-1（空）
//
// 生产演化方向：连接池/复用、pipeline、pub/sub、哨兵/集群；本实现
// 每次命令短连接（演示够用、逻辑最简）。
package redis

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Client 最小 Redis 客户端（每次命令独立短连接）。
type Client struct {
	Addr     string        // host:port
	Password string        // AUTH 密码（可选）
	DB       int           // SELECT 库号（可选）
	Timeout  time.Duration // 读写超时（默认 5s）
}

// RESP 解码防御上限（五5）：防止恶意/损坏响应声明超大长度引发 make 溢出或 OOM。
const (
	maxBulkBytes = 8 << 20 // 单条 bulk string 上限 8MB（会话/记忆均远小于此）
	maxArrayLen  = 1 << 20 // 数组元素个数上限 1M（防 LRANGE 0 -1 等超量预分配）
)

// Do 发送一条命令并解析响应。
// 返回值为 RESP 解码结果：string / int64 / []interface{} / nil（$-1）。
func (c *Client) Do(ctx context.Context, args ...string) (interface{}, error) {
	if len(args) == 0 {
		return nil, errors.New("空命令")
	}
	var buf bytes.Buffer
	buf.WriteString("*" + itoa(len(args)) + "\r\n")
	for _, a := range args {
		buf.WriteString("$" + itoa(len(a)) + "\r\n" + a + "\r\n")
	}

	conn, rd, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(c.timeout()))
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	return readReply(rd)
}

// dial 建立连接并完成 AUTH/SELECT 握手。
func (c *Client) dial(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	conn, err := net.DialTimeout("tcp", c.Addr, c.timeout())
	if err != nil {
		return nil, nil, err
	}
	rd := bufio.NewReader(conn)
	conn.SetDeadline(time.Now().Add(c.timeout()))
	if c.Password != "" {
		if err := c.handshake(conn, rd, "AUTH", c.Password); err != nil {
			conn.Close()
			return nil, nil, fmt.Errorf("AUTH 失败: %w", err)
		}
	}
	if c.DB > 0 {
		if err := c.handshake(conn, rd, "SELECT", itoa(c.DB)); err != nil {
			conn.Close()
			return nil, nil, fmt.Errorf("SELECT 失败: %w", err)
		}
	}
	return conn, rd, nil
}

func (c *Client) handshake(conn net.Conn, rd *bufio.Reader, args ...string) error {
	var buf bytes.Buffer
	buf.WriteString("*" + itoa(len(args)) + "\r\n")
	for _, a := range args {
		buf.WriteString("$" + itoa(len(a)) + "\r\n" + a + "\r\n")
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return err
	}
	_, err := readReply(rd)
	return err
}

// ---- 常用命令便捷方法 ----

// Set 写值；ttl>0 时带 EX 秒数。
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	args := []string{"SET", key, value}
	if ttl > 0 {
		if ms := ttl.Milliseconds(); ms < 1000 {
			args = append(args, "PX", itoa(int(ms))) // 亚秒 TTL 用毫秒精度
		} else {
			args = append(args, "EX", itoa(int(ttl.Seconds())))
		}
	}
	_, err := c.Do(ctx, args...)
	return err
}

// Get 读值；ok=false 表示 key 不存在。
func (c *Client) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := c.Do(ctx, "GET", key)
	if err != nil {
		return "", false, err
	}
	if v == nil {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("GET %s 返回类型异常: %T", key, v)
	}
	return s, true, nil
}

// Del 删除 key，返回删除条数。
func (c *Client) Del(ctx context.Context, keys ...string) (int64, error) {
	args := append([]string{"DEL"}, keys...)
	v, err := c.Do(ctx, args...)
	if err != nil {
		return 0, err
	}
	n, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("DEL 返回类型异常: %T", v)
	}
	return n, nil
}

// Expire 续期；返回 key 是否存在（供 Touch 判断会话是否仍有效）。
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	v, err := c.Do(ctx, "EXPIRE", key, itoa(int(ttl.Seconds())))
	if err != nil {
		return false, err
	}
	n, ok := v.(int64)
	if !ok {
		return false, fmt.Errorf("EXPIRE 返回类型异常: %T", v)
	}
	return n == 1, nil
}

// Keys 按 pattern 列出 key（演示用；生产演化方向：SCAN 避免阻塞）。
func (c *Client) Keys(ctx context.Context, pattern string) ([]string, error) {
	v, err := c.Do(ctx, "KEYS", pattern)
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("KEYS 返回类型异常: %T", v)
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// ---- RESP 解码 ----

// readReply 解析一个 RESP 响应。
func readReply(r *bufio.Reader) (interface{}, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("空响应行")
	}
	switch line[0] {
	case '+': // 简单字符串
		return line[1:], nil
	case '-': // 错误
		return nil, errors.New(string(line[1:]))
	case ':': // 整数
		return strconv.ParseInt(string(line[1:]), 10, 64)
	case '$': // bulk string
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil // nil 值
		}
		if n < 0 {
			return nil, fmt.Errorf("非法 bulk 长度 %d", n)
		}
		// 防御：恶意/损坏的响应可能声明巨大长度导致 make 溢出或 OOM。
		// bulk 为会话/记忆字符串，正常远小于 8MB；超限视为协议异常。
		if n > maxBulkBytes {
			return nil, fmt.Errorf("bulk 长度 %d 超过上限 %d", n, maxBulkBytes)
		}
		buf := make([]byte, n+2) // 含结尾 \r\n
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*': // 数组
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil // nil 数组（RESP2 等价 nil）
		}
		if n < 0 {
			return nil, fmt.Errorf("非法数组长度 %d", n)
		}
		// 防御：数组元素个数声明过大（如 LRANGE 0 -1 的 40 亿）会预先分配
		// 巨型切片导致 OOM；限制后返回错误而非崩进程。
		if n > maxArrayLen {
			return nil, fmt.Errorf("数组长度 %d 超过上限 %d", n, maxArrayLen)
		}
		arr := make([]interface{}, 0, n)
		for i := 0; i < n; i++ {
			v, err := readReply(r)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("未知 RESP 前缀 %q", line[0])
	}
}

// readLine 读一行（去掉 \r\n）。
// 容错：非法行（缺少 \r 或过短）不 panic，返回内容或错误。
// 六11：无上限的 ReadString 会无限缓冲恶意响应（如超长简单串/错误行），
// 用 LimitReader 封顶（maxLineBytes）。
func readLine(r *bufio.Reader) (string, error) {
	line, err := readLineBytes(r)
	if err != nil {
		return "", err
	}
	// 去掉行尾分隔符：readLineBytes 已去掉 \n，这里再兜底去掉结尾 \r
	if len(line) >= 1 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return string(line), nil
}

// maxLineBytes 单行上限（六11）：超过即视为协议异常，拒绝继续缓冲。
// 正常 RESP 行（+OK / :100 / $123 / -ERR msg）都远小于此。
const maxLineBytes = 1 << 20 // 1MB

func readLineBytes(r *bufio.Reader) ([]byte, error) {
	var b []byte
	for {
		var chunk [1]byte
		n, err := r.Read(chunk[:])
		if n == 1 {
			if chunk[0] == '\n' {
				return b, nil
			}
			b = append(b, chunk[0])
			if len(b) > maxLineBytes {
				return nil, fmt.Errorf("RESP 行超过上限 %d 字节", maxLineBytes)
			}
		}
		if err != nil {
			if err == io.EOF && len(b) > 0 {
				return b, nil // 无换行的末尾行
			}
			return nil, err
		}
	}
}

func (c *Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 5 * time.Second
	}
	return c.Timeout
}

func itoa(n int) string { return strconv.Itoa(n) }
