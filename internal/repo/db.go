// Package repo 提供数据库初始化与迁移。
//
// 双驱动说明见 docs/数据库设计.md 第 0 节：生产用 MySQL 8，
// 本地开发与单测用纯 Go 实现的 SQLite（不引入 CGO）。
package repo

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// DefaultAdminUser / DefaultAdminPassword 是种子管理员账号。
//
// 首次启动会创建；若已存在则不改动（避免覆盖用户改过的密码）。
const (
	DefaultAdminUser     = "admin"
	DefaultAdminPassword = "admin123"
)

// Open 按配置建立数据库连接。
func Open(cfg config.DatabaseConfig, debug bool) (*gorm.DB, error) {
	logLevel := logger.Warn
	if debug {
		logLevel = logger.Info
	}

	gormCfg := &gorm.Config{
		Logger: gormLogger(logLevel),
		// 关闭默认事务可以略微提升写入性能；平台的关键写入（结果落库）
		// 显式使用 db.Transaction，不依赖 GORM 的自动包裹。
		SkipDefaultTransaction: true,
		// 表名不做复数化：表名与 docs/数据库设计.md 的 DDL 保持一致。
		NamingStrategy: namingStrategy(),
	}

	var dialector gorm.Dialector
	switch cfg.Driver {
	case "mysql":
		dialector = mysql.Open(cfg.DSN)
	case "sqlite":
		if err := ensureSQLiteDir(cfg.DSN); err != nil {
			return nil, err
		}
		// 说明：不使用 WAL，避免在部分容器/网络盘上出现锁文件问题；
		// busy_timeout 让并发写入等待而不是直接报 SQLITE_BUSY。
		dialector = sqlite.Open(cfg.DSN + sqliteParams(cfg.DSN))
	default:
		return nil, fmt.Errorf("不支持的数据库驱动 %q", cfg.Driver)
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败（driver=%s）: %w", cfg.Driver, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层连接失败: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("数据库连通性检查失败: %w", err)
	}
	return db, nil
}

// gormLogger 返回平台的 GORM 日志器。
//
// 与 logger.Default 的唯一差别是 IgnoreRecordNotFoundError=true。
//
// 为什么必须改：平台里"按 id 查一条记录、没查到"是**正常的业务分支**
// （映射成业务码 40002 返回给前端），不是故障。用默认 logger 时，
// 每一次"NoRoute / 删掉的资源 / 传错 id"都会在日志里留下一行
// 「record not found」的 error 级噪音 —— 真出问题时会被淹没在里面。
// 之前在一处 `Find` 上打补丁绕过（见 resolveEnv 的注释），
// 这里从日志器层面一次解决全部调用点。
func gormLogger(level logger.LogLevel) logger.Interface {
	return logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// namingStrategy 返回 GORM 命名策略。
//
// 关键点：**关闭复数化**。GORM 默认会把 TestCase 映射成 test_cases，
// 而 docs/数据库设计.md 的 DDL 用的是单数形式。两者必须一致，
// 否则文档与实现会逐渐漂移。
//
// 另外所有实体都实现了 TableName()，这里只是兜底。
func namingStrategy() schema.NamingStrategy {
	return schema.NamingStrategy{SingularTable: true}
}

// sqliteParams 返回 SQLite 的连接参数。
//
// SQLite 的 DSN 参数用 "?" 分隔；若 DSN 已带参数则不重复追加。
func sqliteParams(dsn string) string {
	for _, c := range dsn {
		if c == '?' {
			return "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
		}
	}
	return "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

// ensureSQLiteDir 确保 SQLite 文件所在目录存在。
func ensureSQLiteDir(dsn string) error {
	dir := filepath.Dir(dsn)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 SQLite 目录 %s 失败: %w", dir, err)
	}
	return nil
}

// Migrate 执行表结构迁移。
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	return nil
}

// Seed 写入必要的初始数据。
//
// 目前只有一件事：确保存在一个管理员账号，否则平台无法登录。
func Seed(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.User{}).Where("username = ?", DefaultAdminUser).Count(&count).Error; err != nil {
		return fmt.Errorf("查询管理员账号失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(DefaultAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成密码哈希失败: %w", err)
	}

	admin := &model.User{
		Username: DefaultAdminUser,
		Password: string(hash),
		Nickname: "管理员",
		Role:     model.RoleAdmin,
		Enabled:  true,
	}
	if err := db.Create(admin).Error; err != nil {
		return fmt.Errorf("创建管理员账号失败: %w", err)
	}
	return nil
}

// ErrNotFound 是仓储层统一的"记录不存在"错误。
//
// 单独定义是为了让上层能把 gorm.ErrRecordNotFound 映射成业务码 40002，
// 而不必在每个 handler 里 import gorm。
var ErrNotFound = errors.New("记录不存在")

// IsNotFound 判断错误是否为"记录不存在"。
func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNotFound)
}
