// A1 对话 API：/v1/chat（JSON）与 /v1/chat/stream（SSE）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/supervisor"
)

// ChatRequest 请求体。
type ChatRequest struct {
	SessionID    string   `json:"session_id,omitempty"`    // 空则新建会话
	Message      string   `json:"message"`                 // 用户输入
	Stream       bool     `json:"stream,omitempty"`        // 是否流式
	ConfirmRisky bool     `json:"confirm_risky,omitempty"` // D20 高危工具二次确认授权
	Mode         string   `json:"mode,omitempty"`          // "plan"=规划-执行(P10)；"reflect"=反思(P40)；"react"=ReAct(P45)；空=普通执行
	Shadow       bool     `json:"shadow,omitempty"`        // P21 显式触发影子评测（默认按采样率）
	Images       []string `json:"images,omitempty"`        // C14 多模态：http(s) URL 或 data: 数据 URI 列表（可空）
}

// ChatResponse 非流式响应。
type ChatResponse struct {
	SessionID string `json:"session_id"`
	Reply     string `json:"reply"`
	Shadow    bool   `json:"shadow_sampled,omitempty"` // P21 是否进了影子对比
}

// handleChat 非流式对话。
func (s *APIServer) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "bad request: message 必填", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	sess, err := s.lockSession(ctx, req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	defer sess.runMu.Unlock()

	ag := s.agentFor(sess)
	opts := agent.RunOptions{Images: req.Images}
	// D20 高危二次确认：仅当客户端显式传 confirm_risky=true 时，才向执行层
	// 授权高危工具；且按角色收敛——调用方角色不在该工具 AllowedRoles 内时
	// 一律拒绝（防止普通用户借 confirm 越权执行 admin-only 工具）。
	if req.ConfirmRisky {
		opts.Confirm = s.confirmFor(sess.User, sess.Role)
	}
	// P10 编排模式：先规划再逐步执行；P13 多 Agent：自动路由到专业 Worker
	var reply string
	switch req.Mode {
	case "plan":
		reply, err = ag.PlanAndExecute(ctx, req.Message, opts)
	case "supervisor":
		if s.deps.Supervisor == nil {
			http.Error(w, "supervisor 未启用", http.StatusNotImplemented)
			return
		}
		var workerName string
		var w *supervisor.Worker
		reply, w, err = s.deps.Supervisor.Run(ctx, req.Message, sess.ID, sess.Role, sess.User, sess.History(), opts)
		if w != nil {
			workerName = w.Name
		}
		s.deps.Logger.Info("supervisor 路由", "worker", workerName)
	case "reflect":
		// P40 反思：先正常回答，再让模型批判-改进一轮；修订版写回历史并重新审核
		// （P2-B：RunReflect 统一处理修订版的 D18 审核与 replaceLastAssistant）。
		reply, err = ag.RunReflect(ctx, req.Message, opts)
	case "react":
		// P45 ReAct：显式"思考→行动→观察→答案"轨迹
		reply, err = ag.ReAct(ctx, req.Message, opts, 6)
	case "debate":
		// P46 双 Agent 辩论：左右立场独立作答→交换观点→评审选优
		reply, _, err = ag.Debate(ctx, req.Message, "", "", req.Images...)
	case "consistent":
		// P40 自一致性：独立采样多份回答再择优，降低单次采样随机性。
		// 采样数可配（SELF_CONSISTENT_SAMPLES，默认 3），与 CLI mode 对齐。
		reply, err = ag.SelfConsistent(ctx, req.Message, atoiDefault(os.Getenv("SELF_CONSISTENT_SAMPLES"), 3), req.Images...)
	default:
		reply, err = ag.Run(ctx, req.Message, opts)
	}
	if err != nil {
		// 失败轮次 Agent 已把 user/assistant 写入会话历史（rememberTurn），
		// 仍须落库，否则 Redis 存储下下一轮请求会丢失该轮上下文。
		s.persistHistory(sess)
		s.writeAgentError(w, "chat failed", err)
		return
	}

	resp := ChatResponse{SessionID: sess.ID, Reply: reply}

	// P28 水平扩展：Redis 会话存储需要把 Agent 修改后的历史写回，
	// 否则下一轮请求打到其它副本时读不到多轮上下文。
	s.persistHistory(sess)

	// P21 影子模式：真实流量按采样率（或显式请求）复制给候选模型对比。
	// 异步执行，不阻塞用户响应；结论落影子记录，供换模型前的回归评估。
	if s.deps.Shadow != nil && s.deps.Shadow.WantSample(req.Shadow) {
		resp.Shadow = true
		go func() {
			res := s.deps.Shadow.Run(context.Background(), sess.User, sess.ID, req.Message, reply, s.deps.Model)
			s.deps.Logger.Info("shadow sampled", "id", res.ID, "verdict", res.Verdict)
			s.maybeShadowRollback() // P42 金丝雀自动回滚
		}()
	}
	json.NewEncoder(w).Encode(resp)

	// P4 主动出站：任务完成异步通知业务系统（不阻塞响应）
	if s.deps.Notifier != nil {
		go func() {
			ev := notify.Event{
				Type: "task.complete", Session: sess.ID, Title: "对话完成",
				Payload: map[string]any{"user": sess.User, "reply_summary": truncateText(reply, 100)},
			}
			if err := s.deps.Notifier.Send(context.Background(), ev); err != nil {
				s.deps.Logger.Warn("webhook 通知失败", "session", sess.ID, "err", err)
			}
		}()
	}
}

// confirmFor 构造 D20 高危二次确认回调：
//   - 该工具非高危（风险 < 2）→ 无需确认，直接放行
//   - 该工具声明了 AllowedRoles 且当前角色不在其中 → 拒绝（防越权）
//   - 高危 + 角色匹配 → 放行（客户端已显式传 confirm_risky=true）
//
// 核心改进：不再是"给了 Confirm 就恒 true"，而是按工具风险等级与角色收敛
// 做真实授权，普通用户不能借 confirm 参数执行 admin-only 高危工具。
func (s *APIServer) confirmFor(user, role string) func(string, map[string]interface{}) bool {
	return func(name string, _ map[string]interface{}) bool {
		if s.deps.Tools.RiskLevel(name) < 2 {
			return true // 非高危，无需确认
		}
		roles := s.deps.Tools.AllowedRolesOf(name)
		if len(roles) > 0 {
			for _, rl := range roles {
				if rl == role {
					return true
				}
			}
			s.deps.Logger.Warn("高危工具确认被拒绝（角色越权）", "user", user, "role", role, "tool", name)
			return false
		}
		return true
	}
}

// truncateText 截断摘要，避免把完整回复推给业务系统。
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
}

// redactErr 工具失败错误脱敏（S-2）：错误里可能携带命令完整输出或机密
// （run_command 失败时输出并入 err），日志只留脱敏+截断后的摘要。
func redactErr(err error) string {
	if err == nil {
		return ""
	}
	return truncateText(safety.Redact(err.Error()), 240)
}

// handleChatStream SSE 流式：事件逐条推给客户端（C10）。
func (s *APIServer) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "bad request: message 必填", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	sess, err := s.lockSession(ctx, req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	defer sess.runMu.Unlock()

	ag := s.agentFor(sess)
	opts := agent.RunOptions{Images: req.Images}
	if req.ConfirmRisky {
		opts.Confirm = s.confirmFor(sess.User, sess.Role)
	}

	// SSE 头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// 先回传 session_id（客户端据此续接会话）。必须按 JSON 编码（引号包裹），
	// 否则前端 JSON.parse 失败，会话 id 永远拿不到。
	sidJSON, _ := json.Marshal(sess.ID)
	fmt.Fprintf(w, "event: session\ndata: %s\n\n", sidJSON)
	if flusher != nil {
		flusher.Flush()
	}

	// 按 mode 分发：plan/react 推送阶段轨迹；supervisor/reflect/debate
	// 在流式下输出最终结果（单 delta）；其余走原生流式。
	evs, err := s.streamForMode(ctx, ag, sess, req, opts)
	if err != nil {
		s.persistHistory(sess)
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err)
		return
	}
	for ev := range evs {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	// P28：流式对话结束后同样写回历史
	s.persistHistory(sess)
}

// streamForMode 按对话模式构造事件流。
func (s *APIServer) streamForMode(ctx context.Context, ag *agent.Agent, sess *Session, req ChatRequest, opts agent.RunOptions) (<-chan agent.Event, error) {
	switch req.Mode {
	case "plan", "react", "supervisor", "reflect", "debate":
		ch := make(chan agent.Event, 32)
		go func() {
			defer close(ch)
			emit := func(ev agent.Event) { ch <- ev }
			var reply string
			var err error
			streamed := false // plan/react 已在内部流式输出答案，无需补发
			switch req.Mode {
			case "plan":
				reply, err = ag.PlanAndExecuteStream(ctx, req.Message, opts, emit)
				streamed = true
			case "react":
				maxSteps := s.deps.ReActMaxSteps
				if maxSteps <= 0 {
					maxSteps = 6
				}
				reply, err = ag.ReActStream(ctx, req.Message, opts, maxSteps, emit)
				streamed = true
			case "supervisor":
				emit(agent.Event{Type: agent.EventPhase, Phase: "多 Agent 路由中…"})
				if s.deps.Supervisor == nil {
					err = fmt.Errorf("supervisor 未启用")
					break
				}
				reply, _, err = s.deps.Supervisor.Run(ctx, req.Message, sess.ID, sess.Role, sess.User, sess.History(), opts)
			case "reflect":
				emit(agent.Event{Type: agent.EventPhase, Phase: "回答后反思改进…"})
				reply, err = ag.RunReflect(ctx, req.Message, opts)
			case "debate":
				emit(agent.Event{Type: agent.EventPhase, Phase: "双 Agent 辩论中…"})
				reply, _, err = ag.Debate(ctx, req.Message, "", "", req.Images...)
			}
			if err != nil {
				// 流式失败：内部细节只进日志，客户端收通用文案（P1-10）
				s.deps.Logger.Warn("stream failed", "session", sess.ID, "err", err)
				ch <- agent.Event{Type: agent.EventError, Message: userFacingError(err), Err: err}
			} else if reply != "" && !streamed {
				ch <- agent.Event{Type: agent.EventDelta, Content: reply}
			}
			ch <- agent.Event{Type: agent.EventDone}
		}()
		return ch, nil
	default:
		return ag.RunStream(ctx, req.Message, opts)
	}
}

// persistHistory 若会话存储实现了 HistoryPersister，把最新历史写回。
func (s *APIServer) persistHistory(sess *Session) {
	if hp, ok := s.deps.Sessions.(HistoryPersister); ok {
		if err := hp.Save(sess); err != nil {
			s.deps.Logger.Warn("会话历史写回失败", "session", sess.ID, "err", err)
		}
	}
}

// sessionFor 获取或创建会话，并刷新 TTL（A2）。
func (s *APIServer) sessionFor(ctx context.Context, sessionID string) (*Session, error) {
	p, ok := principal(ctx)
	if !ok {
		return nil, fmt.Errorf("missing principal")
	}
	if sessionID != "" {
		if sess, ok := s.deps.Sessions.Get(sessionID); ok {
			if sess.Tenant == p.Tenant && sess.User == p.User { // A4 会话归属校验
				s.deps.Sessions.Touch(sessionID)
				return sess, nil
			}
			return nil, fmt.Errorf("session 不属于当前用户")
		}
		return nil, fmt.Errorf("session 不存在或已过期")
	}
	sess, err := s.deps.Sessions.Create(p.User, p.Tenant, p.Role, 30*time.Minute)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// lockSession 获取会话的执行权（串行化）并返回最新历史快照。
//
// 为什么必须"锁内重取"：Redis 会话存储每次 Get 都重建 *Session，历史是
// 锁外快照。若只加锁不重取，并发请求会各自基于过期副本追加并整写回，
// 后写的覆盖先写的 → 丢轮次。故先拿共享锁（见 Session.runMu），再在锁内
// Get 一次拿到"上一个请求已落库"的最新历史。内存存储 Get 返回同一对象，
// 重取是幂等无副作用的。
func (s *APIServer) lockSession(ctx context.Context, sessionID string) (*Session, error) {
	sess, err := s.sessionFor(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sess.runMu.Lock() // 共享执行锁（Redis 模式下按会话 ID 统一互斥）
	// 用 sess.ID 而非原始参数重取：新建会话时 sessionID 为空，直接 Get("")
	// 永远取不到（P0-3 改为报错后，空 ID 会把新建会话误判为"已过期"）。
	if fresh, ok := s.deps.Sessions.Get(sess.ID); ok {
		// 锁内重取最新历史；fresh 与 sess 共享同一把 runMu（同一指针）
		return fresh, nil
	}
	// 锁内重取失败 = 会话已被删除/过期（P0-3）。绝不能回退锁外陈旧快照
	// 继续执行并整历史写回——那会让被遗忘/过期的会话"复活"。解锁后报错，
	// 与 sessionFor 的语义一致。
	sess.runMu.Unlock()
	return nil, fmt.Errorf("session 不存在或已过期")
}

// atoiDefault 字符串转 int，失败返回默认值（与 cmd/server 装配同款）。
func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
