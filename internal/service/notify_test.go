package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

func TestBuildRequest_四渠道Payload(t *testing.T) {
	msg := notifyMessage{Title: "执行失败：demo #7", Text: "**执行失败**\n\n> 项目：demo"}

	// 企微
	req, _, err := buildRequest(NotifyWeCom, notifyPayload{WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k"}, msg)
	if err != nil {
		t.Fatal(err)
	}
	body := readReqBody(t, req)
	if body["msgtype"] != "markdown" {
		t.Fatalf("企微 msgtype = %v", body["msgtype"])
	}
	if md, ok := body["markdown"].(map[string]any); !ok || md["content"] == "" {
		t.Fatalf("企微 markdown = %v", body["markdown"])
	}

	// 钉钉（带签名）
	req, _, err = buildRequest(NotifyDingTalk,
		notifyPayload{WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=t", Secret: "SECabc123"}, msg)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	if q.Get("timestamp") == "" || q.Get("sign") == "" {
		t.Fatalf("钉钉缺少签名参数: %s", req.URL)
	}
	body = readReqBody(t, req)
	md, _ := body["markdown"].(map[string]any)
	if body["msgtype"] != "markdown" || md["title"] != msg.Title {
		t.Fatalf("钉钉 payload = %v", body)
	}

	// 飞书（带签名）
	req, _, err = buildRequest(NotifyFeishu,
		notifyPayload{WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/x", Secret: "SECabc123"}, msg)
	if err != nil {
		t.Fatal(err)
	}
	body = readReqBody(t, req)
	if body["msg_type"] != "text" || body["timestamp"] == "" || body["sign"] == "" {
		t.Fatalf("飞书 payload = %v", body)
	}
	if c, ok := body["content"].(map[string]any); !ok || c["text"] == "" {
		t.Fatalf("飞书 content = %v", body["content"])
	}

	// 通用 webhook（带 body 签名）
	req, raw, err := buildRequest(NotifyWebhook,
		notifyPayload{WebhookURL: "https://example.com/hook", Secret: "s3"}, msg)
	if err != nil {
		t.Fatal(err)
	}
	want := hmac.New(sha256.New, []byte("s3"))
	want.Write([]byte(raw))
	if got := req.Header.Get("X-Hub-Signature-256"); got != "sha256="+hex.EncodeToString(want.Sum(nil)) {
		t.Fatalf("通用 webhook 签名 = %q", got)
	}
	body = readReqBody(t, req)
	if body["event"] != "run_finished" || body["title"] != msg.Title {
		t.Fatalf("通用 webhook payload = %v", body)
	}
}

func TestNotifySignature_已知向量(t *testing.T) {
	// 期望值由独立实现（Python hmac/base64）算出，防止"自己算自己验"的同义反复
	if got := dingTalkSign(1700000000000, "SECabc123"); got != "N5P09a4+p1AMJIJWnIvQd2Yxw9+fu/oEBnPrjCcsLXk=" {
		t.Fatalf("钉钉签名 = %q", got)
	}
	if got := feishuSign("1700000000", "SECabc123"); got != "UqhI0v4zAkSwI4hNYBuHQvnrAqshA0UaeBGHCUMPX70=" {
		t.Fatalf("飞书签名 = %q", got)
	}
	_ = base64.StdEncoding
}

func TestNotifyConfig_校验与掩码(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Notify

	if _, err := svc.Create(proj.ID, NotifyConfigReq{Name: "x", Channel: "telegram", WebhookURL: "https://e.com/h"}); err == nil {
		t.Fatal("未知渠道应拒绝")
	}
	if _, err := svc.Create(proj.ID, NotifyConfigReq{Name: "x", Channel: NotifyWebhook, WebhookURL: "ftp://e.com/h"}); err == nil {
		t.Fatal("非 http(s) 地址应拒绝")
	}

	cfg, err := svc.Create(proj.ID, NotifyConfigReq{
		Name: "群", Channel: NotifyWeCom, WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc", Secret: "s", OnSuccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 非管理员：URL 掩码为 host/…，secret 不返回
	list, err := svc.List(proj.ID, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("列表 = %+v, %v", list, err)
	}
	if list[0].WebhookURL == cfg.WebhookURL || list[0].WebhookURL == "" {
		t.Fatalf("非管理员不应看到完整 URL: %q", list[0].WebhookURL)
	}
	// 管理员看到完整
	adminList, _ := svc.List(proj.ID, true)
	if adminList[0].WebhookURL != cfg.WebhookURL {
		t.Fatalf("管理员应看到完整 URL: %q", adminList[0].WebhookURL)
	}

	// 回传 *** 表示保持原值
	if _, err := svc.Update(cfg.ID, NotifyConfigReq{
		Name: "群", Channel: NotifyWeCom, WebhookURL: "***", Secret: "***", OnSuccess: false,
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.List(proj.ID, true)
	if after[0].WebhookURL != cfg.WebhookURL || !after[0].HasSecret {
		t.Fatalf("掩码回传应保持原值: %+v", after[0])
	}
}

func TestDispatchRun_过滤与重试(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Notify

	var hits int64
	failFirst := int64(2) // 前两次返回 500，第三次成功
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if atomic.LoadInt64(&failFirst) > 0 {
			atomic.AddInt64(&failFirst, -1)
			w.WriteHeader(500)
			_, _ = w.Write([]byte("boom"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer srv.Close()

	if _, err := svc.Create(proj.ID, NotifyConfigReq{
		Name: "群", Channel: NotifyWebhook, WebhookURL: srv.URL + "/hook", OnSuccess: false,
	}); err != nil {
		t.Fatal(err)
	}

	// 成功执行且 on_success=false：不推送
	okRun := &model.RunRecord{ProjectID: proj.ID, Status: model.RunSuccess, Total: 1, Passed: 1}
	if err := d.DB.Create(okRun).Error; err != nil {
		t.Fatal(err)
	}
	svc.DispatchRun(okRun.ID, model.RunSuccess)
	if atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("成功执行不应推送，实际请求 %d 次", hits)
	}

	// 失败执行：重试 3 次后成功，日志 success、attempts=3
	failRun := &model.RunRecord{ProjectID: proj.ID, Status: model.RunFailed, Total: 2, Failed: 1, Passed: 1}
	if err := d.DB.Create(failRun).Error; err != nil {
		t.Fatal(err)
	}
	svc.DispatchRun(failRun.ID, model.RunFailed)
	if atomic.LoadInt64(&hits) != 3 {
		t.Fatalf("应尝试 3 次，实际 %d", hits)
	}
	logs, err := svc.Logs(proj.ID, 10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("投递日志 = %+v, %v", logs, err)
	}
	if logs[0].Status != NotifySuccess || logs[0].Attempts != 3 || logs[0].RunID != failRun.ID {
		t.Fatalf("日志内容 = %+v", logs[0])
	}
}

func TestDispatchRun_全部失败也记录(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Notify

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("still down"))
	}))
	defer srv.Close()
	if _, err := svc.Create(proj.ID, NotifyConfigReq{
		Name: "群", Channel: NotifyWebhook, WebhookURL: srv.URL + "/hook", OnSuccess: true,
	}); err != nil {
		t.Fatal(err)
	}
	run := &model.RunRecord{ProjectID: proj.ID, Status: model.RunError, Total: 1, Error: 1}
	if err := d.DB.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	svc.DispatchRun(run.ID, model.RunError)

	logs, _ := svc.Logs(proj.ID, 10)
	if len(logs) != 1 || logs[0].Status != NotifyFailed || logs[0].HTTPStatus != 500 || logs[0].Attempts != 3 {
		t.Fatalf("失败日志 = %+v", logs)
	}
	if logs[0].Response == "" || logs[0].Error == "" {
		t.Fatalf("应保留响应片段与错误: %+v", logs[0])
	}
}

func TestNotify_超时常量(t *testing.T) {
	if notifyTimeout != 5*time.Second || notifyAttempts != 3 {
		t.Fatal("超时与重试次数应保持 5s/3 次")
	}
}

func readReqBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("请求体不是 JSON: %s", raw)
	}
	return m
}
