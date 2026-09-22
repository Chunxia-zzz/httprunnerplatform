package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
)

// 本文件覆盖 CI 入口（/open）的三条边界：
//
//  1. 没有令牌 / 令牌无效 ⇒ 40100；
//  2. **令牌的项目必须与请求里的 project_id 一致**（决策 1 的全部意义）；
//  3. 权限只有 run:read 的令牌不能触发。
//
// "真的触发一次执行"依赖 hrp 二进制，交给 scripts/smoke-ci.mjs 端到端测。

// issueToken 直接走 service 签一枚令牌，返回明文。
func (e *testEnv) issueToken(t *testing.T, projectID uint64, name, scope string) string {
	t.Helper()
	issued, err := e.svcs.Token.Issue(projectID, service.CreateTokenReq{Name: name, Scope: scope})
	if err != nil {
		t.Fatalf("签发令牌失败: %v", err)
	}
	return issued.Token
}

func (e *testEnv) seedProject(t *testing.T, code string) *model.Project {
	t.Helper()
	p := &model.Project{Code: code, Name: "项目" + code, HrpVersion: "v4.3.6"}
	if err := e.db.Create(p).Error; err != nil {
		t.Fatalf("创建项目失败: %v", err)
	}
	return p
}

// openJSON 打一个 /open 端点，带上令牌。
func openJSON(t *testing.T, r *gin.Engine, method, path string, body any, token string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码请求体失败: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func bodyCode(t *testing.T, out map[string]any) int {
	t.Helper()
	v, ok := out["code"]
	if !ok {
		t.Fatalf("响应里没有 code: %v", out)
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("code 不是数字: %v", v)
	}
	return int(n)
}

func Test开放接口_没有令牌一律拒绝(t *testing.T) {
	e := newTestEnv(t)
	p := e.seedProject(t, "ci1")

	// 三种"没带令牌"的形态都要拒：完全没有头、空 Bearer、自定义头也是空
	for _, tk := range []string{"", "   "} {
		code, out := openJSON(t, e.r, http.MethodPost, "/open/runs",
			map[string]any{"project_id": p.ID, "target_type": "suite", "target_id": 1}, tk)
		if code != http.StatusUnauthorized {
			t.Errorf("HTTP = %d, want 401", code)
		}
		if bodyCode(t, out) != 40100 {
			t.Errorf("code = %v, want 40100", out["code"])
		}
	}
}

func Test开放接口_自定义头也能带令牌(t *testing.T) {
	e := newTestEnv(t)
	p := e.seedProject(t, "ci2")
	token := e.issueToken(t, p.ID, "流水线", service.DefaultTokenScope)

	// X-HRP-Token 是给"Authorization 被 CI 自己占用"的场景留的后门
	req := httptest.NewRequest(http.MethodPost, "/open/runs",
		bytes.NewBufferString(`{"project_id":`+itoa(p.ID)+`,"target_type":"suite","target_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HRP-Token", token)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)

	// 用例集 1 不存在 ⇒ 40002；关键是它**通过了鉴权**（不是 40100）
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if bodyCode(t, out) == 40100 {
		t.Errorf("X-HRP-Token 没有被识别：%v", out)
	}
	if bodyCode(t, out) != 40002 {
		t.Errorf("code = %v, want 40002（鉴权已过，是用例集不存在）", out["code"])
	}
}

// ⭐ 这是"按项目签发"的全部意义：不校验的话，一个项目的 token
// 能触发任意项目的执行，按项目签发等于白做。
func Test开放接口_令牌不能触发别的项目(t *testing.T) {
	e := newTestEnv(t)
	p1 := e.seedProject(t, "ci3a")
	p2 := e.seedProject(t, "ci3b")
	token := e.issueToken(t, p1.ID, "流水线", service.DefaultTokenScope)

	_, out := openJSON(t, e.r, http.MethodPost, "/open/runs",
		map[string]any{"project_id": p2.ID, "target_type": "suite", "target_id": 1}, token)
	if bodyCode(t, out) != 40300 {
		t.Errorf("code = %v, want 40300（跨项目触发必须拦住）", out["code"])
	}

	// 不传 project_id 也要拦住（不能默认成令牌所属项目）
	_, out = openJSON(t, e.r, http.MethodPost, "/open/runs",
		map[string]any{"target_type": "suite", "target_id": 1}, token)
	if bodyCode(t, out) != 40000 {
		t.Errorf("缺 project_id 的 code = %v, want 40000", out["code"])
	}
}

func Test开放接口_只读令牌不能触发(t *testing.T) {
	e := newTestEnv(t)
	p := e.seedProject(t, "ci4")
	readOnly := e.issueToken(t, p.ID, "只读", service.ScopeRunRead)

	_, out := openJSON(t, e.r, http.MethodPost, "/open/runs",
		map[string]any{"project_id": p.ID, "target_type": "suite", "target_id": 1}, readOnly)
	if bodyCode(t, out) != 40300 {
		t.Errorf("code = %v, want 40300（只读令牌不该能触发）", out["code"])
	}

	// 反过来：只有触发权限的令牌不该能读结果
	triggerOnly := e.issueToken(t, p.ID, "只触发", service.ScopeRunTrigger)
	_, out = openJSON(t, e.r, http.MethodGet, "/open/runs/1/result", nil, triggerOnly)
	if bodyCode(t, out) != 40300 {
		t.Errorf("code = %v, want 40300（只触发令牌不该能读结果）", out["code"])
	}
}

func Test开放接口_读结果也要校验项目(t *testing.T) {
	e := newTestEnv(t)
	p1 := e.seedProject(t, "ci5a")
	p2 := e.seedProject(t, "ci5b")
	token := e.issueToken(t, p1.ID, "流水线", service.DefaultTokenScope)

	// 造一条属于 p2 的执行记录
	run := &model.RunRecord{ProjectID: p2.ID, TargetType: model.TargetSuite, Status: model.RunSuccess}
	if err := e.db.Create(run).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	_, out := openJSON(t, e.r, http.MethodGet, "/open/runs/"+itoa(run.ID)+"/result", nil, token)
	if bodyCode(t, out) != 40300 {
		t.Errorf("code = %v, want 40300（按项目签发要同时管住读）", out["code"])
	}

	// 本项目的执行应该读得到
	own := &model.RunRecord{ProjectID: p1.ID, TargetType: model.TargetSuite, Status: model.RunSuccess,
		Total: 3, Passed: 3}
	if err := e.db.Create(own).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	_, out = openJSON(t, e.r, http.MethodGet, "/open/runs/"+itoa(own.ID)+"/result", nil, token)
	if bodyCode(t, out) != 0 {
		t.Fatalf("读自己的执行失败: %v", out)
	}
	data := out["data"].(map[string]any)
	if data["exit_code_for_ci"].(float64) != 0 {
		t.Errorf("exit_code_for_ci = %v, want 0（成功就该是 0）", data["exit_code_for_ci"])
	}
}

// ⭐ pipeline 只需要一个数字。不给的话每条流水线都要写一段 jq 去解析
// status 枚举，而且每个人写的判断都不一样（有人会把 error 当成功）。
func Test开放接口_给CI一个能直接判成败的数字(t *testing.T) {
	e := newTestEnv(t)
	p := e.seedProject(t, "ci6")
	token := e.issueToken(t, p.ID, "流水线", service.DefaultTokenScope)

	cases := []struct {
		status string
		want   float64
	}{
		{model.RunSuccess, 0},  // 成功
		{model.RunFailed, 1},   // 有用例没过
		{model.RunError, 1},    // 执行本身出错
		{model.RunCanceled, 2}, // 没跑完
		{model.RunRunning, 2},  // 还在跑（绝不能当成成功）
		{model.RunQueued, 2},
	}
	for _, c := range cases {
		run := &model.RunRecord{ProjectID: p.ID, TargetType: model.TargetSuite, Status: c.status}
		if err := e.db.Create(run).Error; err != nil {
			t.Fatalf("创建执行记录失败: %v", err)
		}
		_, out := openJSON(t, e.r, http.MethodGet, "/open/runs/"+itoa(run.ID)+"/result", nil, token)
		if bodyCode(t, out) != 0 {
			t.Fatalf("读取失败: %v", out)
		}
		got := out["data"].(map[string]any)["exit_code_for_ci"].(float64)
		if got != c.want {
			t.Errorf("status=%s 的 exit_code_for_ci = %v, want %v", c.status, got, c.want)
		}
	}
}

func Test开放接口_失败用例清单(t *testing.T) {
	e := newTestEnv(t)
	p := e.seedProject(t, "ci7")
	token := e.issueToken(t, p.ID, "流水线", service.DefaultTokenScope)

	run := &model.RunRecord{ProjectID: p.ID, TargetType: model.TargetSuite,
		Status: model.RunFailed, Total: 2, Passed: 1, Failed: 1}
	if err := e.db.Create(run).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	for _, cr := range []model.CaseResult{
		{RunID: run.ID, CaseCode: "ok1", Seq: 1, Status: model.StatusPass},
		{RunID: run.ID, CaseCode: "bad1", Seq: 2, Status: model.StatusFail,
			Attribution: model.AttrSystemUnderTest, ErrorMsg: "断言未通过"},
	} {
		if err := e.db.Create(&cr).Error; err != nil {
			t.Fatalf("创建用例结果失败: %v", err)
		}
	}

	_, out := openJSON(t, e.r, http.MethodGet, "/open/runs/"+itoa(run.ID)+"/result", nil, token)
	data := out["data"].(map[string]any)
	list, ok := data["failed_cases"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("failed_cases = %v, want 1 条", data["failed_cases"])
	}
	first := list[0].(map[string]any)
	if first["case_code"] != "bad1" {
		t.Errorf("case_code = %v, want bad1", first["case_code"])
	}
	if first["attribution"] != model.AttrSystemUnderTest {
		t.Errorf("attribution = %v, want system_under_test", first["attribution"])
	}
}

func Test开放接口_过期的令牌不能用(t *testing.T) {
	e := newTestEnv(t)
	p := e.seedProject(t, "ci8")
	token := e.issueToken(t, p.ID, "会过期", service.DefaultTokenScope)

	// 直接把库里的过期时间改到过去
	if err := e.db.Model(&model.APIToken{}).Where("project_id = ?", p.ID).
		Update("expire_at", timePast()).Error; err != nil {
		t.Fatalf("改过期时间失败: %v", err)
	}
	_, out := openJSON(t, e.r, http.MethodGet, "/open/runs/1/result", nil, token)
	if bodyCode(t, out) != 40100 {
		t.Errorf("code = %v, want 40100（过期令牌）", out["code"])
	}
}

// timePast 返回一个已经过去的时间点，用于把令牌改成"已过期"。
func timePast() time.Time { return time.Now().Add(-time.Hour) }
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
