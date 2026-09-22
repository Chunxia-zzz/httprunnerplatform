package parser

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// reHrpExit 匹配 stdout 中的 `hrp exit N` 行。
//
// ⚠️ 修正一条曾写错的结论：`hrp exit N` 并不是"正常退出就打印"，
// 而是**只在退出码非 0 时打印**。官方源码 code.go 里：
//
//	func GetErrorCode(err error) (errCode int) {
//	    if err == nil { return Success }   // ← 提前 return，跳过打印
//	    ...
//	    fmt.Printf("hrp exit %d\n", errCode)
//	}
//
// 实测印证：exit=0 的两个夹具（pass_2steps / pass_extract）末尾**没有**这一行，
// 而 exit=1/21/23 的夹具都有。
//
// 因此它的作用是「印证进程是主动退出并给出了明确错误码的」，
// 而不是"是否成功"。判定成功请看 ExitCode。
var reHrpExit = regexp.MustCompile(`(?m)^hrp exit \d+\s*$`)

// Parse 把一次执行的原始输出解析成结构化结果。
//
// 输入是**未经任何裁剪**的两条流原文 + 退出码；调用方（执行器）
// 不得预先过滤，尤其是 stderr 尾部的 Go 栈回溯必须完整传入，
// 因为"是否 panic"是归因的第一判据。
func Parse(in Input) *Result {
	res := &Result{
		ExitCode: in.ExitCode,
		Panic:    strings.Contains(in.Stderr, "panic:"),
		// 进程是"主动退出并给出明确错误码"的 ⇔ 退出码为 0，或 stdout 出现了
		// `hrp exit N`（后者只在 N≠0 时打印，见 reHrpExit 的说明）。
		// 两者都不满足 ⇒ panic 崩溃或被平台强杀。
		CleanExit: in.ExitCode == 0 || reHrpExit.MatchString(in.Stdout),
	}

	// 1) 解析两条流
	caseName, stepEvents, caseStarted, caseEnded := parseStderr(in.Stderr)
	_ = caseName
	pairs := parseStdout(in.Stdout)
	res.CaseStarted = caseStarted
	res.CaseEnded = caseEnded

	// 2) summary.json（若产出）
	var doc *summaryDoc
	if len(in.SummaryJSON) > 0 {
		doc = parseSummary(in.SummaryJSON)
	}
	if doc != nil {
		res.HasSummary = true
		res.Platform = doc.Platform
	}

	// 3) 归因（必须在步骤对齐之前：步骤状态要参考整例归因，
	//    例如"被平台强杀导致的步骤未结束"不能算被测系统的问题）
	//
	// errText 同时喂给判别器和错误摘要：判别器需要在 exit=1 内部
	// 区分「请求超时」与「连接失败」（实测 F13），两者退出码完全相同。
	errText := lastErrorText(in.Stderr)
	res.Attribution = Classify(ClassifyInput{
		ExitCode:    in.ExitCode,
		Panic:       res.Panic,
		TimedOut:    in.TimedOut,
		Canceled:    in.Canceled,
		ErrorText:   errText,
		CaseStarted: res.CaseStarted,
	})
	res.Status = StatusFromAttribution(res.Attribution)

	// 4) 对齐步骤
	res.Steps = buildSteps(in, stepEvents, pairs, doc, res.Attribution)

	// 5) 错误摘要
	res.ErrorMsg = pickErrorMsg(in, res, errText)
	return res
}

// buildSteps 把「声明步骤 + 引擎事件 + stdout 快照 + summary」对齐成步骤结果。
//
// 对齐策略：**按顺序前缀对齐**。
// 引擎事件里的步骤顺序与声明顺序一致，且 failfast 会在首次失败处中止，
// 因此"已启动的步骤"必然是"启用步骤"的一个前缀。
// 这样既不依赖步骤名唯一，也不需要引擎回传文件名。
func buildSteps(in Input, events []*stepEvent, pairs []stdoutPair, doc *summaryDoc, caseAttr string) []StepOutcome {
	decls := in.EnabledSteps
	out := make([]StepOutcome, 0, len(decls))

	var records []summaryRecord
	if doc != nil && len(doc.Details) > 0 {
		records = doc.Details[0].Records
	}

	pairCursor := 0
	recordCursor := 0

	for i, ev := range events {
		var decl StepDecl
		if i < len(decls) {
			decl = decls[i]
		}

		oc := StepOutcome{
			Seq:      decl.Seq,
			Name:     firstNonEmpty(decl.Name, ev.Name),
			StepType: stepTypeFromEngine(ev.Type, decl.StepType),
			Started:  true,
		}

		if ev.Ended {
			oc.ElapsedMs = ev.ElapsedMs
			oc.ExtractResult = ev.Export
			// 带 `run step end` 的失败 = 步骤没跑完（请求失败/变量未定义/hook 抛错），
			// 而不是"断言不通过" —— 断言不通过会 panic，走不到 end（实测 F8）。
			oc.Status = StepStatus(true, ev.Success)
			oc.ErrorMsg = ev.Error
			// 引擎给出的断言明细（仅"通过"路径存在）
			oc.Assertions = assertionFromLogLines(ev.Validations)
		} else {
			// ⭐ 启动了但没有配对 end ⇒ 引擎在这一步中断（F8）
			oc.InferredFailed = true
			if caseAttr == model.AttrSystemUnderTest {
				// 断言失败 panic：用例跑到了断言阶段，是被测行为不符预期
				oc.Status = model.StatusFail
				oc.ErrorMsg = "该步骤未正常结束：引擎在断言阶段崩溃（实测 F8），引擎未给出断言明细"
			} else {
				// 被平台强杀（超时/取消）等：步骤同样是"没跑完"，但根因在平台侧
				oc.Status = model.StatusError
				oc.ErrorMsg = "该步骤未正常结束：执行进程被中断"
			}
			oc.Assertions = rebuildAssertions(decl.Assertions, nil)
		}

		// --- 快照对齐 ---
		//
		// 判据是"这一步是否发出了 HTTP 请求"，只要 stdout 里还有报文块就消费一个。
		//
		// ⚠️ 不能因为 `ev.Error != ""` 就跳过：请求失败（连接被拒、请求超时）
		// 的步骤**是有报文块的**（引擎打印了请求行与请求头后才失败），
		// 而那个请求块恰恰是用户最需要看的 —— "它到底请求了哪个地址"。
		// 请求前就失败的步骤（变量未定义、hook 抛错）则**完全没有报文块**，
		// 由 `pairCursor < len(pairs)` 自然兜住，不会错位。
		//
		// 之所以不会因此错位：引擎默认 failfast，首次失败即中止整例，
		// 因此"跳过了报文块却还有后续步骤"的情况不可能出现（实测 F10）。
		if isHTTPStep(oc.StepType) && pairCursor < len(pairs) {
			p := pairs[pairCursor]
			pairCursor++
			oc.Request = p.Request
			oc.Response = p.Response
			if p.Request != nil {
				oc.FinalURL = reconstructURL(p.Request)
				oc.FinalURLSource = "reconstructed"
			}
		}

		// summary.json 是权威来源，能覆盖 stdout 重建的结果
		if recordCursor < len(records) {
			rec := records[recordCursor]
			recordCursor++
			applySummaryRecord(&oc, rec)
		}

		// 断言失败但 summary 没给明细 ⇒ 用快照重建
		if oc.InferredFailed {
			oc.Assertions = rebuildAssertions(decl.Assertions, oc.Response)
		}

		out = append(out, oc)
	}

	// 未启动的步骤标为 skipped
	for i := len(events); i < len(decls); i++ {
		out = append(out, StepOutcome{
			Seq:      decls[i].Seq,
			Name:     decls[i].Name,
			StepType: decls[i].StepType,
			Status:   model.StatusSkipped,
		})
	}
	return out
}

// applySummaryRecord 用 summary.json 的记录补全步骤结果。
func applySummaryRecord(oc *StepOutcome, rec summaryRecord) {
	if rec.Name != "" {
		oc.Name = rec.Name
	}
	if rec.StepType != "" {
		oc.StepType = rec.StepType
	}
	if rec.ElapsedMs > 0 {
		oc.ElapsedMs = rec.ElapsedMs
	}

	// 请求/响应：summary 的值比 stdout 重建的更权威（含完整 URL）
	if rec.Data.ReqResps.Request.URL != "" {
		oc.FinalURL = rec.Data.ReqResps.Request.URL
		oc.FinalURLSource = "summary"
	}
	if oc.Request == nil && (rec.Data.ReqResps.Request.Method != "" || rec.Data.ReqResps.Request.URL != "") {
		oc.Request = &RequestSnapshot{
			Method:  rec.Data.ReqResps.Request.Method,
			URL:     rec.Data.ReqResps.Request.URL,
			Headers: rec.Data.ReqResps.Request.Headers,
		}
	}
	if oc.Response == nil && rec.Data.ReqResps.Response.StatusCode != 0 {
		oc.Response = &ResponseSnapshot{
			StatusCode: rec.Data.ReqResps.Response.StatusCode,
			Proto:      rec.Data.ReqResps.Response.Proto,
			Headers:    rec.Data.ReqResps.Response.Headers,
			Body:       rec.Data.ReqResps.Response.Body,
		}
	}

	// summary 给了断言明细就直接用（比平台重建可信）
	if len(rec.Data.Validators) > 0 {
		items := make([]AssertionOutcome, 0, len(rec.Data.Validators))
		for i, v := range rec.Data.Validators {
			expectText, expectType := renderValue(v.Expect)
			checkText, checkType := renderValue(v.CheckValue)
			items = append(items, AssertionOutcome{
				Seq:             i + 1,
				CheckExpr:       v.Check,
				AssertMethod:    v.Assert,
				ExpectValue:     expectText,
				ExpectValueType: expectType,
				CheckValue:      checkText,
				CheckValueType:  checkType,
				Passed:          strings.EqualFold(v.CheckResult, "pass"),
				Rebuilt:         false,
				Msg:             v.Msg,
			})
		}
		oc.Assertions = items
	}

	// 失败步骤的错误文本只在 attachments 里（实测：失败记录没有 data 段）
	if msg := rec.ErrorText(); msg != "" {
		oc.ErrorMsg = msg
	}
}

// assertionFromLogLines 把 stderr 的 validate 行转成断言结果（引擎权威）。
func assertionFromLogLines(lines []logLine) []AssertionOutcome {
	if len(lines) == 0 {
		return nil
	}
	out := make([]AssertionOutcome, 0, len(lines))
	for i, l := range lines {
		expectText, expectType := renderValue(l.ExpectValue)
		checkText, checkType := renderValue(l.CheckValue)
		if l.ExpectValueType != "" {
			expectType = l.ExpectValueType
		}
		if l.CheckValueType != "" {
			checkType = l.CheckValueType
		}
		passed := false
		if l.Result != nil {
			passed = *l.Result
		}
		out = append(out, AssertionOutcome{
			Seq:             i + 1,
			CheckExpr:       firstNonEmpty(l.CheckExpr, strings.TrimPrefix(l.Message, evValidatePfx)),
			AssertMethod:    l.AssertMethod,
			ExpectValue:     expectText,
			ExpectValueType: expectType,
			CheckValue:      checkText,
			CheckValueType:  checkType,
			Passed:          passed,
			Rebuilt:         false,
		})
	}
	return out
}

// reconstructURL 由 stdout 请求快照重建最终请求 URL。
//
// 为什么要重建：断言失败时没有 summary.json，而"最终生效 URL"
// 恰恰是用户最需要看到的（hrp 会给无查询串的 URL 自动补尾斜杠，实测 F9）。
// 请求行里的 path 就是真实发出的路径，Host 头给出主机，两者拼起来即完整地址。
func reconstructURL(req *RequestSnapshot) string {
	if req == nil || req.Host == "" || req.Path == "" {
		return ""
	}
	scheme := "http"
	if req.Proto == "HTTP/2" || strings.Contains(strings.ToLower(req.Host), ":443") {
		scheme = "https"
	}
	return scheme + "://" + req.Host + req.Path
}

// isHTTPStep 判断该步骤类型是否会发出 HTTP 请求（据此决定是否消耗 stdout 快照）。
func isHTTPStep(stepType string) bool {
	switch stepType {
	case model.StepRequest, model.StepAPI:
		return true
	default:
		return false
	}
}

// stepTypeFromEngine 从引擎的 `type` 字段（形如 request-GET）还原步骤类型。
func stepTypeFromEngine(engineType, fallback string) string {
	if engineType == "" {
		return fallback
	}
	// "request-GET" → "request"；"testcase" → "testcase"
	if idx := strings.Index(engineType, "-"); idx > 0 {
		return engineType[:idx]
	}
	return engineType
}

// telemetryErrorPrefixes 是引擎自身遥测组件抛出的错误前缀。
//
// 为什么要单独认出来（实测 A20）：hrp 的 GA4 上报失败是用 **log.Error()** 打出来的
// （v4.3.6 源码 `hrp/internal/sdk/ga4.go`：
// `log.Error().Err(err).Msg("send GA4 event failed")`），
// 在 stderr 里与"用例失败"完全同形。不排除的后果实测过：
// 一条**成功**的用例，error_msg 却是
// `request GA4 failed: ... context deadline exceeded`。
//
// 只按**前缀**匹配，不去搜关键词：用户用例自己去请求 google-analytics.com
// 时报错文本是 `do request failed: Get "https://www.google-analytics.com/..."`，
// 前缀不同，不会被误伤。
//
// 注：这是第二道防线。第一道在 executor —— 默认注入 DISABLE_GA=true
// 从源头关掉遥测（见 C21）。只有用户刻意打开遥测时才会走到这里。
var telemetryErrorPrefixes = []string{
	"request GA4 failed",
	"send GA4 event failed",
	"init sentry sdk failed",
}

func isTelemetryNoise(errText string) bool {
	for _, p := range telemetryErrorPrefixes {
		if strings.HasPrefix(errText, p) {
			return true
		}
	}
	return false
}

// lastErrorText 取 stderr 中最后一条 error 级日志的 error 字段。
//
// 为什么是"最后一条"：failfast 会在首次失败处追加一条
// `abort running due to failfast setting: <原始错误>`，它是最完整的表述；
// 而前一条 `run step end` 的错误是被包裹过的短版本。
func lastErrorText(stderr string) string {
	last := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var l logLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue
		}
		if l.Error != "" && !isTelemetryNoise(l.Error) {
			last = l.Error
		}
	}
	return last
}

// pickErrorMsg 汇总一条对用户最有价值的错误描述。
func pickErrorMsg(in Input, res *Result, errText string) string {
	if in.Canceled {
		return "执行已被终止"
	}
	if in.TimedOut {
		return "执行超时，已被平台强制终止"
	}
	if res.Panic {
		return "引擎在断言阶段崩溃（panic）。最常见的原因是断言未通过 —— " +
			"这是引擎 v4.3.6 的已知缺陷，平台已用原始报文重建失败明细"
	}

	// 假绿（实测 F5）：退出码 0、零告警，但一条用例都没跑。
	// 这种时候 stderr 里没有任何 error 行，如果不说清楚，用户只会看到一个
	// "无错误信息"的失败，完全无从下手。
	if !res.CaseStarted && in.ExitCode == 0 {
		return "引擎以成功退出，但没有任何用例被执行 —— " +
			"用例文件很可能被引擎静默丢弃了（实测：畸形文件不报错、退出码 0）。" +
			"请检查 YAML 结构、字段名与缩进"
	}

	if errText != "" {
		return errText
	}

	// 兜底：stderr 里的 `Error: ...` 行
	for _, line := range strings.Split(in.Stderr, "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "Error: ") {
			return strings.TrimPrefix(s, "Error: ")
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
