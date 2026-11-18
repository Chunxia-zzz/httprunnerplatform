package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/compiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/executor"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/parser"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/hrpclient"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// DebugService 提供单步调试能力（M3）。
//
// 核心事实（实测 A2）：hrp 的最小执行单位是「一个用例文件」，
// **没有"只跑第 N 步"的开关**。因此单步调试的实现是：
//
//	平台临时构造一个"只含第 1..k 步"的最小用例 → 编译成 YAML →
//	在独立临时工作区里跑一个子进程 → 解析结果 → 跑完即弃。
//
// 与正式执行（RunService）的关键差异：
//
//  1. **不落库**：调试不进执行历史，不留 RunRecord，跑完即弃。
//  2. **同步返回**：调试是交互式的，必须阻塞等待结果（默认 30s 兜底）。
//  3. **前置步骤真实执行**（方案 A）：用户最想调试的往往是"依赖上一步
//     提取 token"的步骤。只跑目标步骤会让 $token 变成空值、断言必失败，
//     等于调试了个寂寞。所以默认把 1..k-1 步**真实执行一遍**拿真实变量值，
//     前端如实标注"本次实际执行了前 k 步"。
type DebugService struct {
	Deps
}

// DebugStepRequest 是一次单步调试的请求。
type DebugStepRequest struct {
	ProjectID uint64 `json:"project_id"`
	CaseID    uint64 `json:"case_id"`
	// StepSeq 是要调试的目标步骤的 seq（数据库里的原始序号）。
	// 平台会取「启用的、seq ≤ StepSeq」的步骤前缀构造最小用例，
	// 即真实执行第 1..k 步（含目标步）。
	StepSeq int `json:"step_seq"`
	// EnvID 为 0 时回退到项目默认环境（与正式执行一致）。
	EnvID uint64 `json:"env_id"`
	// TimeoutSec 是同步阻塞上限（秒）。0 表示用平台默认 30s。
	TimeoutSec int `json:"timeout_sec"`
}

// DebugStepResult 是一次单步调试的结构化结果。
//
// 字段刻意对齐 parser.Result + StepOutcome：前端调试面板可以直接复用
// 执行详情页的步骤渲染逻辑，不必为调试另写一套视图。
type DebugStepResult struct {
	// Status 是整条「最小用例」的终态：pass / fail / error。
	Status string `json:"status"`
	// Attribution 是归因（与正式执行同款中文标签）。
	Attribution string `json:"attribution"`
	// ErrorMsg 是给用户看的错误摘要。
	ErrorMsg string `json:"error_msg"`
	// Panic 表示引擎在断言阶段崩溃（F8），断言明细由平台重建。
	Panic bool `json:"panic"`
	// DurationMs 是墙钟耗时。
	DurationMs int64 `json:"duration_ms"`

	// Steps 是实际执行过的步骤结果（含变量值、请求/响应、断言明细）。
	Steps []DebugStep `json:"steps"`

	// TargetSeq 是用户要调试的步骤 seq，便于前端高亮目标步。
	TargetSeq int `json:"target_seq"`
	// ActualStepCount 是实际执行的步骤数（= 前缀长度）。
	ActualStepCount int `json:"actual_step_count"`
	// CompileError 非空表示临时用例编译失败（通常是不能定位到目标步骤）。
	CompileError string `json:"compile_error,omitempty"`
}

// DebugStep 是单步调试里一个步骤的结果。
type DebugStep struct {
	Seq      int    `json:"seq"`
	Name     string `json:"name"`
	StepType string `json:"step_type"`
	Status   string `json:"status"`
	// IsTarget 标记这一条是否就是用户要调试的目标步骤。
	IsTarget bool `json:"is_target"`

	ElapsedMs     int64          `json:"elapsed_ms"`
	ExtractResult map[string]any `json:"extract_result,omitempty"`

	FinalURL       string `json:"final_url,omitempty"`
	FinalURLSource string `json:"final_url_source,omitempty"`

	Request  *parser.RequestSnapshot  `json:"request,omitempty"`
	Response *parser.ResponseSnapshot `json:"response,omitempty"`

	Assertions []parser.AssertionOutcome `json:"assertions,omitempty"`
	ErrorMsg   string                    `json:"error_msg,omitempty"`
}

// DefaultDebugTimeout 是单步调试的默认同步阻塞上限。
//
// 取值理由：本地调试一个正常用例 1~3 秒，给 30 秒已经覆盖慢接口。
// 调试是交互式的，用户盯着页面等，超时必须给个明确结果而不是一直转圈。
const DefaultDebugTimeout = 30 * time.Second

// MaxDebugTimeout 是调试超时的硬上限，防止前端传一个把 goroutine 挂住的值。
const MaxDebugTimeout = 5 * time.Minute

// DebugStep 执行单步调试。
//
// 返回值永远是"能展示给用户看的结果"，不会因为断言失败而返回 error——
// 断言失败是调试的正常结果，写在 DebugStepResult.Status 里。
// 只有"平台侧无法完成调试"（用例不存在、找不到目标步骤、引擎不可用）才返回 error。
func (s *DebugService) DebugStep(ctx context.Context, req DebugStepRequest) (*DebugStepResult, error) {
	if req.ProjectID == 0 || req.CaseID == 0 {
		return nil, errBadParam("project_id 与 case_id 不能为空")
	}
	if req.StepSeq <= 0 {
		return nil, errBadParam("step_seq 必须为正整数（要调试的步骤序号）")
	}

	project, err := loadProject(s.DB, req.ProjectID)
	if err != nil {
		return nil, err
	}
	tc, err := loadCase(s.DB, req.CaseID)
	if err != nil {
		return nil, err
	}
	if tc.ProjectID != project.ID {
		return nil, errBadParam("用例 %q 不属于项目 %q", tc.Code, project.Code)
	}

	steps, err := caseSteps(s.DB, tc.ID)
	if err != nil {
		return nil, err
	}

	// 只取启用的步骤（与正式执行同一口径：compiler 也只会渲染 enabled 的）。
	enabled := enabledStepsForDebug(steps)
	if len(enabled) == 0 {
		return nil, errBadParam("用例 %q 没有任何启用的步骤，无法调试", tc.Code)
	}

	// 定位目标步骤：在启用步骤里找 seq == StepSeq 的那一条。
	targetIdx := -1
	for i, st := range enabled {
		if st.Seq == req.StepSeq {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return nil, errBadParam(
			"找不到 seq=%d 的启用步骤（用例 %q 的启用步骤 seq 为 %s）",
			req.StepSeq, tc.Code, stepSeqs(enabled))
	}

	// 前缀 = 第 1..k 步（含目标步）。
	prefix := enabled[:targetIdx+1]

	env, err := resolveEnv(s.DB, project.ID, req.EnvID)
	if err != nil {
		return nil, err
	}

	return s.runSyntheticCase(ctx, project, env, tc, prefix, req.TimeoutSec, req.StepSeq)
}

// runSyntheticCase 执行一个「不落库的合成用例」并返回结构化结果。
//
// 单步调试（DebugStep）与调试台（DebugRequest）共用这条管线：
// 构造临时用例 → 编译 → 独立临时工作区 → 跑一个子进程 → 解析 → 跑完即弃。
// 抽出来是为了避免两套实现漂移——调试台和单步调试对同一个请求必须给出同样的结果。
func (s *DebugService) runSyntheticCase(
	ctx context.Context,
	project *model.Project,
	env *model.Environment,
	tc *model.TestCase,
	steps []model.TestStep,
	timeoutSec int,
	targetSeq int,
) (*DebugStepResult, error) { // 编译临时最小用例。config.name 加后缀，避免与项目里其它用例的
	// summary.json 标识串味（虽然工作区隔离，稳妥起见）。
	debugCase := *tc
	debugCase.Name = tc.Name + "【调试】"
	datasets, err := loadDatasets(s.DB, project.ID)
	if err != nil {
		return nil, err
	}
	apis, err := loadAPIs(s.DB, project.ID)
	if err != nil {
		return nil, err
	}
	// db 步骤（R1）：调试与正式执行走同一回环端点，因此也要登记临时通道
	// 并把 token 写进调试 .env——否则合成用例里的 db 步骤会 401。
	dbToken, releaseDBToken := s.DBStepTokens.RegisterEphemeral(project.ID)
	defer releaseDBToken() // 合成用例跑完即关，不留活口
	comp, err := compiler.Render(&compiler.Input{
		Project:       project,
		Env:           env,
		Cases:         []compiler.CaseSpec{{Case: &debugCase, Steps: steps}},
		WorkspaceRoot: projectWorkspace(s.Cfg, project),
		Datasets:      datasets,
		APIs:          apis,
		LoopbackURL:   loopbackURLFor(s.Cfg),
	})
	if err != nil {
		return &DebugStepResult{
			Status:       model.StatusError,
			CompileError: err.Error(),
		}, nil
	}
	if len(comp.Cases) != 1 {
		return nil, errInternal("单步调试编译产物异常", fmt.Errorf("期望 1 条用例，得到 %d 条", len(comp.Cases)))
	}
	cc := comp.Cases[0]

	// 临时工作区：复用 executor.Workspace，但根目录独立于正式运行。
	// 用纳秒级后缀避免同一秒内多次调试互相覆盖（A5）。
	tmpRoot := filepath.Join(s.Cfg.RuntimeDir(), "debug",
		strconv.FormatUint(tc.ID, 10)+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	ws, err := executor.NewWorkspace(tmpRoot)
	if err != nil {
		return nil, errInternal("创建调试工作区失败", err)
	}
	defer os.RemoveAll(tmpRoot) // 跑完即弃

	// 直接写 .env 与用例文件，不走 CopyFrom（CopyFrom 是从项目工作区复制，
	// 而调试的临时用例不在项目工作区里）。
	if err := os.MkdirAll(ws.TestcasesDir(), 0o755); err != nil {
		return nil, errInternal("创建调试用例目录失败", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), ".env"),
		[]byte(comp.EnvText+"dbstep_token="+dbToken+"\n"), 0o644); err != nil {
		return nil, errInternal("写调试 .env 失败", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), filepath.FromSlash(cc.FileName)), []byte(cc.YAML), 0o644); err != nil {
		return nil, errInternal("写调试用例文件失败", err)
	}
	// 参数化派生文件（limit 裁剪版 CSV 等）。
	for rel, content := range cc.ExtraFiles {
		p := filepath.Join(ws.Root(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, errInternal("创建调试数据目录失败", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return nil, errInternal("写调试参数化文件失败", err)
		}
	}
	if err := ws.PrepareForCase(); err != nil {
		return nil, errInternal("清理调试工作区失败", err)
	}

	bin, err := hrpclient.Resolve(s.Cfg.Engine.BinaryPath)
	if err != nil {
		return nil, errInternal("引擎不可用", err)
	}

	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		timeout = DefaultDebugTimeout
	}
	if timeout > MaxDebugTimeout {
		timeout = MaxDebugTimeout
	}

	outcome, err := executor.RunCase(ctx, executor.Options{
		BinaryPath:    bin,
		WorkspaceDir:  ws.Root(),
		CaseFile:      cc.FileName,
		Timeout:       timeout,
		GenHTMLReport: false, // 调试不需要 report.html
		// 请求/响应明细必须保留：断言失败时引擎不产出 summary.json，
		// stdout 报文快照是唯一线索（F8）。
		DisableRequestsLog: false,
		DisableTelemetry:   s.Cfg.Engine.DisableTelemetry,
	})
	if err != nil {
		return nil, errInternal("调试执行失败", err)
	}

	res := parser.Parse(parser.Input{
		ExitCode:     outcome.ExitCode,
		Stdout:       outcome.Stdout,
		Stderr:       outcome.Stderr,
		TimedOut:     outcome.TimedOut,
		Canceled:     outcome.Canceled,
		EnabledSteps: parserDecls(cc.Steps),
		SummaryJSON:  outcome.SummaryJSON,
		DurationMs:   outcome.DurationMs,
	})

	// 组装结果：把 parser 的 StepOutcome 映射成 DebugStep，并标记目标步。
	debugSteps := make([]DebugStep, 0, len(res.Steps))
	for _, so := range res.Steps {
		debugSteps = append(debugSteps, DebugStep{
			Seq:            so.Seq,
			Name:           so.Name,
			StepType:       so.StepType,
			Status:         so.Status,
			IsTarget:       so.Seq == targetSeq,
			ElapsedMs:      so.ElapsedMs,
			ExtractResult:  so.ExtractResult,
			FinalURL:       so.FinalURL,
			FinalURLSource: so.FinalURLSource,
			Request:        so.Request,
			Response:       so.Response,
			Assertions:     so.Assertions,
			ErrorMsg:       so.ErrorMsg,
		})
	}

	return &DebugStepResult{
		Status:          res.Status,
		Attribution:     res.Attribution,
		ErrorMsg:        res.ErrorMsg,
		Panic:           res.Panic,
		DurationMs:      outcome.DurationMs,
		Steps:           debugSteps,
		TargetSeq:       targetSeq,
		ActualStepCount: len(steps),
	}, nil
}

// DebugRequestReq 是调试台的 ad-hoc 请求（F3）。
//
// 与 DebugStepRequest 的区别：没有已保存的用例，请求内容直接来自页面。
type DebugRequestReq struct {
	ProjectID uint64 `json:"project_id"`
	// EnvID 为 0 时回退到项目默认环境（与正式执行一致）。
	EnvID uint64 `json:"env_id"`
	// Request 就是页面填的请求；$base_url 与环境变量由编译器按现有规则渲染。
	Request model.RequestSpec `json:"request"`
	// TimeoutSec 是同步阻塞上限（秒）。0 表示用平台默认 30s。
	TimeoutSec int `json:"timeout_sec"`
}

// DebugRequest 执行一次「不落库」的临时请求（F3 调试台）。
//
// 语义与 DebugStep 完全一致：不产生 run_record / case_result，临时工作区跑完即弃。
// 实现上合成一个单步骤用例走同一条管线 —— hrp 的最小执行单位是「一个用例文件」
// （实测 A2），没有"直接发一个请求"的引擎入口。
func (s *DebugService) DebugRequest(ctx context.Context, req DebugRequestReq) (*DebugStepResult, error) {
	if req.ProjectID == 0 {
		return nil, errBadParam("project_id 不能为空")
	}
	project, err := loadProject(s.DB, req.ProjectID)
	if err != nil {
		return nil, err
	}

	r := req.Request
	r.URL = strings.TrimSpace(r.URL)
	if r.URL == "" {
		return nil, errBadParam("请求地址不能为空")
	}
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method == "" {
		r.Method = "GET"
	}
	if !containsMethod(r.Method) {
		return nil, errBadParam("请求方法 %q 不受支持", r.Method)
	}
	r.BodyType = strings.ToLower(strings.TrimSpace(r.BodyType))
	switch r.BodyType {
	case "":
		r.BodyType = model.BodyTypeNone
	case model.BodyTypeNone, model.BodyTypeJSON, model.BodyTypeForm, model.BodyTypeRaw:
	default:
		return nil, errBadParam("请求体类型只支持 json / form / raw / none，收到 %q", r.BodyType)
	}

	env, err := resolveEnv(s.DB, project.ID, req.EnvID)
	if err != nil {
		return nil, err
	}

	// 合成用例：不落库，Code 用 adhoc 只为日志可读（不参与文件名冲突判断，
	// 因为临时工作区是独立的）。
	adhoc := &model.TestCase{
		ProjectID: project.ID,
		Code:      "adhoc",
		Name:      "临时请求",
		Priority:  model.PriorityP0,
		Status:    model.CaseStatusActive,
	}
	step := model.TestStep{
		Seq:      1,
		StepType: model.StepRequest,
		Name:     "临时请求",
		Request:  jsonx.Any{Val: r},
		Enabled:  true,
		Extract:  jsonx.Slice[model.ExtractItem]{},
		Validate: jsonx.Slice[model.AssertItem]{},
	}
	return s.runSyntheticCase(ctx, project, env, adhoc, []model.TestStep{step}, req.TimeoutSec, 1)
}

// enabledStepsForDebug 过滤出启用的步骤，并按 seq 升序排列。
//
// 与 compiler 的 enabledSteps 同口径，但这里直接复用 service 层已加载的
// 步骤切片（避免再 import compiler 的内部过滤函数）。
func enabledStepsForDebug(steps []model.TestStep) []model.TestStep {
	out := make([]model.TestStep, 0, len(steps))
	for _, st := range steps {
		if st.Enabled {
			out = append(out, st)
		}
	}
	// 已是 seq 升序（caseSteps 保证），这里再做一次防御性稳定排序。
	return sortStepsBySeq(out)
}

// sortStepsBySeq 按 seq 升序稳定排序（seq 相同按 id）。
func sortStepsBySeq(steps []model.TestStep) []model.TestStep {
	// 插入排序：步骤数量通常很小（< 20），且已经是近似有序的。
	for i := 1; i < len(steps); i++ {
		for j := i; j > 0 && stepLess(steps[j], steps[j-1]); j-- {
			steps[j], steps[j-1] = steps[j-1], steps[j]
		}
	}
	return steps
}

func stepLess(a, b model.TestStep) bool {
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	return a.ID < b.ID
}

// stepSeqs 把步骤的 seq 拼成人类可读的列表，用于报错提示。
func stepSeqs(steps []model.TestStep) string {
	parts := make([]string, 0, len(steps))
	for _, st := range steps {
		parts = append(parts, strconv.Itoa(st.Seq))
	}
	return strings.Join(parts, ", ")
}
