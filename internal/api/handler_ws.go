package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// wsHandler 提供执行进度推送（F9）。
type wsHandler struct {
	run    *service.RunService
	member *service.MemberService
	events *service.EventHub
}

func newWSHandler(deps *Deps) *wsHandler {
	return &wsHandler{
		run:    deps.Services.Run,
		member: deps.Services.Member,
		events: deps.Services.Events,
	}
}

// upgrader 允许同源与开发服务器跨源。
//
// 平台是内网工具，Origin 校验不做白名单；真正的权限闸门在下面
// （只有能访问该项目的登录用户才能订阅）。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// RunEvents 订阅某次执行的进度。
//
// 连接建立后先发一条当前状态（避免"连上但界面还是空的"），随后推送：
//   - status：执行进入 running
//   - case：用例集里每完成一条
//   - finished：终态（发完即关闭，前端不必猜什么时候停）
func (h *wsHandler) RunEvents(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	run, err := h.run.GetRun(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	allowed, err := h.member.CanAccess(run.ProjectID, currentUserID(c), currentIsAdmin(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	if !allowed {
		// WS 握手要按 HTTP 语义返回真实 403：客户端（浏览器/工具）只看状态码
		// 决定是否建立连接，返回 200+业务码会被当成"握手失败"但原因不明。
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": response.CodeForbidden, "message": "你不是该项目成员，无权订阅该执行", "data": nil,
		})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade 内部已写过响应，这里只需记日志。
		return
	}
	defer conn.Close()

	subID, events := h.events.Subscribe(id)
	defer h.events.Unsubscribe(id, subID)

	// 初始快照
	if err := conn.WriteJSON(service.RunEvent{
		RunID: id, Type: service.EventStatus, Status: run.Status,
		Total: run.Total, At: time.Now().Format(time.RFC3339), Finished: isFinalRunStatus(run.Status),
	}); err != nil {
		return
	}
	if isFinalRunStatus(run.Status) {
		// 已经结束了：给完快照就关闭，前端直接走"已完成"分支。
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "已完成"), time.Now().Add(time.Second))
		return
	}

	// 读循环：客户端发心跳/关闭都能被感知（不处理业务消息）。
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
			if ev.Finished {
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "已完成"), time.Now().Add(time.Second))
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-done:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

// isFinalRunStatus 判断执行是否已结束（用于快照的 Finished 标记）。
func isFinalRunStatus(status string) bool {
	switch status {
	case model.RunSuccess, model.RunFailed, model.RunError, model.RunCanceled:
		return true
	}
	return false
}
