package validator

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// 字段白名单校验 —— 实测 A7 的直接产物。
//
// 为什么必须有这一层：
//
//	hrp **不做任何 YAML schema 校验**。字段名拼错、字段放错层级，
//	引擎既不报错也不告警，只是**不生效**（实测 A7/A10/A13）。
//
// 语义校验（Validate）看的是 DB 模型，它无法回答"最终写进文件的那份 YAML
// 引擎认不认"。因此渲染完还要再查一遍键名。
//
// 白名单的来源是**实测**，不是官方文档 ——
// 官方文档没写清 `timeout` / `setup_hooks` 的合法层级，
// 而实测证明它们的层级是硬性的（A8/A10/A12/A13）。

// 各层允许出现的键。
//
// 键名一律小写比较，因为引擎用的是 mapstructure 的默认行为
// （大小写不敏感匹配字段名）。
var (
	allowedRoot = keySet("config", "teststeps")

	allowedConfig = keySet(
		"name",       // 必填
		"variables",  // 变量
		"parameters", // 参数化数据集（实测 A11 生效）
		"headers",    // 请求头（M1 不写，但引擎支持）
		"verify",     // TLS 校验开关
		"export",     // 导出变量
		"weight",     // 权重（实测 A11 生效）
		"path",       // 用例引用路径（M2）
		"base_url",   // v4.1 起已废弃，迁到 .env
	)

	allowedTestStep = keySet(
		"name",
		"variables",
		"setup_hooks", // ⚠️ 只有这一层生效（实测 A12/A13）
		"request",
		"extract",
		"validate",
		"teardown_hooks", // ⚠️ 同上
		// M2 起开放
		"api",
		"testcase",
		"think_time",
		"transaction",
		"rendezvous",
		"websocket",
	)

	allowedRequest = keySet(
		"method",
		"url",
		"params",
		"headers",
		"cookies",
		"data",    // 实测：form / raw 用它
		"json",    // 实测：引擎自动补 application/json
		"body",    // 实测：需显式给 Content-Type
		"timeout", // ⚠️ 只有这一层生效（实测 A8/A10），单位秒
	)
)

// deprecatedKeys 是引擎仍接受、但已废弃的键：只告警，不阻断。
var deprecatedKeys = map[string]string{
	"base_url": "base_url 自 hrp v4.1 起已从 config 迁移到 .env，请改用环境配置",
}

func keySet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// ValidateYAMLFields 检查一份渲染好的用例 YAML 里是否出现了引擎不认识的键。
//
// 输入是**最终要写盘的内容**（compiler.Output 里的 YAML 字段），
// 因此这一层能捕捉到模型或编译器改动引入的意外字段。
func ValidateYAMLFields(fileName, yamlText string) []Issue {
	var issues []Issue

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &root); err != nil {
		return []Issue{{
			Level: LevelError, Scope: ScopeCase, Field: "yaml",
			Code:    CodeUnknownField,
			Message: fmt.Sprintf("%s 不是合法的 YAML：%v", fileName, err),
			Hint:    "这通常意味着编译器渲染逻辑有缺陷，请附上该文件内容反馈",
		}}
	}

	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return []Issue{{
			Level: LevelError, Scope: ScopeCase, Field: "yaml",
			Code:    CodeUnknownField,
			Message: fmt.Sprintf("%s 的顶层结构应为 config + teststeps", fileName),
			Hint:    "用例文件的根节点必须是映射",
		}}
	}

	// --- 第一层：config / teststeps ---
	var configNode, stepsNode *yaml.Node
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		val := doc.Content[i+1]
		switch strings.ToLower(key) {
		case "config":
			configNode = val
		case "teststeps":
			stepsNode = val
		default:
			issues = append(issues, unknownField(fileName, 0, key, "用例根节点",
				[]string{"config", "teststeps"}))
		}
	}

	if configNode != nil {
		issues = append(issues, checkMapKeys(fileName, 0, "config", configNode, allowedConfig)...)
	}
	if stepsNode != nil {
		issues = append(issues, checkSteps(fileName, stepsNode)...)
	}
	return issues
}

// checkSteps 逐个检查 teststep。
func checkSteps(fileName string, node *yaml.Node) []Issue {
	var issues []Issue
	if node.Kind != yaml.SequenceNode {
		return append(issues, Issue{
			Level: LevelError, Scope: ScopeCase, Field: "teststeps",
			Code:    CodeUnknownField,
			Message: fmt.Sprintf("%s 的 teststeps 应为列表", fileName),
			Hint:    "引擎会把它当成块状序列逐条执行",
		})
	}

	for i, st := range node.Content {
		seq := i + 1
		if st.Kind != yaml.MappingNode {
			issues = append(issues, Issue{
				Level: LevelError, Scope: ScopeStep, Seq: seq, Field: "teststeps",
				Code:    CodeUnknownField,
				Message: fmt.Sprintf("%s 的第 %d 个步骤不是映射结构", fileName, seq),
			})
			continue
		}

		for j := 0; j+1 < len(st.Content); j += 2 {
			key, val := st.Content[j].Value, st.Content[j+1]
			if !allowedTestStep[strings.ToLower(key)] {
				issues = append(issues, unknownField(fileName, seq, key, "步骤",
					sortedKeysOf(allowedTestStep)))
				continue
			}
			if strings.EqualFold(key, "request") {
				issues = append(issues, checkMapKeys(fileName, seq, "request", val, allowedRequest)...)
			}
		}
	}
	return issues
}

// checkMapKeys 检查一个映射节点的键是否都在白名单内。
func checkMapKeys(fileName string, seq int, path string, node *yaml.Node, allowed map[string]bool) []Issue {
	var issues []Issue
	if node.Kind != yaml.MappingNode {
		return issues
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if allowed[strings.ToLower(key)] {
			if hint, deprecated := deprecatedKeys[strings.ToLower(key)]; deprecated {
				issues = append(issues, Issue{
					Level: LevelWarning, Scope: ScopeStep, Seq: seq,
					Field: path + "." + key, Code: CodeUnknownField,
					Message: fmt.Sprintf("%s 中使用了已废弃的字段 %q", path, key),
					Hint:    hint,
				})
			}
			continue
		}
		issues = append(issues, unknownField(fileName, seq, key, path, sortedKeysOf(allowed)))
	}
	return issues
}

// unknownField 构造一条"引擎不认识这个键"的问题。
//
// 级别是 **error**：引擎对未知字段静默忽略（实测 A7），
// 也就是说放行这条等于让用户拿到一份"看着配了、实际没生效"的用例。
func unknownField(fileName string, seq int, key, where string, allowed []string) Issue {
	scope := ScopeCase
	if seq > 0 {
		scope = ScopeStep
	}
	return Issue{
		Level:   LevelError,
		Scope:   scope,
		Seq:     seq,
		Field:   key,
		Code:    CodeUnknownField,
		Message: fmt.Sprintf("%s 的 %s 里出现了引擎不认识的字段 %q", fileName, where, key),
		Hint: fmt.Sprintf("引擎对未知字段既不报错也不生效，为避免「配了却没生效」必须拦下。"+
			"%s 允许的字段：%s", where, strings.Join(allowed, " / ")),
	}
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
