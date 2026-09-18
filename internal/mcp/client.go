// MCP 客户端：stdio / HTTP 双传输 + initialize 握手 + 工具调用封装。
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Transport 传输层抽象。
type Transport interface {
	Send(ctx context.Context, req *Request) (*Response, error)
	Close() error
}

// ---- stdio 传输 ----

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan []byte // 常驻读行 goroutine 推入的行；Close 后关闭
	readErr chan error
	mu      sync.Mutex
	nextID  int
}

// NewStdioClient 启动子进程并建立 stdio 通道。
func NewStdioClient(command string, args ...string) (Transport, error) {
	cmd := exec.Command(command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &stdioTransport{
		cmd: cmd, stdin: stdin, lines: make(chan []byte, 64), readErr: make(chan error, 1), nextID: 1,
	}
	// 常驻读行 goroutine：持续消费 stdout，EOF/读错误后关闭 lines。
	// 修复竞态：旧实现每次 Send 临时起 goroutine 读一行，ctx 取消时
	// goroutine 泄漏且可能吞掉下一轮响应；改为"一传一读 goroutine"生命周期
	// 与传输一致，取消只影响 select，不泄漏也不抢读。
	go t.readLoop(bufio.NewReader(stdout))
	return t, nil
}

// readLoop 常驻读取 stdout 的每一行，推入 lines 通道。
func (t *stdioTransport) readLoop(r *bufio.Reader) {
	defer close(t.lines)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.readErr <- err
			return
		}
		select {
		case t.lines <- line:
		default:
			// 客户端消费慢：丢弃最旧行防内存增长（服务器应答应有界）
			select {
			case <-t.lines:
			default:
			}
			t.lines <- line
		}
	}
}

func (t *stdioTransport) Send(ctx context.Context, req *Request) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	req.JSONRPC = "2.0"
	req.ID = t.nextID
	t.nextID++
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(t.stdin, "%s\n", data); err != nil {
		return nil, err
	}
	// 通知类请求（notifications/*）服务器不返回响应：写后即返回。
	// 若也阻塞读，会占住互斥锁直到超时，饿死后续请求（修复的 stdio 竞态）。
	if strings.HasPrefix(req.Method, "notifications/") {
		return &Response{JSONRPC: "2.0"}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-t.readErr:
		return nil, err
	case line, ok := <-t.lines:
		if !ok {
			return nil, io.EOF
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, err
		}
		// C-2：严格校验响应 ID 与本请求一致。前一个请求若在超时/取消后
		// 其响应行仍滞留 lines，这里不校验就会把"上一条请求的响应"当成本
		// 请求结果回喂模型（并发 MCP 工具调用时结果错位）。丢弃错位行并
		// 重读，最多 bound 次（服务器可能出现合法重排序，须留余量）。
		if !idMatches(resp.ID, req.ID) {
			for attempt := 0; attempt < 8; attempt++ {
				select {
				case line, ok = <-t.lines:
					if !ok {
						return nil, io.EOF
					}
					if err := json.Unmarshal(line, &resp); err != nil {
						return nil, err
					}
					if idMatches(resp.ID, req.ID) {
						return &resp, nil
					}
				case <-ctx.Done():
					return nil, ctx.Err()
				case err := <-t.readErr:
					return nil, err
				}
			}
			return nil, fmt.Errorf("MCP 响应 ID 不匹配: 期望 %d，实际 %v", req.ID, resp.ID)
		}
		return &resp, nil
	}
}

func (t *stdioTransport) Close() error {
	t.stdin.Close()
	return t.cmd.Wait()
}

// idMatches 比较 JSON-RPC ID：JSON 数字解码后为 float64，与发送侧的 int
// 不直接可比，统一转 float64 再比（C-2）。
func idMatches(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == b
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// ---- HTTP 传输 ----

type httpTransport struct {
	url    string
	client *http.Client
	mu     sync.Mutex
	nextID int
}

// NewHTTPClient 建立到 MCP HTTP 服务器的客户端。
func NewHTTPClient(url string) Transport {
	return &httpTransport{url: url, client: &http.Client{Timeout: 10 * time.Second}, nextID: 1}
}

func (t *httpTransport) Send(ctx context.Context, req *Request) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	req.JSONRPC = "2.0"
	req.ID = t.nextID
	t.nextID++
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (t *httpTransport) Close() error { return nil }

// ---- 高级封装 ----

// Client MCP 客户端，封装握手与工具调用。
type Client struct {
	transport Transport
}

// NewClient 构造。
func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

// Initialize 握手：协商协议版本与能力（官方规范要求）。
func (c *Client) Initialize(ctx context.Context) error {
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientInfo":      map[string]any{"name": "zebra", "version": "0.1.0"},
		"capabilities":    map[string]any{},
	})
	resp, err := c.transport.Send(ctx, &Request{Method: "initialize", Params: params})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize 失败: %d %s", resp.Error.Code, resp.Error.Message)
	}
	// 发送 initialized 通知（无需响应）
	c.transport.Send(ctx, &Request{Method: "notifications/initialized", Params: json.RawMessage("{}")})
	return nil
}

// ListTools 拉取远端工具列表。
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	resp, err := c.transport.Send(ctx, &Request{Method: "tools/list"})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list 失败: %d %s", resp.Error.Code, resp.Error.Message)
	}
	var out ListToolsResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool 调用远端工具。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	params, _ := json.Marshal(CallToolParams{Name: name, Arguments: args})
	resp, err := c.transport.Send(ctx, &Request{Method: "tools/call", Params: params})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call 失败: %d %s", resp.Error.Code, resp.Error.Message)
	}
	var out CallToolResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Close 关闭传输。
func (c *Client) Close() error { return c.transport.Close() }
