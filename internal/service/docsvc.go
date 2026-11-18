package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// DocService 从接口库与用例生成接口文档（F6）。
//
// 两条设计约定：
//   - **实时生成**，不做快照：接口改了文档立即跟上（与「改一处、全部生效」一致）；
//   - HTML 用 html/template 渲染，用户内容自动转义，禁止字符串拼接。
type DocService struct {
	Deps
}

// APIDoc 是一份完整的项目接口文档。
type APIDoc struct {
	Project     string     `json:"project"`
	ProjectCode string     `json:"project_code"`
	GeneratedAt string     `json:"generated_at"`
	TotalAPIs   int        `json:"total_apis"`
	CoveredAPIs int        `json:"covered_apis"`
	Groups      []DocGroup `json:"groups"`
}

// DocGroup 是一个服务（模块）下的接口。
type DocGroup struct {
	Module string   `json:"module"`
	APIs   []DocAPI `json:"apis"`
}

// DocAPI 是文档里的一个接口。
type DocAPI struct {
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Description  string    `json:"description"`
	Headers      jsonx.Map `json:"headers,omitempty"`
	Params       jsonx.Map `json:"params,omitempty"`
	BodyType     string    `json:"body_type"`
	BodyText     string    `json:"body_text,omitempty"`
	ResponseType string    `json:"response_type"`
	ResponseText string    `json:"response_text,omitempty"`
	Cases        []DocCase `json:"cases"`
}

// DocCase 是「引用该接口的用例」摘要。
type DocCase struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	LastStatus string `json:"last_status"`
}

// docModuleUnclassified 与接口库保持一致。
const docModuleUnclassified = "未分类"

// Build 生成文档结构（JSON 与 HTML 共用的中间表示）。
func (s *DocService) Build(projectID uint64) (*APIDoc, error) {
	project, err := loadProject(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	var apis []model.APIDef
	if err := s.DB.Where("project_id = ?", projectID).Order("module asc, code asc").Find(&apis).Error; err != nil {
		return nil, errInternal("查询接口定义失败", err)
	}

	refs, err := s.caseRefs(projectID)
	if err != nil {
		return nil, err
	}

	doc := &APIDoc{
		Project:     project.Name,
		ProjectCode: project.Code,
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		TotalAPIs:   len(apis),
		Groups:      []DocGroup{},
	}

	byModule := map[string][]DocAPI{}
	modules := []string{}
	covered := 0
	for i := range apis {
		a := &apis[i]
		cases := refs[a.ID]
		if len(cases) > 0 {
			covered++
		}
		mod := a.Module
		if mod == "" {
			mod = docModuleUnclassified
		}
		if _, ok := byModule[mod]; !ok {
			modules = append(modules, mod)
		}
		byModule[mod] = append(byModule[mod], DocAPI{
			Code:         a.Code,
			Name:         a.Name,
			Method:       a.Method,
			Path:         a.Path,
			Description:  a.Description,
			Headers:      a.Headers,
			Params:       a.Params,
			BodyType:     a.BodyType,
			BodyText:     prettyBody(a.Body),
			ResponseType: a.ResponseType,
			ResponseText: prettyResponse(a.ResponseBody, a.ResponseType, a.Body),
			Cases:        cases,
		})
	}
	doc.CoveredAPIs = covered

	sort.SliceStable(modules, func(i, j int) bool {
		// 「未分类」永远排最后，其余按字典序
		if modules[i] == docModuleUnclassified {
			return false
		}
		if modules[j] == docModuleUnclassified {
			return true
		}
		return modules[i] < modules[j]
	})
	for _, m := range modules {
		doc.Groups = append(doc.Groups, DocGroup{Module: m, APIs: byModule[m]})
	}
	return doc, nil
}

// caseRefs 返回 api_id → 引用它的用例列表（含最近执行状态）。
func (s *DocService) caseRefs(projectID uint64) (map[uint64][]DocCase, error) {
	type row struct {
		RefAPIID   uint64
		Code       string
		Name       string
		LastStatus string
	}
	var rows []row
	err := s.DB.Table("test_step").
		Select("test_step.ref_api_id AS ref_api_id, test_case.code AS code, test_case.name AS name, test_case.last_status AS last_status").
		Joins("JOIN test_case ON test_case.id = test_step.case_id").
		Where("test_step.step_type = ? AND test_case.project_id = ?", model.StepAPI, projectID).
		Order("test_case.code ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("查询接口关联用例失败", err)
	}
	out := map[uint64][]DocCase{}
	for _, r := range rows {
		out[r.RefAPIID] = append(out[r.RefAPIID], DocCase{Code: r.Code, Name: r.Name, LastStatus: r.LastStatus})
	}
	return out, nil
}

// HTML 渲染文档页面。
func (s *DocService) HTML(projectID uint64) ([]byte, error) {
	doc, err := s.Build(projectID)
	if err != nil {
		return nil, err
	}
	return renderDocHTML(doc)
}

// ErrorPageHTML 渲染公开链接的失败提示页（过期/失效共用一套样式）。
func ErrorPageHTML(title, message string, status int) []byte {
	var buf bytes.Buffer
	_ = docErrorTemplate.Execute(&buf, map[string]any{
		"Title":   title,
		"Message": message,
		"Status":  status,
	})
	return buf.Bytes()
}

// ---------------------------------------------------------------------------

func prettyBody(a jsonx.Any) string {
	if a.Val == nil {
		return ""
	}
	if s, ok := a.Val.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(a.Val, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// prettyResponse 优先响应示例，为空时回退请求体（与 Mock 的取值口径一致）。
func prettyResponse(resp jsonx.Any, respType string, fallback jsonx.Any) string {
	if respType != "" && respType != model.ResponseTypeNone && resp.Val != nil {
		return prettyBody(resp)
	}
	return ""
}

var docTemplate = template.Must(template.New("doc").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Project}} · 接口文档</title>
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  body { margin: 0; font: 14px/1.6 -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; color: #1f2328; background: #f6f8fa; }
  header { background: #fff; border-bottom: 1px solid #d8dee4; padding: 20px 28px; position: sticky; top: 0; z-index: 2; }
  h1 { margin: 0 0 4px; font-size: 20px; }
  .meta { color: #57606a; font-size: 12px; }
  .stats { margin-top: 8px; font-size: 13px; }
  .stats b { color: #0969da; }
  main { display: flex; gap: 24px; padding: 20px 28px 48px; align-items: flex-start; }
  nav { width: 220px; flex: none; position: sticky; top: 96px; max-height: calc(100vh - 120px); overflow: auto; background: #fff; border: 1px solid #d8dee4; border-radius: 8px; padding: 10px; font-size: 13px; }
  nav a { color: #1f2328; text-decoration: none; display: block; padding: 3px 6px; border-radius: 4px; }
  nav a:hover { background: #f3f4f6; }
  nav .group { font-weight: 600; margin-top: 6px; }
  section.doc { flex: 1; min-width: 0; }
  .group-title { font-size: 16px; font-weight: 600; margin: 22px 0 10px; }
  .api { background: #fff; border: 1px solid #d8dee4; border-radius: 8px; padding: 14px 16px; margin-bottom: 12px; }
  .api-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .method { font: 12px/1 ui-monospace, monospace; font-weight: 700; padding: 4px 7px; border-radius: 5px; background: #ddf4ff; color: #0969da; }
  .method.POST { background: #dafbe1; color: #1a7f37; }
  .method.PUT, .method.PATCH { background: #fff8c5; color: #9a6700; }
  .method.DELETE { background: #ffebe9; color: #cf222e; }
  .path { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 13px; word-break: break-all; }
  .api-name { color: #57606a; font-size: 12px; margin-left: auto; }
  .desc { color: #57606a; font-size: 13px; margin-top: 6px; white-space: pre-wrap; }
  .label { font-size: 12px; color: #57606a; margin: 10px 0 4px; font-weight: 600; }
  pre { background: #f6f8fa; border: 1px solid #eaeef2; border-radius: 6px; padding: 10px; overflow: auto; font-size: 12px; line-height: 1.5; margin: 0; }
  table { width: 100%; border-collapse: collapse; font-size: 12px; }
  th, td { text-align: left; padding: 5px 8px; border-bottom: 1px solid #eaeef2; vertical-align: top; }
  th { color: #57606a; font-weight: 500; width: 30%; }
  .case { display: inline-block; font-size: 12px; border: 1px solid #d8dee4; border-radius: 999px; padding: 1px 8px; margin: 2px 6px 2px 0; }
  .case.pass { border-color: #4ac26b; color: #1a7f37; }
  .case.fail { border-color: #ff8182; color: #cf222e; }
  .empty { color: #57606a; padding: 40px 0; text-align: center; }
  @media print {
    header { position: static; }
    main { display: block; padding: 0; }
    nav { display: none; }
    .api { break-inside: avoid; }
  }
</style>
</head>
<body>
<header>
  <h1>{{.Project}} · 接口文档</h1>
  <div class="meta">项目标识 {{.ProjectCode}} ｜ 生成于 {{.GeneratedAt}} ｜ 本页为只读视图</div>
  <div class="stats">接口 <b>{{.TotalAPIs}}</b> 个，被用例覆盖 <b>{{.CoveredAPIs}}</b> 个</div>
</header>
<main>
  <nav>
    {{range .Groups}}
      <a class="group" href="#g-{{.Module}}">{{.Module}}（{{len .APIs}}）</a>
      {{range .APIs}}<a href="#a-{{.Code}}">{{.Method}} {{.Path}}</a>{{end}}
    {{end}}
  </nav>
  <section class="doc">
  {{if eq .TotalAPIs 0}}
    <p class="empty">这个项目还没有接口定义。到「接口库」建接口后，文档会自动生成。</p>
  {{end}}
  {{range .Groups}}
    <h2 class="group-title" id="g-{{.Module}}">{{.Module}}</h2>
    {{range .APIs}}
      <div class="api" id="a-{{.Code}}">
        <div class="api-head">
          <span class="method {{.Method}}">{{.Method}}</span>
          <span class="path">{{.Path}}</span>
          <span class="api-name">{{.Name}} · {{.Code}}</span>
        </div>
        {{if .Description}}<div class="desc">{{.Description}}</div>{{end}}
        {{if .Headers}}
          <div class="label">请求头</div>
          <table>{{range $k, $v := .Headers}}<tr><th>{{$k}}</th><td>{{$v}}</td></tr>{{end}}</table>
        {{end}}
        {{if .Params}}
          <div class="label">查询参数</div>
          <table>{{range $k, $v := .Params}}<tr><th>{{$k}}</th><td>{{$v}}</td></tr>{{end}}</table>
        {{end}}
        {{if .BodyText}}
          <div class="label">请求体（{{.BodyType}}）</div>
          <pre>{{.BodyText}}</pre>
        {{end}}
        {{if .ResponseText}}
          <div class="label">响应示例（{{.ResponseType}}）</div>
          <pre>{{.ResponseText}}</pre>
        {{end}}
        <div class="label">测试覆盖</div>
        {{if .Cases}}
          {{range .Cases}}<span class="case {{.LastStatus}}">{{.Name}}（{{.Code}}）</span>{{end}}
        {{else}}
          <span class="empty-inline" style="color:#57606a;font-size:12px">暂无用例引用这个接口</span>
        {{end}}
      </div>
    {{end}}
  {{end}}
  </section>
</main>
</body>
</html>
`))

var docErrorTemplate = template.Must(template.New("err").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>{{.Title}}</title>
<style>body{font:14px/1.6 -apple-system,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#f6f8fa;color:#1f2328;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
.card{background:#fff;border:1px solid #d8dee4;border-radius:10px;padding:28px 32px;max-width:520px}
h1{font-size:18px;margin:0 0 8px}p{color:#57606a;margin:0}.code{font:12px ui-monospace,monospace;color:#57606a;margin-top:12px}</style>
</head><body><div class="card"><h1>{{.Title}}</h1><p>{{.Message}}</p><div class="code">HTTP {{.Status}}</div></div></body></html>
`))

func renderDocHTML(doc *APIDoc) ([]byte, error) {
	var buf bytes.Buffer
	if err := docTemplate.Execute(&buf, doc); err != nil {
		return nil, fmt.Errorf("渲染文档失败: %w", err)
	}
	return buf.Bytes(), nil
}
