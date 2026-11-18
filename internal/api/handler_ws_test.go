package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
)

// dialRunWS 用登录 cookie 连上执行进度 WS。
func dialRunWS(t *testing.T, srvURL string, runID uint64, cookies []*http.Cookie) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srvURL, "http") + fmt.Sprintf("/api/v1/ws/runs/%d", runID)
	h := http.Header{}
	for _, c := range cookies {
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(u, h)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("WS 连接失败: %v（HTTP %d）", err, code)
	}
	return conn
}

func TestWS_执行进度推送(t *testing.T) {
	e := newTestEnv(t)
	admin := e.seedUser(t, "ws_admin", model.RoleAdmin, true)
	cookies := e.login(t, admin.Username, seedPassword)

	p := &model.Project{Code: "ws_proj", Name: "WS 项目", HrpVersion: "v4.3.6", OwnerID: admin.ID}
	if err := e.db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	run := &model.RunRecord{
		ProjectID: p.ID, TargetType: model.TargetCase, TargetID: 1,
		TargetName: "用例", Status: model.RunRunning, TriggerType: model.TriggerManual,
	}
	if err := e.db.Create(run).Error; err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(e.r)
	defer srv.Close()

	conn := dialRunWS(t, srv.URL, run.ID, cookies)
	defer conn.Close()

	// 1) 连上先给当前状态快照
	var snap service.RunEvent
	if err := conn.ReadJSON(&snap); err != nil {
		t.Fatalf("读取初始快照失败: %v", err)
	}
	if snap.RunID != run.ID || snap.Status != model.RunRunning || snap.Finished {
		t.Fatalf("初始快照 = %+v", snap)
	}

	// 2) 推送用例事件
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	e.svcs.Events.Publish(service.RunEvent{
		RunID: run.ID, Type: service.EventCase, Status: model.StatusPass,
		Case: &service.CaseEvent{Code: "c1", Name: "用例一", Status: model.StatusPass, Seq: 1},
	})
	var ev service.RunEvent
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("读取用例事件失败: %v", err)
	}
	if ev.Type != service.EventCase || ev.Case == nil || ev.Case.Code != "c1" {
		t.Fatalf("用例事件 = %+v", ev)
	}

	// 3) 终态事件后服务端主动关闭
	e.svcs.Events.Publish(service.RunEvent{
		RunID: run.ID, Type: service.EventFinished, Status: model.RunSuccess, Finished: true,
	})
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("读取终态事件失败: %v", err)
	}
	if !ev.Finished || ev.Status != model.RunSuccess {
		t.Fatalf("终态事件 = %+v", ev)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("终态之后连接应被关闭")
	}
}

func TestWS_已结束的执行给完快照即关闭(t *testing.T) {
	e := newTestEnv(t)
	admin := e.seedUser(t, "ws_admin2", model.RoleAdmin, true)
	cookies := e.login(t, admin.Username, seedPassword)

	p := &model.Project{Code: "ws_proj2", Name: "WS 项目2", HrpVersion: "v4.3.6", OwnerID: admin.ID}
	if err := e.db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	run := &model.RunRecord{
		ProjectID: p.ID, TargetType: model.TargetCase, TargetID: 1,
		TargetName: "用例", Status: model.RunSuccess, TriggerType: model.TriggerManual,
	}
	if err := e.db.Create(run).Error; err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(e.r)
	defer srv.Close()

	conn := dialRunWS(t, srv.URL, run.ID, cookies)
	defer conn.Close()

	var ev service.RunEvent
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	if !ev.Finished || ev.Status != model.RunSuccess {
		t.Fatalf("已结束执行应标记 Finished: %+v", ev)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("已结束的执行不应保持连接")
	}
}
