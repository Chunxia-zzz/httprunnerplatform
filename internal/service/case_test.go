package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/validator"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

func wantCode(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望失败（code=%d），实际成功", code)
	}
	var bizErr *response.Error
	if !errors.As(err, &bizErr) {
		t.Fatalf("期望业务错误，实际 %T: %v", err, err)
	}
	if bizErr.Code != code {
		t.Fatalf("错误码 = %d, want %d（%v）", bizErr.Code, code, bizErr)
	}
}

// TestCase_保存侧的结构性校验 覆盖三个入口闸门。
func TestCase_保存侧的结构性校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Case

	// code 非法：它是编译后的文件名片段，必须在入口严格限制字符集
	_, err := svc.Create(p.ID, CaseReq{Code: "../escape", Name: "越权"}, 1)
	wantCode(t, err, response.CodeInvalidIdent)

	// 名称为空
	_, err = svc.Create(p.ID, CaseReq{Code: "tc_x", Name: "  "}, 1)
	wantCode(t, err, response.CodeBadParam)

	mustCase(t, d, p.ID, simpleCaseReq("tc_login", "登录成功", "/login"))

	// 重名：引擎以 config.name 作 summary.json 的唯一标识（实测 F11）
	_, err = svc.Create(p.ID, simpleCaseReq("tc_login2", "登录成功", "/login"), 1)
	wantCode(t, err, response.CodeConflict)

	// code 重复
	_, err = svc.Create(p.ID, simpleCaseReq("tc_login", "另一个名字", "/login"), 1)
	wantCode(t, err, response.CodeConflict)
}

// TestCase_更新时code不可改 code 决定编译后的文件名与历史结果的可追溯性。
func TestCase_更新时code不可改(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	detail := mustCase(t, d, p.ID, simpleCaseReq("tc_login", "登录成功", "/login"))

	req := simpleCaseReq("tc_other", "登录成功", "/login")
	_, err := New(d).Case.Update(detail.ID, req)
	wantCode(t, err, response.CodeBadParam)

	// 不传 code（编辑器只改别的字段）应当被接受
	req = simpleCaseReq("", "登录成功（改名）", "/login")
	req.Code = ""
	if _, err := New(d).Case.Update(detail.ID, req); err != nil {
		t.Fatalf("只改名称时不应要求带 code: %v", err)
	}
}

// TestCase_步骤seq归一化 保证"数组顺序 = 执行顺序"这个直觉永远成立。
func TestCase_步骤seq归一化(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	req := CaseReq{
		Code: "tc_seq", Name: "步骤排序",
		Steps: []StepReq{
			{Seq: 30, StepType: model.StepRequest, Name: "第三个", Request: reqAny(map[string]any{"url": "$base_url/c"})},
			{Seq: 10, StepType: model.StepRequest, Name: "第一个", Request: reqAny(map[string]any{"url": "$base_url/a"})},
			{Seq: 20, StepType: model.StepRequest, Name: "第二个", Request: reqAny(map[string]any{"url": "$base_url/b"})},
		},
	}
	detail := mustCase(t, d, p.ID, req)

	if len(detail.Steps) != 3 {
		t.Fatalf("步骤数 = %d, want 3", len(detail.Steps))
	}
	wantOrder := []string{"第一个", "第二个", "第三个"}
	for i, st := range detail.Steps {
		if st.Seq != i+1 {
			t.Errorf("第 %d 个步骤 seq = %d, want %d（应重编号为 1..n）", i, st.Seq, i+1)
		}
		if st.Name != wantOrder[i] {
			t.Errorf("第 %d 个步骤 = %q, want %q", i, st.Name, wantOrder[i])
		}
	}

	// 重新读取：落库顺序必须与返回一致（List/Get 走的是 ORDER BY seq）
	got, err := New(d).Case.Get(detail.ID)
	if err != nil {
		t.Fatalf("读取用例失败: %v", err)
	}
	for i, st := range got.Steps {
		if st.Seq != i+1 || st.Name != wantOrder[i] {
			t.Errorf("落库后第 %d 步骤 = (seq=%d, %q)", i, st.Seq, st.Name)
		}
	}
}

// TestCase_更新是全量替换步骤 步骤留空即清空，与契约一致。
func TestCase_更新是全量替换步骤(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	detail := mustCase(t, d, p.ID, simpleCaseReq("tc_replace", "替换步骤", "/a"))

	svc := New(d).Case
	req := simpleCaseReq("", "替换步骤", "/b")
	if _, err := svc.Update(detail.ID, req); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	got, err := svc.Get(detail.ID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("步骤数 = %d, want 1（旧步骤必须被替换而不是追加）", len(got.Steps))
	}
	if got := reqURL(t, got.Steps[0].Request); got != "$base_url/b" {
		t.Errorf("步骤未被替换，url = %q", got)
	}

	// 不传 steps ⇒ 视为无步骤（全量覆盖语义）
	empty := CaseReq{Name: "替换步骤"}
	if _, err := svc.Update(detail.ID, empty); err != nil {
		t.Fatalf("清空步骤失败: %v", err)
	}
	got, _ = svc.Get(detail.ID)
	if len(got.Steps) != 0 {
		t.Errorf("不传 steps 时应清空，实际剩 %d 步", len(got.Steps))
	}
}

// TestCase_YAML预览路径与执行一致 契约要求预览与执行逐字节相同。
func TestCase_YAML预览路径与执行一致(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	detail := mustCase(t, d, p.ID, simpleCaseReq("tc_preview", "预览用例", "/get"))

	preview, err := New(d).Case.RenderYAML(detail.ID, 0)
	if err != nil {
		t.Fatalf("渲染 YAML 失败: %v", err)
	}

	// 文件名必须与执行时写盘、以及传给引擎的相对路径一致
	if preview.FileName != "testcases/tc_preview.yaml" {
		t.Errorf("filename = %q, want testcases/tc_preview.yaml", preview.FileName)
	}
	if !strings.HasPrefix(preview.YAML, "config:") {
		t.Errorf("YAML 应以 config 开头:\n%s", preview.YAML)
	}
	if !strings.Contains(preview.YAML, "预览用例") {
		t.Error("YAML 里应包含 config.name")
	}
	// 环境为空时也要给出可用的 base_url，否则预览里的相对地址无从解释
	if !strings.Contains(preview.Env, "base_url=") {
		t.Errorf(".env 预览缺少 base_url:\n%s", preview.Env)
	}
}

// TestCase_禁用步骤不进YAML 禁用是"暂时不跑"，不是"删掉"。
func TestCase_禁用步骤不进YAML(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	disabled := false
	detail := mustCase(t, d, p.ID, CaseReq{
		Code: "tc_disabled", Name: "禁用步骤",
		Steps: []StepReq{
			{Seq: 1, StepType: model.StepRequest, Name: "要跑的步骤", Request: reqAny(map[string]any{"url": "$base_url/keep"})},
			{Seq: 2, StepType: model.StepRequest, Name: "被禁用的步骤", Request: reqAny(map[string]any{"url": "$base_url/skip"}), Enabled: &disabled},
		},
	})

	// 步骤本身要保留（编辑器要能再打开）
	if len(detail.Steps) != 2 {
		t.Fatalf("步骤数 = %d, want 2（禁用步骤不应被删除）", len(detail.Steps))
	}

	preview, err := New(d).Case.RenderYAML(detail.ID, 0)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if !strings.Contains(preview.YAML, "要跑的步骤") {
		t.Error("启用步骤应出现在 YAML 中")
	}
	if strings.Contains(preview.YAML, "被禁用的步骤") || strings.Contains(preview.YAML, "/skip") {
		t.Errorf("禁用步骤不应进入 YAML:\n%s", preview.YAML)
	}

	// 列表上的步骤数只数启用步骤：显示 2 会让用户以为有 2 步会执行
	list, _, err := New(d).Case.List(p.ID, CaseListQuery{}, Page{})
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	for _, it := range list {
		if it.Code == "tc_disabled" && it.StepCount != 1 {
			t.Errorf("step_count = %d, want 1（只数启用步骤）", it.StepCount)
		}
	}
}

// TestCase_校验能发现静默失效的配置 这是 validator 存在于架构里的理由（实测 A7）。
func TestCase_校验能发现静默失效的配置(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Case

	// 1) 正常用例必须全绿：否则校验器的信噪比会崩掉，用户就不再看了
	good := mustCase(t, d, p.ID, simpleCaseReq("tc_ok", "正常用例", "/get"))
	out, err := svc.Validate(good.ID, 0)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !out.OK {
		t.Fatalf("正常用例应校验通过，实际 issues=%+v", out.Issues)
	}

	// 2) 引用了未定义的变量（对应引擎退出码 21）
	bad := CaseReq{
		Code: "tc_badvar", Name: "变量未定义",
		Steps: []StepReq{{
			Seq: 1, StepType: model.StepRequest, Name: "带未定义变量",
			Request: reqAny(map[string]any{
				"url":     "$base_url/get",
				"headers": map[string]any{"X-Token": "$this_var_does_not_exist"},
			}),
		}},
	}
	detail := mustCase(t, d, p.ID, bad)
	out, err = svc.Validate(detail.ID, 0)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if out.OK {
		t.Fatal("引用了未定义变量必须报错，否则用户会在运行期才发现")
	}
	if !hasIssueCode(out.Issues, validator.CodeUndefinedVariable) {
		t.Errorf("应报 UNDEFINED_VARIABLE，实际: %+v", out.Issues)
	}

	// 3) 缺少 url
	noURL := mustCase(t, d, p.ID, CaseReq{
		Code: "tc_nourl", Name: "没有地址",
		Steps: []StepReq{{
			Seq: 1, StepType: model.StepRequest, Name: "空地址",
			Request: reqAny(map[string]any{"method": "GET"}),
		}},
	})
	out, err = svc.Validate(noURL.ID, 0)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !hasIssueCode(out.Issues, validator.CodeMissingURL) {
		t.Errorf("应报 MISSING_URL，实际: %+v", out.Issues)
	}

	// 4) 一条启用的步骤都没有
	empty := mustCase(t, d, p.ID, CaseReq{Code: "tc_empty", Name: "空用例"})
	out, err = svc.Validate(empty.ID, 0)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !hasIssueCode(out.Issues, validator.CodeEmptySteps) {
		t.Errorf("应报 EMPTY_STEPS，实际: %+v", out.Issues)
	}
}

// TestCase_校验能发现跨用例重名 覆盖 SiblingCaseNames 这条输入。
//
// 保存侧已经拦了重名，但历史数据或直接改库都可能造出重名，
// 校验器必须能独立发现它（实测 F11 的兜底）。
func TestCase_校验能发现跨用例重名(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	a := mustCase(t, d, p.ID, simpleCaseReq("tc_a", "重名用例", "/a"))

	// 绕过保存侧的检查，直接改库制造重名
	if err := d.DB.Model(&model.TestCase{}).Where("id = ?", a.ID).
		Update("name", "重名用例").Error; err != nil {
		t.Fatalf("构造重名数据失败: %v", err)
	}
	b := mustCase(t, d, p.ID, simpleCaseReq("tc_b", "另一个名字", "/b"))
	// 再把它改成与 a 同名，模拟"历史脏数据"
	detail, err := New(d).Case.Get(b.ID)
	if err != nil {
		t.Fatalf("读取用例失败: %v", err)
	}
	req := CaseReq{Name: "重名用例"}
	req.Code = ""
	// Update 会被保存侧的重名检查挡住，因此这里直接改库
	if err := d.DB.Model(&model.TestCase{}).Where("id = ?", detail.ID).
		Update("name", "重名用例").Error; err != nil {
		t.Fatalf("构造重名数据失败: %v", err)
	}

	out, err := New(d).Case.Validate(b.ID, 0)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !hasIssueCode(out.Issues, validator.CodeDuplicateCaseName) {
		t.Errorf("应报 DUPLICATE_CASE_NAME，实际: %+v", out.Issues)
	}
}

// TestCase_删除被引用的用例被拒绝 避免用例集出现悬空引用。
func TestCase_删除被引用的用例被拒绝(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	detail := mustCase(t, d, p.ID, simpleCaseReq("tc_ref", "被引用的用例", "/a"))

	suite := model.TestSuite{ProjectID: p.ID, Code: "s1", Name: "冒烟"}
	if err := d.DB.Create(&suite).Error; err != nil {
		t.Fatalf("创建用例集失败: %v", err)
	}
	if err := d.DB.Create(&model.SuiteCase{SuiteID: suite.ID, CaseID: detail.ID, Seq: 1}).Error; err != nil {
		t.Fatalf("创建用例集成员失败: %v", err)
	}

	wantCode(t, New(d).Case.Delete(detail.ID), response.CodeInUse)
}

// hasIssueCode 判断 issues 里是否包含指定 code。
func hasIssueCode(issues []validator.Issue, code string) bool {
	for _, is := range issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

// reqURL 从步骤的 request 字段取出 url。
//
// 走一次 JSON 往返而不是直接断言 map 的键：
// 这条路径与 validator / compiler 实际读它的方式完全一致，
// 若 jsonx.Any 的编码出了问题，这里会一起暴露。
func reqURL(t *testing.T, a jsonx.Any) string {
	t.Helper()
	b, err := json.Marshal(a.Val)
	if err != nil {
		t.Fatalf("request 序列化失败: %v", err)
	}
	var rs model.RequestSpec
	if err := json.Unmarshal(b, &rs); err != nil {
		t.Fatalf("request 反序列化失败: %v", err)
	}
	return rs.URL
}
