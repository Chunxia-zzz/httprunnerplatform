package service

import (
	"sync"
	"time"
)

// RunEvent 是一次执行进度事件（F9）。
//
// 事件只带「变化了什么」，不重复全量结果：前端收到后按需拉一次详情
// （或直接用 case 事件追加一行）。这样事件体足够小，推送链路不会成为瓶颈。
type RunEvent struct {
	RunID  uint64 `json:"run_id"`
	Type   string `json:"type"` // status / case / finished
	Status string `json:"status,omitempty"`
	Total  int    `json:"total,omitempty"`
	// Case 仅 type=case 时存在（用例集执行时逐条推送）。
	Case *CaseEvent `json:"case,omitempty"`
	At   string     `json:"at"`
	// Finished 是给客户端用的结束标记：true 时可以关闭连接。
	Finished bool `json:"finished"`
}

// CaseEvent 是一条用例的执行结果（用例集逐条推送）。
type CaseEvent struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Seq        int    `json:"seq"`
	DurationMs int64  `json:"duration_ms"`
}

// 事件类型。
const (
	EventStatus   = "status"
	EventCase     = "case"
	EventFinished = "finished"
)

// EventHub 是进程内的执行进度广播（F9）。
//
// 设计取舍：
//   - **进程内**：平台是单实例部署（30 人团队），多实例广播要靠消息中间件，
//     在这里是不必要的复杂度；
//   - **慢订阅者丢事件而不是阻塞**：发送端是执行协程，绝不能因为某个
//     卡住的浏览器标签页把执行卡死。丢事件后前端下一次拉详情仍能对齐。
type EventHub struct {
	mu     sync.Mutex
	subs   map[uint64]map[int]chan RunEvent
	nextID int
}

// NewEventHub 创建事件中心。
func NewEventHub() *EventHub {
	return &EventHub{subs: map[uint64]map[int]chan RunEvent{}}
}

// Subscribe 订阅某次执行的进度，返回订阅 id 与只读通道。
func (h *EventHub) Subscribe(runID uint64) (int, <-chan RunEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	ch := make(chan RunEvent, 32)
	if h.subs[runID] == nil {
		h.subs[runID] = map[int]chan RunEvent{}
	}
	h.subs[runID][id] = ch
	return id, ch
}

// Unsubscribe 取消订阅并关闭通道。
func (h *EventHub) Unsubscribe(runID uint64, id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[runID]
	if set == nil {
		return
	}
	if ch, ok := set[id]; ok {
		delete(set, id)
		close(ch)
	}
	if len(set) == 0 {
		delete(h.subs, runID)
	}
}

// Publish 广播事件。非阻塞：订阅者缓冲区满时丢弃该事件。
func (h *EventHub) Publish(ev RunEvent) {
	if ev.At == "" {
		ev.At = time.Now().Format(time.RFC3339)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs[ev.RunID] {
		select {
		case ch <- ev:
		default:
			// 订阅者跟不上：丢弃。前端靠「收到任意事件就刷新」保证最终一致。
		}
	}
}

// SubscriberCount 返回某次执行的订阅数（测试与排障用）。
func (h *EventHub) SubscriberCount(runID uint64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[runID])
}

// Close 关闭全部订阅（服务优雅退出时调用）。
func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for runID, set := range h.subs {
		for id, ch := range set {
			delete(set, id)
			close(ch)
		}
		delete(h.subs, runID)
	}
}

// ---------------------------------------------------------------------------

// publishStatus 推送执行状态变化。
func (s *RunService) publishStatus(runID uint64, status string, total int, finished bool) {
	if s.Events == nil {
		return
	}
	ev := RunEvent{RunID: runID, Type: EventStatus, Status: status, Total: total, Finished: finished}
	if finished {
		ev.Type = EventFinished
	}
	s.Events.Publish(ev)
}

// publishCase 推送用例集里单条用例的结果。
func (s *RunService) publishCase(runID uint64, code, name, status string, seq, total int, durationMs int64) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(RunEvent{
		RunID: runID, Type: EventCase, Status: status, Total: total,
		Case: &CaseEvent{Code: code, Name: name, Status: status, Seq: seq, DurationMs: durationMs},
	})
}
