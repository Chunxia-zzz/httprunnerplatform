// Package parser 把 hrp 引擎的原始输出翻译成结构化结果。
//
// 这是全系统对引擎行为最敏感的一个包，其设计完全由 docs/引擎实测记录.md
// 的实测结论驱动。三条最关键的事实：
//
//  1. **双流分离**：引擎日志在 stderr（JSON Lines），请求/响应明细在 stdout（纯文本）。
//     两者必须分别解析，缺一不可。
//
//  2. **断言失败会让引擎 panic**（F8）：退出码 2，且 **summary.json / report.html
//     完全不产出**，stderr 里也**没有任何断言明细**。
//     因此最常见的失败类型反而拿不到引擎侧结果，必须由平台自行重建。
//
//  3. **panic 会跳过 `hrp exit N` 的打印**：可作为"是否正常退出"的旁证。
package parser

import "github.com/Chunxia-zzz/httprunnerplatform/internal/model"

// Input 是一次执行的原始输入。
type Input struct {
	// ExitCode 是用例子进程的退出码。
	ExitCode int
	// Stdout / Stderr 是两条流的完整原文（未做任何裁剪）。
	Stdout string
	Stderr string
	// TimedOut / Canceled 由执行器标记，优先级高于退出码。
	TimedOut bool
	Canceled bool
	// EnabledSteps 是该用例被启用且会被执行的步骤，按 seq 升序。
	// 用于两件事：断言重建、以及把引擎事件对齐回声明步骤。
	EnabledSteps []StepDecl
	// SummaryJSON 是 `-s` 产出的 summary.json 原文。
	//
	// ⚠️ 断言失败（panic）时**不会有这个文件**（实测 F5/F8），
	// 因此它是"锦上添花"而不是"必需品"：有则用引擎的权威数据，
	// 没有则靠 stdout 报文快照 + 声明断言重建。
	SummaryJSON []byte
	// DurationMs 是墙钟耗时（引擎日志里没有总耗时）。
	DurationMs int64
}

// StepDecl 是一个被声明的步骤，供解析器对齐与重建断言。
type StepDecl struct {
	Seq      int
	Name     string
	StepType string
	// Method 是 request 步骤的 HTTP 方法（仅用于展示与快照校验）。
	Method string
	// URL 是 YAML 中声明的 URL（未经引擎归一化）。
	// 与 StepOutcome.FinalURL 对比即可暴露 F9（尾斜杠自动补齐）。
	URL string
	// Assertions 是该步骤声明的断言，断言失败时用于重建。
	Assertions []model.AssertItem
}

// Result 是一次执行的解析结果。
type Result struct {
	Status      string // model.StatusPass / StatusFail / StatusError
	ExitCode    int
	Panic       bool
	CleanExit   bool
	Attribution string
	ErrorMsg    string

	Steps []StepOutcome

	// CaseStarted / CaseEnded 表示引擎日志里是否出现过
	// `run testcase start` / `run testcase end`。
	//
	// ⭐ 用途是发现「假绿」（实测 F5）：**畸形用例文件会被引擎静默丢弃** ——
	// 不执行、不报错、不告警，而且**退出码是 0**。
	// 只看退出码会把这种情况报成"通过"，而实际上一个请求都没发出去。
	//
	// 因为平台是一个用例一个子进程，"用例数对账"就退化成
	// "这条用例到底跑没跑"，两个布尔量就够表达了。
	CaseStarted bool
	CaseEnded   bool

	// Platform 来自 summary.json，可能为空。
	Platform PlatformInfo
	// HasSummary 标记本次是否拿到了 summary.json。
	HasSummary bool
}

// MissedRun 报告「实际上一个用例都没跑起来」。
//
// 判据刻意用 CaseStarted 而不是退出码：退出码在假绿场景里是 0（实测 F5）。
func (r *Result) MissedRun() bool { return !r.CaseStarted }

// FalseGreen 报告「假绿」：退出码为 0、看起来成功，但用例根本没启动。
//
// 必须与普通的"失败"区分开：假绿的成因是文件格式问题（用例压根没被加载），
// 而不是被测系统的问题。若不当成异常上报，用户会得到一个绿色的假象。
//
// 判据只有 CaseStarted —— **刻意不看 len(Steps)**：
// 平台调用时总会把已声明的步骤作为 EnabledSteps 传进来，未执行的步骤会被
// 回填成 skipped，所以 Steps 在任何情况下都非空。若把 len(Steps) == 0
// 也写进条件，这个函数就会在最该命中的时候返回 false，
// 与 Classify 给出的 case_issue 自相矛盾。
func (r *Result) FalseGreen() bool {
	return r.ExitCode == 0 && !r.CaseStarted
}

// PlatformInfo 是 summary.json 里的引擎信息。
type PlatformInfo struct {
	HttprunnerVersion string `json:"httprunner_version"`
	GoVersion         string `json:"go_version"`
	Platform          string `json:"platform"`
}

// StepOutcome 是一个步骤的解析结果。
type StepOutcome struct {
	Seq      int
	Name     string
	StepType string
	Status   string

	// InferredFailed 表示该步骤"启动了但没正常结束"。
	// 这是 F8（断言失败 panic）的唯一可靠识别方式。
	InferredFailed bool

	ElapsedMs     int64
	ExtractResult map[string]any

	// FinalURL 是引擎实际请求的地址。
	// 优先取自 summary.json；否则由 stdout 快照的 Host + 请求行重建。
	FinalURL string
	// FinalURLSource 说明 FinalURL 的来源，便于前端标注可信度：
	// "summary"（引擎权威）/ "reconstructed"（平台重建）/ ""（未知）。
	FinalURLSource string

	Request  *RequestSnapshot
	Response *ResponseSnapshot

	Assertions []AssertionOutcome
	ErrorMsg   string

	// Started 表示引擎确实进入过这个步骤；未进入的步骤会被标为 skipped。
	Started bool
}

// RequestSnapshot 是 stdout 中解析出的请求快照。
type RequestSnapshot struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Proto   string            `json:"proto,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// ResponseSnapshot 是 stdout 中解析出的响应快照。
type ResponseSnapshot struct {
	StatusCode int               `json:"status_code,omitempty"`
	Proto      string            `json:"proto,omitempty"`
	StatusText string            `json:"status_text,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Transport  string            `json:"transport,omitempty"`
}

// AssertionOutcome 是一条断言的解析结果。
//
// Rebuilt 为 true 表示这一行不是引擎给出的，而是平台用
// 「声明步骤里的断言 + stdout 报文快照」自行比对得到的（F8 补偿）。
// 前端必须显式标注，不能让用户误以为这是引擎的判定。
type AssertionOutcome struct {
	Seq             int
	CheckExpr       string
	AssertMethod    string
	ExpectValue     string
	ExpectValueType string
	CheckValue      string
	CheckValueType  string
	Passed          bool
	Rebuilt         bool
	Msg             string
}
