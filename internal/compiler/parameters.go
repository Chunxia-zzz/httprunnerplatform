// 参数化编译转换（M3 ③-b）。
//
// 平台层与引擎层的分界（实测 A22 后定稿）：
//
//   - **平台层**（DB / 前端 / 编辑器）：用例 config.parameters 只写引用——
//
//     {"datasets": [{"name": "用户数据", "limit": 2}]}
//
//   - **引擎层**（编译产物 YAML）：按数据源转成两种实测生效的形态——
//
//     列表来源（行式 Inline）→ 关联参数内联 pairs（键 = 列名 '-' 连接）：
//         username-password: [["alice","pw1"], ["bob","pw2"]]
//
//     CSV 来源 → ${P()} 函数引用（文件随工作区复制进运行 cwd）：
//         username-password: "${P(data/users.csv)}"
//
// 为什么不能把行式数据拆成独立键（username: [...] + password: [...]）：
// 多个独立键是**笛卡尔积**（实测 A22 结论二），行式数据是**成对关联**（结论五）。
// 拆开会让"第 1 行的 username 配第 2 行的 password"，数据错配且不报错。
package compiler

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// datasetRefKey 是用例 config JSON 里平台层引用的键。
//
// 平台层格式：config.datasets = [{name, limit}]——**独立键**，不塞进
// config.parameters。原因：parameters 会被原样渲染进 YAML 给引擎读，
// 而引擎不认识 datasets 键（渲染前必须摘掉）；把它放 parameters 里
// 就得靠"渲染时过滤保留键"来擦屁股，独立键则天然隔离。
//
// 唯一真源在 model.CaseConfig：前端保存时写 config.datasets，
// applyCaseConfig 解出后由 renderCase 传进来。
const datasetRefKey = "datasets"

// datasetRef 是一条数据集引用。
type datasetRef struct {
	Name  string `json:"name"`
	Limit int    `json:"limit"`
}

// resolveParameters 把用例 config.datasets 里的平台层引用展开进 cfg.Parameters。
//
// 返回需要额外写盘的派生文件（相对路径 → 内容）；没有参数化时返回 nil。
//
// cfg.Parameters 只承载**引擎语法**：用户可以在用例 config.parameters 里
// 直接手写（如单变量独立参数），与 datasets 引用展开的结果合并。
func resolveParameters(in *Input, refs []datasetRef, cfg *yamlConfig) (map[string]string, error) {
	if cfg == nil {
		return nil, nil
	}
	if len(refs) == 0 {
		return nil, nil
	}

	extra := map[string]string{}
	if cfg.Parameters == nil {
		cfg.Parameters = jsonx.Map{}
	}
	out := jsonx.Map{}
	// 保留用户手写的引擎语法键
	for k, v := range cfg.Parameters {
		out[k] = v
	}

	for _, ref := range refs {
		ds := in.Datasets[ref.Name]
		if ds == nil {
			return nil, fmt.Errorf(
				"引用的数据集 %q 不存在。config.parameters 里的 name 必须与参数集页的名称完全一致", ref.Name)
		}

		// limit 的两级语义：引用处（用例级）显式给了就用它，否则用数据集默认值。
		// 两个都是 0 = 全部数据。
		limit := ref.Limit
		if limit <= 0 {
			limit = ds.Limit
		}

		switch ds.Source {
		case model.ParamSourceCSV:
			rel, content, err := csvParameter(in, ds, limit)
			if err != nil {
				return nil, err
			}
			if content != "" {
				extra[rel] = content
			}
			out[paramKeyFor(in, ds)] = "${P(" + rel + ")}"

		case model.ParamSourceList:
			key, pairs, err := inlineParameter(ds, limit)
			if err != nil {
				return nil, err
			}
			out[key] = pairs

		default:
			return nil, fmt.Errorf("数据集 %q 的来源 %q 无法识别", ds.Name, ds.Source)
		}
	}

	cfg.Parameters = out
	return extra, nil
}

// decodeDatasetRefs 把 datasets 键的值解析成引用列表。
func decodeDatasetRefs(raw any) ([]datasetRef, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(
			"%s 必须是数组（形如 [{\"name\": \"数据集名\", \"limit\": 2}]），实际是 %T", datasetRefKey, raw)
	}
	refs := make([]datasetRef, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s 第 %d 项必须是对象", datasetRefKey, i+1)
		}
		name, _ := m["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s 第 %d 项缺少 name", datasetRefKey, i+1)
		}
		limit := 0
		switch v := m["limit"].(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
		if limit < 0 {
			limit = 0
		}
		refs = append(refs, datasetRef{Name: name, Limit: limit})
	}
	return refs, nil
}

// datasetRefsOf 从用例 config JSON 里解出数据集引用。
//
// config 是 jsonx.Any（map[string]any），datasets 键缺失 = 无参数化。
// 解析失败直接报错：引用写错了必须让用户知道，静默忽略就是
// "参数化神秘失效"的又一个来源。
func datasetRefsOf(tc *model.TestCase) ([]datasetRef, error) {
	if tc == nil || tc.Config.Val == nil {
		return nil, nil
	}
	m, ok := tc.Config.Val.(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, ok := m[datasetRefKey]
	if !ok || raw == nil {
		return nil, nil
	}
	return decodeDatasetRefs(raw)
}

// paramKeyFor 生成关联参数键：CSV 的列名按表头顺序 '-' 连接。
//
// 列名来自 CSV 文件本体（第一行），而不是 DB——引擎的 ${P()} 按文件列名
// 注入变量，键必须与文件一致，否则变量对不上。
func paramKeyFor(in *Input, ds *model.ParamDataset) string {
	cols := readCSVColumns(in, ds.CsvPath)
	if len(cols) == 0 {
		// 读不到列名时兜底用文件名主干。正常流程到不了这里：
		// 数据集保存时已校验 CSV 至少有表头。
		return strings.TrimSuffix(baseName(ds.CsvPath), ".csv")
	}
	return strings.Join(cols, "-")
}

// baseName 取路径最后一段（分隔符兼容 / 与 \）。
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ---------------------------------------------------------------------------
// CSV 来源
// ---------------------------------------------------------------------------

// csvParameter 生成 CSV 数据集的引用。
//
// 返回（相对路径、派生文件内容）。limit=0 或 >= 行数时直接引用原文件，
// 不生成派生文件（content 返回空串）。
func csvParameter(in *Input, ds *model.ParamDataset, limit int) (string, string, error) {
	rel := ds.CsvPath
	rows, err := readCSVRowsFromWorkspace(in, rel)
	if err != nil {
		return "", "", fmt.Errorf("数据集 %q 的 CSV 不可读: %w", ds.Name, err)
	}
	if len(rows) < 2 {
		return "", "", fmt.Errorf("数据集 %q 的 CSV 至少需要表头 + 1 行数据", ds.Name)
	}

	// limit 裁剪：引擎没有 limit 参数（实测 A22），平台在编译期生成
	// 裁剪版派生文件。派生文件名带 __lN 后缀，与原文件永远可区分。
	if limit > 0 && limit < len(rows)-1 {
		derived := derivedCSVPath(rel, limit)
		content := renderCSV(rows[0], rows[1:limit+1])
		return derived, content, nil
	}
	return rel, "", nil
}

// derivedCSVPath 生成派生 CSV 的工作区相对路径。
func derivedCSVPath(rel string, limit int) string {
	dir, name := "", rel
	if i := strings.LastIndexAny(rel, `/\`); i >= 0 {
		dir, name = rel[:i+1], rel[i+1:]
	}
	ext := ".csv"
	stem := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		stem, ext = name[:i], name[i:]
	}
	return dir + stem + "__l" + fmt.Sprint(limit) + ext
}

// renderCSV 把表头与数据行渲染成 CSV 文本。
func renderCSV(header []string, rows [][]string) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(header)
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
	return b.String()
}

// readCSVRowsFromWorkspace 从项目工作区读取 CSV 全部行。
func readCSVRowsFromWorkspace(in *Input, rel string) ([][]string, error) {
	if in.WorkspaceRoot == "" {
		return nil, fmt.Errorf("编译输入缺少工作区根目录，无法读取参数化文件")
	}
	// 相对路径由服务端拼接（data/<name>），非用户原始输入；
	// Clean 防御性规整一次。
	return readCSVFile(filepath.Join(filepath.Clean(in.WorkspaceRoot), filepath.FromSlash(rel)))
}

// readCSVColumns 读 CSV 表头（列名列表）。文件不可读时返回 nil。
func readCSVColumns(in *Input, rel string) []string {
	rows, err := readCSVRowsFromWorkspace(in, rel)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return rows[0]
}

// readCSVFile 读一个 CSV 文件的全部行。
func readCSVFile(abs string) ([][]string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return csv.NewReader(f).ReadAll()
}

// ---------------------------------------------------------------------------
// 列表来源
// ---------------------------------------------------------------------------

// inlineParameter 把行式 Inline（[{k:v},...]）转成关联参数 pairs。
//
// 返回（参数键、成对值列表）。limit 在这里裁剪。
func inlineParameter(ds *model.ParamDataset, limit int) (string, []any, error) {
	list, ok := ds.Inline.Val.([]any)
	if !ok || len(list) == 0 {
		return "", nil, fmt.Errorf("数据集 %q 的 inline 列表为空", ds.Name)
	}

	// 列名取自第一行对象的键，**排序后**拼接：
	// map 遍历顺序随机，不排序的话同一份数据两次编译会产出不同的参数键，
	// 变量顺序错位 → 全部步骤的 $引用 命中错误的数据。
	var cols []string
	if first, ok := list[0].(map[string]any); ok {
		cols = make([]string, 0, len(first))
		for k := range first {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}
	if len(cols) == 0 {
		return "", nil, fmt.Errorf("数据集 %q 的 inline 数据必须是对象数组（形如 [{\"username\":\"a\"}]）", ds.Name)
	}

	if limit > 0 && limit < len(list) {
		list = list[:limit]
	}

	pairs := make([]any, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("数据集 %q 第 %d 行不是对象", ds.Name, i+1)
		}
		row := make([]any, 0, len(cols))
		for _, c := range cols {
			row = append(row, m[c])
		}
		pairs = append(pairs, row)
	}

	key := strings.Join(cols, "-")
	return key, pairs, nil
}
