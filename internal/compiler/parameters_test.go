package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// 测试工具
// ---------------------------------------------------------------------------

// datasetsInput 构造带数据集的编译输入。
func datasetsInput(datasets map[string]*model.ParamDataset) *Input {
	return &Input{
		Project:       &model.Project{Code: "demo"},
		WorkspaceRoot: "testdata-fake-root",
		Datasets:      datasets,
	}
}

// resolveWith 按引用列表解析（测试便捷入口）。
func resolveWith(in *Input, refs []map[string]any, cfg *yamlConfig) (map[string]string, error) {
	decoded, err := decodeDatasetRefs(toAnySlice(refs))
	if err != nil {
		return nil, err
	}
	return resolveParameters(in, decoded, cfg)
}

func toAnySlice(ms []map[string]any) []any {
	out := make([]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, m)
	}
	return out
}

// listDataset 构造 list 来源的数据集。
func listDataset(name string, rows []map[string]any, limit int) *model.ParamDataset {
	items := make([]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, r)
	}
	return &model.ParamDataset{
		Name:     name,
		Source:   model.ParamSourceList,
		Inline:   jsonx.Any{Val: items},
		Strategy: model.ParamStrategySequential,
		Limit:    limit,
	}
}

// caseWithRef 构造一条 config.datasets 引用数据集的用例（平台层格式）。
func caseWithRef(code string, refs []map[string]any) *model.TestCase {
	return &model.TestCase{
		Code: code,
		Name: "用例-" + code,
		Config: jsonx.Any{Val: map[string]any{
			"datasets": toAnySlice(refs),
		}},
	}
}

func ref(name string, limit int) map[string]any {
	return map[string]any{"name": name, "limit": float64(limit)}
}

// ---------------------------------------------------------------------------
// list 来源
// ---------------------------------------------------------------------------

// Test参数化_list来源转关联pairs 行式数据必须转成 `-` 连接的关联参数键，
// 且值成对（实测 A22 结论五锁定的形态；拆成独立键会变笛卡尔积）。
func Test参数化_list来源转关联pairs(t *testing.T) {
	ds := listDataset("用户数据", []map[string]any{
		{"username": "alice", "password": "pw1"},
		{"username": "bob", "password": "pw2"},
	}, 0)

	cfg := &yamlConfig{Name: "t"}
	extra, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"用户数据": ds}), []map[string]any{ref("用户数据", 0)}, cfg)
	if err != nil {
		t.Fatalf("resolveParameters 失败: %v", err)
	}
	if len(extra) != 0 {
		t.Errorf("list 来源不应产生派生文件，得到 %v", extra)
	}
	// 列名会按字典序拼接（password < username），值列序跟着键序走。
	// 排序是为了编译产物稳定（map 遍历随机，不排序两次编译键不同）；
	// 变量按名字注入，与列顺序无关。
	pairs, ok := cfg.Parameters["password-username"]
	if !ok {
		t.Fatalf("参数键应为 password-username（列名字典序），实际键集 %v", cfg.Parameters)
	}
	list, ok := pairs.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("pairs 应为 2 组，实际 %T %v", pairs, pairs)
	}
	row0, ok := list[0].([]any)
	if !ok || len(row0) != 2 || row0[0] != "pw1" || row0[1] != "alice" {
		t.Fatalf("第一组应为 [pw1 alice]（跟着键序 password,username），实际 %v", list[0])
	}
	row1, _ := list[1].([]any)
	if !ok || row1[0] != "pw2" || row1[1] != "bob" {
		t.Fatalf("第二组应为 [pw2 bob]，实际 %v", list[1])
	}
}

// Test参数化_list来源limit裁剪 数据集自身 ds.Limit 生效于编译期
//（引用处未显式给 limit 时回退到数据集默认值）。
func Test参数化_list来源limit裁剪(t *testing.T) {
	ds := listDataset("d", []map[string]any{
		{"u": "a"}, {"u": "b"}, {"u": "c"},
	}, 2)
	cfg := &yamlConfig{Name: "t"}
	if _, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"d": ds}), []map[string]any{ref("d", 0)}, cfg); err != nil {
		t.Fatalf("resolveParameters 失败: %v", err)
	}
	pairs, ok := cfg.Parameters["u"].([]any)
	if !ok {
		t.Fatalf("pairs 类型异常: %v", cfg.Parameters["u"])
	}
	if len(pairs) != 2 {
		t.Fatalf("ds.Limit=2 应裁剪到 2 组，实际 %d", len(pairs))
	}
}

// Test参数化_引用级limit覆盖数据集默认 引用处显式 limit 优先于 ds.Limit。
func Test参数化_引用级limit覆盖数据集默认(t *testing.T) {
	ds := listDataset("d", []map[string]any{
		{"u": "a"}, {"u": "b"}, {"u": "c"},
	}, 3)
	cfg := &yamlConfig{Name: "t"}
	if _, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"d": ds}), []map[string]any{ref("d", 1)}, cfg); err != nil {
		t.Fatalf("resolveParameters 失败: %v", err)
	}
	if got := len(cfg.Parameters["u"].([]any)); got != 1 {
		t.Fatalf("ref.limit=1 应覆盖 ds.Limit=3，实际 %d 组", got)
	}
}

// Test参数化_list来源limit大于行数 limit 超过行数时取全部。
func Test参数化_list来源limit大于行数(t *testing.T) {
	ds := listDataset("d", []map[string]any{{"u": "a"}, {"u": "b"}}, 99)
	cfg := &yamlConfig{Name: "t"}
	_, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"d": ds}), []map[string]any{ref("d", 0)}, cfg)
	if err != nil {
		t.Fatalf("limit 大于行数不应报错: %v", err)
	}
	if got := len(cfg.Parameters["u"].([]any)); got != 2 {
		t.Fatalf("应取全部 2 行，实际 %d", got)
	}
}

// Test参数化_多列键排序稳定 列名排序后拼接，同一数据两次编译键一致。
func Test参数化_多列键排序稳定(t *testing.T) {
	mk := func() *model.ParamDataset {
		return listDataset("d", []map[string]any{
			{"zebra": "1", "alpha": "2", "mid": "3"},
		}, 0)
	}
	keys := map[string]bool{}
	for i := 0; i < 20; i++ {
		cfg := &yamlConfig{Name: "t"}
		if _, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"d": mk()}), []map[string]any{ref("d", 0)}, cfg); err != nil {
			t.Fatalf("第 %d 次失败: %v", i, err)
		}
		for k := range cfg.Parameters {
			keys[k] = true
		}
	}
	// 20 次编译必须产出同一个键（map 遍历随机，不排序就会偶尔不同）
	if len(keys) != 1 {
		t.Fatalf("20 次编译出现了 %d 种参数键 %v，列名排序不稳定", len(keys), keys)
	}
	for k := range keys {
		if k != "alpha-mid-zebra" {
			t.Fatalf("键应为列名字典序 alpha-mid-zebra，实际 %q", k)
		}
	}
}

// ---------------------------------------------------------------------------
// 引用校验
// ---------------------------------------------------------------------------

// Test参数化_引用不存在的数据集 必须显式报错。
func Test参数化_引用不存在的数据集(t *testing.T) {
	cfg := &yamlConfig{Name: "t"}
	_, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{}), []map[string]any{ref("幽灵", 0)}, cfg)
	if err == nil || !strings.Contains(err.Error(), "幽灵") {
		t.Fatalf("引用不存在的数据集应报错并含名称，实际 err=%v", err)
	}
}

// Test参数化_无引用不动用户手写语法 没有平台层引用时 parameters 原样保留。
func Test参数化_无引用不动用户手写语法(t *testing.T) {
	cfg := &yamlConfig{Name: "t", Parameters: jsonx.Map{"env": []any{"dev", "staging"}}}
	extra, err := resolveParameters(datasetsInput(nil), nil, cfg)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(extra) != 0 {
		t.Fatalf("不应有派生文件")
	}
	if _, ok := cfg.Parameters["env"]; !ok {
		t.Fatalf("用户手写的键应保留，实际 %v", cfg.Parameters)
	}
}

// Test参数化_引用与手写共存 引用展开后不覆盖用户手写键。
func Test参数化_引用与手写共存(t *testing.T) {
	ds := listDataset("d", []map[string]any{{"u": "a"}}, 0)
	cfg := &yamlConfig{Name: "t", Parameters: jsonx.Map{"env": []any{"dev"}}}
	_, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"d": ds}), []map[string]any{ref("d", 0)}, cfg)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if _, ok := cfg.Parameters["env"]; !ok {
		t.Errorf("手写键 env 应保留")
	}
	if _, ok := cfg.Parameters["u"]; !ok {
		t.Errorf("引用展开的键 u 应存在，实际 %v", cfg.Parameters)
	}
}

// Test参数化_ref格式错误 引用不是对象 / 缺 name 时报可读的错误。
func Test参数化_ref格式错误(t *testing.T) {
	// refs 走 decodeDatasetRefs：非数组
	if _, err := decodeDatasetRefs("不是数组"); err == nil {
		t.Fatal("datasets 非数组应报错")
	}
	// 缺 name
	if _, err := decodeDatasetRefs([]any{map[string]any{}}); err == nil {
		t.Fatal("缺 name 应报错")
	}
}

// Test参数化_空datasets数组 空引用列表 = 无参数化。
func Test参数化_空datasets数组(t *testing.T) {
	cfg := &yamlConfig{Name: "t", Parameters: jsonx.Map{"env": []any{"dev"}}}
	extra, err := resolveParameters(datasetsInput(nil), nil, cfg)
	if err != nil {
		t.Fatalf("空引用列表不应报错: %v", err)
	}
	if len(extra) != 0 {
		t.Fatalf("不应有派生文件")
	}
	if _, ok := cfg.Parameters["env"]; !ok {
		t.Fatalf("空引用不应动 parameters，实际 %v", cfg.Parameters)
	}
}

// ---------------------------------------------------------------------------
// CSV 来源
// ---------------------------------------------------------------------------

// Test参数化_CSV来源生成P引用端到端 真实文件读写：CSV → ${P()} + limit 派生文件。
func Test参数化_CSV来源生成P引用端到端(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	csvText := "username,password\nalice,pw1\nbob,pw2\ncarol,pw3\n"
	if err := os.WriteFile(filepath.Join(dataDir, "users.csv"), []byte(csvText), 0o644); err != nil {
		t.Fatal(err)
	}

	// ① 无 limit：直接引用原文件，无派生
	ds := &model.ParamDataset{
		Name: "用户", Source: model.ParamSourceCSV, CsvPath: "data/users.csv",
	}
	cfg := &yamlConfig{Name: "t"}
	in := datasetsInput(map[string]*model.ParamDataset{"用户": ds})
	in.WorkspaceRoot = root
	extra, err := resolveWith(in, []map[string]any{ref("用户", 0)}, cfg)
	if err != nil {
		t.Fatalf("无 limit 引用失败: %v", err)
	}
	if len(extra) != 0 {
		t.Fatalf("无 limit 不应产生派生文件，得到 %v", extra)
	}
	if got := cfg.Parameters["username-password"]; got != "${P(data/users.csv)}" {
		t.Fatalf("引用值应为 ${P(data/users.csv)}，实际 %v", got)
	}

	// ② ref.limit=2：生成派生文件 data/users__l2.csv，内容只有前 2 行数据
	ds2 := &model.ParamDataset{
		Name: "用户", Source: model.ParamSourceCSV, CsvPath: "data/users.csv",
	}
	cfg3 := &yamlConfig{Name: "t"}
	in3 := datasetsInput(map[string]*model.ParamDataset{"用户": ds2})
	in3.WorkspaceRoot = root
	extra3, err := resolveWith(in3, []map[string]any{ref("用户", 2)}, cfg3)
	if err != nil {
		t.Fatalf("limit=2 引用失败: %v", err)
	}
	if len(extra3) != 1 {
		t.Fatalf("limit=2 应产生 1 个派生文件，实际 %v", extra3)
	}
	derivedRel := ""
	for rel := range extra3 {
		derivedRel = rel
	}
	if !strings.Contains(derivedRel, "__l2.csv") {
		t.Fatalf("派生文件名应含 __l2，实际 %q", derivedRel)
	}
	lines := strings.Split(strings.TrimSpace(extra3[derivedRel]), "\n")
	if len(lines) != 3 { // 表头 + 2 行
		t.Fatalf("派生 CSV 应为 3 行（表头+2 数据），实际 %d 行：%q", len(lines), extra3[derivedRel])
	}
	if !strings.Contains(extra3[derivedRel], "bob,pw2") || strings.Contains(extra3[derivedRel], "carol") {
		t.Fatalf("派生 CSV 应含前 2 行数据且不含第 3 行，实际 %q", extra3[derivedRel])
	}
	if got := cfg3.Parameters["username-password"]; got != "${P("+derivedRel+")}" {
		t.Fatalf("limit 引用应指向派生文件，实际 %v", got)
	}

	// ③ ds.Limit 默认生效：数据集 limit=1，ref 未给 limit
	ds3 := &model.ParamDataset{
		Name: "用户", Source: model.ParamSourceCSV, CsvPath: "data/users.csv", Limit: 1,
	}
	cfg4 := &yamlConfig{Name: "t"}
	in4 := datasetsInput(map[string]*model.ParamDataset{"用户": ds3})
	in4.WorkspaceRoot = root
	extra4, err := resolveWith(in4, []map[string]any{ref("用户", 0)}, cfg4)
	if err != nil {
		t.Fatalf("ds.Limit 引用失败: %v", err)
	}
	if len(extra4) != 1 {
		t.Fatalf("ds.Limit=1 应产生派生文件，实际 %v", extra4)
	}
	for rel := range extra4 {
		if !strings.Contains(rel, "__l1.csv") {
			t.Fatalf("派生文件名应含 __l1，实际 %q", rel)
		}
	}
}

// Test参数化_CSV文件缺失报错 引用了数据集但 CSV 文件被手动删掉。
func Test参数化_CSV文件缺失报错(t *testing.T) {
	ds := &model.ParamDataset{
		Name: "幽灵CSV", Source: model.ParamSourceCSV, CsvPath: "data/ghost.csv",
	}
	cfg := &yamlConfig{Name: "t"}
	_, err := resolveWith(datasetsInput(map[string]*model.ParamDataset{"幽灵CSV": ds}), []map[string]any{ref("幽灵CSV", 0)}, cfg)
	if err == nil {
		t.Fatal("CSV 文件缺失应报错")
	}
}

// ---------------------------------------------------------------------------
// 端到端
// ---------------------------------------------------------------------------

// Test参数化_端到端渲染YAML 从 Input 到最终 YAML 文本的全链路形态检查。
func Test参数化_端到端渲染YAML(t *testing.T) {
	ds := listDataset("用户数据", []map[string]any{
		{"username": "alice", "password": "pw1"},
		{"username": "bob", "password": "pw2"},
	}, 0)
	tc := caseWithRef("tc_param", []map[string]any{ref("用户数据", 0)})
	steps := []model.TestStep{{
		Seq: 1, StepType: model.StepRequest, Name: "请求", Enabled: true,
		Request: jsonx.Any{Val: map[string]any{
			"method": "GET", "url": "http://127.0.0.1:8899/get",
			"params": map[string]any{"u": "$username", "p": "$password"},
		}},
	}}

	out, err := Render(&Input{
		Project:  &model.Project{Code: "demo"},
		Cases:    []CaseSpec{{Case: tc, Steps: steps}},
		Datasets: map[string]*model.ParamDataset{"用户数据": ds},
	})
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	yaml := out.Cases[0].YAML
	// 列名排序后 password < username
	if !strings.Contains(yaml, "password-username:") {
		t.Fatalf("YAML 应含关联参数键：\n%s", yaml)
	}
	if !strings.Contains(yaml, "alice") {
		t.Fatalf("YAML 应含数据行：\n%s", yaml)
	}
	if strings.Contains(yaml, "datasets") {
		t.Fatalf("YAML 不应含平台保留键 datasets：\n%s", yaml)
	}
}

// Test参数化_datasetRefsOf从用例config取引用 平台层格式从 config.datasets 解出。
func Test参数化_datasetRefsOf从用例config取引用(t *testing.T) {
	tc := caseWithRef("tc", []map[string]any{ref("a", 2), ref("b", 0)})
	refs, err := datasetRefsOf(tc)
	if err != nil {
		t.Fatalf("datasetRefsOf 失败: %v", err)
	}
	if len(refs) != 2 || refs[0].Name != "a" || refs[0].Limit != 2 || refs[1].Name != "b" {
		t.Fatalf("refs 解析错误: %+v", refs)
	}

	// 无 config / 无 datasets 键 → 空引用不报错
	empty := &model.TestCase{Code: "e", Name: "e"}
	refs2, err := datasetRefsOf(empty)
	if err != nil || len(refs2) != 0 {
		t.Fatalf("无 datasets 键应返回空引用: refs=%v err=%v", refs2, err)
	}
}
