package service

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ParamService 管理参数化数据集（M3 ③-b）。
//
// 两种数据源（model.ParamDataset.Source）：
//
//   - list：Inline 存 [{...}, ...] 列表，编译时直接内联进 YAML；
//   - csv ：CsvPath 指向项目工作区下的 data/{code}.csv，编译时用 ${P(...)} 引用。
//
// 为什么 CSV 存文件而不是 DB：引擎的 ${P()} 读的就是**文件**（实测 A22），
// 编译时随工作区一起复制即可生效；存 DB 反而要在每次编译时导出成临时文件，
// 白绕一圈。
type ParamService struct {
	Deps
}

// ---------------------------------------------------------------------------
// 视图
// ---------------------------------------------------------------------------

// ParamDatasetView 是数据集的列表/详情视图，附带给前端直接展示的行数与列名。
type ParamDatasetView struct {
	model.ParamDataset
	// RowCount 是数据行数（list = len(Inline)；csv = 数据行数，不含表头）。
	// 编译期裁剪 limit 也按这个口径展示"将迭代 N 次"。
	RowCount int `json:"row_count"`
	// Columns 是列名列表，前端展示与编辑用。
	Columns []string `json:"columns"`
	// LimitEffective 是实际生效的迭代次数 = limit>0 ? min(limit, row_count) : row_count。
	LimitEffective int `json:"limit_effective"`
}

// ParamDatasetReq 是创建/更新的请求体。
type ParamDatasetReq struct {
	Name     string    `json:"name"`
	Source   string    `json:"source"`
	Inline   jsonx.Any `json:"inline"`
	CsvName  string    `json:"csv_name"`  // Source=csv 时必填，形如 users.csv（不含路径）
	CsvText  string    `json:"csv_text"`  // Source=csv 时可选：直接传 CSV 文本（新建/替换内容）
	Strategy string    `json:"strategy"`
	Limit    int       `json:"limit"`
}

// List 返回项目下的数据集列表。
func (s *ParamService) List(projectID uint64) ([]ParamDatasetView, error) {
	var list []model.ParamDataset
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&list).Error; err != nil {
		return nil, errInternal("查询数据集列表失败", err)
	}
	out := make([]ParamDatasetView, 0, len(list))
	for i := range list {
		out = append(out, s.viewOf(&list[i], projectID))
	}
	return out, nil
}

// Get 返回数据集详情。
func (s *ParamService) Get(id uint64) (*ParamDatasetView, error) {
	ds, err := s.load(id)
	if err != nil {
		return nil, err
	}
	v := s.viewOf(ds, ds.ProjectID)
	return &v, nil
}

// CsvText 返回 CSV 文件的文本内容（前端编辑用）。
func (s *ParamService) CsvText(id uint64) (string, error) {
	ds, err := s.load(id)
	if err != nil {
		return "", err
	}
	if ds.Source != model.ParamSourceCSV {
		return "", errBadParam("数据集 %q 不是 CSV 来源", ds.Name)
	}
	b, err := os.ReadFile(s.csvAbsPath(ds.ProjectID, ds.CsvPath))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNotFound("CSV 文件不存在：%s", ds.CsvPath)
		}
		return "", errInternal("读取 CSV 文件失败", err)
	}
	return string(b), nil
}

// Create 新建数据集。
func (s *ParamService) Create(projectID uint64, req ParamDatasetReq) (*ParamDatasetView, error) {
	if projectID == 0 {
		return nil, errBadParam("project_id 不能为空")
	}
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	req, err := s.normalize(req)
	if err != nil {
		return nil, err
	}

	// 同项目内数据集名唯一：它是 config.parameters 里的引用键，重名会让引用歧义。
	var count int64
	if err := s.DB.Model(&model.ParamDataset{}).
		Where("project_id = ? AND name = ?", projectID, req.Name).
		Count(&count).Error; err != nil {
		return nil, errInternal("查询数据集重名失败", err)
	}
	if count > 0 {
		return nil, errConflict("数据集名称 %q 已存在", req.Name)
	}

	ds := &model.ParamDataset{
		ProjectID: projectID,
		Name:      req.Name,
		Source:    req.Source,
		Inline:    req.Inline,
		Strategy:  req.Strategy,
		Limit:     req.Limit,
	}
	if req.Source == model.ParamSourceCSV {
		if err := s.writeCSV(projectID, req.CsvName, req.CsvText); err != nil {
			return nil, err
		}
		ds.CsvPath = csvRelPath(req.CsvName)
	}
	if err := s.DB.Create(ds).Error; err != nil {
		return nil, errInternal("创建数据集失败", err)
	}
	v := s.viewOf(ds, projectID)
	return &v, nil
}

// Update 全量更新数据集。
//
// CSV 来源支持两种改法：换文件名（重命名）或替换内容（CsvText 非空）。
func (s *ParamService) Update(id uint64, req ParamDatasetReq) (*ParamDatasetView, error) {
	ds, err := s.load(id)
	if err != nil {
		return nil, err
	}
	req, err = s.normalize(req)
	if err != nil {
		return nil, err
	}

	var count int64
	if err := s.DB.Model(&model.ParamDataset{}).
		Where("project_id = ? AND name = ? AND id <> ?", ds.ProjectID, req.Name, ds.ID).
		Count(&count).Error; err != nil {
		return nil, errInternal("查询数据集重名失败", err)
	}
	if count > 0 {
		return nil, errConflict("数据集名称 %q 已存在", req.Name)
	}

	// 换了 CSV 文件名：旧文件不删（可能还被历史执行引用过），只写新文件。
	if req.Source == model.ParamSourceCSV && req.CsvName != "" && req.CsvName != csvBaseName(ds.CsvPath) {
		if err := s.writeCSV(ds.ProjectID, req.CsvName, req.CsvText); err != nil {
			return nil, err
		}
		ds.CsvPath = csvRelPath(req.CsvName)
	} else if req.Source == model.ParamSourceCSV && req.CsvText != "" {
		if err := s.writeCSV(ds.ProjectID, csvBaseName(ds.CsvPath), req.CsvText); err != nil {
			return nil, err
		}
	}

	ds.Name = req.Name
	ds.Source = req.Source
	ds.Inline = req.Inline
	ds.Strategy = req.Strategy
	ds.Limit = req.Limit
	if err := s.DB.Save(ds).Error; err != nil {
		return nil, errInternal("保存数据集失败", err)
	}
	v := s.viewOf(ds, ds.ProjectID)
	return &v, nil
}

// Delete 删除数据集。
//
// CSV 文件一并删除（工作区里的孤儿文件只会误导人），但只删自己目录下的，
// 路径由本服务拼接，不存在用户传路径的问题。
func (s *ParamService) Delete(id uint64) error {
	ds, err := s.load(id)
	if err != nil {
		return err
	}
	if err := s.DB.Delete(ds).Error; err != nil {
		return errInternal("删除数据集失败", err)
	}
	if ds.Source == model.ParamSourceCSV && ds.CsvPath != "" {
		_ = os.Remove(s.csvAbsPath(ds.ProjectID, ds.CsvPath)) // 删不掉不阻塞（可能已被手动清理）
	}
	return nil
}

// ---------------------------------------------------------------------------
// 编译期取数
// ---------------------------------------------------------------------------

// loadDatasets 把项目全部数据集查好、按名称索引，供 compiler.Input 使用。
//
// 所有的编译路径（执行 / 预览 / 校验 / 调试）都必须带上它：
// 否则引用了数据集的用例会在预览时报"数据集不存在"，
// 而执行时却正常（或反过来）——「预览与执行不一致」是本平台的大忌。
func loadDatasets(db *gorm.DB, projectID uint64) (map[string]*model.ParamDataset, error) {
	var list []model.ParamDataset
	if err := db.Where("project_id = ?", projectID).Find(&list).Error; err != nil {
		return nil, errInternal("查询参数化数据集失败", err)
	}
	out := make(map[string]*model.ParamDataset, len(list))
	for i := range list {
		out[list[i].Name] = &list[i]
	}
	return out, nil
}

// ResolveByName 按名称取数据集（compiler 渲染 config.parameters 时用）。
//
// 返回的切片按引用需要裁剪过 limit —— 编译器拿到就能直接用。
// 名称不存在时返回错误：引用了不存在的数据集必须显式报错，
// 静默跳过会让"改了数据集名"这种小改动变成"参数化神秘失效"。
func (s *ParamService) ResolveByName(projectID uint64, name string) (*model.ParamDataset, error) {
	var ds model.ParamDataset
	err := s.DB.Where("project_id = ? AND name = ?", projectID, name).First(&ds).Error
	if err != nil {
		if isNotFound(err) {
			return nil, errNotFound("数据集 %q 不存在（config.parameters 里引用的名字必须与参数集页的一致）", name)
		}
		return nil, errInternal("查询数据集失败", err)
	}
	return &ds, nil
}

// ---------------------------------------------------------------------------
// 内部
// ---------------------------------------------------------------------------

func (s *ParamService) load(id uint64) (*model.ParamDataset, error) {
	var ds model.ParamDataset
	if err := s.DB.First(&ds, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("数据集不存在（id=%d）", id)
		}
		return nil, errInternal("查询数据集失败", err)
	}
	return &ds, nil
}

func (s *ParamService) viewOf(ds *model.ParamDataset, projectID uint64) ParamDatasetView {
	v := ParamDatasetView{ParamDataset: *ds}
	if ds.Source == model.ParamSourceCSV {
		rows, cols, err := s.readCSVRows(projectID, ds.CsvPath)
		if err == nil {
			v.RowCount = rows
			v.Columns = cols
		}
	} else if list := inlineList(ds.Inline); list != nil {
		v.RowCount = len(list)
		if len(list) > 0 {
			if m, ok := list[0].(map[string]any); ok {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				v.Columns = keys
			}
		}
	}
	v.LimitEffective = effectiveLimit(ds.Limit, v.RowCount)
	return v
}

// normalize 校验并归一请求。
func (s *ParamService) normalize(req ParamDatasetReq) (ParamDatasetReq, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return req, errBadParam("数据集名称不能为空")
	}
	if len([]rune(req.Name)) > 64 {
		return req, errBadParam("数据集名称过长（最多 64 字符）")
	}
	if req.Source == "" {
		req.Source = model.ParamSourceList
	}
	if req.Source != model.ParamSourceList && req.Source != model.ParamSourceCSV {
		return req, errBadParam("source 只支持 list / csv，收到 %q", req.Source)
	}
	switch req.Strategy {
	case "":
		req.Strategy = model.ParamStrategySequential
	case model.ParamStrategySequential:
		// 引擎只支持顺序迭代（实测 A22：无 random/unique 开关）。
	default:
		return req, errBadParam("strategy 目前只支持 sequential（引擎没有 random/unique 开关），收到 %q", req.Strategy)
	}
	if req.Limit < 0 {
		req.Limit = 0
	}

	if req.Source == model.ParamSourceList {
		req.CsvName, req.CsvText = "", ""
		if list := inlineList(req.Inline); len(list) == 0 {
			return req, errBadParam("list 来源的数据集必须提供非空的 inline 列表（形如 [{\"username\":\"a\"}, ...]）")
		}
		return req, nil
	}

	// CSV 来源
	req.Inline = jsonx.Any{}
	if req.CsvName == "" && req.CsvText == "" {
		return req, errBadParam("csv 来源必须提供 csv_name 或 csv_text")
	}
	return req, nil
}

// writeCSV 把 CSV 文本写到项目工作区的 data/ 目录。
//
// ⚠️ CsvName 必须是纯文件名：它会参与文件路径拼接，任何路径分隔符都是
// path traversal 的入口。这里显式拒绝而不是静默清洗——静默清洗会让
// "上传 a/../b.csv" 变成"上传到了 b.csv"，用户根本不知道发生了什么。
func (s *ParamService) writeCSV(projectID uint64, name, text string) error {
	if strings.TrimSpace(name) == "" {
		// 只替换内容（Update 场景），沿用现有文件名
		if strings.TrimSpace(text) == "" {
			return errBadParam("CSV 内容不能为空")
		}
		return nil // 调用方会用现有文件名，这里无事可做
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") {
		return errBadParam("CSV 文件名 %q 非法：只允许纯文件名（不含路径分隔符）", name)
	}
	if !strings.EqualFold(filepath.Ext(name), ".csv") {
		return errBadParam("CSV 文件名必须以 .csv 结尾：%q", name)
	}
	if strings.TrimSpace(text) == "" {
		return errBadParam("CSV 内容不能为空")
	}
	if err := validateCSV(text); err != nil {
		return err
	}
	dir := filepath.Join(projectWorkspace(s.Cfg, &model.Project{Code: projectCodeOf(s.DB, projectID)}), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errInternal("创建数据目录失败", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		return errInternal("写入 CSV 文件失败", err)
	}
	return nil
}

// validateCSV 解析 CSV 做基本校验：至少表头 + 1 行数据、列数一致。
//
// 在写入时就拦住坏数据，而不是等执行时被引擎静默丢弃（实测 A22 结论三）。
func validateCSV(text string) error {
	reader := csv.NewReader(strings.NewReader(text))
	rows, err := reader.ReadAll()
	if err != nil {
		return errBadParam("CSV 解析失败：%v", err)
	}
	if len(rows) < 2 {
		return errBadParam("CSV 至少需要表头 + 1 行数据")
	}
	cols := len(rows[0])
	if cols == 0 {
		return errBadParam("CSV 表头为空")
	}
	for i, r := range rows[1:] {
		if len(r) != cols {
			return errBadParam("CSV 第 %d 行有 %d 列，与表头的 %d 列不一致", i+2, len(r), cols)
		}
	}
	return nil
}

func (s *ParamService) readCSVRows(projectID uint64, relPath string) (int, []string, error) {
	abs := s.csvAbsPath(projectID, relPath)
	f, err := os.Open(abs)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	rows, err := reader.ReadAll()
	if err != nil || len(rows) == 0 {
		return 0, nil, fmt.Errorf("CSV 不可读: %w", err)
	}
	return len(rows) - 1, rows[0], nil
}

// csvAbsPath 把相对路径转成项目工作区内的绝对路径。
func (s *ParamService) csvAbsPath(projectID uint64, relPath string) string {
	return filepath.Join(projectWorkspace(s.Cfg, &model.Project{Code: projectCodeOf(s.DB, projectID)}), filepath.FromSlash(relPath))
}

// csvRelPath 数据集存库里的相对路径固定为 data/<name>。
func csvRelPath(name string) string { return "data/" + name }

func csvBaseName(p string) string { return filepath.Base(filepath.FromSlash(p)) }

// projectCodeOf 查项目 code（拼工作区路径用）。
func projectCodeOf(db *gorm.DB, id uint64) string {
	var p model.Project
	if err := db.Select("code").First(&p, id).Error; err != nil {
		return ""
	}
	return p.Code
}

// inlineList 把 Inline（jsonx.Any）安全地取成 []any。
func inlineList(a jsonx.Any) []any {
	if a.Val == nil {
		return nil
	}
	if list, ok := a.Val.([]any); ok {
		return list
	}
	return nil
}

// effectiveLimit 实际迭代次数。
func effectiveLimit(limit, rowCount int) int {
	if limit > 0 && limit < rowCount {
		return limit
	}
	return rowCount
}

// InlineRowsNumbered 把 Inline 列表裁剪到 limit 行后返回。
//
// 供 compiler 渲染 list 数据集时使用：limit 是**平台在编译期裁剪**的
// （引擎没有 limit 参数，实测 A22），裁剪后的列表直接内联进 YAML。
func InlineRowsNumbered(a jsonx.Any, limit int) []any {
	list := inlineList(a)
	if limit <= 0 || limit >= len(list) {
		return list
	}
	return list[:limit]
}

// CSVLimitHint 返回 CSV 的 limit 提示文案（前端展示用）。
//
// CSV 的 limit 无法在 ${P()} 语法里表达（实测 A22），平台的做法是：
// 编译 CSV 数据集时**裁剪 CSV 文件内容**写进运行工作区（只写前 N 行）。
// 这里只是把规则说清楚，真正的裁剪在 compiler 里做。
func CSVLimitHint(limit int) string {
	if limit <= 0 {
		return "全部数据"
	}
	return "前 " + strconv.Itoa(limit) + " 行"
}
