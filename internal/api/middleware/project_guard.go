package middleware

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// ProjectAccessChecker 由 MemberService 实现（用接口避免 middleware ↔ service 循环依赖）。
type ProjectAccessChecker interface {
	ProjectOfResource(kind string, id uint64) (uint64, bool)
	CanAccess(projectID, userID uint64, isAdmin bool) (bool, error)
}

// resourcePathRe 匹配「资源 id 挂在根路径下」的端点，用于反查所属项目。
//
// 只认 /api/v1/{资源}/{数字}；不带 id 的集合端点（/projects/:id/cases）走 projects 分支。
var resourcePathRe = regexp.MustCompile(`^/api/v1/(projects|cases|environments|apis|suites|plans|datasets|runs)/(\d+)`)

// RequireProjectAccess 在项目级做访问控制（F8）。
//
// 放行条件由 CanAccess 决定：管理员、或项目尚未设置成员、或是项目成员。
// 资源不存在时**放行**，让 handler 按原语义返回 404——中间件不该改变 404/403 的区分。
func RequireProjectAccess(checker ProjectAccessChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := CurrentPrincipal(c)
		if !ok || p.IsAdmin() {
			c.Next()
			return
		}
		m := resourcePathRe.FindStringSubmatch(c.Request.URL.Path)
		if m == nil {
			c.Next()
			return
		}
		id, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			c.Next()
			return
		}
		projectID, ok := checker.ProjectOfResource(m[1], id)
		if !ok {
			c.Next()
			return
		}
		allowed, err := checker.CanAccess(projectID, p.UserID, false)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code": response.CodeInternal, "message": "校验项目访问权限失败", "data": nil,
			})
			return
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    response.CodeForbidden,
				"message": "你不是该项目成员，无权访问；请联系管理员添加",
				"data":    nil,
			})
			return
		}
		c.Next()
	}
}
