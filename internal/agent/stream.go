// 流式运行器（C10）：Agent 层事件模型 + SSE 语义。
package agent

import (
	"context"

	"github.com/ericthz/zebra/internal/provider"
)

// EventType Agent 层事件类型。
type EventType string

const (
	EventDelta EventType = "delta"     // 文本增量
	EventTool  EventType = "tool_call" // 模型调用工具
	EventSkill EventType = "skill"     // 注入技能（P61）
	EventPhase EventType = "phase"     // 阶段提示（规划/执行/思考/观察等）
	EventDone  EventType = "done"      // 本轮完成
	EventError EventType = "error"     // 出错
)

// Event Agent 层事件。
type Event struct {
	Type    EventType              `json:"type"`
	Content string                 `json:"content,omitempty"`
	Name    string                 `json:"tool_name,omitempty"`
	Skill   string                 `json:"skill_name,omitempty"` // skill 事件的技能名
	Args    map[string]interface{} `json:"tool_args,omitempty"`
	Phase   string                 `json:"phase,omitempty"`   // phase 事件的阶段文案
	Message string                 `json:"message,omitempty"` // error 时携带错误文本
	Err     error                  `json:"-"`
}

// RunStream 流式执行一轮对话：返回事件通道，调用方逐条消费（如转 SSE）。
func (a *Agent) RunStream(ctx context.Context, userInput string, opts RunOptions) (<-chan Event, error) {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		_, err := a.run(a.usageCtx(ctx), userInput, opts, func(ev Event) { ch <- ev })
		if err != nil {
			ch <- Event{Type: EventError, Message: err.Error(), Err: err}
		}
		ch <- Event{Type: EventDone}
	}()
	return ch, nil
}

// collectStream 消费 provider 流式事件，聚合成完整回复，同时转发增量。
func collectStream(ch <-chan provider.StreamEvent, emit func(Event)) (provider.Message, error) {
	var msg provider.Message
	msg.Role = "assistant"
	for ev := range ch {
		switch ev.Type {
		case provider.StreamEventDelta:
			msg.Content += ev.Content
			emit(Event{Type: EventDelta, Content: ev.Content})
		case provider.StreamEventTool:
			if ev.ToolCall != nil {
				msg.ToolCalls = append(msg.ToolCalls, *ev.ToolCall)
				// 工具事件统一由 execTool 在【执行完成】后发出（带参数），
				// 这里只收集调用，避免同一调用在声明/执行两处各发一次导致 UI 重复计数。
			}
		case provider.StreamEventDone:
			return msg, nil
		case provider.StreamEventError:
			if ev.Err != nil {
				return msg, ev.Err
			}
		}
	}
	return msg, nil
}
