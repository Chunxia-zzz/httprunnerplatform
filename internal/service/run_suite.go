package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
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

// 本文件是用例集执行。
//
// ⭐ 与单用例执行的关键区别，以及为什么不能"复用同一条路径 + 外层 for 循环"：
//
//	1 条 RunRecord（target_type=suite）对应 N 条 CaseResult，
//	而单用例执行是 1 条 RunRecord 对应 1 条 CaseResult。
//	因此"执行记录的终态"在用例集场景下是**聚合**出来的，不是解析出来的。
//
// 三条硬约束全部继承自既有的执行器设计：
//
//   - 每个用例**独立编译、独立子进程**（实测 A2：断言失败会让引擎 panic，
//     目录模式下整批结果丢失）。编译逐条进行，邻居用例写坏了不挡住本条。
//   - 一次执行**共用一个**运行工作区，每个用例的产物落在自己的
//     CaseArtifactsDir 下，互不覆盖。
//   - 用例数对账：expected = 打算让引擎跑的条数，actual = 引擎真正启动的条数。
//     二者不等时**强制升为 error**（实测 F5 的防假绿规则）。
//
// ⭐ 对账口径必须排除"我们本来就打算跳过"的条目：
// 已禁用的成员是用户主动关掉的，已删除的成员根本不存在，
// 它们都不该让对账报警 —— 否则"10 个成员里禁用 1 个"会永远显示成红色。

// runSuiteJob 是一次用例集执行的不可变输入。
type runSuiteJob struct {
	runID   uint64
	project *model.Project
	suite   *model.TestSuite
	env     *model.Environment
	// specs 是执行开始时固定下来的**可跑成员快照**。
	// 长度就是对账里的 expected：中途改动成员不影响本次执行。
	specs     []compiler.CaseSpec
	timeout   time.Duration // 单用例超时
	genHTML   bool
	onFailure string
}

// ---------------------------------------------------------------------------
// 启动
// ---------------------------------------------------------------------------

// StartSuite 创建一条用例集执行记录并异步启动。
func (s *RunService) StartSuite(req StartRunRequest, triggerBy uint64) (*model.RunRecord, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, response.New(response.CodeExecutorFail, "服务正在关闭，暂不接受新的执行")
	}
	if req.ProjectID == 0 || req.TargetID == 0 {
		return nil, errBadParam("project_id 与 target_id 不能为空")
	}

	project, err := loadProject(s.DB, req.ProjectID)
	if err != nil {
		return nil, err
	}
	su, err := loadSuite(s.DB, req.TargetID)
	if err != nil {
		return nil, err
	}
	if su.ProjectID != project.ID {
		return nil, errBadParam("用例集 %q 不属于项目 %q", su.Code, project.Code)
	}
	env, err := resolveEnv(s.DB, project.ID, req.EnvID)
	if err != nil {
		return nil, err
	}

	// ⭐ 成员在执行开始时就**固定成快照**。
	// 否则用户在执行过程中改动成员，会导致"预期用例数"在跑的中途变化，
	// 对账将无从谈起 —— 而对账是防假绿的唯一依据。
	members, broken, err := s.suiteRunMembers(su.ID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 && len(broken) == 0 {
		return nil, errBadParam("用例集 %q 没有成员，无法执行", su.Code)
	}

	specs := make([]compiler.CaseSpec, 0, len(members))
	for _, tc := range members {
		steps, err := caseSteps(s.DB, tc.ID)
		if err != nil {
			return nil, err
		}
		specs = append(specs, compiler.CaseSpec{Case: tc, Steps: steps})
	}

	trigger := strings.TrimSpace(req.TriggerType)
	if trigger == "" {
		trigger = model.TriggerManual
	}
	run := &model.RunRecord{
		ProjectID:         project.ID,
		TargetType:        model.TargetSuite,
		TargetID:          su.ID,
		TargetName:        su.Name,
		TriggerType:       trigger,
		TriggerBy:         triggerBy,
		PlanID:            req.PlanID,
		Status:            model.RunQueued,
		ExpectedCaseCount: len(specs),
		Attribution:       jsonx.Map{},
	}
	if env != nil {
		run.EnvID = env.ID
	}
	if err := s.DB.Create(run).Error; err != nil {
		return nil, errInternal("创建执行记录失败", err)
	}

	job := &runSuiteJob{
		runID:     run.ID,
		project:   project,
		suite:     su,
		env:       env,
		specs:     specs,
		timeout:   s.resolveTimeout(req.Options.CaseTimeout, nil),
		genHTML:   boolOr(s.Cfg.Engine.GenHTMLReport, req.Options.GenHTMLReport),
		onFailure: su.OnFailure,
	}

	// ctx 刻意不继承 HTTP 请求的 ctx，理由同单用例执行。
	ctx, cancel := context.WithCancel(context.Background())
	s.register(run.ID, &runHandle{cancel: cancel, done: make(chan struct{})})

	logx.L().Info().
		Uint64("run_id", run.ID).
		Uint64("suite_id", su.ID).
		Int("expected", len(specs)).
		Int("broken", len(broken)).
		Str("on_failure", job.onFailure).
		Msg("用例集执行已入队")

	go s.executeSuite(ctx, job, broken)
	return run, nil
}

// suiteRunMembers 取出本次要跑的用例，以及那些"成员还在但这条跑不了"的条目。
//
// 已删除的成员**不能默默丢掉**：丢掉的话用户只会看到"预期 10 条实际 8 条"
// 却不知道少了哪两条。这里把它们单独返回，由执行阶段记成 error。
//
// 返回值 broken 里的条目不计入对账的 expected —— 我们本来就打算跳过它们。
func (s *RunService) suiteRunMembers(suiteID uint64) ([]*model.TestCase, []suiteBrokenMember, error) {
	var links []model.SuiteCase
	if err := s.DB.Where("suite_id = ?", suiteID).
		Order("seq asc, id asc").Find(&links).Error; err != nil {
		return nil, nil, errInternal("查询用例集成员失败", err)
	}

	ids := make([]uint64, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.CaseID)
	}
	// 走 TestCase 模型查询 ⇒ GORM 自动带上软删除条件 ⇒ 查不到的即"已删除"。
	var cases []model.TestCase
	if len(ids) > 0 {
		if err := s.DB.Where("id IN ?", ids).Find(&cases).Error; err != nil {
			return nil, nil, errInternal("查询成员用例失败", err)
		}
	}
	byID := make(map[uint64]*model.TestCase, len(cases))
	for i := range cases {
		byID[cases[i].ID] = &cases[i]
	}

	members := make([]*model.TestCase, 0, len(links))
	var broken []suiteBrokenMember
	for _, l := range links {
		tc, ok := byID[l.CaseID]
		if !ok {
			broken = append(broken, suiteBrokenMember{
				CaseID: l.CaseID, Seq: l.Seq, CaseCode: "(已删除)",
				Reason: "用例已被删除，无法执行",
			})
			continue
		}
		if tc.Status == model.CaseStatusDisabled {
			// 已禁用是"用户主动关掉"，记为 skipped 而不是 error。
			broken = append(broken, suiteBrokenMember{
				CaseID: tc.ID, Seq: l.Seq, CaseCode: tc.Code, CaseName: tc.Name,
				Skipped: true, Reason: "用例已禁用，已跳过",
			})
			continue
		}
		members = append(members, tc)
	}
	return members, broken, nil
}

// suiteBrokenMember 是"成员还在但这条跑不了"的条目。
type suiteBrokenMember struct {
	CaseID   uint64
	Seq      int
	CaseCode string
	CaseName string
	Skipped  bool
	Reason   string
}

// ---------------------------------------------------------------------------
// 执行主体
// ---------------------------------------------------------------------------

// suiteOutcome 是一条用例在用例集里的执行结果。
type suiteOutcome struct {
	Status      string
	Attribution string
	DurationMs  int64
	// Started 表示引擎是否真的启动了这个用例 —— 用于对账。
	// 编译失败、用例被删都算"没启动"，即使它们的 Status 是 error。
	Started  bool
	CaseCode string
	ErrorMsg string
}

func (s *RunService) executeSuite(ctx context.Context, job *runSuiteJob, broken []suiteBrokenMember) {
	defer s.unregister(job.runID)

	// 并发闸门：先抢槽位再做别的事，避免"编译完了才发现要排队"。
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		s.finishSuite(job, nil, model.RunCanceled, "执行在排队期间被终止", time.Time{}, len(job.specs))
		return
	}

	started := time.Now()
	if err := s.patchRun(job.runID, map[string]any{
		"status":     model.RunRunning,
		"started_at": &started,
	}); err != nil {
		logx.L().Error().Err(err).Uint64("run_id", job.runID).Msg("更新执行状态失败")
	}

	bin, err := hrpclient.Resolve(s.Cfg.Engine.BinaryPath)
	if err != nil {
		s.finishSuite(job, nil, model.RunError, "引擎不可用："+err.Error(), started, len(job.specs))
		return
	}

	// 1) 逐条编译。逐条是刻意的：一次性编译整批的话，
	//    任意一个用例写坏都会让整个编译失败、整批都跑不了 ——
	//    而单用例执行时我们定的原则就是"邻居写坏了不挡住本条"。
	projectWS := projectWorkspace(s.Cfg, job.project)
	type planned struct {
		tc *model.TestCase
		cc compiler.CompiledCase
	}
	plans := make([]planned, 0, len(job.specs))
	compileErrors := make([]suiteOutcome, 0)
	for _, spec := range job.specs {
		comp, err := compiler.Compile(&compiler.Input{
			Project: job.project,
			Env:     job.env,
			Cases:   []compiler.CaseSpec{spec},
		}, projectWS)
		if err != nil {
			compileErrors = append(compileErrors, suiteOutcome{
				Status:   model.StatusError,
				CaseCode: spec.Case.Code,
				ErrorMsg: "编译失败：" + err.Error(),
			})
			continue
		}
		plans = append(plans, planned{tc: spec.Case, cc: comp.Cases[0]})
	}

	// 2) 工作区（一次创建，全程复用）
	runRoot := filepath.Join(s.Cfg.RunsDir(), strconv.FormatUint(job.runID, 10))
	ws, err := executor.NewWorkspace(runRoot)
	if err != nil {
		s.finishSuite(job, nil, model.RunError, "创建工作区失败："+err.Error(), started, len(job.specs))
		return
	}
	if err := ws.CopyFrom(projectWS); err != nil {
		s.finishSuite(job, nil, model.RunError, "复制工作区失败："+err.Error(), started, len(job.specs))
		return
	}
	if err := ws.PrepareForCase(); err != nil {
		s.finishSuite(job, nil, model.RunError, "清理 results 目录失败："+err.Error(), started, len(job.specs))
		return
	}

	// 3) 把"跑不了"的成员先写进去：它们进 total，但不进对账的 expected。
	outcomes := make([]suiteOutcome, 0, len(plans)+len(broken)+len(compileErrors))
	seq := 0
	for _, b := range broken {
		seq++
		status := model.StatusError
		if b.Skipped {
			status = model.StatusSkipped
		}
		s.persistSuiteSkipped(job.runID, b, seq, status)
		outcomes = append(outcomes, suiteOutcome{
			Status:   status,
			CaseCode: b.CaseCode,
			ErrorMsg: b.Reason,
		})
	}
	for _, e := range compileErrors {
		seq++
		// 编译失败的条目不写 CaseResult（没有可用的编译产物与产物路径），
		// 但要计入 outcomes，好把整批改判为 error。
		outcomes = append(outcomes, e)
	}

	// 4) 逐条执行
	aborted := false
	attempted := 0
	for i, p := range plans {
		if ctx.Err() != nil {
			break
		}
		seq++
		attempted = i + 1
		o := s.runOneCase(ctx, job, ws, bin, p.cc, p.tc, seq)
		outcomes = append(outcomes, o)

		// 遇错即停：只在明确配了 abort 时才停。默认 continue，
		// 这样 CI 里一次就能看到全部失败，不用"修一个跑一次"。
		if job.onFailure == model.OnFailureAbort && o.Status != model.StatusPass {
			aborted = true
			logx.L().Info().
				Uint64("run_id", job.runID).
				Str("case", p.tc.Code).
				Str("status", o.Status).
				Msg("on_failure=abort，后续用例不再执行")
			break
		}
	}

	// ⭐ abort 与取消都是"我们主动不跑后面的用例"，不是"用例被引擎弄丢了"。
	// 这两种情况下 expected 必须收缩到实际尝试过的条数，
	// 否则每一次 abort 都会额外报一个"对账不一致"，把真正的信号淹掉。
	// （对账要抓的是 F5 那种引擎静默丢弃，不是用户自己的选择。）
	expected := len(job.specs)
	if aborted || ctx.Err() != nil {
		expected = attempted
	}

	// 取消由 ctx 驱动：上面循环 break 之后，这里统一收尾成 canceled。
	// 不用 outcomes 里有没有 canceled 来判断 —— 用户点了终止就是终止，
	// 哪怕所有用例恰好都跑完了。
	if ctx.Err() != nil {
		s.finishSuite(job, outcomes, model.RunCanceled, "执行被取消", started, expected)
		return
	}

	v := decideSuite(outcomes, expected)
	s.finishSuite(job, outcomes, v.Status, v.ErrMsg, started, expected)
}

// runOneCase 编译产物已在盘上，这里只负责跑 + 解析 + 落库。
func (s *RunService) runOneCase(
	ctx context.Context,
	job *runSuiteJob,
	ws *executor.Workspace,
	bin string,
	cc compiler.CompiledCase,
	tc *model.TestCase,
	seq int,
) suiteOutcome {
	caseFile, err := ws.CaseFile(tc.Code)
	if err != nil {
		return suiteOutcome{Status: model.StatusError, CaseCode: tc.Code,
			ErrorMsg: "解析用例文件路径失败：" + err.Error()}
	}
	artifactsDir, err := ws.CaseArtifactsDir(tc.Code)
	if err != nil {
		return suiteOutcome{Status: model.StatusError, CaseCode: tc.Code,
			ErrorMsg: "解析产物目录失败：" + err.Error()}
	}

	oc, err := executor.RunCase(ctx, executor.Options{
		BinaryPath:    bin,
		WorkspaceDir:  ws.Root(),
		CaseFile:      caseFile,
		Timeout:       job.timeout,
		ArtifactsDir:  artifactsDir,
		GenHTMLReport: job.genHTML,
		// 请求/响应明细**必须保留**：断言失败时引擎不产出 summary.json，
		// stdout 的报文快照是唯一可用的失败线索（实测 F8）。
		DisableRequestsLog: false,
		// 遥测默认关闭（configs 里默认 true）：不关的话每次执行都要在
		// GA4 上报上白等 5 秒，而且上报失败会被算进 error_msg（实测 A20）。
		DisableTelemetry: s.Cfg.Engine.DisableTelemetry,
	})
	if err != nil {
		return suiteOutcome{
			Status:   model.StatusError,
			CaseCode: tc.Code,
			ErrorMsg: "执行器错误：" + err.Error(),
		}
	}

	res := parser.Parse(parser.Input{
		ExitCode:     oc.ExitCode,
		Stdout:       oc.Stdout,
		Stderr:       oc.Stderr,
		TimedOut:     oc.TimedOut,
		Canceled:     oc.Canceled,
		EnabledSteps: parserDecls(cc.Steps),
		SummaryJSON:  oc.SummaryJSON,
		DurationMs:   oc.DurationMs,
	})

	started := time.Now()
	s.persistSuiteCase(job.runID, tc, cc, res, oc, seq, started)

	return suiteOutcome{
		Status:      res.Status,
		Attribution: res.Attribution,
		DurationMs:  oc.DurationMs,
		Started:     res.CaseStarted,
		CaseCode:    tc.Code,
		ErrorMsg:    res.ErrorMsg,
	}
}

// ---------------------------------------------------------------------------
// 终态裁决（纯函数，可脱离引擎被测）
// ---------------------------------------------------------------------------

// suiteVerdict 是用例集执行记录的终态裁决。
type suiteVerdict struct {
	Status string
	ErrMsg string
}

// decideSuite 由各条用例的结果推导用例集执行记录的终态。
//
// 抽成纯函数，与 decideRun 同理：这条规则决定了"一批用例跑完之后到底
// 显示成绿还是红"，是最不能出错的一处判断，必须能脱离 hrp 子进程被测。
//
// 四条规则，按优先级从高到低：
//
//  1. 有一条 canceled ⇒ canceled（用户点了终止就是终止）；
//  2. 有一条 error（含编译失败、用例被删除）⇒ error；
//  3. 用例数对账不一致 ⇒ **强制 error**，不管 statuses 多好看 ——
//     沿用实测 F5：畸形用例被引擎静默丢弃时退出码是 0、success 还是 true，
//     只看退出码会给出一个"全绿"的假象；
//  4. 否则有一条 fail ⇒ failed；全 pass ⇒ success。
//
// ⭐ expected 的口径是"我们打算让引擎跑的条数"，不含主动跳过的成员。
// 否则"10 个成员里禁用 1 个"会永远因为对账不一致而显示成红色。
func decideSuite(outcomes []suiteOutcome, expected int) suiteVerdict {
	actual := 0
	skipped := 0
	hasError := false
	hasFail := false
	hasCanceled := false
	var failedList []string

	for _, o := range outcomes {
		switch o.Status {
		case model.StatusPass:
			actual++
		case model.StatusFail:
			actual++
			hasFail = true
			failedList = append(failedList, o.CaseCode)
		case model.StatusError:
			// 只有"引擎真的启动了"才算进 actual。
			// 编译失败、用例被删都没启动过，它们靠 hasError 表达，不靠对账。
			if o.Started {
				actual++
			}
			hasError = true
			failedList = append(failedList, o.CaseCode)
		case model.StatusSkipped:
			skipped++
		default:
			hasError = true
		}
		if o.Attribution == model.AttrCanceled {
			hasCanceled = true
		}
	}

	status := model.RunSuccess
	switch {
	case hasCanceled:
		status = model.RunCanceled
	case hasError:
		status = model.RunError
	case hasFail:
		status = model.RunFailed
	}

	errMsg := ""
	// canceled 不再叠加对账结论：用户点了终止之后跑了几个用例是无意义的，
	// 再报一句"对账不一致"只会让错误原因变得难读。
	if !hasCanceled && actual != expected {
		// 对账不一致压过一切：这是"整批里有一部分根本没跑"的信号。
		status = model.RunError
		errMsg = fmt.Sprintf("用例数对账不一致：预期执行 %d 条，实际执行 %d 条（跳过 %d 条）。",
			expected, actual, skipped)
	}
	if len(failedList) > 0 && status != model.RunSuccess {
		if errMsg != "" {
			errMsg += " "
		}
		errMsg += "未通过的用例：" + strings.Join(failedList, "、")
	}
	return suiteVerdict{Status: status, ErrMsg: errMsg}
}

// ---------------------------------------------------------------------------
// 落库
// ---------------------------------------------------------------------------

// persistSuiteCase 写一条用例集内的用例结果（含步骤与断言）。
//
// 四层结果必须同生共死，理由同单用例执行：半截结果比没有结果更糟 ——
// 用户会看到一条"状态是 fail 但步骤全绿"的记录，完全无法判断真相。
func (s *RunService) persistSuiteCase(
	runID uint64,
	tc *model.TestCase,
	cc compiler.CompiledCase,
	res *parser.Result,
	oc *executor.Outcome,
	seq int,
	caseStart time.Time,
) {
	caseMs := oc.DurationMs
	if caseMs <= 0 {
		caseMs = time.Since(caseStart).Milliseconds()
	}
	cr := &model.CaseResult{
		RunID:       runID,
		CaseID:      tc.ID,
		CaseCode:    tc.Code,
		ConfigName:  cc.ConfigName,
		Seq:         seq,
		Status:      res.Status,
		ExitCode:    res.ExitCode,
		Panic:       res.Panic,
		CleanExit:   res.CleanExit,
		Attribution: res.Attribution,
		DurationMs:  caseMs,
		StepTotal:   len(res.Steps),
		StepPassed:  countStepStatus(res.Steps, model.StatusPass),
		ErrorMsg:    clip(res.ErrorMsg, 1024),
		StdoutPath:  oc.StdoutPath,
		StderrPath:  oc.StderrPath,
		SummaryPath: oc.SummaryPath,
		ReportPath:  oc.ReportPath,
	}

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(cr).Error; err != nil {
			return err
		}
		if err := insertCaseLayers(tx, runID, cr.ID, tc.Code, res.Steps); err != nil {
			return err
		}
		// 用例卡片上的"最近一次结果"由这里维护，避免列表接口再聚合一次。
		return tx.Model(&model.TestCase{}).Where("id = ?", tc.ID).Updates(map[string]any{
			"last_run_id": runID,
			"last_status": res.Status,
		}).Error
	})
	if err != nil {
		logx.L().Error().Err(err).Uint64("run_id", runID).Str("case", tc.Code).
			Msg("写入用例集内用例结果失败")
	}
}

// persistSuiteSkipped 写一条"跑不了"的成员结果。
func (s *RunService) persistSuiteSkipped(runID uint64, b suiteBrokenMember, seq int, status string) {
	code := b.CaseCode
	name := b.CaseName
	if name == "" {
		name = b.Reason
	}
	cr := &model.CaseResult{
		RunID:      runID,
		CaseID:     b.CaseID,
		CaseCode:   code,
		ConfigName: name,
		Seq:        seq,
		Status:     status,
		ErrorMsg:   clip(b.Reason, 1024),
	}
	if err := s.DB.Create(cr).Error; err != nil {
		logx.L().Error().Err(err).Uint64("run_id", runID).Uint64("case_id", b.CaseID).
			Msg("写入跳过的成员结果失败")
	}
}

// insertCaseLayers 写步骤结果与断言结果两层。
//
// 单用例执行与用例集执行**共用**这一段：两处的步骤/断言写库逻辑必须完全一致，
// 否则会出现"单跑看得到步骤、放进用例集就看不到"的诡异差异。
func insertCaseLayers(tx *gorm.DB, runID, caseResultID uint64, caseCode string, steps []parser.StepOutcome) error {
	rows := make([]model.StepResult, 0, len(steps))
	for i := range steps {
		st := steps[i]
		rows = append(rows, model.StepResult{
			RunID:            runID,
			CaseResultID:     caseResultID,
			CaseCode:         caseCode,
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
	if len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
	}

	var asserts []model.AssertionResult
	for i := range steps {
		for _, a := range steps[i].Assertions {
			asserts = append(asserts, model.AssertionResult{
				RunID:           runID,
				CaseResultID:    caseResultID,
				StepResultID:    rows[i].ID,
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
		return tx.Create(&asserts).Error
	}
	return nil
}

// finishSuite 汇总写回执行记录。
func (s *RunService) finishSuite(
	job *runSuiteJob,
	outcomes []suiteOutcome,
	status, errMsg string,
	started time.Time,
	expected int,
) {
	total, passed, failed, errored, skipped, actual := 0, 0, 0, 0, 0, 0
	attr := map[string]int{}
	var durationMs int64
	for _, o := range outcomes {
		total++
		durationMs += o.DurationMs
		switch o.Status {
		case model.StatusPass:
			passed++
			actual++
		case model.StatusFail:
			failed++
			actual++
		case model.StatusError:
			errored++
			if o.Started {
				actual++
			}
		case model.StatusSkipped:
			skipped++
		}
		if o.Attribution != "" {
			attr[o.Attribution]++
		}
	}

	wall := durationMs
	if !started.IsZero() {
		wall = time.Since(started).Milliseconds()
	}
	if wall < 0 {
		wall = 0
	}
	// 归因是"计数"而不是"单值"：用例集里多个用例可能各归不同原因。
	attrMap := make(jsonx.Map, len(attr))
	for k, v := range attr {
		attrMap[k] = v
	}

	updates := map[string]any{
		"status":              status,
		"total":               total,
		"passed":              passed,
		"failed":              failed,
		"error":               errored,
		"skipped":             skipped,
		"duration_ms":         wall,
		"attribution":         attrMap,
		"expected_case_count": expected,
		"actual_case_count":   actual,
		// 取消是用户主动行为，不该被记成"对账不一致"。
		"count_mismatch": actual != expected && status != model.RunCanceled,
		"workspace_path": clip(s.runWorkspace(job.runID), 512),
		"log_path":       clip(filepath.Join(s.runWorkspace(job.runID), "artifacts"), 512),
		"error_msg":      clip(errMsg, 1024),
	}
	finished := time.Now()
	updates["finished_at"] = &finished
	if err := s.patchRun(job.runID, updates); err != nil {
		logx.L().Error().Err(err).Uint64("run_id", job.runID).Msg("更新用例集执行终态失败")
	}
	logx.L().Info().
		Uint64("run_id", job.runID).
		Str("status", status).
		Int("total", total).Int("passed", passed).
		Int("failed", failed).Int("error", errored).Int("skipped", skipped).
		Int("expected", expected).Int("actual", actual).
		Msg("用例集执行完成")
}
