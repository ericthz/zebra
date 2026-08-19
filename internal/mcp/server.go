// MCP 服务器框架：stdio / HTTP 双模式 + 工具注册 + initialize 握手。
package mcp

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
)

// ToolHandler 工具处理器。
type ToolHandler func(ctx context.Context, args map[string]interface{}) (*CallToolResult, error)

// Server MCP 服务器。
type Server struct {
	mu        sync.RWMutex
	tools     map[string]ToolDef
	handler   map[string]ToolHandler
	logger    *slog.Logger
	httpToken string // HTTP 模式访问令牌（空 = 不鉴权，仅建议本地/dev）
}

// NewServer 构造。
func NewServer(logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{tools: make(map[string]ToolDef), handler: make(map[string]ToolHandler), logger: logger}
}

// SetHTTPToken 设置 HTTP 模式 Bearer 访问令牌；空串 = 关闭鉴权。
// 设置后所有 HTTP 请求必须携带 Authorization: Bearer <token>。
func (s *Server) SetHTTPToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.httpToken = token
}

// ValidateHTTPStart 校验 HTTP 模式启动前提（P2-E）：HTTP 模式必须显式配置
// Bearer 令牌，否则拒绝启动（默认拒绝、显式放行）。stdio 模式走本地管道，
// 由启动方授权，无需令牌。
func ValidateHTTPStart(httpAddr, token string) error {
	if httpAddr != "" && token == "" {
		return fmt.Errorf("MCP HTTP 模式必须配置鉴权令牌：请用 -http-token 或 MCP_HTTP_TOKEN 设置 Bearer 令牌后重启")
	}
	return nil
}

// checkToken 校验 Bearer 令牌（常量时间比较，防时序侧信道）。
func (s *Server) checkToken(r *http.Request) bool {
	s.mu.RLock()
	token := s.httpToken
	s.mu.RUnlock()
	if token == "" {
		return true // 未启用鉴权
	}
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(token)) == 1
}

// RegisterTool 注册工具。
func (s *Server) RegisterTool(def ToolDef, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[def.Name] = def
	s.handler[def.Name] = handler
}

func (s *Server) process(ctx context.Context, req *Request) *Response {
	resp := &Response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result, _ = json.Marshal(InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      map[string]any{"name": "zebra-mcp", "version": "0.1.0"},
		})
	case "notifications/initialized":
		resp.ID = nil
	case "tools/list":
		s.mu.RLock()
		tools := make([]ToolDef, 0, len(s.tools))
		for _, t := range s.tools {
			tools = append(tools, t)
		}
		s.mu.RUnlock()
		resp.Result, _ = json.Marshal(ListToolsResult{Tools: tools})
	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &Error{Code: -32602, Message: "Invalid params"}
			return resp
		}
		s.mu.RLock()
		h, ok := s.handler[params.Name]
		s.mu.RUnlock()
		if !ok {
			resp.Error = &Error{Code: -32601, Message: "Tool not found: " + params.Name}
			return resp
		}
		result, err := h(ctx, params.Arguments)
		if err != nil {
			resp.Error = &Error{Code: -32603, Message: err.Error()}
			return resp
		}
		resp.Result, _ = json.Marshal(result)
	default:
		resp.Error = &Error{Code: -32601, Message: "Method not found"}
	}
	return resp
}

// ServeStdio 以 stdio 模式运行（JSON-RPC over 每行一条消息）。
func (s *Server) ServeStdio(ctx context.Context) error {
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		resp := s.process(ctx, &req)
		if resp.ID == nil {
			continue // notification 无需响应
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintf(os.Stdout, "%s\n", data)
	}
}

// ServeHTTP 以 HTTP 模式运行。
func (s *Server) ServeHTTP(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		// D19 HTTP 鉴权：设置了 token 则校验 Bearer，失败返回 401 且不处理请求。
		if !s.checkToken(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		resp := s.process(r.Context(), &req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	s.logger.Info("MCP server listening", "addr", addr)
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}
