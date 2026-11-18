package service

import (
	"testing"
	"time"
)

func TestEventHub_订阅与广播(t *testing.T) {
	h := NewEventHub()
	id1, ch1 := h.Subscribe(7)
	id2, ch2 := h.Subscribe(7)
	if h.SubscriberCount(7) != 2 {
		t.Fatalf("订阅数 = %d", h.SubscriberCount(7))
	}

	h.Publish(RunEvent{RunID: 7, Type: EventStatus, Status: "running"})
	for _, ch := range []<-chan RunEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.RunID != 7 || ev.Status != "running" || ev.At == "" {
				t.Fatalf("事件内容 = %+v", ev)
			}
		case <-time.After(time.Second):
			t.Fatal("订阅者未收到事件")
		}
	}

	// 其它执行的事件不应串台
	h.Publish(RunEvent{RunID: 8, Type: EventStatus, Status: "running"})
	select {
	case ev := <-ch1:
		t.Fatalf("收到了不该收到的事件: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	h.Unsubscribe(7, id1)
	h.Unsubscribe(7, id2)
	if h.SubscriberCount(7) != 0 {
		t.Fatalf("退订后订阅数 = %d", h.SubscriberCount(7))
	}
}

func TestEventHub_慢订阅者不阻塞(t *testing.T) {
	h := NewEventHub()
	_, ch := h.Subscribe(1) // 故意不读，缓冲区 32

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			h.Publish(RunEvent{RunID: 1, Type: EventStatus, Status: "running"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("发布被慢订阅者阻塞")
	}
	// 缓冲满后丢弃，但已到达的事件仍是完整且有序的
	got := 0
	for {
		select {
		case <-ch:
			got++
			continue
		default:
		}
		break
	}
	if got == 0 || got > 32 {
		t.Fatalf("缓冲区事件数 = %d（应在 1..32）", got)
	}
}

func TestEventHub_关闭全部订阅(t *testing.T) {
	h := NewEventHub()
	_, ch := h.Subscribe(3)
	h.Close()
	if _, ok := <-ch; ok {
		t.Fatal("Close 后通道应已关闭")
	}
	if h.SubscriberCount(3) != 0 {
		t.Fatal("Close 后不应有订阅")
	}
}
