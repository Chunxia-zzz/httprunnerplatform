package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/compiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/executor"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/parser"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/hrpclient"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// RunService 编排"一次执行"的完整生命周期。
//
// 执行是**异步**的：POST /runs 立刻返回 run_id，真正的编译—执行—解析—落库
// 在后台 goroutine 里完成，前端轮询详情。
//
// 整个编排顺序由实测结论决定，顺序不可调整：
//
//	编译（DB → 工作区）→ 复制到**本次运行专属**工作区（A5）→ 清空 results/
//	→ 执行单个用例（A2：一例一进程）→ 解析双流 → 归因 → 落库 → 用例数对账
//
// 其中"复制到专属工作区 + 清空 results"必须在执行前完成：
// 引擎产物按**秒级时间戳**落盘，共用目录会让并发运行互相覆盖。
type RunService struct {
	Deps

	mu      sync.Mutex
	running map[uint64]*runHandle
	sem     chan struct{}
	closed  bool
}

// runHandle 是在跑的执行句柄。
type runHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewRunService 构造运行服务。并发上限取自配置（engine.max_concurrency）。
func NewRunService(d Deps) *RunService {
	n := d.Cfg.Engine.MaxConcurrency
	if n <= 0 {
		n = 3
	}
	return &RunService{
		Deps:    d,
		running: make(map[uint64]*runHandle),
		sem:     make(chan struct{}, n),
	}
}

// ---------------------------------------------------------------------------
// 启动
// ---------------------------------------------------------------------------

// RunOptions 是执行选项。
type RunOptions struct {
	// CaseTimeout 是单用例超时（秒），0 表示按 用例设置 → 平台默认 逐级回退。
	CaseTimeout int `json:"case_timeout"`
	// GenHTMLReport 用指针以区分"没传"与"传了 false"。
	GenHTMLReport *bool `json:"gen_html_report"`
	// Concurrency 仅对 suite/plan 生效（M2），M1 忽略。
	Concurrency int `json:"concurrency"`
}

// StartRunRequest 是启动执行的请求。
type StartRunRequest struct {
	ProjectID  uint64     `json:"project_id"`
	TargetType string     `json:"target_type"`
	TargetID   uint64     `json:"target_id"`
	EnvID      uint64     `json:"env_id"`
	Options    RunOptions `json:"options"`
}

// runJob 是一次执行的不可变输入。
type runJob struct {
	runID   uint64
	project *model.Project
	tc      *model.TestCase
	env     *model.Environment
	timeout time.Duration
	genHTML bool
}

// Start 创建执行记录并异步启动。
func (s *RunService) Start(req StartRunRequest, triggerBy uint64) (*model.RunRecord, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, response.New(response.CodeExecutorFail, "服务正在关闭，暂不接受新的执行")
	}

	if req.TargetType == "" {
		req.TargetType = model.TargetCase
	}
	if req.TargetType != model.TargetCase {
		return nil, errBadParam(
			"M1 只支持 target_type=case（suite / plan 在 M2 提供），收到 %q", req.TargetType)
	}
	if req.ProjectID == 0 || req.TargetID == 0 {
		return nil, errBadParam("project_id 与 target_id 不能为空")
	}

	project, err := loadProject(s.DB, req.ProjectID)
	if err != nil {
		return nil, err
	}

	tc, err := loadCase(s.DB, req.TargetID)
	if err != nil {
		return nil, err
	}
	if tc.ProjectID != project.ID {
		return nil, errBadParam("用例 %q 不属于项目 %q", tc.Code, project.Code)
	}
	if tc.Status == model.CaseStatusDisabled {
		return nil, errBadParam("用例 %q 已被禁用，请先在用例设置中启用", tc.Name)
	}

	env, err := resolveEnv(s.DB, project.ID, req.EnvID)
	if err != nil {
		return nil, err
	}

	run := &model.RunRecord{
		ProjectID:   project.ID,
		TargetType:  model.TargetCase,
		TargetID:    tc.ID,
		TargetName:  tc.Name,
		TriggerType: model.TriggerManual,
		TriggerBy:   triggerBy,
		Status:      model.RunQueued,
		// 单用例执行，预期一定是 1 条；真正对账用的是编译产物的数量，
		// 这里先记 1，收尾时会被编译结果覆盖。
		ExpectedCaseCount: 1,
		Attribution:       jsonx.Map{},
	}
	if env != nil {
		run.EnvID = env.ID
	}

	if err := s.DB.Create(run).Error; err != nil {
		return nil, errInternal("创建执行记录失败", err)
	}

	job := &runJob{
		runID:   run.ID,
		project: project,
		tc:      tc,
		env:     env,
		timeout: s.resolveTimeout(req.Options.CaseTimeout, tc),
		genHTML: boolOr(s.Cfg.Engine.GenHTMLReport, req.Options.GenHTMLReport),
	}

	// ctx 刻意不继承 HTTP 请求的 ctx：执行是后台任务，
	// 用户关掉浏览器不应该让正在跑的用例被杀掉。
	ctx, cancel := context.WithCancel(context.Background())
	s.register(run.ID, &runHandle{cancel: cancel, done: make(chan struct{})})

	logx.L().Info().
		Uint64("run_id", run.ID).
		Str("case", tc.Code).
		Int("timeout_sec", int(job.timeout.Seconds())).
		Msg("执行已入队")

	go s.execute(ctx, job)

	return run, nil
}

// resolveTimeout 逐级回退解析单用例超时。
//
// 优先级：本次请求选项 → 用例自身设置 → 平台配置默认值 → 执行器默认。
func (s *RunService) resolveTimeout(reqSec int, tc *model.TestCase) time.Duration {
	sec := reqSec
	if sec <= 0 {
		sec = tc.CaseTimeout
	}
	if sec <= 0 {
		sec = s.Cfg.Engine.DefaultCaseTimeout
	}
	if sec <= 0 {
		return executor.DefaultTimeout
	}
	return time.Duration(sec) * time.Second
}

// ---------------------------------------------------------------------------
// 执行主体
// ---------------------------------------------------------------------------

func (s *RunService) execute(ctx context.Context, job *runJob) {
	defer s.unregister(job.runID)

	// 并发闸门：先抢槽位再做别的事，避免"编译完了才发现要排队"。
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		s.finish(job, "执行在排队期间被终止", model.RunCanceled, time.Time{})
		return
	}

	started := time.Now()
	if err := s.patchRun(job.runID, map[string]any{
		"status":     model.RunRunning,
		"started_at": &started,
	}); err != nil {
		logx.L().Error().Err(err).Uint64("run_id", job.runID).Msg("更新执行状态失败")
	}

	// 1) 编译：DB → 项目工作区。
	//    只编译目标用例，不编译整个项目 —— 邻居用例写坏了不应该
	//    把这条用例的执行也一起挡住。
	steps, err := caseSteps(s.DB, job.tc.ID)
	if err != nil {
		s.finish(job, err.Error(), model.RunError, started)
		return
	}
	projectWS := projectWorkspace(s.Cfg, job.project)
	comp, err := compiler.Compile(&compiler.Input{
		Project: job.project,
		Env:     job.env,
		Cases:   []compiler.CaseSpec{{Case: job.tc, Steps: steps}},
	}, projectWS)
	if err != nil {
		s.finish(job, "编译失败："+err.Error(), model.RunError, started)
		return
	}
	cc := comp.Cases[0]

	// 2) 本次运行专属工作区（实测 A5）。
	runRoot := filepath.Join(s.Cfg.RunsDir(), strconv.FormatUint(job.runID, 10))
	ws, err := executor.NewWorkspace(runRoot)
	if err != nil {
		s.finish(job, "创建工作区失败："+err.Error(), model.RunError, started)
		return
	}
	if err := ws.CopyFrom(projectWS); err != nil {
		s.finish(job, "复制工作区失败："+err.Error(), model.RunError, started)
		return
	}
	if err := ws.PrepareForCase(); err != nil {
		s.finish(job, "清理 results 目录失败："+err.Error(), model.RunError, started)
		return
	}

	caseFile, err := ws.CaseFile(job.tc.Code)
	if err != nil {
		s.finish(job, "解析用例文件路径失败："+err.Error(), model.RunError, started)
		return
	}
	artifactsDir, err := ws.CaseArtifactsDir(job.tc.Code)
	if err != nil {
		s.finish(job, "解析产物目录失败："+err.Error(), model.RunError, started)
		return
	}

	bin, err := hrpclient.Resolve(s.Cfg.Engine.BinaryPath)
	if err != nil {
		s.finish(job, "引擎不可用："+err.Error(), model.RunError, started)
		return
	}

	// 3) 执行
	outcome, err := executor.RunCase(ctx, executor.Options{
		BinaryPath:    bin,
		WorkspaceDir:  ws.Root(),
		CaseFile:      caseFile,
		Timeout:       job.timeout,
		ArtifactsDir:  artifactsDir,
		GenHTMLReport: job.genHTML,
		// 请求/响应明细**必须保留**：断言失败时引擎不产出 summary.json，
		// stdout 的报文快照是唯一可用的失败线索（实测 F8）。
		DisableRequestsLog: false,
		// 遥测默认关闭（configs 里默认 true）：
		// 不关的话每次执行都要在 GA4 上报上白等 5 秒，而且上报失败会被
		// 算进 error_msg（实测 A20）。
		DisableTelemetry: s.Cfg.Engine.DisableTelemetry,
	})
	if err != nil {
		s.finish(job, "执行器错误："+err.Error(), model.RunError, started)
		return
	}

	// 4) 解析双流 + 归因
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

	// 5) 落库
	s.persist(job, cc, res, outcome, started)

	logx.L().Info().
		Uint64("run_id", job.runID).
		Str("case", job.tc.Code).
		Str("status", res.Status).
		Str("attribution", res.Attribution).
		Int("exit_code", outcome.ExitCode).
		Int64("duration_ms", outcome.DurationMs).
		Msg("执行完成")
}

// parserDecls 把编译期步骤声明转成解析器的对齐依据。
func parserDecls(in []compiler.StepDecl) []parser.StepDecl {
	out := make([]parser.StepDecl, 0, len(in))
	for _, d := range in {
		out = append(out, parser.StepDecl{
			Seq:        d.Seq,
			Name:       d.Name,
			StepType:   d.StepType,
			Method:     d.Method,
			URL:        d.URL,
			Assertions: d.Assertions,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// 落库
// ---------------------------------------------------------------------------

// runVerdict 是一次执行的终态裁决。
type runVerdict struct {
	Status   string
	Expected int
	Actual   int
	Mismatch bool
	ErrMsg   string
}

// decideRun 由解析结果推导执行终态与用例数对账结论。
//
// 抽成**纯函数**是刻意的：这条规则决定了"一次一个请求都没发出去的执行"
// 到底显示成绿色还是红色，是整个 M1 最不能出错的一处判断，
// 必须能脱离 hrp 子进程被单测直接覆盖（见 run_test.go）。
//
// 对账判据用 res.CaseStarted 而不是退出码，理由见实测 F5：
// **畸形用例被引擎静默丢弃时，退出码是 0，summary.json 里 success 还是 true**。
// 只看退出码会把这种执行记成成功——那是最坏的一种错误，
// 因为它给出的是一个"全绿"的假象，用户不会有任何机会去怀疑它。
func decideRun(res *parser.Result) runVerdict {
	expected := 1
	actual := 0
	if res.CaseStarted {
		actual = 1
	}
	mismatch := actual != expected

	status := model.RunSuccess
	switch {
	case res.Attribution == model.AttrCanceled:
		status = model.RunCanceled
	case mismatch:
		// 对账不一致强制升为 error（方案 6.4）：无论退出码多好看，
		// "预期跑 1 条、实际跑 0 条"本身就是一次失败。
		status = model.RunError
	case res.Status == model.StatusPass:
		status = model.RunSuccess
	case res.Status == model.StatusFail:
		status = model.RunFailed
	default:
		status = model.RunError
	}

	errMsg := res.ErrorMsg
	if mismatch {
		errMsg = fmt.Sprintf("用例数对账不一致：预期执行 %d 条，实际执行 %d 条。%s",
			expected, actual, res.ErrorMsg)
	}
	return runVerdict{
		Status:   status,
		Expected: expected,
		Actual:   actual,
		Mismatch: mismatch,
		ErrMsg:   errMsg,
	}
}

// persist 把解析结果写进结果表，并完成用例数对账。
//
// 全部写在一个事务里：一次执行的 run / case / step / assertion 四层结果
// 必须同生共死。半截的结果比没有结果更糟——用户会看到一条
// "状态是 error 但步骤全 pass"的记录，完全无法判断真相。
func (s *RunService) persist(job *runJob, cc compiler.CompiledCase, res *parser.Result, oc *executor.Outcome, started time.Time) {
	now := time.Now()
	wallMs := now.Sub(started).Milliseconds()

	v := decideRun(res)
	status := v.Status
	errMsg := v.ErrMsg

	caseMs := oc.DurationMs
	if caseMs <= 0 {
		caseMs = wallMs
	}

	caseRes := model.CaseResult{
		RunID:       job.runID,
		CaseID:      job.tc.ID,
		CaseCode:    job.tc.Code,
		ConfigName:  cc.ConfigName,
		Seq:         1,
		Status:      res.Status,
		ExitCode:    res.ExitCode,
		Panic:       res.Panic,
		CleanExit:   res.CleanExit,
		Attribution: res.Attribution,
		DurationMs:  caseMs,
		StepTotal:   len(res.Steps),
		StepPassed:  countStepStatus(res.Steps, model.StatusPass),
		ErrorMsg:    clip(errMsg, 1024),
		StdoutPath:  oc.StdoutPath,
		StderrPath:  oc.StderrPath,
		SummaryPath: oc.SummaryPath,
		ReportPath:  oc.ReportPath,
	}

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&caseRes).Error; err != nil {
			return err
		}

		steps := make([]model.StepResult, 0, len(res.Steps))
		for i := range res.Steps {
			st := res.Steps[i]
			steps = append(steps, model.StepResult{
				RunID:            job.runID,
				CaseResultID:     caseRes.ID,
				CaseCode:         job.tc.Code,
				Seq:              st.Seq,
				StepName:         clip(st.Name, 128),
				StepType:         st.StepType,
				Status:           st.Status,
				InferredFailed:   st.InferredFailed,
				FinalURL:         clip(st.FinalURL, 1024),
				RequestSnapshot:  anyOf(st.Request),
				ResponseSnapshot: anyOf(st.Response),
				ElapsedMs:        st.ElapsedMs,
				ExtractResult:    st.ExtractResult,
				ErrorMsg:         clip(st.ErrorMsg, 1024),
			})
		}
		if len(steps) > 0 {
			if err := tx.Create(&steps).Error; err != nil {
				return err
			}
		}

		var asserts []model.AssertionResult
		for i := range res.Steps {
			for _, a := range res.Steps[i].Assertions {
				asserts = append(asserts, model.AssertionResult{
					RunID:           job.runID,
					CaseResultID:    caseRes.ID,
					StepResultID:    steps[i].ID,
					Seq:             a.Seq,
					CheckExpr:       clip(a.CheckExpr, 512),
					AssertMethod:    clip(a.AssertMethod, 32),
					ExpectValue:     clip(a.ExpectValue, 1024),
					ExpectValueType: clip(a.ExpectValueType, 32),
					CheckValue:      clip(a.CheckValue, 1024),
					CheckValueType:  clip(a.CheckValueType, 32),
					Passed:          a.Passed,
					Rebuilt:         a.Rebuilt,
					Msg:             clip(a.Msg, 512),
				})
			}
		}
		if len(asserts) > 0 {
			if err := tx.Create(&asserts).Error; err != nil {
				return err
			}
		}

		finished := time.Now()
		updates := map[string]any{
			"status":              status,
			"total":               1,
			"passed":              boolToInt(status == model.RunSuccess),
			"failed":              boolToInt(status == model.RunFailed),
			"error":               boolToInt(status == model.RunError),
			"skipped":             0,
			"duration_ms":         finished.Sub(started).Milliseconds(),
			"attribution":         jsonx.Map{res.Attribution: 1},
			"expected_case_count": v.Expected,
			"actual_case_count":   v.Actual,
			"count_mismatch":      v.Mismatch,
			"workspace_path":      clip(s.runWorkspace(job.runID), 512),
			"log_path":            clip(filepath.Dir(caseRes.StdoutPath), 512),
			"error_msg":           clip(errMsg, 1024),
			"finished_at":         &finished,
		}
		if err := tx.Model(&model.RunRecord{}).Where("id = ?", job.runID).Updates(updates).Error; err != nil {
			return err
		}

		// 用例卡片上的"最近一次结果"由这里维护，避免列表接口再去做一次聚合。
		return tx.Model(&model.TestCase{}).Where("id = ?", job.tc.ID).Updates(map[string]any{
			"last_run_id": job.runID,
			"last_status": res.Status,
		}).Error
	})
	if err != nil {
		logx.L().Error().Err(err).Uint64("run_id", job.runID).Msg("落库失败")
		// 落库失败时至少把执行置成失败，否则这条记录会永远停在 running。
		_ = s.patchRun(job.runID, map[string]any{
			"status":    model.RunError,
			"error_msg": clip("结果落库失败："+err.Error(), 1024),
		})
	}
}

// finish 在"平台侧失败"（编译失败、工作区异常、引擎不可用等）时收尾。
//
// 这类失败没有解析结果可用，因此直接把原因写进 error_msg。
// total 记 0 而不是 1：一条用例都没跑起来是事实，
// actual_case_count = 0 与 expected = 1 之间的差额由 count_mismatch 表达。
func (s *RunService) finish(job *runJob, msg, status string, started time.Time) {
	updates := map[string]any{
		"status":              status,
		"total":               0,
		"error":               boolToInt(status == model.RunError),
		"error_msg":           clip(msg, 1024),
		"expected_case_count": 1,
		"actual_case_count":   0,
		// 取消是用户主动行为，不该被记成"对账不一致"。
		"count_mismatch": status != model.RunCanceled,
	}
	if !started.IsZero() {
		finished := time.Now()
		updates["finished_at"] = &finished
		updates["duration_ms"] = finished.Sub(started).Milliseconds()
	} else {
		finished := time.Now()
		updates["finished_at"] = &finished
	}
	if err := s.patchRun(job.runID, updates); err != nil {
		logx.L().Error().Err(err).Uint64("run_id", job.runID).Msg("更新执行终态失败")
	}
}

func (s *RunService) runWorkspace(runID uint64) string {
	return filepath.Join(s.Cfg.RunsDir(), strconv.FormatUint(runID, 10))
}

func (s *RunService) patchRun(runID uint64, updates map[string]any) error {
	return s.DB.Model(&model.RunRecord{}).Where("id = ?", runID).Updates(updates).Error
}

// ---------------------------------------------------------------------------
// 取消与关闭
// ---------------------------------------------------------------------------

// Cancel 终止一次执行。
//
// 终态返回 40003（契约）。取消后**等待**后台协程写完终态再返回，
// 否则响应里的 status 还是 running，前端会以为没取消成功。
func (s *RunService) Cancel(runID uint64) (*model.RunRecord, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if isTerminalRun(run.Status) {
		return nil, errInUse("执行 %d 已是终态（%s），无法终止", runID, run.Status)
	}

	s.mu.Lock()
	h := s.running[runID]
	s.mu.Unlock()

	if h == nil {
		// 进程已不在（例如服务重启过）。直接把记录收尾，不然它会永远停在 running。
		s.finish(&runJob{runID: runID},
			"执行进程已不存在（可能是服务重启导致），已直接标记为终止", model.RunCanceled, time.Time{})
		return s.GetRun(runID)
	}

	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		logx.L().Warn().Uint64("run_id", runID).Msg("等待执行收尾超时，状态可能稍后才更新")
	}
	return s.GetRun(runID)
}

// Shutdown 取消所有在跑的执行并等待收尾。服务退出前必须调用。
func (s *RunService) Shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	handles := make([]*runHandle, 0, len(s.running))
	for _, h := range s.running {
		handles = append(handles, h)
	}
	s.mu.Unlock()

	if len(handles) == 0 {
		return
	}
	logx.L().Info().Int("count", len(handles)).Msg("正在终止在跑的执行")

	for _, h := range handles {
		h.cancel()
	}
	deadline := time.After(10 * time.Second)
	done := make(chan struct{})
	go func() {
		for _, h := range handles {
			<-h.done
		}
		close(done)
	}()
	select {
	case <-done:
	case <-deadline:
		logx.L().Warn().Msg("部分执行未能在 10 秒内收尾")
	}
}

func (s *RunService) register(runID uint64, h *runHandle) {
	s.mu.Lock()
	s.running[runID] = h
	s.mu.Unlock()
}

func (s *RunService) unregister(runID uint64) {
	s.mu.Lock()
	if h, ok := s.running[runID]; ok {
		close(h.done)
		delete(s.running, runID)
	}
	s.mu.Unlock()
}

// isTerminalRun 判断执行是否已到终态。
func isTerminalRun(status string) bool {
	switch status {
	case model.RunSuccess, model.RunFailed, model.RunError, model.RunCanceled:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// RunListQuery 是执行列表的过滤条件。
type RunListQuery struct {
	ProjectID  uint64
	Status     string
	TargetType string
	TargetID   uint64
}

// GetRun 返回单条执行记录。
func (s *RunService) GetRun(id uint64) (*model.RunRecord, error) {
	var run model.RunRecord
	if err := s.DB.First(&run, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("执行记录不存在（id=%d）", id)
		}
		return nil, errInternal("查询执行记录失败", err)
	}
	if run.EnvID > 0 {
		if env, err := loadEnv(s.DB, run.EnvID); err == nil {
			run.EnvName = env.Name
		}
	}
	return &run, nil
}

// ListRuns 返回执行记录列表。
func (s *RunService) ListRuns(q RunListQuery, page Page) ([]model.RunRecord, int64, error) {
	tx := s.DB.Model(&model.RunRecord{})
	if q.ProjectID > 0 {
		tx = tx.Where("project_id = ?", q.ProjectID)
	}
	if v := strings.TrimSpace(q.Status); v != "" {
		tx = tx.Where("status = ?", v)
	}
	if v := strings.TrimSpace(q.TargetType); v != "" {
		tx = tx.Where("target_type = ?", v)
	}
	if q.TargetID > 0 {
		tx = tx.Where("target_id = ?", q.TargetID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计执行记录数失败", err)
	}

	page = page.Normalize()
	var list []model.RunRecord
	if err := tx.Order("id desc").Offset(page.Offset()).Limit(page.Limit()).Find(&list).Error; err != nil {
		return nil, 0, errInternal("查询执行记录失败", err)
	}
	if err := s.attachEnvNames(list); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// attachEnvNames 批量补环境名。
//
// 用一次 IN 查询而不是逐条 JOIN：外键没有约束，
// 环境被删掉时 JOIN 会让整条执行记录从列表里消失，那是更糟的行为。
func (s *RunService) attachEnvNames(list []model.RunRecord) error {
	ids := make([]uint64, 0, len(list))
	for i := range list {
		if list[i].EnvID > 0 {
			ids = append(ids, list[i].EnvID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var envs []model.Environment
	if err := s.DB.Where("id IN ?", ids).Find(&envs).Error; err != nil {
		return errInternal("查询环境名失败", err)
	}
	names := make(map[uint64]string, len(envs))
	for i := range envs {
		names[envs[i].ID] = envs[i].Name
	}
	for i := range list {
		list[i].EnvName = names[list[i].EnvID]
	}
	return nil
}

// EnrichCaseResult 给用例结果补上"产物是否存在"两个非落库字段。
//
// 直接看文件系统而不是靠字段推断：报告可能被运维清理掉，
// 那时前端应该走"报告已不存在"的降级展示，而不是给一个 404 的 iframe。
func (s *RunService) EnrichCaseResult(cr *model.CaseResult) {
	cr.HasSummary = fileExists(cr.SummaryPath)
	cr.HasReport = fileExists(cr.ReportPath)
}

// CaseResults 返回某次执行下的用例结果列表（不含步骤明细）。
func (s *RunService) CaseResults(runID uint64) ([]model.CaseResult, error) {
	if _, err := s.GetRun(runID); err != nil {
		return nil, err
	}
	var list []model.CaseResult
	if err := s.DB.Where("run_id = ?", runID).Order("seq asc, id asc").Find(&list).Error; err != nil {
		return nil, errInternal("查询用例结果失败", err)
	}
	for i := range list {
		s.EnrichCaseResult(&list[i])
	}
	return list, nil
}

// CaseResultView 是用例结果的对外视图。
//
// 归因标签与判断依据由**服务端**生成：归因规则来自引擎实测结论，
// 集中在一处生成才不会被前端的文案版本差异带偏——否则同一份数据
// 在不同页面可能给出互相矛盾的解释。
type CaseResultView struct {
	model.CaseResult
	AttributionLabel  string `json:"attribution_label"`
	AttributionReason string `json:"attribution_reason"`

	// 下面两个是**派生统计**：模型上只有 step_total / step_passed（由 parser
	// 从 summary.json 读出），failed 与 error 需要按步骤状态再分一次。
	// 刻意不加数据库列 —— 派生值一旦落库就有机会和明细不一致。
	StepFailed int `json:"step_failed"`
	StepError  int `json:"step_error"`
}

// stepCounts 是步骤状态分布。
type stepCounts struct {
	Failed int
	Error  int
}

// CaseResultViews 返回带归因文案的用例结果列表。
func (s *RunService) CaseResultViews(runID uint64) ([]CaseResultView, error) {
	list, err := s.CaseResults(runID)
	if err != nil {
		return nil, err
	}
	counts, err := s.stepStatusCounts(runID)
	if err != nil {
		return nil, err
	}
	out := make([]CaseResultView, 0, len(list))
	for i := range list {
		c := counts[list[i].ID]
		out = append(out, CaseResultView{
			CaseResult:        list[i],
			AttributionLabel:  model.AttributionLabel(list[i].Attribution),
			AttributionReason: model.AttributionReason(list[i].Attribution),
			StepFailed:        c.Failed,
			StepError:         c.Error,
		})
	}
	return out, nil
}

// stepStatusCounts 统计某次执行下每个用例结果的 fail / error 步骤数。
//
// 一次 GROUP BY 取完，而不是给每个用例结果各查一次步骤表：
// 一次批量执行可能有几十个用例结果，逐条查会变成 N+1。
func (s *RunService) stepStatusCounts(runID uint64) (map[uint64]stepCounts, error) {
	type row struct {
		CaseResultID uint64 `gorm:"column:case_result_id"`
		Status       string `gorm:"column:status"`
		N            int    `gorm:"column:n"`
	}
	var rows []row
	err := s.DB.Model(&model.StepResult{}).
		Select("case_result_id, status, count(*) AS n").
		Where("run_id = ?", runID).
		Group("case_result_id, status").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("统计步骤状态失败", err)
	}

	out := make(map[uint64]stepCounts, len(rows))
	for _, r := range rows {
		c := out[r.CaseResultID]
		switch r.Status {
		case model.StatusFail:
			c.Failed += r.N
		case model.StatusError:
			c.Error += r.N
		}
		out[r.CaseResultID] = c
	}
	return out, nil
}

// CaseSteps 是步骤明细接口的返回结构。
type CaseSteps struct {
	CaseResultID uint64             `json:"case_result_id"`
	CaseCode     string             `json:"case_code"`
	ConfigName   string             `json:"config_name"`
	Status       string             `json:"status"`
	Attribution  string             `json:"attribution"`
	Steps        []model.StepResult `json:"steps"`
}

// CaseStepsDetail 返回某次执行下每个用例的步骤与断言明细。
//
// 单独一个端点（而不是塞进详情接口）：步骤 + 报文快照 + 逐条断言
// 很容易达到几百 KB，会让"轮询执行状态"变成一件昂贵的事。
func (s *RunService) CaseStepsDetail(runID uint64) ([]CaseSteps, error) {
	cases, err := s.CaseResults(runID)
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return []CaseSteps{}, nil
	}

	ids := make([]uint64, 0, len(cases))
	for i := range cases {
		ids = append(ids, cases[i].ID)
	}

	var steps []model.StepResult
	err = s.DB.Where("case_result_id IN ?", ids).Order("seq asc, id asc").Find(&steps).Error
	if err != nil {
		return nil, errInternal("查询步骤结果失败", err)
	}

	var asserts []model.AssertionResult
	if len(steps) > 0 {
		sids := make([]uint64, 0, len(steps))
		for i := range steps {
			sids = append(sids, steps[i].ID)
		}
		err = s.DB.Where("step_result_id IN ?", sids).Order("seq asc, id asc").Find(&asserts).Error
		if err != nil {
			return nil, errInternal("查询断言结果失败", err)
		}
	}

	byStep := make(map[uint64][]model.AssertionResult, len(asserts))
	for _, a := range asserts {
		byStep[a.StepResultID] = append(byStep[a.StepResultID], a)
	}

	out := make([]CaseSteps, 0, len(cases))
	index := make(map[uint64]int, len(cases))
	for i := range cases {
		index[cases[i].ID] = i
		out = append(out, CaseSteps{
			CaseResultID: cases[i].ID,
			CaseCode:     cases[i].CaseCode,
			ConfigName:   cases[i].ConfigName,
			Status:       cases[i].Status,
			Attribution:  cases[i].Attribution,
			Steps:        []model.StepResult{},
		})
	}
	for i := range steps {
		st := steps[i]
		st.Assertions = byStep[st.ID]
		if st.Assertions == nil {
			st.Assertions = []model.AssertionResult{}
		}
		if j, ok := index[st.CaseResultID]; ok {
			out[j].Steps = append(out[j].Steps, st)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 产物：HTML 报告与原始日志
// ---------------------------------------------------------------------------

// MaxLogBytes 是单个日志文件的读取上限（2MB，见契约 5 节）。
const MaxLogBytes = 2 << 20

// Report 返回 HTML 报告内容。
//
// caseResultID 为 0 时取本次执行的第一条用例结果（M1 单用例）。
func (s *RunService) Report(runID, caseResultID uint64) ([]byte, error) {
	cr, err := s.pickCaseResult(runID, caseResultID)
	if err != nil {
		return nil, err
	}
	if cr.ReportPath == "" {
		return nil, reportMissing()
	}
	data, err := os.ReadFile(cr.ReportPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, reportMissing()
		}
		return nil, errInternal("读取 HTML 报告失败", err)
	}
	return data, nil
}

// reportMissing 构造"报告不存在"错误。
//
// 这不是异常，而是**引擎的既定行为**：断言失败时引擎 panic，
// 根本走不到生成报告那一步（实测 F8）。文案要把这件事讲清楚，
// 否则用户会以为平台把报告弄丢了。
func reportMissing() *response.Error {
	return response.New(response.CodeReportMissing,
		"本次执行未产出 HTML 报告（引擎在断言失败时会 panic，不会生成报告）")
}

// RunLogs 是原始日志。
type RunLogs struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

// Logs 返回原始双流日志。
//
// 原始日志**永久保留**（方案 6.6）：一旦引擎输出格式在升级后漂移，
// 这些原文是唯一能离线重放、修复历史数据的依据。
func (s *RunService) Logs(runID, caseResultID uint64) (*RunLogs, error) {
	cr, err := s.pickCaseResult(runID, caseResultID)
	if err != nil {
		return nil, err
	}

	out := &RunLogs{}
	if out.Stdout, out.Truncated, err = readCapped(cr.StdoutPath); err != nil {
		return nil, err
	}
	stderr, truncated, err := readCapped(cr.StderrPath)
	if err != nil {
		return nil, err
	}
	out.Stderr = stderr
	out.Truncated = out.Truncated || truncated
	return out, nil
}

// pickCaseResult 取出指定的（或第一条）用例结果。
func (s *RunService) pickCaseResult(runID, caseResultID uint64) (*model.CaseResult, error) {
	if _, err := s.GetRun(runID); err != nil {
		return nil, err
	}

	var cr model.CaseResult
	q := s.DB.Where("run_id = ?", runID)
	if caseResultID > 0 {
		q = q.Where("id = ?", caseResultID)
	}
	if err := q.Order("seq asc, id asc").First(&cr).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("该执行下没有用例结果（run_id=%d）", runID)
		}
		return nil, errInternal("查询用例结果失败", err)
	}
	return &cr, nil
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func readCapped(path string) (string, bool, error) {
	if path == "" {
		return "", false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errInternal("读取日志失败", err)
	}
	defer f.Close()

	buf := make([]byte, MaxLogBytes+1)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		// io.EOF：空文件
		return "", false, nil
	}
	if n > MaxLogBytes {
		return string(buf[:MaxLogBytes]) + "\n\n...（日志过大，已截断，完整内容见服务器上的原始文件）", true, nil
	}
	return string(buf[:n]), false, nil
}

// anyOf 把可空指针包成 JSON 字段。
//
// 显式判空而不是依赖 json.Marshal：类型化的 nil 指针在 interface 里
// **不等于 nil**，直接塞进去会落库成字符串 "null"，前端拿到一个假对象。
func anyOf(v any) jsonx.Any {
	switch p := v.(type) {
	case *parser.RequestSnapshot:
		if p == nil {
			return jsonx.Any{}
		}
		return jsonx.Any{Val: p}
	case *parser.ResponseSnapshot:
		if p == nil {
			return jsonx.Any{}
		}
		return jsonx.Any{Val: p}
	case nil:
		return jsonx.Any{}
	default:
		return jsonx.Any{Val: v}
	}
}

func countStepStatus(steps []parser.StepOutcome, status string) int {
	n := 0
	for i := range steps {
		if steps[i].Status == status {
			n++
		}
	}
	return n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolOr(def bool, override *bool) bool {
	if override != nil {
		return *override
	}
	return def
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// clip 按**字符数**截断，避免超出列宽。
//
// 按 rune 而不是 byte 统计：MySQL 的 varchar(n) 计的是字符数，
// 中文文案按字节截断会白白浪费一半长度，还可能把汉字劈成半个。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
