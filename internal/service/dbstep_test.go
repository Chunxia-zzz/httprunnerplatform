package service

import (
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

func TestBuildDSN(t *testing.T) {
	cases := []struct {
		driver string
		want   string
	}{
		{"sqlite", "/data/test.db"},
		{"mysql", "u:p@tcp(127.0.0.1:3306)/pay?parseTime=true&multiStatements=true"},
		{"postgres", "host=127.0.0.1 port=5432 user=u password=p dbname=pay sslmode=disable"},
		{"unknown", ""},
	}
	for _, c := range cases {
		dbname := "/data/test.db"
		if c.driver != "sqlite" {
			dbname = "pay"
		}
		got := buildDSN(&model.DbSource{Driver: c.driver, Host: "127.0.0.1",
			Port:   map[string]int{"mysql": 3306, "postgres": 5432, "sqlite": 0}[c.driver],
			DBName: dbname, Username: "u", Password: "p"})
		if got != c.want {
			t.Fatalf("%s:\n got %q\nwant %q", c.driver, got, c.want)
		}
	}
}

func TestConvertPlaceholders(t *testing.T) {
	sqlText, args := convertPlaceholders("INSERT INTO t(a,b) VALUES (?,?)", []any{"x", 1, 2})
	if sqlText != "INSERT INTO t(a,b) VALUES ($1,$2)" {
		t.Fatalf("占位符未转换: %q", sqlText)
	}
	if len(args) != 2 || args[0] != "x" || args[1] != 1 {
		t.Fatalf("参数应裁剪到占位符数量: %v", args)
	}
}

func TestDBStepRegistry(t *testing.T) {
	r := newDBStepRegistry()

	tok := r.Register(7, 1)
	runID, projectID, ok := r.Verify(tok)
	if !ok || runID != 7 || projectID != 1 {
		t.Fatalf("验证失败: %v %v %v", runID, projectID, ok)
	}

	// 空串与垃圾 token
	if _, _, ok := r.Verify(""); ok {
		t.Fatal("空 token 不应通过")
	}
	if _, _, ok := r.Verify("junk"); ok {
		t.Fatal("垃圾 token 不应通过")
	}

	// 终态关闭
	r.Close(7)
	if _, _, ok := r.Verify(tok); ok {
		t.Fatal("关闭后不应通过")
	}

	// 调试临时通道：唯一且可释放
	t1, rel1 := r.RegisterEphemeral(1)
	t2, rel2 := r.RegisterEphemeral(1)
	if t1 == t2 {
		t.Fatal("两个临时通道 token 不应相同")
	}
	if _, _, ok := r.Verify(t1); !ok {
		t.Fatal("临时通道应可验证")
	}
	rel1()
	if _, _, ok := r.Verify(t1); ok {
		t.Fatal("释放后不应通过")
	}
	if _, _, ok := r.Verify(t2); !ok {
		t.Fatal("另一个通道不应受影响")
	}
	rel2()
}

func TestDBSourceService_掩码与校验(t *testing.T) {
	d := testDeps(t)
	svc := New(d).DBSource

	v, err := svc.Create(1, DbSourceReq{Name: "paydb", Driver: "sqlite", DBName: "/tmp/t.db", Password: "secret"})
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if v.Password != MaskedValue {
		t.Fatalf("password 应掩码: %q", v.Password)
	}

	// driver 非法
	if _, err := svc.Create(1, DbSourceReq{Name: "x", Driver: "oracle"}); err == nil {
		t.Fatal("非法 driver 应被拒")
	}
	// 重名
	if _, err := svc.Create(1, DbSourceReq{Name: "paydb", Driver: "sqlite", DBName: "/tmp/x.db"}); err == nil {
		t.Fatal("重名应被拒")
	}
	// 回传 *** 保持原值
	upd, err := svc.Update(v.ID, DbSourceReq{Name: "paydb", Driver: "sqlite", DBName: "/tmp/t.db", Password: MaskedValue})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if upd.Password != MaskedValue {
		t.Fatalf("更新后仍应掩码: %q", upd.Password)
	}
	// 改名拒绝
	if _, err := svc.Update(v.ID, DbSourceReq{Name: "other", Driver: "sqlite", DBName: "/tmp/t.db"}); err == nil {
		t.Fatal("改名应被拒（引用会断链）")
	}
}

func TestDBStepExec_SQLite(t *testing.T) {
	d := testDeps(t)
	svc := New(d).DBStep
	if err := d.DB.Create(&model.DbSource{ProjectID: 1, Name: "t", Driver: "sqlite", DBName: t.TempDir() + "/x.db"}).Error; err != nil {
		t.Fatal(err)
	}
	// 建表 + 插入 + 查询
	if _, err := svc.Exec(1, DBStepRequest{Conn: "t", SQL: "CREATE TABLE o (id INTEGER PRIMARY KEY, name TEXT)"}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	res, err := svc.Exec(1, DBStepRequest{Conn: "t", SQL: "INSERT INTO o(name) VALUES (?)", Params: []any{"张三"}})
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	if res.Affected != 1 {
		t.Fatalf("affected = %d", res.Affected)
	}
	q, err := svc.Exec(1, DBStepRequest{Conn: "t", SQL: "SELECT id, name FROM o WHERE name = ?", Params: []any{"张三"}})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(q.Rows) != 1 || q.Rows[0]["name"] != "张三" {
		t.Fatalf("查询结果异常: %v", q.Rows)
	}
	// 跨项目连接拒绝
	if _, err := svc.Exec(2, DBStepRequest{Conn: "t", SQL: "SELECT 1"}); err == nil {
		t.Fatal("跨项目连接应被拒")
	}
	// 空连接
	if _, err := svc.Exec(1, DBStepRequest{Conn: "nope", SQL: "SELECT 1"}); err == nil {
		t.Fatal("未知连接应被拒")
	}
}
