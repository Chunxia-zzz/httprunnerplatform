package api

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api/middleware"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
)

// ---------------------------------------------------------------------------
// 共用的绑定工具
// ---------------------------------------------------------------------------

// pageQuery 是所有列表接口共用的查询参数。
type pageQuery struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

// toPage 转成 service 层的分页参数（已在 service 侧做默认值与上限保护）。
func (q pageQuery) toPage() service.Page {
	return service.Page{Page: q.Page, PageSize: q.PageSize}
}

// currentUserID 返回当前登录用户 ID。未登录时中间件已经拦掉了。
func currentUserID(c *gin.Context) uint64 {
	if p, ok := middleware.CurrentPrincipal(c); ok {
		return p.UserID
	}
	return 0
}

// currentIsAdmin 判断当前用户是否管理员。
//
// 用途只有一处：环境敏感值是否掩码。M1 不做角色差异化，
// 但"非管理员看不到别人的 token"这件事必须从第一天就成立 ——
// 事后补掩码意味着敏感值已经写进过日志与响应体了。
func currentIsAdmin(c *gin.Context) bool {
	if p, ok := middleware.CurrentPrincipal(c); ok {
		return p.IsAdmin()
	}
	return false
}

// queryUint64 解析可选的数值查询参数。
func queryUint64(c *gin.Context, name string) uint64 {
	v, err := strconv.ParseUint(c.Query(name), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
