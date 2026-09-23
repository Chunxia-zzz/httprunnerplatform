package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/decompiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/hrpclient"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// ImportService 把 HAR / Postman / curl 转成平台用例（M4）。
//
// 转换分两段：
//  1. hrp convert 把原始文件转成标准 hrp YAML（实测 A23：-d 目录必须预先存在，
//     HAR 的 validate 是字典形态、URL 是绝对地址、HAR 步骤名为空）；
//  2. decompiler 把 YAML 反解析回结构化 CaseReq，补齐占位名后走 CaseService.Create。
//
// 预览（Import）与提交（Commit）共用同一条转换链路，区别只在是否落库 ——
// 「先看再导」是导入功能的基本体验，不能让用户闭着眼睛点确定。
type ImportService struct {
	DB   *gorm.DB
	Cfg  *config.Config
	Case *CaseService
}

func NewImportService(d Deps, caseSvc *CaseService) *ImportService {
	return &ImportService{DB: d.DB, Cfg: d.Cfg, Case: caseSvc}
}

// ImportFormat 支持的导入格式。
const (
	ImportFormatAuto    = "auto"
	ImportFormatHAR     = "har"
	ImportFormatPostman = "postman"
	ImportFormatCurl    = "curl"
)

// ImportRequest 是一次导入请求：原始文件内容 + 格式提示。
type ImportRequest struct {
	Format   string `json:"format"`   // auto / har / postman / curl
	Filename string `json:"filename"` // 原始文件名（决定转换产物名与用例名）
	Content  []byte `json:"-"`
}

// ImportedCase 是转换出的一个候选用例（预览与提交共用）。
type ImportedCase struct {
	Name       string   `json:"name"`
	Code       string   `json:"code"`
	Module     string   `json:"module"`
	StepCount  int      `json:"step_count"`
	Summary    []string `json:"summary"` // 每步「METHOD path」摘要
	SourceFile string   `json:"source_file"`
	CaseReq    *CaseReq `json:"-"` // 提交时复用
}

// ImportPreviewResult 是预览结果。
type ImportPreviewResult struct {
	Detected string         `json:"detected"` // 实际使用的格式
	Cases    []ImportedCase `json:"cases"`
}

// ImportCommitResult 是提交结果：逐用例成败都报告，不做整体事务回滚 ——
// 部分失败时用户改个名重导其余的，比全部回滚再来一遍体验好。
type ImportCommitResult struct {
	Created []CreatedCase `json:"created"`
	Failed  []FailedCase  `json:"failed"`
}

type CreatedCase struct {
	ID   uint64 `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

type FailedCase struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// hrp convert 对 HAR 的占位名；见到它就用文件名替代。
var placeholderNames = map[string]bool{
	"testcase description":                 true,
	"testcase converted from curl command": true,
}

var harJSONCheck = regexp.MustCompile(`"entries"\s*:`)

// detectFormat 推断上传文件的真实格式。
func detectFormat(filename string, content []byte) string {
	trimmed := strings.TrimSpace(string(content))
	switch {
	case strings.HasPrefix(trimmed, "curl "):
		return ImportFormatCurl
	case harJSONCheck.Match(content) && strings.Contains(trimmed, `"log"`):
		return ImportFormatHAR
	// 扩展特征：postman 集合的 schema 域名 / _postman 内部字段 / item+request 结构
	case strings.Contains(trimmed, `"postman_collection"`) || strings.Contains(trimmed, `"_postman"`) ||
		strings.Contains(trimmed, "getpostman.com") ||
		(strings.Contains(trimmed, `"item"`) && strings.Contains(trimmed, `"request"`)):
		return ImportFormatPostman
	}
	// 扩展名兜底
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".har":
		return ImportFormatHAR
	}
	return ""
}

// Preview 转换并返回候选用例列表，不落库。
func (s *ImportService) Preview(projectID uint64, req ImportRequest) (*ImportPreviewResult, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	cases, format, err := s.convert(req)
	if err != nil {
		return nil, err
	}
	if err := s.dedupNames(projectID, cases); err != nil {
		return nil, err
	}
	return &ImportPreviewResult{Detected: format, Cases: cases}, nil
}

// Commit 转换并落库，返回逐用例成败。
func (s *ImportService) Commit(projectID uint64, req ImportRequest, ownerID uint64) (*ImportCommitResult, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	cases, _, err := s.convert(req)
	if err != nil {
		return nil, err
	}
	if err := s.dedupNames(projectID, cases); err != nil {
		return nil, err
	}

	out := &ImportCommitResult{Created: []CreatedCase{}, Failed: []FailedCase{}}
	for i := range cases {
		ic := &cases[i]
		detail, err := s.Case.Create(projectID, *ic.CaseReq, ownerID)
		if err != nil {
			out.Failed = append(out.Failed, FailedCase{Name: ic.Name, Reason: err.Error()})
			continue
		}
		out.Created = append(out.Created, CreatedCase{ID: detail.ID, Code: detail.Code, Name: detail.Name})
	}
	return out, nil
}

// convert 执行「hrp convert → 反解析 → 整形」全链路。
func (s *ImportService) convert(req ImportRequest) ([]ImportedCase, string, error) {
	filename := filepath.Base(strings.TrimSpace(req.Filename))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = "imported.har"
	}
	content := req.Content
	if len(content) == 0 {
		return nil, "", errBadParam("文件内容不能为空")
	}

	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" || format == ImportFormatAuto {
		format = detectFormat(filename, content)
		if format == "" {
			return nil, "", errBadParam(
				"无法识别文件格式（已尝试内容探测与扩展名）。请显式指定 har / postman / curl")
		}
	}
	switch format {
	case ImportFormatHAR, ImportFormatPostman, ImportFormatCurl:
	default:
		return nil, "", errBadParam("不支持的导入格式 %q（可选 har / postman / curl）", format)
	}

	// 临时目录：in/ 放原始文件，out/ 必须先建好（实测 A23：hrp convert 不会自建 -d 目录）。
	// Workspace.Root 在单测里可能不存在（testDeps 不预建目录），先补齐父目录。
	if err := os.MkdirAll(s.Cfg.Workspace.Root, 0o755); err != nil {
		return nil, "", errInternal("准备导入工作目录失败", err)
	}
	tmp, err := os.MkdirTemp(s.Cfg.Workspace.Root, "import-")
	if err != nil {
		return nil, "", errInternal("创建导入临时目录失败", err)
	}
	defer os.RemoveAll(tmp)
	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, "", errInternal("创建转换输出目录失败", err)
	}
	inPath := filepath.Join(tmp, filename)
	if err := os.WriteFile(inPath, content, 0o644); err != nil {
		return nil, "", errInternal("写入上传文件失败", err)
	}

	// 跑 hrp convert。GA4 遥测失败会拖 5 秒（实测 A23），给 60s 超时兜底。
	// hrp 二进制的解析与 run/debug 相同：配置项 → 环境变量 → 约定路径 → PATH。
	bin, err := hrpclient.Resolve(s.Cfg.Engine.BinaryPath)
	if err != nil {
		return nil, "", errInternal("定位 hrp 引擎失败", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fromFlag := map[string]string{
		ImportFormatHAR:     "--from-har",
		ImportFormatPostman: "--from-postman",
		ImportFormatCurl:    "--from-curl",
	}[format]
	cmdArgs := []string{"convert", fromFlag, "--to-yaml", "-d", outDir, inPath}
	out, err := runHrpCommand(ctx, bin, cmdArgs)
	if err != nil {
		return nil, "", errBadParam("hrp convert 执行失败：%s", tailLines(out, err))
	}

	// 收集转换产物（按文件名排序，保证多次转换顺序稳定）。
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, "", errInternal("读取转换产物失败", err)
	}
	var yamlPaths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".yaml" || ext == ".yml" {
			yamlPaths = append(yamlPaths, filepath.Join(outDir, e.Name()))
		}
	}
	if len(yamlPaths) == 0 {
		return nil, "", errBadParam("转换未产出任何用例文件（原始文件里可能没有可转换的请求）")
	}
	sort.Strings(yamlPaths)

	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	result := make([]ImportedCase, 0, len(yamlPaths))
	for idx, p := range yamlPaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, "", errInternal("读取转换产物失败", err)
		}
		parsed, err := decompiler.Parse(string(raw))
		if err != nil {
			return nil, "", response.Newf(response.CodeBadParam, "转换产物反解析失败（%s）：%v", filepath.Base(p), err)
		}
		shapeImported(parsed, base, idx)
		summary := stepSummaries(parsed)
		svcReq, err := toServiceCaseReq(parsed, filename)
		if err != nil {
			return nil, "", errInternal("导入用例结构转换失败", err)
		}
		// 临时 code：imp_ + 清洗后的文件名；唯一化由 dedupNames 兜底。
		svcReq.Code = "imp_" + sanitizeIdent(base)
		ic := ImportedCase{
			Name:       svcReq.Name,
			Code:       svcReq.Code,
			Module:     "imported",
			StepCount:  len(svcReq.Steps),
			Summary:    summary,
			SourceFile: filepath.Base(p),
			CaseReq:    svcReq,
		}
		result = append(result, ic)
	}
	return result, format, nil
}

// shapeImported 补齐 hrp convert 的占位内容（实测 A23）：
//   - config.name 是占位（"testcase description" 等）→ 用上传文件名替代；
//   - HAR 的步骤 name 为空 → 用 "METHOD path" 生成可读名。
func shapeImported(cr *decompiler.CaseReq, fileBase string, idx int) {
	if cr.Name == "" || placeholderNames[cr.Name] {
		cr.Name = fileBase
		if idx > 0 {
			cr.Name = fmt.Sprintf("%s_%d", fileBase, idx+1)
		}
	}
	for i := range cr.Steps {
		st := &cr.Steps[i]
		if strings.TrimSpace(st.Name) != "" || st.Request == nil {
			continue
		}
		u := st.Request.URL
		if u == "" {
			st.Name = fmt.Sprintf("第 %d 步", i+1)
			continue
		}
		// 绝对 URL → "METHOD /path?q=1"；相对 → 原样展示。
		display := u
		if p := strings.Index(u, "://"); p >= 0 {
			rest := u[p+3:]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				display = rest[slash:]
			} else {
				display = "/"
			}
		}
		st.Name = strings.TrimSpace(strings.ToUpper(st.Request.Method) + " " + display)
	}
}

// dedupNames 保证用例名与 code 在项目内唯一：
//   - name 冲突 → 加 _2/_3 后缀；
//   - code 由 "imp_" + 文件名（清洗成合法标识符）生成，冲突同样加后缀。
//
// hrp convert 一次导入通常只有 1 个用例，冲突处理是兜底而非主路径。
func (s *ImportService) dedupNames(projectID uint64, cases []ImportedCase) error {
	var existingNames, existingCodes []string
	if err := s.DB.Model(&model.TestCase{}).
		Where("project_id = ?", projectID).
		Pluck("name", &existingNames).Error; err != nil {
		return errInternal("查询现有用例名失败", err)
	}
	if err := s.DB.Model(&model.TestCase{}).
		Where("project_id = ?", projectID).
		Pluck("code", &existingCodes).Error; err != nil {
		return errInternal("查询现有用例 code 失败", err)
	}
	takenName := map[string]bool{}
	for _, n := range existingNames {
		takenName[n] = true
	}
	takenCode := map[string]bool{}
	for _, c := range existingCodes {
		takenCode[c] = true
	}

	for i := range cases {
		// code 唯一化
		code := cases[i].Code

		// name 唯一化
		name := cases[i].Name
		if takenName[name] {
			for n := 2; ; n++ {
				cand := fmt.Sprintf("%s_%d", name, n)
				if !takenName[cand] {
					name = cand
					break
				}
			}
			cases[i].Name = name
			cases[i].CaseReq.Name = name
		}
		takenName[name] = true

		// code 唯一化
		if takenCode[code] {
			for n := 2; ; n++ {
				cand := fmt.Sprintf("%s_%d", code, n)
				if !takenCode[cand] {
					code = cand
					break
				}
			}
			cases[i].Code = code
			cases[i].CaseReq.Code = code
		}
		takenCode[code] = true
	}
	return nil
}

// sanitizeIdent 把任意字符串清洗成合法 code（^[a-zA-Z0-9_-]{1,64}$）。
func sanitizeIdent(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 48 { // 给后缀留空间
			break
		}
	}
	return b.String()
}

// toServiceCaseReq 把 decompiler 产物转成 service.CaseReq（JSON 中转，与 SaveYAML 同法）。
func toServiceCaseReq(cr *decompiler.CaseReq, sourceFile string) (*CaseReq, error) {
	raw, err := json.Marshal(cr)
	if err != nil {
		return nil, err
	}
	var req CaseReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	req.Status = "active"
	req.Priority = "P1"
	req.Tags = "imported"
	req.Description = "由 hrp convert 从 " + sourceFile + " 导入"
	return &req, nil
}

// stepSummaries 生成每步的「METHOD path」摘要，供预览展示。
// 入参是 decompiler 产物（Request 是具体结构，序列化成 service 层的 jsonx.Any 后就取不到字段了）。
func stepSummaries(cr *decompiler.CaseReq) []string {
	out := make([]string, 0, len(cr.Steps))
	for _, st := range cr.Steps {
		if st.Request == nil {
			out = append(out, st.Name)
			continue
		}
		u := st.Request.URL
		if p := strings.Index(u, "://"); p >= 0 {
			rest := u[p+3:]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				u = rest[slash:]
			} else {
				u = "/"
			}
		}
		out = append(out, strings.ToUpper(st.Request.Method)+" "+u)
	}
	return out
}

// runHrpCommand 执行 hrp 子命令并返回合并输出。与 executor 的子进程管理保持一致的超时语义。
func runHrpCommand(ctx context.Context, bin string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	combined, err := cmd.CombinedOutput()
	return string(combined), err
}

// tailLines 把命令输出与错误压成可读的尾部片段（错误信息里最有用的往往是最后几行）。
func tailLines(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	msg := strings.Join(lines, " | ")
	if err != nil {
		msg += fmt.Sprintf("（exit: %v）", err)
	}
	return msg
}
