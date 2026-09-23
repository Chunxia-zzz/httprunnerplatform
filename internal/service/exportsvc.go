package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/compiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// ExportService 把整个项目导出为 hrp 标准目录的 zip（M4）。
//
// 设计依据（项目方案 4.2）：YAML 只是编译产物，可随时全量重新生成。
// 因此导出**不用**持久层工作区里可能过期的文件，而是把每个用例现场
// 重新渲染一遍 —— 保证导出的永远是「DB 当前内容的忠实编译产物」，
// 且整个过程对工作区零写入（只读语义）。
//
// 单个用例渲染失败不阻断整体导出：归档的价值在于尽可能完整，
// 坏掉的用例在 README 里写明原因，由用户修复后重导。
type ExportService struct {
	DB  *gorm.DB
	Cfg *config.Config
}

func NewExportService(d Deps) *ExportService {
	return &ExportService{DB: d.DB, Cfg: d.Cfg}
}

// Export 渲染并打包项目，返回 zip 字节流与建议文件名。
func (s *ExportService) Export(projectID, envID uint64) ([]byte, string, error) {
	project, err := loadProject(s.DB, projectID)
	if err != nil {
		return nil, "", err
	}

	// 环境列表：默认环境排最前（与 environment.List 同序）。
	var envs []model.Environment
	if err := s.DB.Where("project_id = ?", projectID).
		Order("is_default desc, id asc").Find(&envs).Error; err != nil {
		return nil, "", errInternal("查询环境失败", err)
	}
	// .env 用哪个环境：显式指定 > 默认 > 第一个。
	chosen := -1
	for i := range envs {
		if envID > 0 && envs[i].ID == envID {
			chosen = i
		}
		if chosen < 0 && envs[i].IsDefault {
			chosen = i
		}
	}
	if chosen < 0 && len(envs) > 0 {
		chosen = 0
	}

	// 用例与步骤：一次查全量（软删除由 gorm 自动排除）。
	var cases []model.TestCase
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&cases).Error; err != nil {
		return nil, "", errInternal("查询用例失败", err)
	}
	caseIDs := make([]uint64, 0, len(cases))
	for i := range cases {
		caseIDs = append(caseIDs, cases[i].ID)
	}
	stepsByCase := map[uint64][]model.TestStep{}
	if len(caseIDs) > 0 {
		var steps []model.TestStep
		if err := s.DB.Where("case_id IN ?", caseIDs).Order("case_id asc, seq asc").Find(&steps).Error; err != nil {
			return nil, "", errInternal("查询步骤失败", err)
		}
		for i := range steps {
			stepsByCase[steps[i].CaseID] = append(stepsByCase[steps[i].CaseID], steps[i])
		}
	}

	datasets, err := loadDatasets(s.DB, projectID)
	if err != nil {
		return nil, "", err
	}
	projectWS := projectWorkspace(s.Cfg, project)

	// 逐用例渲染：单用例失败不阻断导出，记进 README。
	type rendered struct{ name, yaml string }
	var ok []rendered
	var failures []string
	for i := range cases {
		tc := &cases[i]
		comp, err := compiler.Render(&compiler.Input{
			Project:       project,
			Env:           pickEnv(envs, chosen),
			Cases:         []compiler.CaseSpec{{Case: tc, Steps: stepsByCase[tc.ID]}},
			WorkspaceRoot: projectWS,
			Datasets:      datasets,
		})
		if err != nil {
			failures = append(failures, fmt.Sprintf("- %s：%s", tc.Code, err.Error()))
			continue
		}
		ok = append(ok, rendered{name: comp.Cases[0].FileName, yaml: comp.Cases[0].YAML})
	}

	// 参数化 CSV：只带原件（编译期派生的 __lN.csv 是可再生产物，不带）。
	var csvFiles []csvFile
	for _, ds := range datasets {
		if ds.Source != model.ParamSourceCSV || strings.TrimSpace(ds.CsvPath) == "" {
			continue
		}
		abs := filepath.Join(projectWS, filepath.FromSlash(ds.CsvPath))
		content, err := os.ReadFile(abs)
		if err != nil {
			failures = append(failures, fmt.Sprintf("- %s：读取 CSV 失败（%v）", ds.CsvPath, err))
			continue
		}
		csvFiles = append(csvFiles, csvFile{name: filepath.Base(ds.CsvPath), content: content})
	}

	buf := &bytes.Buffer{}
	root := project.Code
	zw := zip.NewWriter(buf)

	// README：导出元信息 + 渲染失败清单（有失败时）。
	var b strings.Builder
	fmt.Fprintf(&b, "hrp 项目导出\n\n项目：%s（%s）\n导出时间：%s\n引擎版本：%s\n",
		project.Name, project.Code, time.Now().Format("2006-01-02 15:04:05"), project.HrpVersion)
	if chosen >= 0 {
		fmt.Fprintf(&b, ".env 来源环境：%s\n", envs[chosen].Name)
	}
	fmt.Fprintf(&b, "用例数：%d（成功 %d）\n\n本目录可被 hrp run 直接执行：\n  hrp run testcases/*.yaml\n", len(cases), len(ok))
	if len(failures) > 0 {
		b.WriteString("\n## 以下内容导出失败（需在平台修复后重导）\n")
		b.WriteString(strings.Join(failures, "\n"))
		b.WriteString("\n")
	}
	writeZipFile(zw, root+"/README.txt", []byte(b.String()))

	// .env（选中环境的渲染结果）
	if chosen >= 0 {
		writeZipFile(zw, root+"/.env", []byte(compiler.RenderEnv(&envs[chosen])))
	}
	// 全部环境单独存档（environments/ 不影响 hrp run，便于 code review）。
	for i := range envs {
		name := sanitizeZipName(envs[i].Name)
		writeZipFile(zw, fmt.Sprintf("%s/environments/%s.env", root, name), []byte(compiler.RenderEnv(&envs[i])))
	}
	// 用例：FileName 已含 testcases/ 相对目录（如 testcases/case_ok.yaml），直接拼根。
	for _, r := range ok {
		writeZipFile(zw, root+"/"+r.name, []byte(r.yaml))
	}
	// 参数化 CSV
	for _, c := range csvFiles {
		writeZipFile(zw, root+"/data/"+c.name, c.content)
	}

	if err := zw.Close(); err != nil {
		return nil, "", errInternal("生成导出包失败", err)
	}
	filename := fmt.Sprintf("%s-export-%s.zip", project.Code, time.Now().Format("20060102-150405"))
	return buf.Bytes(), filename, nil
}

type csvFile struct {
	name    string
	content []byte
}

func pickEnv(envs []model.Environment, idx int) *model.Environment {
	if idx < 0 || idx >= len(envs) {
		return nil
	}
	return &envs[idx]
}

// sanitizeZipName 让环境名可以安全地成为文件名的一部分。
func sanitizeZipName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "env"
	}
	return b.String()
}

func writeZipFile(zw *zip.Writer, name string, content []byte) {
	f, _ := zw.Create(name)
	_, _ = f.Write(content)
}
