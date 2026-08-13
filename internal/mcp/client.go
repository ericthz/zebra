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
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
	nextID int
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
	return &stdioTransport{
		cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout), nextID: 1,
	}, nil
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
	line, err := readLineCtx(ctx, t.reader)
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (t *stdioTransport) Close() error {
	t.stdin.Close()
	return t.cmd.Wait()
}

func readLineCtx(ctx context.Context, r *bufio.Reader) ([]byte, error) {
	type res struct {
		line []byte
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- res{line, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.line, r.err
	}
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
