package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// OpenAPIService 把 OpenAPI 3.x / Swagger 2.0 文档转成接口定义（F5）。
//
// 三条设计约定：
//   - 只导**接口定义**，不自动生成用例（断言意图机器猜不出）；
//   - 请求体写 `body`、响应示例写 `response_body`——两者语义不同，不能混用；
//   - 预览与提交两段式：提交的 JSON 就是预览的子集，用户可勾选裁剪。
type OpenAPIService struct {
	Deps
}

// openAPIMaxAPIs 是单次导入的接口上限：超过则截断并告警。
// 目的是避免一个几千接口的巨型文档把预览响应撑爆。
const openAPIMaxAPIs = 500

// openAPIMaxRefDepth 是 $ref 解析深度上限（防环 + 防超深嵌套）。
const openAPIMaxRefDepth = 5

// ImportedAPI 是一条待导入的接口（预览与提交共用）。
type ImportedAPI struct {
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Module       string    `json:"module"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Headers      jsonx.Map `json:"headers,omitempty"`
	Params       jsonx.Map `json:"params,omitempty"`
	Body         any       `json:"body,omitempty"`
	BodyType     string    `json:"body_type"`
	ResponseBody any       `json:"response_body,omitempty"`
	ResponseType string    `json:"response_type"`
	Description  string    `json:"description,omitempty"`
	// Conflicts 表示同标识的接口定义已存在（提交时按 policy 处理）。
	Conflicts bool `json:"conflict"`
}

// OpenAPIPreview 是预览结果。
type OpenAPIPreview struct {
	Detected string        `json:"detected"` // openapi3 / swagger2
	Servers  []string      `json:"servers"`  // 文档声明的地址前缀
	APIs     []ImportedAPI `json:"apis"`
	Warnings []string      `json:"warnings"`
}

// OpenAPICommitReq 是提交请求：apis 为预览里勾选的子集。
type OpenAPICommitReq struct {
	APIs   []ImportedAPI `json:"apis"`
	Policy string        `json:"policy"` // skip（默认）/ overwrite
}

// OpenAPICommitFailure 是单条导入失败。
type OpenAPICommitFailure struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// OpenAPICommitResult 是提交结果：逐条报告，不做整体回滚。
type OpenAPICommitResult struct {
	Created     int                    `json:"created"`
	Skipped     int                    `json:"skipped"`
	Overwritten int                    `json:"overwritten"`
	Failed      []OpenAPICommitFailure `json:"failed"`
}

// Preview 解析文档并返回候选项（不落库）。
func (s *OpenAPIService) Preview(projectID uint64, content []byte) (*OpenAPIPreview, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	doc, err := parseOpenAPIDoc(content)
	if err != nil {
		return nil, err
	}
	existing, err := s.existingCodes(projectID)
	if err != nil {
		return nil, err
	}
	for i := range doc.APIs {
		doc.APIs[i].Conflicts = existing[doc.APIs[i].Code]
	}
	return doc, nil
}

// Commit 按策略写入接口定义。
func (s *OpenAPIService) Commit(projectID uint64, req OpenAPICommitReq) (*OpenAPICommitResult, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	policy := strings.ToLower(strings.TrimSpace(req.Policy))
	if policy == "" {
		policy = "skip"
	}
	if policy != "skip" && policy != "overwrite" {
		return nil, errBadParam("policy 只支持 skip / overwrite，收到 %q", req.Policy)
	}
	if len(req.APIs) == 0 {
		return nil, errBadParam("没有可导入的接口")
	}

	out := &OpenAPICommitResult{Failed: []OpenAPICommitFailure{}}
	for _, item := range req.APIs {
		apiReq := APIReq{
			Code:         item.Code,
			Name:         item.Name,
			Module:       item.Module,
			Method:       item.Method,
			Path:         item.Path,
			Headers:      item.Headers,
			Params:       item.Params,
			Body:         jsonx.Any{Val: item.Body},
			BodyType:     item.BodyType,
			ResponseBody: jsonx.Any{Val: item.ResponseBody},
			ResponseType: item.ResponseType,
			Description:  item.Description,
		}
		apiReq, verr := normalizeAPIReq(apiReq)
		if verr != nil {
			out.Failed = append(out.Failed, OpenAPICommitFailure{Code: item.Code, Reason: verr.Error()})
			continue
		}

		var existing model.APIDef
		err := s.DB.Where("project_id = ? AND code = ?", projectID, apiReq.Code).First(&existing).Error
		switch {
		case err == nil:
			if policy == "skip" {
				out.Skipped++
				continue
			}
			existing.Module = apiReq.Module
			existing.Name = apiReq.Name
			existing.Method = apiReq.Method
			existing.Path = apiReq.Path
			existing.Headers = apiReq.Headers
			existing.Params = apiReq.Params
			existing.Body = apiReq.Body
			existing.BodyType = apiReq.BodyType
			existing.ResponseBody = apiReq.ResponseBody
			existing.ResponseType = apiReq.ResponseType
			existing.Description = apiReq.Description
			existing.Source = model.APISourceOpenAPI
			if err := s.DB.Save(&existing).Error; err != nil {
				out.Failed = append(out.Failed, OpenAPICommitFailure{Code: item.Code, Reason: err.Error()})
				continue
			}
			out.Overwritten++
		case isNotFound(err):
			a := &model.APIDef{
				ProjectID:    projectID,
				Module:       apiReq.Module,
				Code:         apiReq.Code,
				Name:         apiReq.Name,
				Method:       apiReq.Method,
				Path:         apiReq.Path,
				Headers:      apiReq.Headers,
				Params:       apiReq.Params,
				Body:         apiReq.Body,
				BodyType:     apiReq.BodyType,
				ResponseBody: apiReq.ResponseBody,
				ResponseType: apiReq.ResponseType,
				Description:  apiReq.Description,
				Source:       model.APISourceOpenAPI,
			}
			if err := s.DB.Create(a).Error; err != nil {
				out.Failed = append(out.Failed, OpenAPICommitFailure{Code: item.Code, Reason: err.Error()})
				continue
			}
			out.Created++
		default:
			out.Failed = append(out.Failed, OpenAPICommitFailure{Code: item.Code, Reason: err.Error()})
		}
	}
	return out, nil
}

func (s *OpenAPIService) existingCodes(projectID uint64) (map[string]bool, error) {
	var codes []string
	if err := s.DB.Model(&model.APIDef{}).Where("project_id = ?", projectID).Pluck("code", &codes).Error; err != nil {
		return nil, errInternal("查询已有接口标识失败", err)
	}
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[c] = true
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

// parseOpenAPIDoc 把文档解析成统一的预览结构。
//
// 兼容两代规范：OpenAPI 3.x 与 Swagger 2.0。差异集中在三处——
// 地址前缀（servers vs host+basePath）、请求体（requestBody vs parameters[in=body]）、
// schema 容器（components.schemas vs definitions），其余结构同形。
func parseOpenAPIDoc(content []byte) (*OpenAPIPreview, error) {
	var root map[string]any
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, errBadParam("解析失败（既不是合法 JSON 也不是合法 YAML）：%s", err.Error())
	}
	if root == nil {
		return nil, errBadParam("文档为空")
	}

	out := &OpenAPIPreview{Warnings: []string{}}

	var prefix string
	if v := asString(root["openapi"]); strings.HasPrefix(v, "3.") {
		out.Detected = "openapi3"
		out.Servers = serverURLs(root)
		if len(out.Servers) > 0 {
			prefix = strings.TrimRight(out.Servers[0], "/")
		}
	} else if asString(root["swagger"]) == "2.0" {
		out.Detected = "swagger2"
		if p := swaggerPrefix(root); p != "" {
			out.Servers = []string{p}
			prefix = strings.TrimRight(p, "/")
		}
	} else {
		return nil, errBadParam("无法识别文档版本：既没有 openapi: 3.x，也没有 swagger: \"2.0\"")
	}
	if prefix == "" {
		prefix = "$base_url"
		out.Warnings = append(out.Warnings, "文档未声明服务器地址，接口路径使用 $base_url 前缀（按环境替换）")
	}

	paths := asMap(root["paths"])
	if len(paths) == 0 {
		return nil, errBadParam("文档里没有 paths，无法导入")
	}

	seen := map[string]bool{}
	truncated := false
	warnRef := map[string]bool{}

	pathKeys := make([]string, 0, len(paths))
	for k := range paths {
		pathKeys = append(pathKeys, k)
	}
	sort.Strings(pathKeys)

	for _, rawPath := range pathKeys {
		item := asMap(paths[rawPath])
		if item == nil {
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
			op := asMap(item[method])
			if op == nil {
				continue
			}
			if len(out.APIs) >= openAPIMaxAPIs {
				truncated = true
				break
			}
			api := buildImportedAPI(root, out.Detected, rawPath, method, op, prefix, seen)
			api.Conflicts = false
			out.APIs = append(out.APIs, api)

			// $ref 告警去重后收集
			for _, name := range collectRefs(op) {
				if warnRef[name] {
					continue
				}
				warnRef[name] = true
			}
		}
		if truncated {
			break
		}
	}

	if truncated {
		out.Warnings = append(out.Warnings,
			fmt.Sprintf("接口数超过 %d 条，已截断（请拆分文档后分批导入）", openAPIMaxAPIs))
	}
	out.Warnings = append(out.Warnings,
		"已忽略文档中的 security / callbacks / links（平台不执行鉴权与回调模拟）")
	if len(out.APIs) == 0 {
		return nil, errBadParam("文档里没有可导入的接口（paths 下没有 HTTP 操作）")
	}
	return out, nil
}

func buildImportedAPI(root map[string]any, version, rawPath, method string, op map[string]any,
	prefix string, seen map[string]bool) ImportedAPI {
	api := ImportedAPI{
		Method:       strings.ToUpper(method),
		Path:         prefix + rawPath,
		BodyType:     model.BodyTypeNone,
		ResponseType: model.ResponseTypeNone,
	}
	api.Name = firstNonEmpty(asString(op["summary"]), asString(op["operationId"]), api.Method+" "+rawPath)
	api.Description = asString(op["description"])

	// 服务（module）：tags[0] → 路径首段 → 未分类
	if tags := asSlice(op["tags"]); len(tags) > 0 {
		api.Module = asString(tags[0])
	} else {
		api.Module = firstPathSegment(rawPath)
	}

	// 标识：operationId → method_path；清洗 + 去重
	code := asString(op["operationId"])
	if code == "" {
		code = method + "_" + strings.TrimPrefix(rawPath, "/")
	}
	code = sanitizeIdent(code)
	if code == "" {
		code = "api"
	}
	base := code
	for i := 2; seen[code]; i++ {
		code = base + "_" + strconv.Itoa(i)
	}
	seen[code] = true
	api.Code = code

	// 查询参数（path 参数已在路径模板里，不重复落库）
	params := jsonx.Map{}
	for _, p := range asSlice(op["parameters"]) {
		pm := asMap(p)
		if pm == nil || asString(pm["in"]) != "query" {
			continue
		}
		name := asString(pm["name"])
		if name == "" {
			continue
		}
		// OpenAPI 3 的 schema 在 parameter.schema 下；Swagger 2 直接写在参数上
		schema := pm["schema"]
		if schema == nil {
			schema = pm
		}
		params[name] = sampleFromSchema(root, schema, 0, map[string]bool{})
	}
	if len(params) > 0 {
		api.Params = params
	}

	// 请求体
	body, bodyType := requestBodyOf(root, version, op)
	if body != nil {
		api.Body = body
		api.BodyType = bodyType
	}

	// 响应示例（写 response_body：Mock 用它，请求体不被污染）
	resp, respType := responseBodyOf(root, version, op)
	if resp != nil {
		api.ResponseBody = resp
		api.ResponseType = respType
	}
	return api
}

// requestBodyOf 提取请求体与类型。
func requestBodyOf(root map[string]any, version string, op map[string]any) (any, string) {
	if version == "swagger2" {
		for _, p := range asSlice(op["parameters"]) {
			pm := asMap(p)
			if pm == nil || asString(pm["in"]) != "body" {
				continue
			}
			return sampleFromSchema(root, pm["schema"], 0, map[string]bool{}), model.BodyTypeJSON
		}
		return nil, model.BodyTypeNone
	}

	rb := asMap(op["requestBody"])
	content := asMap(rb["content"])
	if len(content) == 0 {
		return nil, model.BodyTypeNone
	}
	ct := pickContentType(content, "application/json", "application/x-www-form-urlencoded", "multipart/form-data")
	media := asMap(content[ct])
	if media == nil {
		return nil, model.BodyTypeNone
	}
	body := mediaExample(root, media)
	switch ct {
	case "application/json":
		return body, model.BodyTypeJSON
	case "application/x-www-form-urlencoded", "multipart/form-data":
		return body, model.BodyTypeForm
	default:
		if s, ok := body.(string); ok {
			return s, model.BodyTypeRaw
		}
		return body, model.BodyTypeJSON
	}
}

// responseBodyOf 提取响应示例与类型（优先 200 → 201 → 第一个 2xx）。
func responseBodyOf(root map[string]any, version string, op map[string]any) (any, string) {
	responses := asMap(op["responses"])
	if len(responses) == 0 {
		return nil, model.ResponseTypeNone
	}
	key := pickResponseKey(responses)
	if key == "" {
		return nil, model.ResponseTypeNone
	}
	r := asMap(responses[key])
	if r == nil {
		return nil, model.ResponseTypeNone
	}

	if version == "swagger2" {
		if ex := asMap(r["examples"]); len(ex) > 0 {
			if v, ok := ex["application/json"]; ok {
				return v, model.ResponseTypeJSON
			}
			for _, v := range ex {
				if s, ok := v.(string); ok {
					return s, model.ResponseTypeRaw
				}
				return v, model.ResponseTypeJSON
			}
		}
		if sch := r["schema"]; sch != nil {
			return sampleFromSchema(root, sch, 0, map[string]bool{}), model.ResponseTypeJSON
		}
		return nil, model.ResponseTypeNone
	}

	content := asMap(r["content"])
	if len(content) == 0 {
		return nil, model.ResponseTypeNone
	}
	ct := pickContentType(content, "application/json")
	media := asMap(content[ct])
	if media == nil {
		return nil, model.ResponseTypeNone
	}
	body := mediaExample(root, media)
	if strings.HasPrefix(ct, "text/") {
		if s, ok := body.(string); ok {
			return s, model.ResponseTypeRaw
		}
	}
	return body, model.ResponseTypeJSON
}

// mediaExample 取 example / examples 的第一个值，否则从 schema 生成。
func mediaExample(root map[string]any, media map[string]any) any {
	if v, ok := media["example"]; ok && v != nil {
		return v
	}
	if exs := asMap(media["examples"]); len(exs) > 0 {
		keys := sortedKeys(exs)
		if em := asMap(exs[keys[0]]); em != nil {
			if v, ok := em["value"]; ok {
				return v
			}
		}
	}
	return sampleFromSchema(root, media["schema"], 0, map[string]bool{})
}

// sampleFromSchema 从 JSON Schema 生成示例值。
//
// 优先级：example → default → enum[0] → 按类型占位。$ref 递归解析，
// 深度上限 openAPIMaxRefDepth，命中环时给空对象（不抛错、不挂起）。
func sampleFromSchema(root map[string]any, schema any, depth int, seen map[string]bool) any {
	m := asMap(schema)
	if m == nil {
		return nil
	}
	if ref := asString(m["$ref"]); ref != "" {
		if depth >= openAPIMaxRefDepth || seen[ref] {
			return map[string]any{}
		}
		next := make(map[string]bool, len(seen)+1)
		for k := range seen {
			next[k] = true
		}
		next[ref] = true
		resolved, ok := resolveLocalRef(root, ref)
		if !ok {
			return map[string]any{}
		}
		return sampleFromSchema(root, resolved, depth+1, next)
	}
	if v, ok := m["example"]; ok && v != nil {
		return v
	}
	if v, ok := m["default"]; ok && v != nil {
		return v
	}
	if en := asSlice(m["enum"]); len(en) > 0 {
		return en[0]
	}
	// 组合：allOf 合并对象；oneOf/anyOf 取第一个
	if all := asSlice(m["allOf"]); len(all) > 0 {
		merged := map[string]any{}
		for _, sub := range all {
			if obj, ok := sampleFromSchema(root, sub, depth+1, seen).(map[string]any); ok {
				for k, v := range obj {
					merged[k] = v
				}
			}
		}
		return merged
	}
	if one := asSlice(m["oneOf"]); len(one) > 0 {
		return sampleFromSchema(root, one[0], depth+1, seen)
	}
	if anyOf := asSlice(m["anyOf"]); len(anyOf) > 0 {
		return sampleFromSchema(root, anyOf[0], depth+1, seen)
	}

	typ := asString(m["type"])
	if typ == "" {
		if _, ok := m["properties"]; ok {
			typ = "object"
		} else {
			return nil
		}
	}
	switch typ {
	case "object":
		out := map[string]any{}
		props := asMap(m["properties"])
		for _, k := range sortedKeys(props) {
			out[k] = sampleFromSchema(root, props[k], depth+1, seen)
		}
		return out
	case "array":
		item := sampleFromSchema(root, m["items"], depth+1, seen)
		if item == nil {
			return []any{}
		}
		return []any{item}
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "string":
		switch asString(m["format"]) {
		case "date-time":
			return "2020-01-01T00:00:00Z"
		case "date":
			return "2020-01-01"
		}
		return "string"
	default:
		return nil
	}
}

// resolveLocalRef 解析文档内引用（#/components/schemas/X、#/definitions/X）。
func resolveLocalRef(root map[string]any, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	cur := any(root)
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m := asMap(cur)
		if m == nil {
			return nil, false
		}
		v, ok := m[seg]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func serverURLs(root map[string]any) []string {
	var out []string
	for _, s := range asSlice(root["servers"]) {
		if u := asString(asMap(s)["url"]); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// swaggerPrefix 组装 Swagger 2.0 的地址前缀：schemes[0]://host + basePath。
func swaggerPrefix(root map[string]any) string {
	host := asString(root["host"])
	if host == "" {
		return ""
	}
	scheme := "http"
	if s := asSlice(root["schemes"]); len(s) > 0 {
		if v := asString(s[0]); v != "" {
			scheme = v
		}
	}
	return scheme + "://" + host + asString(root["basePath"])
}

func pickContentType(content map[string]any, prefer ...string) string {
	for _, p := range prefer {
		if _, ok := content[p]; ok {
			return p
		}
	}
	keys := sortedKeys(content)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func pickResponseKey(responses map[string]any) string {
	for _, k := range []string{"200", "201", "202", "204"} {
		if _, ok := responses[k]; ok {
			return k
		}
	}
	keys := sortedKeys(responses)
	for _, k := range keys {
		if strings.HasPrefix(k, "2") {
			return k
		}
	}
	return ""
}

// firstPathSegment 取路径首段作为服务名：/users/{id} → users。
func firstPathSegment(p string) string {
	for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if seg == "" || strings.HasPrefix(seg, "{") {
			continue
		}
		return seg
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// collectRefs 收集操作里的 $ref（仅用于告警统计）。
func collectRefs(op map[string]any) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if ref := asString(t["$ref"]); ref != "" {
				out = append(out, ref)
			}
			for _, val := range t {
				walk(val)
			}
		case []any:
			for _, val := range t {
				walk(val)
			}
		}
	}
	walk(op)
	return out
}
