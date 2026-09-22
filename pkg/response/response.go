// Package response 定义平台统一的 API 响应体与业务错误码。
//
// 契约见 docs/接口契约.md 第 0 节：
//
//	{ "code": 0, "message": "ok", "data": {...} }
//
// code == 0 表示业务成功。HTTP 状态码与业务 code 同时使用：
// 参数格式错误走 400，业务校验失败走 200 + 非 0 code。
package response

import "fmt"

// Body 是统一响应体。
type Body struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// 业务错误码。与 docs/接口契约.md 第 0.2 节的表一一对应。
const (
	CodeOK = 0

	CodeBadParam        = 40000 // 参数错误
	CodeConflict        = 40001 // 资源名称/标识已存在
	CodeNotFound        = 40002 // 资源不存在
	CodeInUse           = 40003 // 资源被占用，无法删除
	CodeInvalidIdent    = 40004 // 非法标识符
	CodeVersionConflict = 40010 // 变更冲突（乐观锁，M1 未启用）

	CodeUnauthorized = 40100 // 未登录
	CodeForbidden    = 40300 // 无权限

	CodeInternal      = 50000 // 服务端内部错误
	CodeEngineDown    = 50001 // 引擎不可用
	CodeWorkspaceFail = 50002 // 工作区操作失败
	CodeCompileFail   = 50003 // 编译失败
	CodeValidateFail  = 50004 // 校验未通过
	CodeExecutorFail  = 50005 // 执行器错误
	CodeReportMissing = 50006 // 报告不存在
)

// Error 是携带业务码的错误类型，供 service 层向上抛出。
type Error struct {
	Code    int
	Message string
	Data    any
	Err     error // 内部原因，不返回给客户端
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// Unwrap 支持 errors.Is / errors.As 链式判断。
func (e *Error) Unwrap() error { return e.Err }

// New 构造一个业务错误。
func New(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf 构造一个带格式化消息的业务错误。
func Newf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap 在业务错误上附加内部原因。
func Wrap(code int, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

// WithData 附加 data 字段（例如校验器的 issue 列表）。
func (e *Error) WithData(data any) *Error {
	e.Data = data
	return e
}

// FieldError 用于参数绑定失败时定位字段。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
