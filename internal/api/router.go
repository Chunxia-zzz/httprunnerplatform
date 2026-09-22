package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api/middleware"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/webui"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/hrpclient"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// Deps 是路由所需的外部依赖。
//
// 显式传递而不是用包级单例：便于单测注入替身，
// 也让「一个 handler 到底依赖了什么」一目了然。
type Deps struct {
	Cfg      *config.Config
	DB       *gorm.DB
	Sessions *auth.Store
	// Services 是业务编排层。
	//
	// 由调用方（cmd/server）构造而不是在这里 new：运行服务需要持有
	// 在跑执行的取消句柄，进程退出时要由同一个实例负责优雅收尾。
	Services *service.Set
	// WebUI 是内嵌的前端资源。为 nil 时不做静态托管
	// （单测里通常不需要，也就不必构造）。
	WebUI *webui.UI
}

// NewRouter 组装 gin 路由。
func NewRouter(deps *Deps) *gin.Engine {
	if deps.Services == nil {
		deps.Services = service.New(service.Deps{DB: deps.DB, Cfg: deps.Cfg})
	}

	gin.SetMode(ginMode(deps.Cfg.Server.Mode))

	r := gin.New()
	r.Use(middleware.Recovery(), middleware.AccessLog())

	// CORS 默认不放开；前端开发服务器（Vite 默认 5173）需在配置文件里显式登记。
	// 生产环境前端由二进制内嵌，同源，无需 CORS。
	if origins := devOrigins(deps.Cfg); len(origins) > 0 {
		r.Use(middleware.CORS(origins))
	}

	r.NoRoute(noRouteHandler(deps))

	v1 := r.Group("/api/v1")

	// 无需登录
	v1.POST("/auth/login", newAuthHandler(deps).Login)
	v1.GET("/healthz", func(c *gin.Context) {
		OK(c, gin.H{"status": "ok", "time": time.Now().Format(time.RFC3339)})
	})

	// 需登录
	authed := v1.Group("")
	authed.Use(middleware.RequireAuth(deps.Sessions))
	{
		authed.GET("/auth/me", newAuthHandler(deps).Me)
		authed.POST("/auth/logout", newAuthHandler(deps).Logout)
		// 本人改密码：只要求登录，不需要管理员（要验原密码）
		authed.POST("/auth/password", newAuthHandler(deps).ChangePassword)
		authed.GET("/engine/status", engineStatus(deps))

		registerProjectRoutes(authed, deps)
		registerEnvironmentRoutes(authed, deps)
		registerCaseRoutes(authed, deps)
		registerRunRoutes(authed, deps)
		registerUserRoutes(authed, deps)
	}

	return r
}

// registerUserRoutes 注册账号管理路由。
//
// 整个分组都挂 RequireAdmin：账号管理是平台里唯一一类
// "普通成员完全不该碰"的能力（方案 10.2：只 admin / member 两档，不做 RBAC）。
func registerUserRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newUserHandler(deps)
	admins := g.Group("/users", middleware.RequireAdmin())
	admins.GET("", h.List)
	admins.POST("", h.Create)
	admins.PUT("/:id", h.Update)
	admins.DELETE("/:id", h.Delete)
	admins.POST("/:id/password", h.ResetPassword)
}

// registerProjectRoutes 注册项目相关路由。
func registerProjectRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newProjectHandler(deps)
	g.GET("/projects", h.List)
	g.POST("/projects", h.Create)
	g.GET("/projects/:id", h.Get)
	g.PUT("/projects/:id", h.Update)
	// 删除项目是平台里破坏性最大的操作：会连带删掉项目下的环境与用例，
	// 并让已产生的执行记录失去归属。因此收紧为管理员专属
	// （与账号管理同一档；其余用例级操作成员可正常使用）。
	g.DELETE("/projects/:id", middleware.RequireAdmin(), h.Delete)
}

// registerEnvironmentRoutes 注册环境相关路由。
//
// 环境归属项目，因此"创建/列举"挂在项目路径下，而"读取/修改/删除"
// 直接用环境 ID：环境 ID 全局唯一，编辑器保存时不必反复带上 project_id。
func registerEnvironmentRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newEnvironmentHandler(deps)
	g.GET("/projects/:id/environments", h.List)
	g.POST("/projects/:id/environments", h.Create)
	g.GET("/environments/:id", h.Get)
	g.PUT("/environments/:id", h.Update)
	g.DELETE("/environments/:id", h.Delete)
}

// registerCaseRoutes 注册用例与步骤相关路由。
func registerCaseRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newCaseHandler(deps)
	g.GET("/projects/:id/cases", h.List)
	g.POST("/projects/:id/cases", h.Create)
	// tree 必须注册在同前缀的具体路径上；gin 会把静态段优先匹配，
	// 因此它与 /projects/:id/cases 不冲突。
	g.GET("/projects/:id/cases/tree", h.Tree)

	g.GET("/cases/:id", h.Get)
	g.PUT("/cases/:id", h.Update)
	g.DELETE("/cases/:id", h.Delete)
	g.GET("/cases/:id/yaml", h.YAML)
	g.POST("/cases/:id/validate", h.Validate)
}

// registerRunRoutes 注册执行相关路由。
func registerRunRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newRunHandler(deps)
	g.POST("/runs", h.Create)
	g.GET("/runs", h.List)
	g.GET("/runs/:id", h.Get)
	g.GET("/runs/:id/cases", h.Cases)
	g.POST("/runs/:id/cancel", h.Cancel)
	g.GET("/runs/:id/report", h.Report)
	g.GET("/runs/:id/logs", h.Logs)
}

// noRouteHandler 处理所有没有匹配到路由的请求。
//
// 职责边界必须分清（顺序即优先级）：
//
//	/api/...  → 一律 JSON 兜底错误，**绝不进 SPA fallback**
//	非 GET    → 同样是 JSON 错误（内嵌资源只有 GET/HEAD 有意义）
//	其余 GET  → 内嵌前端：存在的静态文件直接给；带扩展名但不存在给 404；
//	           其余当作前端路由，回 index.html
//
// 第一条是本文件里最要紧的一处判断：接口路径打错时若返回 HTML，
// 前端拿到的是「Unexpected token '<'」，与真实原因（路径写错）毫无关系。
func noRouteHandler(deps *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path

		if p == "/api" || strings.HasPrefix(p, "/api/") {
			Fail(c, response.CodeNotFound, "接口不存在")
			return
		}

		if deps.WebUI == nil {
			Fail(c, response.CodeNotFound, "资源不存在")
			return
		}

		if m := c.Request.Method; m != http.MethodGet && m != http.MethodHead {
			Fail(c, response.CodeNotFound, "资源不存在")
			return
		}

		deps.WebUI.Handler()(c)
	}
}

// ginMode 把配置里的 mode 映射到 gin 常量。
func ginMode(mode string) string {
	if mode == "debug" {
		return gin.DebugMode
	}
	return gin.ReleaseMode
}

// devOrigins 返回允许跨域的来源（仅用于本地前端开发）。
func devOrigins(cfg *config.Config) []string {
	if cfg.Server.Mode != "debug" {
		return nil
	}
	return []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
	}
}

// engineStatus 返回引擎可用性。
//
// 引擎不可用时返回 code=50001 但 HTTP 200：前端需要在页面上显示醒目告警，
// 而不是把一个"基础设施状态查询"当成接口错误。
func engineStatus(deps *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		d, err := hrpclient.Check(deps.Cfg.Engine.BinaryPath)
		if err != nil {
			FailWithData(c, response.CodeEngineDown, "引擎不可用", gin.H{
				"available": false,
				"error":     err.Error(),
			})
			return
		}
		OK(c, gin.H{
			"available":   true,
			"binary_path": d.BinaryPath,
			"version":     d.Version,
			"go_version":  d.GoVersion,
			"platform":    d.GOOS + "/" + d.GOARCH,
			// M1–M4 明确不支持 debugtalk.py（见 docs/引擎实测记录.md 2.6）。
			"python_plugin_enabled": false,
			"note":                  "M1–M4 不支持 debugtalk.py 自定义函数",
		})
	}
}
