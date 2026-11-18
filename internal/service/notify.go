package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// NotifyService 负责「执行结束 → 群机器人消息」（F2）。
//
// 设计约定：
//   - 只按官方群机器人协议发 **纯 HTTP POST**：不引入 SDK，签名公式集中成纯函数；
//   - 投递**异步**且失败不重抛：群机器人挂掉绝不能影响执行收尾；
//   - 每次投递都留记录（成功/失败、HTTP 状态、响应片段、尝试次数），
//     "为什么没收到"必须能在页面上回答，而不是去翻服务日志。
type NotifyService struct {
	Deps
}

// 渠道。
const (
	NotifyWeCom    = "wecom"
	NotifyDingTalk = "dingtalk"
	NotifyFeishu   = "feishu"
	NotifyWebhook  = "webhook"
)

var notifyChannels = map[string]bool{
	NotifyWeCom: true, NotifyDingTalk: true, NotifyFeishu: true, NotifyWebhook: true,
}

// 投递结果。
const (
	NotifySuccess = "success"
	NotifyFailed  = "failed"
)

// notifyPayload 是 config 列的存储结构。
type notifyPayload struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret"`
}

// NotifyConfigReq 是创建/更新请求。
type NotifyConfigReq struct {
	Name       string `json:"name"`
	Channel    string `json:"channel"`
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret"`
	OnSuccess  bool   `json:"on_success"`
}

// NotifyConfigView 是列表项（非管理员掩码）。
type NotifyConfigView struct {
	ID         uint64 `json:"id"`
	Name       string `json:"name"`
	Channel    string `json:"channel"`
	WebhookURL string `json:"webhook_url"`
	HasSecret  bool   `json:"has_secret"`
	OnSuccess  bool   `json:"on_success"`
	CreatedAt  string `json:"created_at"`
}

// SendResult 是一次投递的结果（测试发送也用它）。
type SendResult struct {
	OK         bool   `json:"ok"`
	HTTPStatus int    `json:"http_status"`
	Attempts   int    `json:"attempts"`
	Response   string `json:"response"`
	Error      string `json:"error"`
}

const (
	notifyTimeout  = 5 * time.Second
	notifyAttempts = 3
	notifyRespKeep = 512
	maskValue      = "***"
)

// List 返回项目通知配置。
func (s *NotifyService) List(projectID uint64, isAdmin bool) ([]NotifyConfigView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	var list []model.NotifyConfig
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&list).Error; err != nil {
		return nil, errInternal("查询通知配置失败", err)
	}
	out := make([]NotifyConfigView, 0, len(list))
	for i := range list {
		p := decodeNotifyPayload(list[i].Config)
		v := NotifyConfigView{
			ID: list[i].ID, Name: list[i].Name, Channel: list[i].Channel,
			OnSuccess: list[i].OnSuccess, HasSecret: p.Secret != "",
			CreatedAt: list[i].CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if isAdmin {
			v.WebhookURL = p.WebhookURL
		} else {
			// 企微的 key、钉钉的 access_token 都在 URL 里，等同凭证
			v.WebhookURL = maskWebhook(p.WebhookURL)
		}
		out = append(out, v)
	}
	return out, nil
}

// Create 新建通知配置。
func (s *NotifyService) Create(projectID uint64, req NotifyConfigReq) (*NotifyConfigView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	req, err := normalizeNotifyReq(req, nil)
	if err != nil {
		return nil, err
	}
	cfg, _ := json.Marshal(notifyPayload{WebhookURL: req.WebhookURL, Secret: req.Secret})
	c := &model.NotifyConfig{
		ProjectID: projectID, Name: req.Name, Channel: req.Channel,
		Config: jsonx.Any{Val: json.RawMessage(cfg)}, OnSuccess: req.OnSuccess,
	}
	if err := s.DB.Create(c).Error; err != nil {
		return nil, errInternal("保存通知配置失败", err)
	}
	return &NotifyConfigView{ID: c.ID, Name: c.Name, Channel: c.Channel,
		WebhookURL: req.WebhookURL, HasSecret: req.Secret != "", OnSuccess: c.OnSuccess,
		CreatedAt: c.CreatedAt.Format("2006-01-02 15:04:05")}, nil
}

// Update 全量更新。
func (s *NotifyService) Update(id uint64, req NotifyConfigReq) (*NotifyConfigView, error) {
	var c model.NotifyConfig
	if err := s.DB.First(&c, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("通知配置不存在")
		}
		return nil, errInternal("查询通知配置失败", err)
	}
	prev := decodeNotifyPayload(c.Config)
	req, err := normalizeNotifyReq(req, &prev)
	if err != nil {
		return nil, err
	}
	cfg, _ := json.Marshal(notifyPayload{WebhookURL: req.WebhookURL, Secret: req.Secret})
	c.Name, c.Channel, c.OnSuccess = req.Name, req.Channel, req.OnSuccess
	c.Config = jsonx.Any{Val: json.RawMessage(cfg)}
	if err := s.DB.Save(&c).Error; err != nil {
		return nil, errInternal("更新通知配置失败", err)
	}
	return &NotifyConfigView{ID: c.ID, Name: c.Name, Channel: c.Channel,
		WebhookURL: req.WebhookURL, HasSecret: req.Secret != "", OnSuccess: c.OnSuccess,
		CreatedAt: c.CreatedAt.Format("2006-01-02 15:04:05")}, nil
}

// Delete 删除配置。
func (s *NotifyService) Delete(id uint64) error {
	res := s.DB.Delete(&model.NotifyConfig{}, id)
	if res.Error != nil {
		return errInternal("删除通知配置失败", res.Error)
	}
	if res.RowsAffected == 0 {
		return errNotFound("通知配置不存在")
	}
	return nil
}

// Test 发送测试消息（同步返回结果，便于"配完就知道通不通"）。
func (s *NotifyService) Test(id uint64) (*SendResult, error) {
	var c model.NotifyConfig
	if err := s.DB.First(&c, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("通知配置不存在")
		}
		return nil, errInternal("查询通知配置失败", err)
	}
	p := decodeNotifyPayload(c.Config)
	msg := notifyMessage{
		Title:  "测试消息",
		Text:   "这是一条测试消息。收到说明通知配置可用。\n项目：执行平台通知\n时间：" + time.Now().Format("2006-01-02 15:04:05"),
		IsTest: true,
	}
	res := s.send(c.Channel, p, msg)
	s.writeLog(c.ProjectID, c.ID, 0, c.Channel, res)
	return res, nil
}

// Logs 返回最近投递记录。
func (s *NotifyService) Logs(projectID uint64, limit int) ([]model.NotifyLog, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var list []model.NotifyLog
	if err := s.DB.Where("project_id = ?", projectID).Order("id desc").Limit(limit).Find(&list).Error; err != nil {
		return nil, errInternal("查询投递记录失败", err)
	}
	return list, nil
}

// DispatchRun 在执进入终态后异步推送（F2）。
//
// 失败/错误/取消**总是**推；成功由配置的 on_success 决定。
func (s *NotifyService) DispatchRun(runID uint64, status string) {
	var run model.RunRecord
	if err := s.DB.First(&run, runID).Error; err != nil {
		return
	}
	project, err := loadProject(s.DB, run.ProjectID)
	if err != nil {
		return
	}
	var configs []model.NotifyConfig
	if err := s.DB.Where("project_id = ?", run.ProjectID).Find(&configs).Error; err != nil {
		return
	}
	if len(configs) == 0 {
		return
	}

	msg := runNotifyMessage(project, &run, status, s.Cfg.Server.PublicURL)
	for i := range configs {
		c := configs[i]
		if status == model.RunSuccess && !c.OnSuccess {
			continue
		}
		p := decodeNotifyPayload(c.Config)
		res := s.send(c.Channel, p, msg)
		s.writeLog(c.ProjectID, c.ID, run.ID, c.Channel, res)
	}
}

// ---------------------------------------------------------------------------
// 消息与签名
// ---------------------------------------------------------------------------

type notifyMessage struct {
	Title  string
	Text   string
	IsTest bool
}

// runNotifyMessage 生成执行结果消息。
func runNotifyMessage(project *model.Project, run *model.RunRecord, status, publicURL string) notifyMessage {
	result := "成功"
	switch status {
	case model.RunFailed:
		result = "失败"
	case model.RunError:
		result = "异常"
	case model.RunCanceled:
		result = "已终止"
	}
	title := fmt.Sprintf("执行%s：%s #%d", result, project.Name, run.ID)
	text := fmt.Sprintf("**执行%s**\n\n> 项目：%s（%s）\n> 执行：#%d\n> 结果：%s\n> 用例：通过 %d / 失败 %d / 错误 %d / 跳过 %d（共 %d）\n> 耗时：%.1fs\n> 时间：%s",
		result, project.Name, project.Code, run.ID, result,
		run.Passed, run.Failed, run.Error, run.Skipped, run.Total,
		float64(run.DurationMs)/1000, time.Now().Format("2006-01-02 15:04:05"))
	if base := strings.TrimRight(strings.TrimSpace(publicURL), "/"); base != "" {
		text += fmt.Sprintf("\n> 详情：%s/runs/%d", base, run.ID)
	}
	return notifyMessage{Title: title, Text: text}
}

// buildRequest 生成一次投递的 HTTP 请求（渠道差异都在这里）。
func buildRequest(channel string, p notifyPayload, msg notifyMessage) (*http.Request, string, error) {
	endpoint := p.WebhookURL
	body := map[string]any{}
	ctype := "application/json"

	switch channel {
	case NotifyWeCom:
		body["msgtype"] = "markdown"
		body["markdown"] = map[string]any{"content": msg.Text}
	case NotifyDingTalk:
		body["msgtype"] = "markdown"
		body["markdown"] = map[string]any{"title": msg.Title, "text": msg.Text}
		if p.Secret != "" {
			ts := time.Now().UnixMilli()
			sign := dingTalkSign(ts, p.Secret)
			u, err := url.Parse(endpoint)
			if err != nil {
				return nil, "", err
			}
			q := u.Query()
			q.Set("timestamp", strconv.FormatInt(ts, 10))
			q.Set("sign", sign)
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
	case NotifyFeishu:
		body["msg_type"] = "text"
		body["content"] = map[string]any{"text": msg.Title + "\n" + stripMarkdown(msg.Text)}
		if p.Secret != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			body["timestamp"] = ts
			body["sign"] = feishuSign(ts, p.Secret)
		}
	case NotifyWebhook:
		body["event"] = "test"
		if !msg.IsTest {
			body["event"] = "run_finished"
		}
		body["text"] = msg.Title + "\n" + msg.Text
		body["title"] = msg.Title
	default:
		return nil, "", fmt.Errorf("不支持的通知渠道 %q", channel)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", ctype)
	if channel == NotifyWebhook && p.Secret != "" {
		mac := hmac.New(sha256.New, []byte(p.Secret))
		mac.Write(raw)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return req, string(raw), nil
}

// send 执行投递（含重试）。失败不返回 error，结果都体现在 SendResult 里。
func (s *NotifyService) send(channel string, p notifyPayload, msg notifyMessage) *SendResult {
	res := &SendResult{}
	client := &http.Client{Timeout: notifyTimeout}

	var lastErr error
	for i := 1; i <= notifyAttempts; i++ {
		res.Attempts = i
		req, _, err := buildRequest(channel, p, msg)
		if err != nil {
			lastErr = err
			break
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			res.HTTPStatus = resp.StatusCode
			res.Response = clip(string(data), notifyRespKeep)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				res.OK = true
				res.Error = ""
				return res
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if i < notifyAttempts {
			time.Sleep(time.Duration(i) * 500 * time.Millisecond)
		}
	}
	res.OK = false
	if lastErr != nil {
		res.Error = clip(lastErr.Error(), notifyRespKeep)
	}
	return res
}

func (s *NotifyService) writeLog(projectID, configID, runID uint64, channel string, res *SendResult) {
	if res == nil {
		return
	}
	status := NotifyFailed
	if res.OK {
		status = NotifySuccess
	}
	row := &model.NotifyLog{
		ProjectID: projectID, ConfigID: configID, RunID: runID, Channel: channel,
		Status: status, HTTPStatus: res.HTTPStatus, Response: clip(res.Response, notifyRespKeep),
		Attempts: res.Attempts, Error: clip(res.Error, notifyRespKeep),
	}
	if err := s.DB.Create(row).Error; err != nil {
		// 日志写失败不影响业务，但要在服务端可追踪
		_ = err
	}
}

// dingTalkSign：HMAC-SHA256(key=secret, data=timestamp+"\n"+secret) → base64。
func dingTalkSign(timestampMs int64, secret string) string {
	strToSign := fmt.Sprintf("%d\n%s", timestampMs, secret)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuSign：HMAC-SHA256(key=timestamp+"\n"+secret, data="") → base64。
func feishuSign(timestamp string, secret string) string {
	key := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func normalizeNotifyReq(req NotifyConfigReq, prev *notifyPayload) (NotifyConfigReq, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Channel = strings.ToLower(strings.TrimSpace(req.Channel))
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	req.Secret = strings.TrimSpace(req.Secret)
	if req.Name == "" {
		return req, errBadParam("通知名称不能为空")
	}
	if !notifyChannels[req.Channel] {
		return req, errBadParam("渠道只支持 wecom / dingtalk / feishu / webhook，收到 %q", req.Channel)
	}
	if prev != nil {
		if req.WebhookURL == maskValue {
			req.WebhookURL = prev.WebhookURL
		}
		if req.Secret == maskValue {
			req.Secret = prev.Secret
		}
	}
	u, err := url.Parse(req.WebhookURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return req, errBadParam("webhook 地址必须是 http(s) URL")
	}
	return req, nil
}

func decodeNotifyPayload(a jsonx.Any) notifyPayload {
	var p notifyPayload
	if a.Val == nil {
		return p
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(b, &p)
	return p
}

func maskWebhook(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return maskValue
	}
	return u.Scheme + "://" + u.Host + "/…"
}

func stripMarkdown(s string) string {
	replacer := strings.NewReplacer("**", "", "> ", "", "\n\n", "\n")
	return replacer.Replace(s)
}
