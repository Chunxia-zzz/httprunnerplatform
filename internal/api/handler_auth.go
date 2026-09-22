package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api/middleware"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type authHandler struct {
	db       *gorm.DB
	sessions *auth.Store
}

func newAuthHandler(deps *Deps) *authHandler {
	return &authHandler{db: deps.DB, sessions: deps.Sessions}
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login 校验账号密码并建立会话。
func (h *authHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "用户名与密码不能为空")
		return
	}

	var u model.User
	err := h.db.Where("username = ?", strings.TrimSpace(req.Username)).First(&u).Error
	if err != nil {
		// 不区分「用户不存在」与「密码错误」，避免账号枚举。
		Fail(c, response.CodeBadParam, "用户名或密码错误")
		return
	}
	if !u.Enabled {
		Fail(c, response.CodeForbidden, "账号已禁用")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)) != nil {
		Fail(c, response.CodeBadParam, "用户名或密码错误")
		return
	}

	p := auth.Principal{
		UserID:   u.ID,
		Username: u.Username,
		Nickname: u.Nickname,
		Role:     u.Role,
	}
	token := h.sessions.Create(p)

	// 私有化部署下不强制 Secure（可能以 http 提供服务），
	// 但保持 HttpOnly + SameSite=Lax，避免被脚本读取。
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, token, int(h.sessions.TTL().Seconds()), "/", "", false, true)

	logx.L().Info().Str("username", u.Username).Msg("用户登录")
	OK(c, p)
}

// Me 返回当前登录主体。
func (h *authHandler) Me(c *gin.Context) {
	p, ok := middleware.CurrentPrincipal(c)
	if !ok {
		AbortWithStatus(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	OK(c, p)
}

// Logout 注销会话。
func (h *authHandler) Logout(c *gin.Context) {
	if token, err := c.Cookie(auth.CookieName); err == nil && token != "" {
		h.sessions.Delete(token)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, "", -1, "/", "", false, true)
	OK(c, nil)
}
