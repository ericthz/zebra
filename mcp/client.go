package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"
)

// MCP 客户端 (stdio / http)

type Transport interface {
	Send(req *Request) (*Response, error)
	Close() error
}

// --- stdio Transport ---
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	reader *bufio.Reader
	nextID int
}

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
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		reader: bufio.NewReader(stdout),
		nextID: 1,
	}, nil
}

func (t *stdioTransport) Send(req *Request) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	req.JSONRPC = "2.0"
	req.ID = t.nextID
	t.nextID++

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	_, err = fmt.Fprintf(t.stdin, "%s\n", data)
	if err != nil {
		return nil, err
	}

	line, err := t.reader.ReadBytes('\n')
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

// --- HTTP Transport ---
type httpTransport struct {
	url    string
	client *http.Client
	nextID int
	mu     sync.Mutex
}

func NewHTTPClient(url string) Transport {
	return &httpTransport{
		url:    url,
		client: &http.Client{},
		nextID: 1,
	}
}

func (t *httpTransport) Send(req *Request) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	req.JSONRPC = "2.0"
	req.ID = t.nextID
	t.nextID++

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var resp Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (t *httpTransport) Close() error { return nil }

// --- MCP Client 高级封装 ---
type Client struct {
	transport Transport
}

func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

func (c *Client) ListTools() ([]ToolDef, error) {
	req := &Request{Method: "tools/list"}
	resp, err := c.transport.Send(req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var result ListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *Client) CallTool(name string, args map[string]interface{}) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}
	paramsBytes, _ := json.Marshal(params)
	req := &Request{
		Method: "tools/call",
		Params: paramsBytes,
	}
	resp, err := c.transport.Send(req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Close() error {
	return c.transport.Close()
}
