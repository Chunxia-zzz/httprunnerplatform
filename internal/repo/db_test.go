package repo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

func sqliteCfg(t *testing.T) config.DatabaseConfig {
	t.Helper()
	return config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "test.db"),
	}
}

// openTestDB 打开并迁移一个临时库，测试结束后关闭连接。
//
// Windows 上不关连接会导致 t.TempDir 清理失败（文件被占用），
// 那会让断言全绿的测试照样 FAIL，必须显式收尾。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(sqliteCfg(t), false)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestOpen不支持的驱动(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.Driver = "oracle"
	if _, err := Open(cfg, false); err == nil {
		t.Fatal("不支持的驱动应报错")
	}
}

func TestOpenSQLite(t *testing.T) {
	cfg := sqliteCfg(t)
	db, err := Open(cfg, false)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	// Open 只负责连通，不建表；建表是 Migrate 的职责。
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate 失败: %v", err)
	}
	// 真实读写一条记录确认可用。
	if err := db.Create(&model.Project{Code: "p1", Name: "项目"}).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var got model.Project
	if err := db.First(&got, "code = ?", "p1").Error; err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if got.Name != "项目" {
		t.Fatalf("读回 Name = %q", got.Name)
	}
}

func TestMigrate表名单数(t *testing.T) {
	db := openTestDB(t)
	// 表名必须与数据库设计.md 的 DDL 一致。GORM 默认复数化会把
	// TestCase 映射成 test_cases；而本项目约定单数 + 下划线
	//（test_case / test_step / test_suite，见 model.go 的 TableName）。
	// 一旦这里回归，整个文档体系都会漂移。
	for _, table := range []string{"user", "project", "environment", "api_def",
		"test_case", "test_step", "param_dataset", "test_suite", "suite_case",
		"test_plan", "plan_suite", "run_record", "case_result", "step_result",
		"assertion_result", "api_token", "notify_config"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("表 %s 不存在（表名约定被破坏？）", table)
		}
		if db.Migrator().HasTable(table + "s") {
			t.Errorf("出现了复数表 %ss —— NamingStrategy 配置回归", table)
		}
	}
}

func TestSeed幂等且不覆盖密码(t *testing.T) {
	db := openTestDB(t)

	if err := Seed(db); err != nil {
		t.Fatalf("首次 Seed 失败: %v", err)
	}
	// 种子账号可用默认密码登录。
	var admin model.User
	if err := db.First(&admin, "username = ?", DefaultAdminUser).Error; err != nil {
		t.Fatalf("种子管理员不存在: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(DefaultAdminPassword)); err != nil {
		t.Fatalf("种子密码与默认口令不匹配: %v", err)
	}

	// 改掉密码后再 Seed：不允许覆盖用户改过的密码。
	if err := db.Model(&admin).Update("password", "changed-by-user").Error; err != nil {
		t.Fatal(err)
	}
	if err := Seed(db); err != nil {
		t.Fatalf("二次 Seed 失败: %v", err)
	}
	var again model.User
	db.First(&again, admin.ID)
	if again.Password != "changed-by-user" {
		t.Fatal("Seed 覆盖了用户改过的密码")
	}

	// 不产生第二个管理员。
	var count int64
	db.Model(&model.User{}).Where("username = ?", DefaultAdminUser).Count(&count)
	if count != 1 {
		t.Fatalf("种子管理员数量 = %d", count)
	}
}

func TestSqliteParams(t *testing.T) {
	// DSN 已带参数时用 & 追加，否则用 ? 起头。
	if got := sqliteParams("a.db"); got != "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)" {
		t.Fatalf("裸 DSN 的参数 = %q", got)
	}
	if got := sqliteParams("a.db?mode=ro"); got != "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)" {
		t.Fatalf("带参 DSN 的参数 = %q", got)
	}
}

func TestEnsureSQLiteDir(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := ensureSQLiteDir(filepath.Join(deep, "x.db")); err != nil {
		t.Fatalf("深层目录应自动创建: %v", err)
	}
	if fi, err := os.Stat(deep); err != nil || !fi.IsDir() {
		t.Fatalf("目录 %s 未创建", deep)
	}
	// 当前目录（无父目录可建）不应报错。
	if err := ensureSQLiteDir("x.db"); err != nil {
		t.Fatalf("当前目录不应报错: %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(gorm.ErrRecordNotFound) {
		t.Fatal("gorm 的 NotFound 应识别")
	}
	if !IsNotFound(ErrNotFound) {
		t.Fatal("仓储层统一的 NotFound 应识别")
	}
	if IsNotFound(errors.New("别的错误")) {
		t.Fatal("其他错误不应误判")
	}
	// 包装过的 NotFound 也要能识别（errors.Is 语义）。
	if !IsNotFound(errors.Join(gorm.ErrRecordNotFound)) {
		t.Fatal("包装后的 NotFound 应识别")
	}
}
