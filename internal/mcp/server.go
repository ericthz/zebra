// MCP 服务器框架：stdio / HTTP 双模式 + 工具注册 + initialize 握手。
package mcp

import (
	"bufio"
	"context"
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
	mu      sync.RWMutex
	tools   map[string]ToolDef
	handler map[string]ToolHandler
	logger  *slog.Logger
}

// NewServer 构造。
func NewServer(logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{tools: make(map[string]ToolDef), handler: make(map[string]ToolHandler), logger: logger}
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
