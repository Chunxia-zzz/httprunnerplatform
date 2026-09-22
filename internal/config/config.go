// Package config 负责平台配置的加载与校验。
//
// 加载优先级（从低到高）：
//
//  1. 内置默认值（defaults()）
//  2. 配置文件（--config 指定，默认 configs/config.yaml）
//  3. 环境变量覆盖（HRP_PLATFORM_* 前缀）
//
// 设计说明：
//
//   - 配置里的引擎默认值有两项是**有意覆盖引擎原生行为**的，改动前请先读
//     docs/项目方案.md 第三章：case_timeout 从 3600s 下调，遥测默认关闭。
//   - 数据库驱动支持 mysql 与 sqlite 两种，后者仅用于本地开发与单测
//     （纯 Go 实现，不引入 CGO），详见 docs/数据库设计.md 第 0 节。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvConfigPath 是指定配置文件路径的环境变量名。
const EnvConfigPath = "HRP_PLATFORM_CONFIG"

// Config 是平台的全部配置。
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Engine    EngineConfig    `yaml:"engine"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Log       LogConfig       `yaml:"log"`
	Auth      AuthConfig      `yaml:"auth"`
}

// ServerConfig 是 HTTP 服务配置。
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// Mode 取值 debug / release，传给 gin。
	Mode string `yaml:"mode"`
	// AutoMigrate 为 true 时，服务启动时自动执行数据库迁移。
	AutoMigrate bool `yaml:"auto_migrate"`
}

// Addr 返回 gin 监听地址。
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// DatabaseConfig 是数据库配置。
type DatabaseConfig struct {
	// Driver 取值 mysql / sqlite。
	Driver string `yaml:"driver"`
	// DSN：mysql 为 "user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=True&loc=Local"；
	// sqlite 为文件路径（如 ./data/platform.db，目录会自动创建）。
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

// EngineConfig 是 hrp 引擎调用配置。
type EngineConfig struct {
	// BinaryPath 留空则依次回退到环境变量 HRP_BINARY_PATH 与约定路径。
	BinaryPath string `yaml:"binary_path"`
	// DefaultCaseTimeout 用例级超时（秒）。
	// ⚠️ 引擎 --case-timeout 默认 3600s（官方文档未记录），对平台过长，此处有意下调。
	DefaultCaseTimeout int `yaml:"default_case_timeout"`
	// MaxConcurrency 是并行执行的用例数上限。
	MaxConcurrency int `yaml:"max_concurrency"`
	// DisableTelemetry 关闭引擎的 GA4 / Sentry 外发。
	//
	// 落地方式：executor 在起子进程时注入 DISABLE_GA=true / DISABLE_SENTRY=true。
	// 引擎只在**进程启动的包初始化阶段**读取这两个变量，所以只能在环境里传。
	//
	// 为什么必须关（实测 A20）：GA4 的 HTTP 超时是 5 秒，内网必然耗满，
	// 直接算进用例耗时（1.4s → 6.9s）；而且上报失败是用 log.Error() 打的，
	// 会被解析器当成用例错误，挂到一条**成功**的执行上。
	DisableTelemetry bool `yaml:"disable_telemetry"`
	// PythonVenv 留空则用引擎默认（~/.hrp/venv）。M1–M4 不支持 debugtalk.py，通常无需设置。
	PythonVenv string `yaml:"python_venv"`
	// GenHTMLReport 是否默认生成 HTML 报告。
	GenHTMLReport bool `yaml:"gen_html_report"`
}

// WorkspaceConfig 是工作区配置。
type WorkspaceConfig struct {
	// Root 是工作区根目录。其下分两个物理隔离的子树：
	//
	//	{root}/workspaces/{project_code}/  持久层：只有"源代码"
	//	{root}/runtime/runs/{run_id}/      运行层：每次执行的隔离工作区副本
	//	{root}/runtime/reports/            运行层：HTML 报告归档
	Root string `yaml:"root"`
	// RunRetentionDays 是执行记录的保留天数。
	RunRetentionDays int `yaml:"run_retention_days"`
}

// LogConfig 是日志配置。
type LogConfig struct {
	Level string `yaml:"level"`
	JSON  bool   `yaml:"json"`
}

// AuthConfig 是认证配置。
type AuthConfig struct {
	SessionSecret string `yaml:"session_secret"`
	TokenTTLHours int    `yaml:"token_ttl_hours"`
}

// TokenTTL 返回令牌有效期。
func (a AuthConfig) TokenTTL() time.Duration {
	if a.TokenTTLHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(a.TokenTTLHours) * time.Hour
}

// defaults 返回内置默认值。
//
// 默认值刻意选择"本地开箱可跑"：sqlite + ./data 工作区 + hrp 自动探测。
func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host:        "0.0.0.0",
			Port:        8080,
			Mode:        "release",
			AutoMigrate: true,
		},
		Database: DatabaseConfig{
			Driver:       "sqlite",
			DSN:          filepath.Join("data", "platform.db"),
			MaxOpenConns: 20,
			MaxIdleConns: 5,
		},
		Engine: EngineConfig{
			DefaultCaseTimeout: 300,
			MaxConcurrency:     3,
			DisableTelemetry:   true,
			GenHTMLReport:      true,
		},
		Workspace: WorkspaceConfig{
			Root:             "data",
			RunRetentionDays: 90,
		},
		Log: LogConfig{
			Level: "info",
			JSON:  true,
		},
		Auth: AuthConfig{
			SessionSecret: "change-me-in-production",
			TokenTTLHours: 24,
		},
	}
}

// Load 读取配置。
//
// path 为空时依次尝试：环境变量 HRP_PLATFORM_CONFIG、./configs/config.yaml。
// **配置文件不存在不算错误**（全部走默认值），便于首次启动。
func Load(path string) (*Config, error) {
	cfg := defaults()

	resolved := path
	if resolved == "" {
		resolved = os.Getenv(EnvConfigPath)
	}
	if resolved == "" {
		resolved = filepath.Join("configs", "config.yaml")
	}

	if data, err := os.ReadFile(resolved); err == nil {
		// yaml 反序列化会覆盖默认值中出现的字段，未出现的字段保持默认。
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s 失败: %w", resolved, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", resolved, err)
	} else if path != "" {
		// 显式指定的文件不存在，这属于用户错误，必须报出来。
		return nil, fmt.Errorf("配置文件不存在: %s", resolved)
	}

	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyEnvOverrides 用环境变量覆盖配置项。
//
// 只覆盖"运维上确实需要按环境变化"的项，不做全字段映射，避免配置来源难以追查。
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("HRP_PLATFORM_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Server.Port = n
		}
	}
	if v := os.Getenv("HRP_PLATFORM_DB_DRIVER"); v != "" {
		cfg.Database.Driver = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("HRP_PLATFORM_DB_DSN"); v != "" {
		cfg.Database.DSN = v
	}
	if v := os.Getenv("HRP_PLATFORM_WORKSPACE_ROOT"); v != "" {
		cfg.Workspace.Root = v
	}
	if v := os.Getenv("HRP_PLATFORM_LOG_LEVEL"); v != "" {
		cfg.Log.Level = strings.ToLower(strings.TrimSpace(v))
	}
}

// Validate 校验配置的合法性。
func (c *Config) Validate() error {
	switch c.Database.Driver {
	case "mysql", "sqlite":
	case "":
		c.Database.Driver = "sqlite"
	default:
		return fmt.Errorf("不支持的数据库驱动 %q（可选 mysql / sqlite）", c.Database.Driver)
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return errors.New("database.dsn 不能为空")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 非法: %d", c.Server.Port)
	}
	if strings.TrimSpace(c.Workspace.Root) == "" {
		return errors.New("workspace.root 不能为空")
	}
	switch c.Server.Mode {
	case "debug", "release":
	case "":
		c.Server.Mode = "release"
	default:
		return fmt.Errorf("server.mode 非法: %q（可选 debug / release）", c.Server.Mode)
	}
	if c.Engine.DefaultCaseTimeout <= 0 {
		// 引擎默认 3600s；这里给一个平台侧的兜底，避免配成 0 导致超时语义失效。
		c.Engine.DefaultCaseTimeout = 300
	}
	if c.Engine.MaxConcurrency <= 0 {
		c.Engine.MaxConcurrency = 3
	}
	return nil
}

// WorkspacesDir 返回项目持久层目录的父目录。
func (c *Config) WorkspacesDir() string {
	return filepath.Join(c.Workspace.Root, "workspaces")
}

// RuntimeDir 返回运行层目录。
func (c *Config) RuntimeDir() string {
	return filepath.Join(c.Workspace.Root, "runtime")
}

// RunsDir 返回执行工作区目录的父目录。
func (c *Config) RunsDir() string {
	return filepath.Join(c.RuntimeDir(), "runs")
}

// ReportsDir 返回 HTML 报告归档目录。
func (c *Config) ReportsDir() string {
	return filepath.Join(c.RuntimeDir(), "reports")
}

// EnsureDirs 创建所有必需的目录。
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.Workspace.Root,
		c.WorkspacesDir(),
		c.RunsDir(),
		c.ReportsDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	return nil
}
