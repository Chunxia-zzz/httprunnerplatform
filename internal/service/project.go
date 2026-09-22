package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// ProjectService 负责项目（工作区）的增删改查。
type ProjectService struct{ Deps }

// ProjectListQuery 是项目列表的过滤条件。
type ProjectListQuery struct {
	Keyword string
}

// projectSubDirs 是新建项目时要一并创建的子目录。
//
// 目录名沿用 hrp 官方脚手架约定（`hrp startproject` 生成的结构）：
// 即使 M1 只用得到 testcases/，也把 api/ suites/ data/ 先建出来——
// 用户后续往里放 .csv 数据文件或 api 定义时不会因为目录不存在而困惑。
var projectSubDirs = []string{"api", "testcases", "suites", "data"}

// List 返回项目分页列表。
func (s *ProjectService) List(q ProjectListQuery, page Page) ([]model.Project, int64, error) {
	tx := s.DB.Model(&model.Project{})
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		like := "%" + kw + "%"
		tx = tx.Where("code LIKE ? OR name LIKE ?", like, like)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计项目数失败", err)
	}

	page = page.Normalize()
	var list []model.Project
	if err := tx.Order("id desc").Offset(page.Offset()).Limit(page.Limit()).Find(&list).Error; err != nil {
		return nil, 0, errInternal("查询项目列表失败", err)
	}
	return list, total, nil
}

// Get 返回单个项目。
func (s *ProjectService) Get(id uint64) (*model.Project, error) { return loadProject(s.DB, id) }

// CreateProjectReq 是创建项目的请求。
type CreateProjectReq struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	HrpVersion  string `json:"hrp_version"`
}

// Create 创建项目并同步创建工作区目录。
//
// 目录创建与建库记录的顺序刻意是"先建目录、后写库"：
// 反过来会出现"库里有项目、盘上没目录"的不一致，而执行时才失败的代价更大。
// 目录已存在不算错误（幂等），方便用户手工重建目录后重试。
func (s *ProjectService) Create(req CreateProjectReq, ownerID uint64) (*model.Project, error) {
	code, ok := normalizeIdent(req.Code)
	if !ok {
		return nil, errInvalidIdent("项目 code", req.Code)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("项目名称不能为空")
	}

	var exists int64
	if err := s.DB.Model(&model.Project{}).Where("code = ?", code).Count(&exists).Error; err != nil {
		return nil, errInternal("检查项目 code 是否重复失败", err)
	}
	if exists > 0 {
		return nil, errConflict("项目标识 %q 已存在", code)
	}

	version := strings.TrimSpace(req.HrpVersion)
	if version == "" {
		version = "v4.3.6"
	}

	p := &model.Project{
		Code:          code,
		Name:          name,
		Description:   strings.TrimSpace(req.Description),
		HrpVersion:    version,
		WorkspacePath: filepath.Join(s.Cfg.WorkspacesDir(), code),
		OwnerID:       ownerID,
	}

	if err := s.ensureWorkspaceDir(p); err != nil {
		return nil, err
	}

	if err := s.DB.Create(p).Error; err != nil {
		if isDuplicated(err) {
			return nil, errConflict("项目标识 %q 已存在", code)
		}
		return nil, errInternal("创建项目失败", err)
	}
	return p, nil
}

// UpdateProjectReq 是更新项目的请求（PUT 为全量覆盖语义）。
type UpdateProjectReq struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	HrpVersion  string `json:"hrp_version"`
}

// Update 全量更新项目。
//
// **code 不可修改**：它是工作区目录名，改了就要连带迁移整个目录，
// 而目录里可能有用户手工放进去的 data/*.csv。M1 明确不做这件事，
// 与其默默忽略不如直接拒绝，避免用户以为改成功了。
func (s *ProjectService) Update(id uint64, req UpdateProjectReq) (*model.Project, error) {
	p, err := loadProject(s.DB, id)
	if err != nil {
		return nil, err
	}

	if req.Code != "" && strings.TrimSpace(req.Code) != p.Code {
		return nil, errBadParam(
			"项目 code 不可修改（当前 %q，收到 %q）。它是工作区目录名，改名需要迁移目录，M1 不支持",
			p.Code, strings.TrimSpace(req.Code))
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("项目名称不能为空")
	}

	p.Name = name
	p.Description = strings.TrimSpace(req.Description)
	if v := strings.TrimSpace(req.HrpVersion); v != "" {
		p.HrpVersion = v
	}

	if err := s.DB.Save(p).Error; err != nil {
		return nil, errInternal("更新项目失败", err)
	}
	// 工作区目录可能被用户手工删掉了，这里顺手补回，避免执行时才报错。
	if err := s.ensureWorkspaceDir(p); err != nil {
		return nil, err
	}
	return p, nil
}

// Delete 软删除项目。
//
// 有执行在跑时拒绝：软删除不会中断已经在跑的 hrp 子进程，
// 但会让它跑完之后无处落库，产生一条"孤儿结果"。
func (s *ProjectService) Delete(id uint64) error {
	p, err := loadProject(s.DB, id)
	if err != nil {
		return err
	}

	var running int64
	err = s.DB.Model(&model.RunRecord{}).
		Where("project_id = ? AND status IN ?", id, []string{model.RunQueued, model.RunRunning}).
		Count(&running).Error
	if err != nil {
		return errInternal("检查项目下是否有执行中的任务失败", err)
	}
	if running > 0 {
		return errInUse("项目 %q 下还有 %d 个执行中的任务，请先终止后再删除", p.Name, running)
	}

	if err := s.DB.Delete(&model.Project{}, id).Error; err != nil {
		return errInternal("删除项目失败", err)
	}
	return nil
}

// ensureWorkspaceDir 创建（或补齐）项目工作区目录。
//
// 工作区**不随项目删除而清理**：删项目是软删除，可以恢复；
// 而目录里可能有用户手工维护的数据文件，静默删掉是不可逆的损失。
func (s *ProjectService) ensureWorkspaceDir(p *model.Project) error {
	root := projectWorkspace(s.Cfg, p)
	dirs := append([]string{root}, func() []string {
		out := make([]string, 0, len(projectSubDirs))
		for _, d := range projectSubDirs {
			out = append(out, filepath.Join(root, d))
		}
		return out
	}()...)

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return response.Wrap(response.CodeWorkspaceFail,
				"创建工作区目录失败："+d, err)
		}
	}

	// 把真实路径回写到模型：配置里的 workspace.root 可能是相对路径，
	// 存数据库时统一为相对配置推导出的值，便于在不同机器间迁移。
	p.WorkspacePath = root
	return nil
}
