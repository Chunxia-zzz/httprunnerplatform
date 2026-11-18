package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// MockService 提供按接口定义自动生成的 Mock 响应（F4）。
//
// 三条设计约定：
//   - **不建手工规则表**：响应体直接取自 api_def，改接口定义等于改 mock；
//   - 命中日志是**内存态**（每项目最多 mockHitCap 条，重启即失），
//     只服务于当次联调排障，不进数据库（避免结果表膨胀）；
//   - 匹配是纯函数（normalizeMockPath / matchMockPath），便于表驱动测试。
type MockService struct {
	Deps
	mu   sync.Mutex
	hits map[uint64][]MockHit
}

// mockHitCap 是每项目保留的命中记录条数。
const mockHitCap = 200

// MockHit 是一条命中记录（不落库）。
type MockHit struct {
	At      string `json:"at"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	APICode string `json:"api_code"`
	Status  int    `json:"status"`
}

// MockInfo 是 Mock 页需要的项目级信息。
type MockInfo struct {
	Enabled    bool      `json:"enabled"`
	BasePath   string    `json:"base_path"`
	APIs       []MockAPI `json:"apis"`
	SampleCurl string    `json:"sample_curl"`
}

// MockAPI 是可被 mock 的接口摘要。
type MockAPI struct {
	ID     uint64 `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Module string `json:"module"`
	Method string `json:"method"`
	Path   string `json:"path"`
	// HasBody 表示该接口定义了响应体（未定义时 mock 返回空体）。
	HasBody bool `json:"has_body"`
}

// SetEnabled 开关项目的 Mock。
func (s *MockService) SetEnabled(projectID uint64, enabled bool) error {
	p, err := loadProject(s.DB, projectID)
	if err != nil {
		return err
	}
	if err := s.DB.Model(p).Update("mock_enabled", enabled).Error; err != nil {
		return errInternal("更新 Mock 开关失败", err)
	}
	return nil
}

// Info 返回 Mock 页所需信息。
func (s *MockService) Info(projectID uint64) (*MockInfo, error) {
	p, err := loadProject(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	var list []model.APIDef
	if err := s.DB.Where("project_id = ?", projectID).Order("module asc, code asc").Find(&list).Error; err != nil {
		return nil, errInternal("查询接口定义失败", err)
	}
	apis := make([]MockAPI, 0, len(list))
	for i := range list {
		apis = append(apis, MockAPI{
			ID:      list[i].ID,
			Code:    list[i].Code,
			Name:    list[i].Name,
			Module:  list[i].Module,
			Method:  list[i].Method,
			Path:    list[i].Path,
			HasBody: hasMockBody(&list[i]),
		})
	}
	info := &MockInfo{
		Enabled:  p.MockEnabled,
		BasePath: "/mock/" + p.Code,
		APIs:     apis,
	}
	if len(apis) > 0 {
		info.SampleCurl = fmt.Sprintf("curl -i %s%s", info.BasePath, normalizeMockPath(apis[0].Path))
	}
	return info, nil
}

// Serve 处理一次 mock 请求，返回状态码、头与响应体。
//
// projectCode 是项目对外标识（不是内部 id）：mock 调用方是前端/被测系统，
// 不应该看到平台内部编号。
func (s *MockService) Serve(projectCode, method, reqPath string) (int, map[string]string, []byte) {
	var p model.Project
	if err := s.DB.Where("code = ?", projectCode).First(&p).Error; err != nil {
		return http.StatusNotFound, nil, mockError("项目 %q 不存在", projectCode)
	}
	if !p.MockEnabled {
		return http.StatusNotFound, nil, mockError("项目 %q 未开启 Mock：请在「Mock 服务」页打开开关", projectCode)
	}

	var list []model.APIDef
	if err := s.DB.Where("project_id = ?", p.ID).Order("id asc").Find(&list).Error; err != nil {
		return http.StatusInternalServerError, nil, mockError("查询接口定义失败")
	}
	if len(list) == 0 {
		return http.StatusNotFound, nil,
			mockError("项目 %q 还没有接口定义：先在「接口库」里建接口", projectCode)
	}

	method = strings.ToUpper(strings.TrimSpace(method))
	var pathMatched *model.APIDef
	for i := range list {
		if !matchMockPath(list[i].Path, reqPath) {
			continue
		}
		if pathMatched == nil {
			pathMatched = &list[i]
		}
		if strings.ToUpper(list[i].Method) == method {
			body, ctype := mockBody(&list[i])
			s.record(p.ID, MockHit{
				At: time.Now().Format("2006-01-02 15:04:05"), Method: method,
				Path: normalizeMockPath(reqPath), APICode: list[i].Code, Status: http.StatusOK,
			})
			return http.StatusOK, map[string]string{
				"Content-Type":  ctype,
				"X-Mock-Source": "apidef:" + list[i].Code,
			}, body
		}
	}
	if pathMatched != nil {
		s.record(p.ID, MockHit{
			At: time.Now().Format("2006-01-02 15:04:05"), Method: method,
			Path: normalizeMockPath(reqPath), APICode: pathMatched.Code, Status: http.StatusMethodNotAllowed,
		})
		return http.StatusMethodNotAllowed, nil,
			mockError("路径命中接口 %q，但方法不匹配（定义 %s，请求 %s）",
				pathMatched.Code, strings.ToUpper(pathMatched.Method), method)
	}

	s.record(p.ID, MockHit{
		At: time.Now().Format("2006-01-02 15:04:05"), Method: method,
		Path: normalizeMockPath(reqPath), APICode: "", Status: http.StatusNotFound,
	})
	return http.StatusNotFound, nil,
		mockError("未命中任何接口定义（本项目共 %d 条）。已配置的路径：%s",
			len(list), mockPathSummary(list))
}

// Hits 返回项目最近的命中记录（倒序，最多 limit 条）。
func (s *MockService) Hits(projectID uint64, limit int) []MockHit {
	if limit <= 0 || limit > mockHitCap {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.hits[projectID]
	out := make([]MockHit, 0, limit)
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, list[i])
	}
	return out
}

func (s *MockService) record(projectID uint64, h MockHit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hits == nil {
		s.hits = make(map[uint64][]MockHit)
	}
	list := append(s.hits[projectID], h)
	if len(list) > mockHitCap {
		list = list[len(list)-mockHitCap:]
	}
	s.hits[projectID] = list
}

// ---------------------------------------------------------------------------
// 匹配（纯函数，单测重点）
// ---------------------------------------------------------------------------

// normalizeMockPath 把接口定义或请求里的路径归一成「以 / 开头、无 query、
// 无 host/协议、无 $base_url、无结尾斜杠」的形态。
//
// 定义侧可能出现：`$base_url/users/{id}`、`https://api.x.com/users/:id`、
// `/users`、`users`；请求侧可能是 `/users/123?page=1`。归一后可直接按段比较。
func normalizeMockPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "$base_url")
	// 去协议与 host：`https://x.com/users` → `/users`
	if i := strings.Index(p, "://"); i >= 0 {
		rest := p[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			p = rest[j:]
		} else {
			p = "/"
		}
	}
	// 去 query 与 fragment
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// matchMockPath 判断请求路径是否命中定义路径。
// 定义中的 {x} / :x / $x 视为「单段通配」，其余段精确匹配（大小写敏感）。
func matchMockPath(defPath, reqPath string) bool {
	d := splitMockSegments(normalizeMockPath(defPath))
	r := splitMockSegments(normalizeMockPath(reqPath))
	if len(d) != len(r) {
		return false
	}
	for i := range d {
		if isMockPlaceholder(d[i]) {
			continue
		}
		if d[i] != r[i] {
			return false
		}
	}
	return true
}

func splitMockSegments(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func isMockPlaceholder(seg string) bool {
	if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && len(seg) > 2 {
		return true
	}
	if (strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "$")) && len(seg) > 1 {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------

// hasMockBody 表示接口定义里是否有可回放的响应体。
//
// F5 起优先看 response_body（从 OpenAPI 导入的响应示例），
// 为空时回退 body —— 保持 F1 手工建接口的既有行为不变。
func hasMockBody(a *model.APIDef) bool {
	if hasBodyValue(a.ResponseBody, a.ResponseType) {
		return true
	}
	return hasBodyValue(a.Body, a.BodyType)
}

func hasBodyValue(body jsonx.Any, bodyType string) bool {
	if body.Val == nil {
		return false
	}
	switch strings.ToLower(bodyType) {
	case model.BodyTypeNone, "":
		return false
	case model.BodyTypeRaw:
		s, ok := body.Val.(string)
		return ok && s != ""
	}
	return true
}

// mockBody 把接口定义渲染成响应体与 Content-Type。
//
// 优先级：response_body（导入的响应示例）→ body（F1 的请求体，兼作示例）。
// form 也按 JSON 返回：平台里的 form 是「字段对」，联调方拿到 JSON 更可用；
// 返回内容永远来自用户自己写的接口定义，不虚构字段。
func mockBody(a *model.APIDef) ([]byte, string) {
	body, bodyType := a.ResponseBody, a.ResponseType
	if !hasBodyValue(body, bodyType) {
		body, bodyType = a.Body, a.BodyType
	}
	if !hasBodyValue(body, bodyType) {
		return nil, "application/json; charset=utf-8"
	}
	if strings.ToLower(bodyType) == model.BodyTypeRaw {
		if s, ok := body.Val.(string); ok {
			return []byte(s), "text/plain; charset=utf-8"
		}
	}
	b, err := json.Marshal(body.Val)
	if err != nil {
		return nil, "application/json; charset=utf-8"
	}
	return b, "application/json; charset=utf-8"
}

// mockError 生成结构化错误体（mock 是公开端点，也保持统一可读格式）。
func mockError(format string, args ...any) []byte {
	b, _ := json.Marshal(map[string]any{
		"code":    -1,
		"message": fmt.Sprintf(format, args...),
		"data":    nil,
	})
	return b
}

// mockPathSummary 列出已配置路径，用于未命中提示（最多 8 条）。
func mockPathSummary(list []model.APIDef) string {
	parts := make([]string, 0, 8)
	for i := range list {
		if i >= 8 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, strings.ToUpper(list[i].Method)+" "+normalizeMockPath(list[i].Path))
	}
	return strings.Join(parts, ", ")
}
