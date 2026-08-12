package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
)

// MCP 服务器框架 (stdio / http)

type ToolHandler func(args map[string]interface{}) (*CallToolResult, error)

type Server struct {
	mu      sync.RWMutex
	tools   map[string]ToolDef
	handler map[string]ToolHandler
}

func NewServer() *Server {
	return &Server{
		tools:   make(map[string]ToolDef),
		handler: make(map[string]ToolHandler),
	}
}

// RegisterTool 注册工具
func (s *Server) RegisterTool(def ToolDef, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[def.Name] = def
	s.handler[def.Name] = handler
}

// processRequest 处理 JSON‑RPC 请求
func (s *Server) processRequest(req *Request) *Response {
	resp := &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
	}
	switch req.Method {
	case "tools/list":
		s.mu.RLock()
		tools := make([]ToolDef, 0, len(s.tools))
		for _, t := range s.tools {
			tools = append(tools, t)
		}
		s.mu.RUnlock()
		result, _ := json.Marshal(ListToolsResult{Tools: tools})
		resp.Result = result
	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &Error{Code: -32602, Message: "Invalid params"}
			return resp
		}
		s.mu.RLock()
		handler, ok := s.handler[params.Name]
		s.mu.RUnlock()
		if !ok {
			resp.Error = &Error{Code: -32601, Message: "Tool not found: " + params.Name}
			return resp
		}
		result, err := handler(params.Arguments)
		if err != nil {
			resp.Error = &Error{Code: -32603, Message: err.Error()}
			return resp
		}
		data, _ := json.Marshal(result)
		resp.Result = data
	default:
		resp.Error = &Error{Code: -32601, Message: "Method not found"}
	}
	return resp
}

// ServeStdio 以 stdio 模式运行服务器（从标准输入读取，写入标准输出）
func (s *Server) ServeStdio() error {
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
		resp := s.processRequest(&req)
		data, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		fmt.Fprintf(os.Stdout, "%s\n", data)
	}
}

// ServeHTTP 启动 HTTP 服务器（默认监听 :8080 或自定义地址）
func (s *Server) ServeHTTP(addr string) error {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		resp := s.processRequest(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	log.Printf("MCP HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, nil)
}
