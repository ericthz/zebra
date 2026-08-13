// 工作记忆：会话内短期上下文（C12 记忆分层）。
// 按 sessionID 隔离（A4 多租户隔离在会话维度体现），每条记录带时间戳，
// 超过 max 条自动淘汰最旧（滑动）。
package memory

import (
	"fmt"
	"sync"
	"time"
)

// WorkingMemory 进程内工作记忆。
type WorkingMemory struct {
	mu    sync.RWMutex
	max   int
	items map[string][]workingItem
}

type workingItem struct {
	at      time.Time
	summary string
}

// NewWorkingMemory 构造，max 为每个会话保留的最大条数。
func NewWorkingMemory(max int) *WorkingMemory {
	return &WorkingMemory{max: max, items: make(map[string][]workingItem)}
}

// Add 追加一轮对话摘要到指定会话。
func (w *WorkingMemory) Add(sessionID, userInput, assistant string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	summary := fmt.Sprintf("用户: %s\n助手: %s", userInput, assistant)
	list := w.items[sessionID]
	list = append(list, workingItem{at: time.Now(), summary: summary})
	if len(list) > w.max {
		list = list[len(list)-w.max:]
	}
	w.items[sessionID] = list
}

// Recent 返回会话最近的 n 条摘要。
func (w *WorkingMemory) Recent(sessionID string, n int) []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	list := w.items[sessionID]
	if len(list) > n {
		list = list[len(list)-n:]
	}
	out := make([]string, 0, len(list))
	for _, it := range list {
		out = append(out, it.summary)
	}
	return out
}

// Clear 清空指定会话的工作记忆。
func (w *WorkingMemory) Clear(sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.items, sessionID)
}
