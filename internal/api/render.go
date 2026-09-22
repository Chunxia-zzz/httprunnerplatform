// Package api 提供 HTTP 接口层。
//
// 契约见 docs/接口契约.md。本包只负责参数绑定、调用 service、
// 把结果翻译成统一响应体；业务逻辑一律放在 internal/service。
package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// OK 返回成功响应（HTTP 200 + code 0）。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, response.Body{Code: response.CodeOK, Message: "ok", Data: data})
}

// Fail 返回业务失败响应。
//
// HTTP 状态码与业务 code 分开：业务校验失败（如重名、校验未通过）
// 依然返回 HTTP 200，只是 code 非 0，这样前端只需处理一套逻辑。
func Fail(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, response.Body{Code: code, Message: message, Data: nil})
}

// FailWithData 返回带 data 的业务失败响应（如校验器的 issue 列表）。
func FailWithData(c *gin.Context, code int, message string, data any) {
	c.JSON(http.StatusOK, response.Body{Code: code, Message: message, Data: data})
}

// AbortWithStatus 返回指定 HTTP 状态码的错误（用于 400/401/403/404/500）。
func AbortWithStatus(c *gin.Context, httpStatus, code int, message string) {
	c.AbortWithStatusJSON(httpStatus, response.Body{Code: code, Message: message, Data: nil})
}

// WriteError 把 service 层返回的错误翻译成 HTTP 响应。
//
// 这是整个 handler 层唯一的错误出口，保证响应格式一致。
func WriteError(c *gin.Context, err error) {
	if err == nil {
		return
	}

	var bizErr *response.Error
	if errors.As(err, &bizErr) {
		status := httpStatusFor(bizErr.Code)
		if status == http.StatusOK {
			if bizErr.Data != nil {
				FailWithData(c, bizErr.Code, bizErr.Message, bizErr.Data)
				return
			}
			Fail(c, bizErr.Code, bizErr.Message)
			return
		}
		AbortWithStatus(c, status, bizErr.Code, bizErr.Message)
		return
	}

	// 非业务错误：不把内部细节暴露给客户端。
	_ = c.Error(err)
	AbortWithStatus(c, http.StatusInternalServerError, response.CodeInternal, "服务端内部错误")
}

// httpStatusFor 把业务码映射到 HTTP 状态码。
func httpStatusFor(code int) int {
	switch code {
	case response.CodeBadParam, response.CodeInvalidIdent:
		return http.StatusBadRequest
	case response.CodeUnauthorized:
		return http.StatusUnauthorized
	case response.CodeForbidden:
		return http.StatusForbidden
	case response.CodeNotFound:
		return http.StatusNotFound
	default:
		// 其余业务码（含冲突、校验未通过、引擎相关）走 HTTP 200，
		// 由 code 字段表达语义。
		return http.StatusOK
	}
}

// Pagination 是分页请求参数。
type Pagination struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

// Normalize 补齐分页默认值并做上限保护。
func (p *Pagination) Normalize() {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
}

// Offset 返回 SQL OFFSET。
func (p Pagination) Offset() int { return (p.Page - 1) * p.PageSize }

// PageData 是分页响应的统一结构。
type PageData struct {
	List     any   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// OKPage 返回分页成功响应，自动把 nil 列表规范成空数组。
func OKPage(c *gin.Context, list any, total int64, p Pagination) {
	if list == nil {
		list = []any{}
	}
	OK(c, PageData{List: list, Total: total, Page: p.Page, PageSize: p.PageSize})
}

// ParseIDParam 解析路径参数中的 uint64 ID。
//
// 校验失败时已写入响应，调用方直接 return 即可。
func ParseIDParam(c *gin.Context, name string) (uint64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		Fail(c, response.CodeBadParam, "路径参数 "+name+" 非法")
		return 0, false
	}
	return id, true
}
