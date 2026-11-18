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
	// 项目级访问控制（F8）：非成员访问项目及其资源返回 403。
	// 放在鉴权之后、业务之前：越权请求不应进入任何 handler。
	authed.Use(middleware.RequireProjectAccess(deps.Services.Member))
	{
		authed.GET("/auth/me", newAuthHandler(deps).Me)
		authed.POST("/auth/logout", newAuthHandler(deps).Logout)
		// 本人改密码：只要求登录，不需要管理员（要验原密码）
		authed.POST("/auth/password", newAuthHandler(deps).ChangePassword)
		authed.GET("/engine/status", engineStatus(deps))

		registerProjectRoutes(authed, deps)
		registerEnvironmentRoutes(authed, deps)
		registerAPIDefRoutes(authed, deps)
		registerCaseRoutes(authed, deps)
		registerDebugRoutes(authed, deps)
		registerParamRoutes(authed, deps)
		registerSuiteRoutes(authed, deps)
		registerPlanRoutes(authed, deps)
		registerRunRoutes(authed, deps)
		registerStatsRoutes(authed, deps)
		registerImportRoutes(authed, deps)
		registerExportRoutes(authed, deps)
		registerBaselineRoutes(authed, deps)
		registerUserRoutes(authed, deps)
		registerTokenRoutes(authed, deps)
		registerMockAdminRoutes(authed, deps)
		registerDocRoutes(authed, deps)
		registerMemberRoutes(authed, deps)
		registerChangeRoutes(authed, deps)
		registerWSRoutes(authed, deps)
		registerPerfRoutes(authed, deps)
		registerHealthRoutes(authed, deps)
	}

	// ⭐ CI 入口单独一个前缀：不走会话体系，语义与限流都和页面上的人不同，
	// 混在 /api/v1 里迟早要拆，不如一开始就分开。
	open := r.Group("/open")
	open.Use(middleware.RequireToken(deps.Services.Token.Verify))
	registerOpenRoutes(open, deps)

	// ⭐ Mock 端点（F4）也是公开的，但语义完全不同：它模拟的是「被测系统」，
	// 调用方是别人的前端/服务，不是平台用户。因此独立前缀、不挂任何鉴权与
	// JSON 兜底——返回的就是接口定义里写的响应。
	mock := r.Group("/mock")
	registerMockPublicRoutes(mock, deps)

	// 文档只读分享（F6）：与 /open 的 CI 令牌是两套东西，直接挂引擎。
	// 用 Any 注册再在 handler 里拒绝非 GET —— 保证方法错误时是 405 而不是 404。
	r.Any("/open/docs/:token", newDocHandler(deps).PublicDoc)

	return r
}

// registerMemberRoutes 注册项目成员管理（F8）。
//
// 读：项目成员都能看名单；写：仅管理员（与「账号由管理员创建」的权限模型一致）。
func registerMemberRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newMemberHandler(deps)
	g.GET("/projects/:id/members", h.List)
	admins := g.Group("/projects/:id/members", middleware.RequireAdmin())
	admins.POST("", h.Add)
	admins.DELETE("/:userId", h.Remove)
}

// registerHealthRoutes 注册资产健康度（F12）。
func registerHealthRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newHealthHandler(deps)
	g.GET("/projects/:id/health", h.Get)
}

// registerPerfRoutes 注册性能回归分析（F11）。
func registerPerfRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newPerfHandler(deps)
	g.GET("/projects/:id/perf", h.Get)
}

// registerWSRoutes 注册执行进度推送（F9）。
//
// 走会话鉴权（浏览器 WS 握手会带同源 cookie），因此挂在 authed 分组内；
// 权限在 handler 里按「该执行所属项目」显式校验（ProjectGuard 只认 REST 路径）。
func registerWSRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newWSHandler(deps)
	g.GET("/ws/runs/:id", h.RunEvents)
}

// registerChangeRoutes 注册变更历史查询（F8）。
func registerChangeRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newChangeHandler(deps)
	g.GET("/projects/:id/changes", h.List)
}

// registerDocRoutes 注册接口文档与分享链接管理（需登录）。
func registerDocRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newDocHandler(deps)
	g.GET("/projects/:id/docs", h.Get)
	g.GET("/projects/:id/docs/html", h.HTML)
	g.GET("/projects/:id/shares", h.ListShares)
	g.POST("/projects/:id/shares", h.CreateShare)
	g.DELETE("/projects/:id/shares/:shareId", h.RevokeShare)
}

// registerMockPublicRoutes 注册公开的 Mock 端点（F4）。
//
// 两级注册：「/mock/:code」覆盖根路径，「/mock/:code/*rest」覆盖子路径。
// gin 不允许 `:code` 与 `*rest` 组合成可选段，所以拆成两条。
func registerMockPublicRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newMockHandler(deps)
	g.Any("/:code", h.Serve)
	g.Any("/:code/*rest", h.Serve)
}

// registerMockAdminRoutes 注册 Mock 管理端点（需登录）。
func registerMockAdminRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newMockHandler(deps)
	g.GET("/projects/:id/mock", h.Info)
	g.PUT("/projects/:id/mock", h.SetEnabled)
	g.GET("/projects/:id/mock/hits", h.Hits)
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

// registerAPIDefRoutes 注册接口定义库路由（F1：项目 → 服务/模块 → 接口）。
func registerAPIDefRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newAPIDefHandler(deps)
	g.GET("/projects/:id/apis", h.List)
	g.POST("/projects/:id/apis", h.Create)
	g.GET("/projects/:id/apis/tree", h.Tree)

	g.GET("/apis/:id", h.Get)
	g.PUT("/apis/:id", h.Update)
	g.DELETE("/apis/:id", h.Delete)
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
	g.PUT("/cases/:id/yaml", h.SaveYAML)
	g.POST("/cases/:id/validate", h.Validate)
}

// registerDebugRoutes 注册单步调试路由。
//
// 挂在用例 ID 之下（/cases/{id}/debug）：调试的对象是"某条用例的某一步"，
// 语义上属于用例的子资源，与「执行」（/runs）是两回事——执行会落库、进历史，
// 调试是跑完即弃的临时动作。
func registerDebugRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newDebugHandler(deps)
	g.POST("/cases/:id/debug", h.DebugStep)
	// 调试台（F3）：不依赖已保存的用例，请求内容直接来自页面。
	g.POST("/projects/:id/debug/request", h.DebugRequest)
}

// registerParamRoutes 注册参数化数据集路由（M3）。
//
// 与用例集同一套路：「列举/新建」挂在项目路径下，「读取/修改/删除/CSV 内容」
// 直接用数据集 ID。
func registerParamRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newParamHandler(deps)
	g.GET("/projects/:id/datasets", h.List)
	g.POST("/projects/:id/datasets", h.Create)

	g.GET("/datasets/:id", h.Get)
	g.PUT("/datasets/:id", h.Update)
	g.DELETE("/datasets/:id", h.Delete)
	g.GET("/datasets/:id/csv", h.CsvText)
}

// registerSuiteRoutes 注册用例集相关路由。
//
// 与用例/环境同一套路：「列举/新建」挂在项目路径下（用例集归属项目），
// 「读取/修改/删除/成员」直接用用例集 ID。
//
// 成员接口单独成一段（/suites/{id}/cases）：它是"全量替换"语义，
// 与用例集自身的 CRUD 混在一起会让人误以为能单独增删某一条。
func registerSuiteRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newSuiteHandler(deps)
	g.GET("/projects/:id/suites", h.List)
	g.POST("/projects/:id/suites", h.Create)

	g.GET("/suites/:id", h.Get)
	g.PUT("/suites/:id", h.Update)
	g.DELETE("/suites/:id", h.Delete)
	g.GET("/suites/:id/cases", h.Members)
	g.PUT("/suites/:id/cases", h.SetMembers)
}

// registerPlanRoutes 注册测试计划相关路由。
//
// 计划同时具备两件事：一组用例集 + 什么时候跑。因此除了常规 CRUD，
// 还有两个子资源：/plans/{id}/suites（成员）与 /plans/{id}/enabled（开关）。
func registerPlanRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newPlanHandler(deps)
	g.GET("/projects/:id/plans", h.List)
	g.POST("/projects/:id/plans", h.Create)

	g.GET("/plans/:id", h.Get)
	g.PUT("/plans/:id", h.Update)
	g.DELETE("/plans/:id", h.Delete)
	g.PUT("/plans/:id/enabled", h.SetEnabled)
	g.GET("/plans/:id/suites", h.Suites)
	g.PUT("/plans/:id/suites", h.SetSuites)
}

// registerTokenRoutes 注册 CI 令牌管理路由（页面侧，需登录）。
//
// 与「使用令牌」（/open）是两回事：这里是签发与吊销，属于管理动作，
// 必须走会话体系；那边是机器调用，只认令牌。
func registerTokenRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newTokenHandler(deps)
	g.GET("/projects/:id/tokens", h.List)
	g.POST("/projects/:id/tokens", h.Issue)
	g.DELETE("/tokens/:id", h.Revoke)
}

// registerOpenRoutes 注册 CI 触发入口（令牌鉴权）。
func registerOpenRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newOpenHandler(deps)
	g.POST("/runs", h.Trigger)
	g.GET("/runs/:id/result", h.Result)
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

// registerStatsRoutes 注册统计看板路由（M4）。
func registerStatsRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newStatsHandler(deps)
	g.GET("/stats/trend", h.Trend)
	g.GET("/stats/flaky", h.Flaky)
	g.GET("/stats/slowest", h.Slowest)
}

// registerImportRoutes 注册导入路由（M4）。
//
// 挂在项目 ID 之下：导入的用例落进哪个项目由路径决定，
// 与 POST /projects/:id/cases 的归属语义一致。
func registerImportRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newImportHandler(deps)
	g.POST("/projects/:id/import/preview", h.Preview)
	g.POST("/projects/:id/import/commit", h.Commit)

	// OpenAPI / Swagger → 接口定义（F5）：同样是「预览 → 提交」两段式。
	oh := newOpenAPIHandler(deps)
	g.POST("/projects/:id/import/openapi/preview", oh.Preview)
	g.POST("/projects/:id/import/openapi/commit", oh.Commit)
}

// registerExportRoutes 注册导出路由（M4）。
func registerExportRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newExportHandler(deps)
	g.GET("/projects/:id/export", h.Download)
}

// registerBaselineRoutes 注册用例基线对比路由（M4-d）。
//
// 挂在项目 ID 之下：对比的是「某项目里某条用例」的历史执行，
// 与统计看板（/stats）同属只读分析能力，但对象是单条用例而非全项目。
func registerBaselineRoutes(g *gin.RouterGroup, deps *Deps) {
	h := newBaselineHandler(deps)
	g.GET("/projects/:id/baseline", h.Baseline)
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
