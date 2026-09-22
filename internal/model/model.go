// Package model 定义平台的领域模型与 GORM 实体。
//
// 表结构与字段语义见 docs/数据库设计.md，本包是该文档的 Go 侧实现，两者必须同步修改。
package model

import (
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// 基础结构
// ---------------------------------------------------------------------------

// ⚠️ 布尔字段一律**不要**写 `default:` 标签。
//
// 原因是 GORM 的一个静默行为：只要字段带了 default 标签（HasDefaultValue
// 为真且默认值不是接口类型），GORM 就会在 INSERT 时**把该列整个排除掉**，
// 让数据库的默认值生效 —— 包括字段明明是显式的零值的情况。
//
// 后果举例（都真实发生过）：
//
//	TestStep.Enabled = false  → 落库成 true，用户禁用的步骤继续执行
//	CaseResult.CleanExit = false → 落库成 true，一次被强杀的执行看起来"正常退出"
//
// 两者都不会报错，只是把 false 变成了 true —— 属于最难发现的一类错误，
// 因为它给出的错是"看起来更正常"。
//
// 因此本包的约定是：布尔列的取值**只由应用层显式赋值**，
// DDL 里不带 DEFAULT（docs/数据库设计.md 同步遵循）。
// 回归测试见 internal/service/service_test.go 的 TestModel_布尔零值必须能落库。

// Base 是所有表的公共字段。
type Base struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SoftBase 用于配置类表：软删除，可恢复。
//
// 结果类表（RunRecord / CaseResult / ...）**不使用**软删除，
// 它们按保留策略物理清理，见数据库设计第 5 节。
type SoftBase struct {
	Base
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// ---------------------------------------------------------------------------
// 枚举与常量
// ---------------------------------------------------------------------------

// 用户角色。仅两档，不做 RBAC。
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// 执行目标类型。
const (
	TargetCase  = "case"
	TargetSuite = "suite"
	TargetPlan  = "plan"
)

// 触发来源。
const (
	TriggerManual = "manual"
	TriggerCron   = "cron"
	TriggerCI     = "ci"
)

// 执行状态。
//
// StatusError 表示"平台/环境层面的失败"，区别于 StatusFailed（有用例失败）。
// 用例数对账不一致时会强制升为 StatusError（见方案 6.4）。
const (
	RunQueued   = "queued"
	RunRunning  = "running"
	RunSuccess  = "success"
	RunFailed   = "failed"
	RunError    = "error"
	RunCanceled = "canceled"
)

// 用例/步骤结果状态。
const (
	StatusPending = "pending"
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusError   = "error"
	StatusSkipped = "skipped"
)

// 失败归因枚举。判别规则见 docs/数据库设计.md 第 4 节。
//
// ⚠️ 判别顺序不可调换：panic 必须先于 exit_code 判断，
// 因为断言失败与环境错误同为退出码 2（引擎实测结论 F8）。
const (
	AttrPass            = "pass"              // 通过
	AttrSystemUnderTest = "system_under_test" // 被测系统（断言未通过）
	AttrEnvironment     = "environment"       // 环境 / 网络 / 服务不可用
	AttrCaseIssue       = "case_issue"        // 用例或环境配置问题
	AttrOps             = "ops"               // 执行机运维问题（Python/插件环境）
	AttrTimeout         = "timeout"           // 超时
	AttrCanceled        = "canceled"          // 被终止
	AttrUnknown         = "unknown"           // 无法判定
)

// AttributionLabel 返回归因的中文短标签，供前端直接展示。
func AttributionLabel(a string) string {
	switch a {
	case AttrPass:
		return "通过"
	case AttrSystemUnderTest:
		return "被测系统"
	case AttrEnvironment:
		return "环境/服务"
	case AttrCaseIssue:
		return "用例问题"
	case AttrOps:
		return "执行机"
	case AttrTimeout:
		return "超时"
	case AttrCanceled:
		return "已终止"
	default:
		return "未知"
	}
}

// AttributionReason 返回归因的判断依据说明。
//
// 由服务端生成而非前端硬编码：归因规则来自引擎实测结论，
// 集中在一处才不会被前端文案版本差异带偏。
func AttributionReason(a string) string {
	switch a {
	case AttrPass:
		return "全部断言通过"
	case AttrSystemUnderTest:
		return "断言未通过。引擎在断言失败时会 panic（实测 F8），因此退出码为 2 且不产出报告"
	case AttrEnvironment:
		return "请求未能完成（连接被拒 / DNS 失败 / 服务不可用），退出码 1"
	case AttrCaseIssue:
		return "用例加载、解析或 hook 调用阶段失败，通常是引用缺失、变量未定义、函数名写错或用例格式问题"
	case AttrOps:
		return "执行机的 Python 或插件环境异常"
	case AttrTimeout:
		return "请求在 timeout 秒内未完成（引擎报 context deadline exceeded），或整例被平台强制终止"
	case AttrCanceled:
		return "被用户终止"
	default:
		return "无法从退出码与日志判定根因，请查看原始日志"
	}
}

// 用例优先级与状态。
const (
	PriorityP0 = "P0"
	PriorityP1 = "P1"
	PriorityP2 = "P2"
	PriorityP3 = "P3"

	CaseStatusDraft    = "draft"
	CaseStatusActive   = "active"
	CaseStatusDisabled = "disabled"
)

// 步骤类型。hrp 支持 7 种，M1 只开放 request。
//
// 引擎内部优先级（同时出现多个配置键时生效顺序）：
//
//	api > testcase > think_time > request > transaction > rendezvous > websocket
const (
	StepRequest     = "request"
	StepAPI         = "api"
	StepTestCase    = "testcase"
	StepTransaction = "transaction"
	StepRendezvous  = "rendezvous"
	StepThinkTime   = "think_time"
	StepWebSocket   = "websocket"
)

// 提取对象。引擎只支持这 5 类。
const (
	ExtractStatusCode = "status_code"
	ExtractProto      = "proto"
	ExtractHeaders    = "headers"
	ExtractCookies    = "cookies"
	ExtractBody       = "body"
)

// ValidExtractObjects 是提取对象的白名单，校验器使用。
var ValidExtractObjects = []string{
	ExtractStatusCode, ExtractProto, ExtractHeaders, ExtractCookies, ExtractBody,
}

// body 类型。
const (
	BodyTypeJSON = "json"
	BodyTypeForm = "form"
	BodyTypeRaw  = "raw"
	BodyTypeNone = "none"
)

// 环境配置。
const (
	VerifySSLDefault = false
)

// ---------------------------------------------------------------------------
// 子结构（作为 JSON 字段存储）
// ---------------------------------------------------------------------------

// RequestSpec 描述一个请求步骤。
//
// 注意 URL 的语义（引擎实测 A1）：
// hrp 在**最终 URL 不带查询串**时会自动给路径补一个 "/"，
// 带查询串（无论来自 inline ?x=y 还是 params）则不补。
// 平台在展示时必须用「最终生效 URL」，因此这里保留原始声明值，
// 实际值记录在 StepResult.FinalURL。
type RequestSpec struct {
	Method   string    `json:"method"`
	URL      string    `json:"url"`
	Headers  jsonx.Map `json:"headers,omitempty"`
	Params   jsonx.Map `json:"params,omitempty"`
	Body     jsonx.Any `json:"body,omitempty"`
	BodyType string    `json:"body_type,omitempty"`
	Timeout  int       `json:"timeout,omitempty"` // 单请求超时（秒），0 表示不设置
}

// ExtractItem 是一条变量提取配置。
type ExtractItem struct {
	Name       string `json:"name"`
	Object     string `json:"object"`     // 5 类之一
	Expression string `json:"expression"` // jmespath 或正则
}

// AssertItem 是一条断言配置。
//
// 内置校验器清单（引擎支持的全部）：
//
//	eq / equals / equal, lt / le / gt / ge / ne,
//	str_eq, len_eq / len_gt / len_ge / len_lt / len_le,
//	contains, contained_by, type_match, regex_match, startswith, endswith
type AssertItem struct {
	Check      string `json:"check"`                 // 检查表达式，如 status_code、body.args.foo1
	Assert     string `json:"assert"`                // 校验器方法
	Expect     any    `json:"expect,omitempty"`      // 期望值
	ExpectType string `json:"expect_type,omitempty"` // 类型提示（引擎会自行推断，这里仅用于前端回显）
	Msg        string `json:"msg,omitempty"`         // 自定义失败提示
}

// Hooks 是步骤钩子配置。
type Hooks struct {
	Setup    []string `json:"setup,omitempty"`
	Teardown []string `json:"teardown,omitempty"`
}

// CaseConfig 对应用例 YAML 的 config 段。
//
// 注意：base_url 自 hrp v4.1 起**已从 config 移到 .env**，
// 因此这里没有 base_url 字段——它属于 Environment。
type CaseConfig struct {
	Variables  jsonx.Map `json:"variables,omitempty"`
	Parameters jsonx.Map `json:"parameters,omitempty"`
	Headers    jsonx.Map `json:"headers,omitempty"`
	Verify     bool      `json:"verify"`
	Export     []string  `json:"export,omitempty"`
	Weight     int       `json:"weight,omitempty"`
}

// ---------------------------------------------------------------------------
// 实体
// ---------------------------------------------------------------------------

// User 是平台用户。
type User struct {
	SoftBase
	Username string `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Password string `gorm:"size:128;not null" json:"-"`
	Nickname string `gorm:"size:64;not null;default:''" json:"nickname"`
	Role     string `gorm:"size:16;not null;default:'member'" json:"role"`
	Enabled  bool   `gorm:"not null" json:"enabled"`
}

// TableName 显式指定表名，避免 GORM 复数化。
func (User) TableName() string { return "user" }

// Project 是项目，code 同时作为工作区目录名。
type Project struct {
	SoftBase
	Code          string `gorm:"size:64;uniqueIndex;not null" json:"code"`
	Name          string `gorm:"size:128;not null" json:"name"`
	Description   string `gorm:"size:512;not null;default:''" json:"description"`
	WorkspacePath string `gorm:"size:512;not null;default:''" json:"workspace_path"`
	HrpVersion    string `gorm:"size:32;not null;default:'v4.3.6'" json:"hrp_version"`
	OwnerID       uint64 `gorm:"not null;default:0" json:"owner_id"`
}

// Environment 是环境，编译时渲染为工作区的 .env 文件。
type Environment struct {
	SoftBase
	ProjectID     uint64    `gorm:"not null;index" json:"project_id"`
	Name          string    `gorm:"size:64;not null" json:"name"`
	BaseURL       string    `gorm:"size:512;not null;default:''" json:"base_url"`
	Environs      jsonx.Map `gorm:"type:text" json:"environs"`
	GlobalHeaders jsonx.Map `gorm:"type:text" json:"global_headers"`
	VerifySSL     bool      `gorm:"not null" json:"verify_ssl"`
	IsDefault     bool      `gorm:"not null" json:"is_default"`
}

// APIDef 是接口定义库中的接口（M2 启用，M1 建表）。
type APIDef struct {
	SoftBase
	ProjectID   uint64    `gorm:"not null;index" json:"project_id"`
	Code        string    `gorm:"size:64;not null" json:"code"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Method      string    `gorm:"size:16;not null;default:'GET'" json:"method"`
	Path        string    `gorm:"size:512;not null;default:''" json:"path"`
	Headers     jsonx.Map `gorm:"type:text" json:"headers"`
	Params      jsonx.Map `gorm:"type:text" json:"params"`
	Body        jsonx.Any `gorm:"type:text" json:"body"`
	BodyType    string    `gorm:"size:16;not null;default:'json'" json:"body_type"`
	Description string    `gorm:"size:512;not null;default:''" json:"description"`
	Source      string    `gorm:"size:16;not null;default:'manual'" json:"source"`
}

// TestCase 是用例。
//
// 两条硬约束（都来自引擎实测）：
//
//   - Name 在同一项目内唯一 —— 引擎的 summary.json 以 config.name 作为唯一标识，
//     重名会让结果无法区分（实测 F11）。
//   - Code 会作为编译后的文件名片段，必须是标识符，防止路径穿越。
type TestCase struct {
	SoftBase
	ProjectID      uint64    `gorm:"not null;index" json:"project_id"`
	Code           string    `gorm:"size:64;not null" json:"code"`
	Name           string    `gorm:"size:128;not null" json:"name"`
	Module         string    `gorm:"size:64;not null;default:''" json:"module"`
	Priority       string    `gorm:"size:4;not null;default:'P1'" json:"priority"`
	Tags           string    `gorm:"size:255;not null;default:''" json:"tags"`
	Status         string    `gorm:"size:16;not null;default:'active'" json:"status"`
	Description    string    `gorm:"size:512;not null;default:''" json:"description"`
	Config         jsonx.Any `gorm:"type:text" json:"config"`
	RequestTimeout int       `gorm:"not null;default:0" json:"request_timeout"`
	CaseTimeout    int       `gorm:"not null;default:0" json:"case_timeout"`
	OwnerID        uint64    `gorm:"not null;default:0" json:"owner_id"`
	LastRunID      uint64    `gorm:"not null;default:0" json:"last_run_id"`
	LastStatus     string    `gorm:"size:16;not null;default:''" json:"last_status"`

	// Steps 仅用于关联查询的装配，不落库。
	Steps []TestStep `gorm:"-" json:"steps,omitempty"`
}

// TestStep 是用例步骤。
type TestStep struct {
	SoftBase
	CaseID    uint64                   `gorm:"not null;index" json:"case_id"`
	Seq       int                      `gorm:"not null;default:0" json:"seq"`
	StepType  string                   `gorm:"size:16;not null;default:'request'" json:"step_type"`
	Name      string                   `gorm:"size:128;not null" json:"name"`
	RefAPIID  uint64                   `gorm:"not null;default:0" json:"ref_api_id"`
	RefCaseID uint64                   `gorm:"not null;default:0" json:"ref_case_id"`
	Request   jsonx.Any                `gorm:"type:text" json:"request"`
	Variables jsonx.Map                `gorm:"type:text" json:"variables"`
	Extract   jsonx.Slice[ExtractItem] `gorm:"type:text" json:"extract"`
	Validate  jsonx.Slice[AssertItem]  `gorm:"type:text" json:"validate"`
	Hooks     jsonx.Any                `gorm:"type:text" json:"hooks"`
	Enabled   bool                     `gorm:"not null" json:"enabled"`
	WsOpType  string                   `gorm:"size:16;not null;default:''" json:"ws_op_type"`
	WsPayload jsonx.Any                `gorm:"type:text" json:"ws_payload"`
}

// ParamDataset 是参数化数据集（M3 启用）。
type ParamDataset struct {
	SoftBase
	ProjectID uint64    `gorm:"not null;index" json:"project_id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Source    string    `gorm:"size:16;not null;default:'list'" json:"source"`
	Inline    jsonx.Any `gorm:"type:text" json:"inline"`
	CsvPath   string    `gorm:"size:512;not null;default:''" json:"csv_path"`
	Strategy  string    `gorm:"size:16;not null;default:'sequential'" json:"strategy"`
	Limit     int       `gorm:"not null;default:0" json:"limit"`
}

// TestSuite 是用例集（逻辑分组）。与测试计划是两个概念。
type TestSuite struct {
	SoftBase
	ProjectID   uint64 `gorm:"not null;index" json:"project_id"`
	Code        string `gorm:"size:64;not null" json:"code"`
	Name        string `gorm:"size:128;not null" json:"name"`
	Description string `gorm:"size:512;not null;default:''" json:"description"`
	ExecuteMode string `gorm:"size:16;not null;default:'sequential'" json:"execute_mode"`
	OnFailure   string `gorm:"size:16;not null;default:'abort'" json:"on_failure"`
	Timeout     int    `gorm:"not null;default:0" json:"timeout"`
}

// SuiteCase 是用例集成员，带执行顺序。
type SuiteCase struct {
	Base
	SuiteID uint64 `gorm:"not null;index" json:"suite_id"`
	CaseID  uint64 `gorm:"not null" json:"case_id"`
	Seq     int    `gorm:"not null;default:0" json:"seq"`
}

// TestPlan 是测试计划。定时任务不是独立概念，而是它的属性。
type TestPlan struct {
	SoftBase
	ProjectID      uint64 `gorm:"not null;index" json:"project_id"`
	Name           string `gorm:"size:128;not null" json:"name"`
	SuiteIDs       string `gorm:"size:255;not null;default:''" json:"suite_ids"`
	EnvID          uint64 `gorm:"not null;default:0" json:"env_id"`
	TriggerType    string `gorm:"size:16;not null;default:'manual'" json:"trigger_type"`
	CronExpr       string `gorm:"size:64;not null;default:''" json:"cron_expr"`
	NotifyConfigID uint64 `gorm:"not null;default:0" json:"notify_config_id"`
	Timeout        int    `gorm:"not null;default:0" json:"timeout"`
	Enabled        bool   `gorm:"not null" json:"enabled"`
}

// RunRecord 是一次执行的汇总层记录。
//
// ⭐ 关键设计（引擎实测 A2）：平台对每个用例起一个独立子进程，
// 因此一次运行会产生多个退出码。归因与 exit_code 落在 CaseResult，
// 本表只保存汇总结果（Attribution 为 JSON 计数）。
type RunRecord struct {
	Base
	ProjectID         uint64     `gorm:"not null;index" json:"project_id"`
	TargetType        string     `gorm:"size:16;not null" json:"target_type"`
	TargetID          uint64     `gorm:"not null;default:0" json:"target_id"`
	TargetName        string     `gorm:"size:128;not null;default:''" json:"target_name"`
	EnvID             uint64     `gorm:"not null;default:0" json:"env_id"`
	TriggerType       string     `gorm:"size:16;not null;default:'manual'" json:"trigger_type"`
	TriggerBy         uint64     `gorm:"not null;default:0" json:"trigger_by"`
	Status            string     `gorm:"size:16;not null;default:'queued'" json:"status"`
	Total             int        `gorm:"not null;default:0" json:"total"`
	Passed            int        `gorm:"not null;default:0" json:"passed"`
	Failed            int        `gorm:"not null;default:0" json:"failed"`
	Error             int        `gorm:"not null;default:0" json:"error"`
	Skipped           int        `gorm:"not null;default:0" json:"skipped"`
	DurationMs        int64      `gorm:"not null;default:0" json:"duration_ms"`
	Attribution       jsonx.Map  `gorm:"type:text" json:"attribution"`
	ExpectedCaseCount int        `gorm:"not null;default:0" json:"expected_case_count"`
	ActualCaseCount   int        `gorm:"not null;default:0" json:"actual_case_count"`
	CountMismatch     bool       `gorm:"not null" json:"count_mismatch"`
	WorkspacePath     string     `gorm:"size:512;not null;default:''" json:"workspace_path"`
	LogPath           string     `gorm:"size:512;not null;default:''" json:"log_path"`
	ErrorMsg          string     `gorm:"size:1024;not null;default:''" json:"error_msg"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`

	// 非落库字段，便于列表接口一次性带上环境名。
	EnvName string `gorm:"-" json:"env_name,omitempty"`
}

// CaseResult 是用例级结果。
//
// ExitCode / Panic / CleanExit 三个字段共同构成归因的第一手证据，
// 判别顺序见 docs/数据库设计.md 第 4 节。
type CaseResult struct {
	Base
	RunID       uint64 `gorm:"not null;index" json:"run_id"`
	CaseID      uint64 `gorm:"not null;default:0" json:"case_id"`
	CaseCode    string `gorm:"size:64;not null;default:''" json:"case_code"`
	ConfigName  string `gorm:"size:128;not null;default:''" json:"config_name"`
	Seq         int    `gorm:"not null;default:0" json:"seq"`
	Status      string `gorm:"size:16;not null;default:'pending'" json:"status"`
	ExitCode    int    `gorm:"not null;default:0" json:"exit_code"`
	Panic       bool   `gorm:"not null" json:"panic"`
	CleanExit   bool   `gorm:"not null" json:"clean_exit"`
	Attribution string `gorm:"size:32;not null;default:''" json:"attribution"`
	DurationMs  int64  `gorm:"not null;default:0" json:"duration_ms"`
	StepTotal   int    `gorm:"not null;default:0" json:"step_total"`
	StepPassed  int    `gorm:"not null;default:0" json:"step_passed"`
	ErrorMsg    string `gorm:"size:1024;not null;default:''" json:"error_msg"`
	StdoutPath  string `gorm:"size:512;not null;default:''" json:"-"`
	StderrPath  string `gorm:"size:512;not null;default:''" json:"-"`
	SummaryPath string `gorm:"size:512;not null;default:''" json:"-"`
	ReportPath  string `gorm:"size:512;not null;default:''" json:"-"`

	// 非落库字段
	HasSummary bool         `gorm:"-" json:"has_summary"`
	HasReport  bool         `gorm:"-" json:"has_report"`
	Steps      []StepResult `gorm:"-" json:"steps,omitempty"`
}

// StepResult 是步骤级结果。
type StepResult struct {
	Base
	RunID            uint64    `gorm:"not null;index" json:"run_id"`
	CaseResultID     uint64    `gorm:"not null;index" json:"case_result_id"`
	CaseCode         string    `gorm:"size:64;not null;default:''" json:"case_code"`
	Seq              int       `gorm:"not null;default:0" json:"seq"`
	StepName         string    `gorm:"size:128;not null;default:''" json:"step_name"`
	StepType         string    `gorm:"size:16;not null;default:'request'" json:"step_type"`
	Status           string    `gorm:"size:16;not null;default:'pending'" json:"status"`
	InferredFailed   bool      `gorm:"not null" json:"inferred_failed"`
	FinalURL         string    `gorm:"size:1024;not null;default:''" json:"final_url"`
	RequestSnapshot  jsonx.Any `gorm:"type:text" json:"request_snapshot"`
	ResponseSnapshot jsonx.Any `gorm:"type:text" json:"response_snapshot"`
	ElapsedMs        int64     `gorm:"not null;default:0" json:"elapsed_ms"`
	ExtractResult    jsonx.Map `gorm:"type:text" json:"extract_result"`
	ErrorMsg         string    `gorm:"size:1024;not null;default:''" json:"error_msg"`

	// 非落库字段
	Assertions []AssertionResult `gorm:"-" json:"assertions"`
}

// AssertionResult 是断言级结果。
//
// Rebuilt 为 true 表示该行不是引擎产出的，而是平台用
// 「用例声明的断言 + stdout 报文快照」自行比对重建的（引擎实测 F8）。
// 前端必须显式标注，避免用户误以为引擎给出了该判定。
type AssertionResult struct {
	Base
	RunID           uint64 `gorm:"not null;index" json:"run_id"`
	CaseResultID    uint64 `gorm:"not null;index" json:"case_result_id"`
	StepResultID    uint64 `gorm:"not null;index" json:"step_result_id"`
	Seq             int    `gorm:"not null;default:0" json:"seq"`
	CheckExpr       string `gorm:"size:512;not null;default:''" json:"check_expr"`
	AssertMethod    string `gorm:"size:32;not null;default:''" json:"assert_method"`
	ExpectValue     string `gorm:"size:1024;not null;default:''" json:"expect_value"`
	ExpectValueType string `gorm:"size:32;not null;default:''" json:"expect_value_type"`
	CheckValue      string `gorm:"size:1024;not null;default:''" json:"check_value"`
	CheckValueType  string `gorm:"size:32;not null;default:''" json:"check_value_type"`
	Passed          bool   `gorm:"not null" json:"passed"`
	Rebuilt         bool   `gorm:"not null" json:"rebuilt"`
	Msg             string `gorm:"size:512;not null;default:''" json:"msg"`
}

// APIToken 是 CI 令牌（M2 启用）。
type APIToken struct {
	Base
	ProjectID  uint64     `gorm:"not null;default:0" json:"project_id"`
	Name       string     `gorm:"size:64;not null" json:"name"`
	Token      string     `gorm:"size:128;not null;uniqueIndex" json:"token"`
	Scope      string     `gorm:"size:255;not null;default:''" json:"scope"`
	ExpireAt   *time.Time `json:"expire_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// NotifyConfig 是通知配置（M2 启用）。
type NotifyConfig struct {
	SoftBase
	ProjectID uint64    `gorm:"not null;index" json:"project_id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Channel   string    `gorm:"size:16;not null;default:'webhook'" json:"channel"`
	Config    jsonx.Any `gorm:"type:text" json:"config"`
	OnSuccess bool      `gorm:"not null" json:"on_success"`
}

// ---------------------------------------------------------------------------
// 表名映射
// ---------------------------------------------------------------------------

// 显式声明表名，而不是依赖 GORM 的命名推断。
// 理由：表名是 docs/数据库设计.md 的对外契约，显式声明可以避免
// GORM 版本变化导致命名策略漂移，也让「模型 ↔ 表」的对应关系一眼可见。

func (Project) TableName() string         { return "project" }
func (Environment) TableName() string     { return "environment" }
func (APIDef) TableName() string          { return "api_def" }
func (TestCase) TableName() string        { return "test_case" }
func (TestStep) TableName() string        { return "test_step" }
func (ParamDataset) TableName() string    { return "param_dataset" }
func (TestSuite) TableName() string       { return "test_suite" }
func (SuiteCase) TableName() string       { return "suite_case" }
func (TestPlan) TableName() string        { return "test_plan" }
func (RunRecord) TableName() string       { return "run_record" }
func (CaseResult) TableName() string      { return "case_result" }
func (StepResult) TableName() string      { return "step_result" }
func (AssertionResult) TableName() string { return "assertion_result" }
func (APIToken) TableName() string        { return "api_token" }
func (NotifyConfig) TableName() string    { return "notify_config" }

// AllModels 返回需要迁移的全部实体，顺序即建表顺序。
func AllModels() []any {
	return []any{
		&User{},
		&Project{},
		&Environment{},
		&APIDef{},
		&TestCase{},
		&TestStep{},
		&ParamDataset{},
		&TestSuite{},
		&SuiteCase{},
		&TestPlan{},
		&RunRecord{},
		&CaseResult{},
		&StepResult{},
		&AssertionResult{},
		&APIToken{},
		&NotifyConfig{},
	}
}
