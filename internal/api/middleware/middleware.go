// Package middleware 提供 gin 中间件。
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// PrincipalKey 是 gin.Context 中存放登录主体的键。
const PrincipalKey = "hrp_principal"

// abortJSON 写入统一响应体并中断后续处理。
//
// 这里不调用 internal/api 的辅助函数，是为了避免 middleware 反向依赖父包
// （父包 router.go 会 import 本包，形成循环）。
func abortJSON(c *gin.Context, httpStatus, code int, message string) {
	c.AbortWithStatusJSON(httpStatus, response.Body{Code: code, Message: message, Data: nil})
}

// Recovery 捕获 panic，记录日志并返回统一 500 响应。
//
// 平台会执行用户提供的 YAML 并解析引擎输出，任何一处越界都可能 panic；
// 一个用例触发 panic 不应该拖垮整个服务。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logx.L().Error().
			Str("path", c.Request.URL.Path).
			Str("method", c.Request.Method).
			Interface("panic", recovered).
			Msg("请求处理发生 panic")
		abortJSON(c, http.StatusInternalServerError, response.CodeInternal, "服务端内部错误")
	})
}

// AccessLog 记录访问日志。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		ev := logx.L().Info()
		if c.Writer.Status() >= 500 {
			ev = logx.L().Error()
		} else if c.Writer.Status() >= 400 {
			ev = logx.L().Warn()
		}
		ev.
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Int64("elapsed_ms", time.Since(start).Milliseconds()).
			Str("ip", c.ClientIP()).
			Msg("http request")
	}
}

// CORS 允许前端开发服务器跨域访问。
//
// 生产环境前端由 Go 二进制内嵌（同源），因此 allowedOrigins 为空时
// 不添加任何 CORS 头——默认不放开，避免误配。
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allow[strings.TrimSpace(o)] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allow[origin]; ok {
				h := c.Writer.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Requested-With")
				h.Set("Access-Control-Max-Age", "600")
				h.Add("Vary", "Origin")
			}
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// RequireAuth 要求已登录。
func RequireAuth(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(auth.CookieName)
		if err != nil || token == "" {
			abortJSON(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
			return
		}
		p, ok := store.Get(token)
		if !ok {
			abortJSON(c, http.StatusUnauthorized, response.CodeUnauthorized, "登录已过期，请重新登录")
			return
		}
		c.Set(PrincipalKey, p)
		c.Next()
	}
}

// RequireAdmin 要求管理员。
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := CurrentPrincipal(c)
		if !ok || !p.IsAdmin() {
			abortJSON(c, http.StatusForbidden, response.CodeForbidden, "需要管理员权限")
			return
		}
		c.Next()
	}
}

// CurrentPrincipal 取出当前登录主体。
func CurrentPrincipal(c *gin.Context) (auth.Principal, bool) {
	v, ok := c.Get(PrincipalKey)
	if !ok {
		return auth.Principal{}, false
	}
	p, ok := v.(auth.Principal)
	return p, ok
}
