package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// ---------------------------------------------------------------------------
// 脚手架
// ---------------------------------------------------------------------------

func inlineRows(rows ...string) jsonx.Any {
	list := make([]any, 0, len(rows))
	for _, r := range rows {
		list = append(list, map[string]any{"username": r, "password": "pw-" + r})
	}
	return jsonx.Any{Val: list}
}

// wantMsg 断言业务错误的文案片段（错误码由 wantCode 断言，两者互补）。
func wantMsg(t *testing.T, err error, code int, msgPart string) {
	t.Helper()
	var re *response.Error
	if !errors.As(err, &re) {
		t.Fatalf("应返回业务错误，得到 %T: %v", err, err)
	}
	if re.Code != code {
		t.Fatalf("业务码 = %d，应 %d（%v）", re.Code, code, re)
	}
	if !strings.Contains(re.Message, msgPart) {
		t.Fatalf("文案 %q 不含 %q", re.Message, msgPart)
	}
}

// ---------------------------------------------------------------------------
// CRUD 主链路
// ---------------------------------------------------------------------------

func TestParam数据集CRUD(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Param

	// 新建 list 来源：行数与列名（列名按字母序，展示稳定）。
	v, err := svc.Create(p.ID, ParamDatasetReq{
		Name:   "users",
		Source: "list",
		Inline: inlineRows("alice", "bob", "carol"),
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if v.RowCount != 3 {
		t.Fatalf("RowCount = %d", v.RowCount)
	}
	if strings.Join(v.Columns, ",") != "password,username" {
		t.Fatalf("Columns = %v", v.Columns)
	}
	if v.LimitEffective != 3 {
		t.Fatalf("LimitEffective = %d", v.LimitEffective)
	}

	// Get：单条视图。
	got, err := svc.Get(v.ID)
	if err != nil || got.Name != "users" {
		t.Fatalf("Get = %v, %v", got, err)
	}

	// List：能看到。
	list, err := svc.List(p.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %d 条, %v", len(list), err)
	}

	// Update：改 limit 后 LimitEffective 收窄（limit > 0 且 < 行数）。
	upd, err := svc.Update(v.ID, ParamDatasetReq{
		Name: "users", Source: "list",
		Inline: inlineRows("alice", "bob", "carol"),
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if upd.LimitEffective != 2 {
		t.Fatalf("LimitEffective = %d，应为 2", upd.LimitEffective)
	}

	// Delete 后 Get：40002。
	if err := svc.Delete(v.ID); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	err = func() error {
		_, err := svc.Get(v.ID)
		return err
	}()
	wantCode(t, err, response.CodeNotFound)
}

func TestParam重名与跨项目隔离(t *testing.T) {
	d := testDeps(t)
	p1 := seedProject(t, d)
	p2 := seedProject(t, d)
	svc := New(d).Param

	req := ParamDatasetReq{Name: "same", Source: "list", Inline: inlineRows("a")}
	if _, err := svc.Create(p1.ID, req); err != nil {
		t.Fatal(err)
	}

	// 同项目重名 → 40001（数据集名是 config.parameters 的引用键，必须唯一）。
	if _, err := svc.Create(p1.ID, req); err != nil {
		wantCode(t, err, response.CodeConflict)
	} else {
		t.Fatal("同项目重名应 40001")
	}

	// 不同项目同名 → 允许。
	if _, err := svc.Create(p2.ID, req); err != nil {
		t.Fatalf("跨项目同名应允许: %v", err)
	}
}

// ---------------------------------------------------------------------------
// normalize 的全部拒绝分支
// ---------------------------------------------------------------------------

func TestParamNormalize拒绝分支(t *testing.T) {
	d := testDeps(t)
	svc := New(d).Param

	tests := []struct {
		name string
		req  ParamDatasetReq
		want string // 期望错误文案片段
	}{
		{"名字为空", ParamDatasetReq{Source: "list", Inline: inlineRows("a")}, "名称不能为空"},
		{"名字超64字符", ParamDatasetReq{Name: strings.Repeat("很", 65), Source: "list", Inline: inlineRows("a")}, "过长"},
		{"非法来源", ParamDatasetReq{Name: "x", Source: "excel", Inline: inlineRows("a")}, "只支持 list / csv"},
		{"random 策略", ParamDatasetReq{Name: "x", Source: "list", Strategy: "random", Inline: inlineRows("a")}, "只支持 sequential"},
		{"list 空 inline", ParamDatasetReq{Name: "x", Source: "list"}, "非空的 inline"},
		{"csv 缺文件名与内容", ParamDatasetReq{Name: "x", Source: "csv"}, "csv_name 或 csv_text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(seedProject(t, d).ID, tt.req)
			wantMsg(t, err, response.CodeBadParam, tt.want)
		})
	}
}

// ---------------------------------------------------------------------------
// CSV 来源
// ---------------------------------------------------------------------------

const sampleCSV = "username,password\nalice,pw1\nbob,pw2\n"

func TestParamCSV数据集(t *testing.T) {
	d := testDeps(t)
	pid := seedProject(t, d).ID
	svc := New(d).Param

	v, err := svc.Create(pid, ParamDatasetReq{
		Name:     "csvusers",
		Source:   "csv",
		CsvName:  "users.csv",
		CsvText:  sampleCSV,
		Strategy: "sequential",
	})
	if err != nil {
		t.Fatalf("创建 CSV 数据集失败: %v", err)
	}
	// 行数不含表头；列名来自表头。
	if v.RowCount != 2 {
		t.Fatalf("RowCount = %d（应不含表头）", v.RowCount)
	}
	if strings.Join(v.Columns, ",") != "username,password" {
		t.Fatalf("Columns = %v", v.Columns)
	}
	if v.CsvPath != "data/users.csv" {
		t.Fatalf("CsvPath = %q", v.CsvPath)
	}

	// CsvText：读回文件内容。
	text, err := svc.CsvText(v.ID)
	if err != nil || text != sampleCSV {
		t.Fatalf("CsvText = %q, %v", text, err)
	}

	// ResolveByName：编译期按名引用要能命中。
	ds, err := svc.ResolveByName(pid, "csvusers")
	if err != nil || ds.ID != v.ID {
		t.Fatalf("ResolveByName = %v, %v", ds, err)
	}
	err = func() error {
		_, err := svc.ResolveByName(pid, "no-such")
		return err
	}()
	wantCode(t, err, response.CodeNotFound)

	// 对 list 来源调 CsvText → 40000（不是静默返回空）。
	lv, err := svc.Create(pid, ParamDatasetReq{Name: "listusers", Source: "list", Inline: inlineRows("a")})
	if err != nil {
		t.Fatal(err)
	}
	err = func() error {
		_, err := svc.CsvText(lv.ID)
		return err
	}()
	wantCode(t, err, response.CodeBadParam)
}

func TestParamCSV校验(t *testing.T) {
	d := testDeps(t)
	pid := seedProject(t, d).ID
	svc := New(d).Param

	tests := []struct {
		name    string
		csvName string
		csvText string
		wantMsg string
	}{
		// ⭐ 路径穿越必须显式拒绝，而不是静默清洗（writeCSV 注释的约定）。
		{"路径穿越", "../evil.csv", sampleCSV, "纯文件名"},
		{"带子目录", "sub/users.csv", sampleCSV, "纯文件名"},
		{"非csv后缀", "users.txt", sampleCSV, ".csv 结尾"},
		{"只有表头", "users.csv", "username,password\n", "表头 + 1 行"},
		// 列数不一致：Go 的 csv.Reader 默认要求每行字段数一致，
		// 会先以解析错误拦下（"wrong number of fields"）；
		// validateCSV 里自定义的「与表头不一致」分支是防御性兜底，正常输入到不了。
		{"列数不一致", "users.csv", "a,b\n1,2\n3\n", "解析失败"},
		{"空内容", "users.csv", "", "不能为空"},
		{"非法CSV语法", "users.csv", "a\"b\n1,2\n", "解析失败"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(pid, ParamDatasetReq{
				Name: "t_" + tt.name, Source: "csv",
				CsvName: tt.csvName, CsvText: tt.csvText,
			})
			wantMsg(t, err, response.CodeBadParam, tt.wantMsg)
		})
	}
}

func TestParamCSV换文件与替换内容(t *testing.T) {
	d := testDeps(t)
	pid := seedProject(t, d).ID
	svc := New(d).Param

	v, err := svc.Create(pid, ParamDatasetReq{
		Name: "csv1", Source: "csv", CsvName: "a.csv", CsvText: sampleCSV,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 换文件名：CsvPath 跟着变，行数按新文件算。
	upd, err := svc.Update(v.ID, ParamDatasetReq{
		Name: "csv1", Source: "csv",
		CsvName: "b.csv", CsvText: "x,y\n1,2\n3,4\n5,6\n",
	})
	if err != nil {
		t.Fatalf("换文件失败: %v", err)
	}
	if upd.CsvPath != "data/b.csv" || upd.RowCount != 3 {
		t.Fatalf("换文件后 = %q %d", upd.CsvPath, upd.RowCount)
	}

	// 只替换内容（不传文件名）：沿用现有文件。
	upd, err = svc.Update(v.ID, ParamDatasetReq{
		Name: "csv1", Source: "csv",
		CsvText: "x,y\n9,8\n",
	})
	if err != nil {
		t.Fatalf("替换内容失败: %v", err)
	}
	if upd.CsvPath != "data/b.csv" || upd.RowCount != 1 {
		t.Fatalf("替换内容后 = %q %d", upd.CsvPath, upd.RowCount)
	}
	if text, _ := svc.CsvText(v.ID); !strings.Contains(text, "9,8") {
		t.Fatalf("文件内容未更新: %q", text)
	}
}

// ---------------------------------------------------------------------------
// 纯函数
// ---------------------------------------------------------------------------

func TestInlineRowsNumbered(t *testing.T) {
	rows := jsonx.Any{Val: []any{"a", "b", "c"}}
	if got := InlineRowsNumbered(rows, 0); len(got) != 3 {
		t.Fatalf("limit=0 应返回全部，得到 %d", len(got))
	}
	if got := InlineRowsNumbered(rows, 2); len(got) != 2 || got[0] != "a" {
		t.Fatalf("limit=2 应裁剪前 2 行，得到 %v", got)
	}
	if got := InlineRowsNumbered(rows, 99); len(got) != 3 {
		t.Fatalf("limit 超过行数应返回全部，得到 %d", len(got))
	}
	if got := InlineRowsNumbered(jsonx.Any{}, 2); got != nil {
		t.Fatalf("nil 输入应返回 nil，得到 %v", got)
	}
}

func TestCSVLimitHint(t *testing.T) {
	if got := CSVLimitHint(0); got != "全部数据" {
		t.Fatalf("limit=0 提示 = %q", got)
	}
	if got := CSVLimitHint(5); got != "前 5 行" {
		t.Fatalf("limit=5 提示 = %q", got)
	}
}

func TestEffectiveLimit(t *testing.T) {
	tests := []struct{ limit, rows, want int }{
		{0, 10, 10},  // 不限制
		{3, 10, 3},   // 收窄
		{10, 10, 10}, // 等于行数
		{99, 10, 10}, // 超过行数按行数
		{-1, 10, 10}, // 负数按不限制（normalize 已归一为 0，这里兜底）
	}
	for _, tt := range tests {
		if got := effectiveLimit(tt.limit, tt.rows); got != tt.want {
			t.Errorf("effectiveLimit(%d, %d) = %d，应 %d", tt.limit, tt.rows, got, tt.want)
		}
	}
}

func TestCsvBaseName(t *testing.T) {
	// 兼容 Windows / Unix 分隔符（存库统一为 data/<name>）。
	for in, want := range map[string]string{
		"users.csv":   "users.csv",
		"data/xx.csv": "xx.csv",
		`data\yy.csv`: "yy.csv",
		"a/b/c.csv":   "c.csv",
	} {
		if got := csvBaseName(in); got != want {
			t.Errorf("csvBaseName(%q) = %q，应 %q", in, got, want)
		}
	}
}
