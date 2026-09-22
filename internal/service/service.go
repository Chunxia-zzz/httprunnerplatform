// Package service 是平台的业务编排层。
//
// 分层约定（方案 5 节）：
//
//	api      只做参数绑定与响应翻译，不含业务判断
//	service  业务规则、事务边界、跨包编排（compiler / validator / executor / parser）
//	core 包  纯粹能力（编译、校验、执行、解析），不依赖数据库
//
// service 刻意**不 import gin**：业务规则要能在普通单测里直接验证，
// 也让 HTTP 细节不会渗进业务层——平台最需要单测覆盖的恰恰是这里的规则。
package service

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// Deps 是所有 service 共享的依赖。
type Deps struct {
	DB  *gorm.DB
	Cfg *config.Config
	// Sessions 是登录态表。用户管理需要它来"吊销会话"——
	// 禁用/降级/重置密码若不连带吊销，改动在 TTL 内等于没生效（见 auth.Store.Revoke）。
	//
	// 刻意**不做 nil 兜底**：若忘了接线，静默换成一个空表只会让吊销"看起来成功了
	// 但实际没吊销真实会话"，那比直接报错难查得多。见 UserService.revokeSessions。
	Sessions *auth.Store
}

// Set 汇总全部 service，供上层一次性注入与优雅关闭。
type Set struct {
	Project     *ProjectService
	Environment *EnvironmentService
	Case        *CaseService
	Suite       *SuiteService
	Run         *RunService
	User        *UserService
}

// New 构造全部 service。
func New(d Deps) *Set {
	return &Set{
		Project:     &ProjectService{Deps: d},
		Environment: &EnvironmentService{Deps: d},
		Case:        &CaseService{Deps: d},
		Suite:       &SuiteService{Deps: d},
		Run:         NewRunService(d),
		User:        &UserService{Deps: d},
	}
}

// Shutdown 取消所有在跑的用例执行。进程退出前必须调用，
// 否则残留的 hrp 子进程会在服务已经关闭后继续跑（实测 A6 见过这种挂起）。
func (s *Set) Shutdown() {
	if s.Run != nil {
		s.Run.Shutdown()
	}
}

// ---------------------------------------------------------------------------
// 分页
// ---------------------------------------------------------------------------

// Page 是分页参数。
//
// 刻意不直接复用 api 层的 Pagination：那是 HTTP 查询参数的绑定结构，
// service 不应该知道自己是被 HTTP 调用的。
type Page struct {
	Page     int
	PageSize int
}

// Normalize 补齐默认值并做上限保护，返回归一化后的副本。
func (p Page) Normalize() Page {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
	return p
}

// Offset 返回 SQL OFFSET。
func (p Page) Offset() int {
	p = p.Normalize()
	return (p.Page - 1) * p.PageSize
}

// Limit 返回 SQL LIMIT。
func (p Page) Limit() int { return p.Normalize().PageSize }

// ---------------------------------------------------------------------------
// 标识符
// ---------------------------------------------------------------------------

// identRe 是 code 的合法形态。契约见 docs/接口契约.md 0.2（错误码 40004）。
//
// code 会直接参与文件路径（工作区目录名、用例文件名），
// 因此必须在入口处严格限制字符集——这是 path traversal 的第一道闸门。
var identRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validIdent(s string) bool { return identRe.MatchString(s) }

// normalizeIdent 去掉首尾空白后校验标识符。
func normalizeIdent(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, validIdent(s)
}

// ---------------------------------------------------------------------------
// 业务错误
//
// 统一在 service 层构造 response.Error，handler 层只负责翻译成 HTTP 响应，
// 这样错误码与文案只有一处定义。
// ---------------------------------------------------------------------------

func errBadParam(format string, args ...any) *response.Error {
	return response.Newf(response.CodeBadParam, format, args...)
}

func errConflict(format string, args ...any) *response.Error {
	return response.Newf(response.CodeConflict, format, args...)
}

func errNotFound(format string, args ...any) *response.Error {
	return response.Newf(response.CodeNotFound, format, args...)
}

func errInUse(format string, args ...any) *response.Error {
	return response.Newf(response.CodeInUse, format, args...)
}

// errForbidden 用于"操作本身合法，但当前状态不允许这么做"。
//
// 典型场景是用户管理里的自我保护：管理员不能把自己降级/禁用，
// 也不能干掉最后一个可用管理员。这类拒绝不是参数错（40000），
// 也不是"资源被占用"（40003），而是权限与状态约束。
func errForbidden(format string, args ...any) *response.Error {
	return response.Newf(response.CodeForbidden, format, args...)
}

func errInvalidIdent(field, value string) *response.Error {
	return response.Newf(response.CodeInvalidIdent,
		"%s 非法（%q）：只允许字母、数字、下划线与短横线，长度 1-64", field, value)
}

func errInternal(msg string, err error) *response.Error {
	return response.Wrap(response.CodeInternal, msg, err)
}

// ---------------------------------------------------------------------------
// 通用加载器
//
// 放在包里作为自由函数而不是各 service 的私有方法：
// 运行编排需要同时读项目、用例、环境三张表，重复写一遍容易漏掉软删除条件。
// ---------------------------------------------------------------------------

func loadProject(db *gorm.DB, id uint64) (*model.Project, error) {
	var p model.Project
	if err := db.First(&p, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("项目不存在（id=%d）", id)
		}
		return nil, errInternal("查询项目失败", err)
	}
	return &p, nil
}

func loadCase(db *gorm.DB, id uint64) (*model.TestCase, error) {
	var c model.TestCase
	if err := db.First(&c, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("用例不存在（id=%d）", id)
		}
		return nil, errInternal("查询用例失败", err)
	}
	return &c, nil
}

// caseSteps 读取用例的全部步骤，按 seq 升序。
//
// 不区分 enabled：禁用步骤也要返回给编辑器，否则用户会发现自己关掉的步骤"消失了"。
// 执行与编译时再由 compiler 过滤。
func caseSteps(db *gorm.DB, caseID uint64) ([]model.TestStep, error) {
	var steps []model.TestStep
	if err := db.Where("case_id = ?", caseID).Order("seq asc, id asc").Find(&steps).Error; err != nil {
		return nil, errInternal("查询用例步骤失败", err)
	}
	return steps, nil
}

func loadEnv(db *gorm.DB, id uint64) (*model.Environment, error) {
	var e model.Environment
	if err := db.First(&e, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("环境不存在（id=%d）", id)
		}
		return nil, errInternal("查询环境失败", err)
	}
	return &e, nil
}

// resolveEnv 解析本次要用哪个环境。
//
// envID 为 0 时回退到项目默认环境；项目一个环境都没有时返回 nil,
// 表示"无环境也可运行"（引擎会用默认 base_url），而不是报错——
// 让用户在建环境之前就能先调试用例，是本地开箱可用的关键一步。
func resolveEnv(db *gorm.DB, projectID, envID uint64) (*model.Environment, error) {
	if envID > 0 {
		e, err := loadEnv(db, envID)
		if err != nil {
			return nil, err
		}
		if e.ProjectID != projectID {
			return nil, errBadParam("环境 %d 不属于项目 %d", envID, projectID)
		}
		return e, nil
	}

	// 用 Limit(1).Find 而不是 First：First 在查不到时会返回
	// gorm.ErrRecordNotFound，GORM 的日志器会把它记成一行 error。
	// 但"项目还没配环境"是完全正常的状态（用户可以先建用例再建环境），
	// 让它每次都在日志里报错会把真正的错误淹掉。
	var list []model.Environment
	err := db.Where("project_id = ?", projectID).
		Order("is_default desc, id asc").
		Limit(1).Find(&list).Error
	if err != nil {
		return nil, errInternal("查询默认环境失败", err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// siblingCaseNames 返回同项目内**其他**用例的 config.name → code 映射。
//
// 用途是 F11：引擎的 summary.json 以 config.name 作唯一标识，
// 重名会让结果无法区分，因此保存单个用例时也要能发现重名。
func siblingCaseNames(db *gorm.DB, projectID, excludeCaseID uint64) (map[string]string, error) {
	q := db.Model(&model.TestCase{}).Select("name", "code").Where("project_id = ?", projectID)
	if excludeCaseID > 0 {
		q = q.Where("id <> ?", excludeCaseID)
	}
	var rows []struct {
		Name string
		Code string
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, errInternal("查询同项目用例名失败", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[strings.TrimSpace(r.Name)] = r.Code
	}
	return out, nil
}

// projectWorkspace 返回项目的**持久层**工作区目录。
//
// 与运行层（{root}/runtime/runs/{id}）严格分离：持久层只有"源代码"，
// 引擎产物一律落在运行层，见方案 4.3。
func projectWorkspace(cfg *config.Config, project *model.Project) string {
	return filepath.Join(cfg.WorkspacesDir(), project.Code)
}

// isNotFound 统一判断「记录不存在」。
//
// 不直接比较 gorm.ErrRecordNotFound 的相等性：GORM 在部分路径上会包装错误，
// errors.Is 才是正确用法。
func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// isDuplicated 判断唯一索引冲突。
//
// 除了 GORM 的标准化错误，还兜一层文本判断：SQLite（glebarez/sqlite）
// 的驱动错误不一定能被 GORM 的 translator 识别，而"重名"是用户最常撞的错，
// 归错类会让前端提示变成"服务端内部错误"，体验差距很大。
//
// ⚠️ 这类兜底只用于**把错误分类得更准**，不作为正确性保障：
// 真正防重的第一道闸门是 service 层写入前的显式查询。
func isDuplicated(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "UNIQUE CONSTRAINT") ||
		strings.Contains(msg, "DUPLICATE ENTRY") ||
		strings.Contains(msg, "ERROR 1062")
}
