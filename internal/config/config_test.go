package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoadDefaults 验证「配置文件不存在不算错误」+ 内置默认值。
//
// 默认值里的两项是有意覆盖引擎原生行为的（包注释/方案第三章），
// 这里做回归：遥测默认关闭（A20：GA4 超时会把成功执行拖慢并污染成 error）、
// 用例超时默认 300s（引擎原生 3600s 过长）。
func TestLoadDefaults(t *testing.T) {
	t.Setenv(EnvConfigPath, "")
	t.Setenv("HRP_PLATFORM_SERVER_PORT", "")
	t.Setenv("HRP_PLATFORM_DB_DRIVER", "")
	t.Setenv("HRP_PLATFORM_DB_DSN", "")
	t.Setenv("HRP_PLATFORM_WORKSPACE_ROOT", "")
	t.Setenv("HRP_PLATFORM_LOG_LEVEL", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("无配置文件时 Load 不应报错: %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Server.Mode != "release" {
		t.Fatalf("server 默认值 = %+v", cfg.Server)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Fatalf("database.driver 默认 = %q", cfg.Database.Driver)
	}
	if !cfg.Engine.DisableTelemetry {
		t.Fatal("DisableTelemetry 必须默认 true（实测 A20：GA4 超时会污染成功执行）")
	}
	if cfg.Engine.DefaultCaseTimeout != 300 {
		t.Fatalf("默认用例超时 = %d，应为 300", cfg.Engine.DefaultCaseTimeout)
	}
	if cfg.Auth.TokenTTL() != 24*time.Hour {
		t.Fatalf("默认会话 TTL = %v", cfg.Auth.TokenTTL())
	}
}

func TestLoad显式路径不存在必须报错(t *testing.T) {
	// 显式指定的文件不存在属于用户错误，必须报出来（不能静默走默认值）。
	if _, err := Load(filepath.Join(t.TempDir(), "no-such.yaml")); err == nil {
		t.Fatal("显式路径不存在时应报错")
	}
}

func TestLoadYAML覆盖与保留(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// 只覆盖 server.port —— 其余字段必须保持默认（yaml 覆盖语义）。
	yamlText := "server:\n  port: 9127\n"
	if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9127 {
		t.Fatalf("port = %d，未覆盖", cfg.Server.Port)
	}
	if cfg.Server.Mode != "release" {
		t.Fatalf("未覆盖字段应保留默认值：mode = %q", cfg.Server.Mode)
	}

	// 非法 YAML 必须报错，而不是拿默认值继续跑。
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("server: [unclosed"), 0o644)
	if _, err := Load(bad); err == nil {
		t.Fatal("非法 YAML 应报错")
	}
}

func TestLoad环境变量覆盖(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("server:\n  port: 9000\n"), 0o644)

	// 环境变量优先级高于配置文件（加载顺序：默认 < 文件 < 环境变量）。
	t.Setenv("HRP_PLATFORM_SERVER_PORT", "9111")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9111 {
		t.Fatalf("环境变量未生效：port = %d", cfg.Server.Port)
	}

	// 非法的端口值应被忽略（保持文件值），而不是解析失败或写入 0。
	t.Setenv("HRP_PLATFORM_SERVER_PORT", "not-a-number")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9000 {
		t.Fatalf("非法端口值应回退到文件值：port = %d", cfg.Server.Port)
	}
}

func TestValidate(t *testing.T) {
	base := func() *Config {
		c, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	tests := []struct {
		name  string
		mutat func(*Config)
		want  string // 期望错误文案的片段，空串表示应通过
	}{
		{"合法默认通过", func(*Config) {}, ""},
		{"非法驱动", func(c *Config) { c.Database.Driver = "oracle" }, "不支持的数据库驱动"},
		{"空驱动回退sqlite", func(c *Config) { c.Database.Driver = "" }, ""},
		{"空 DSN", func(c *Config) { c.Database.DSN = "" }, "dsn 不能为空"},
		{"端口为0", func(c *Config) { c.Server.Port = 0 }, "server.port 非法"},
		{"端口越界", func(c *Config) { c.Server.Port = 70000 }, "server.port 非法"},
		{"非法 mode", func(c *Config) { c.Server.Mode = "test" }, "server.mode 非法"},
		{"空工作区根", func(c *Config) { c.Workspace.Root = "" }, "workspace.root 不能为空"},
		{"超时为0兜底300", func(c *Config) { c.Engine.DefaultCaseTimeout = 0 }, ""},
		{"并发为0兜底3", func(c *Config) { c.Engine.MaxConcurrency = 0 }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			tt.mutat(c)
			err := c.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("应通过，得到 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("期望错误含 %q，得到 %v", tt.want, err)
			}
		})
	}

	// 兜底值生效的显式断言（上面只验证了「不报错」）。
	c := base()
	c.Engine.DefaultCaseTimeout = 0
	c.Engine.MaxConcurrency = -1
	_ = c.Validate()
	if c.Engine.DefaultCaseTimeout != 300 || c.Engine.MaxConcurrency != 3 {
		t.Fatalf("兜底未生效: %+v", c.Engine)
	}
}

func TestTokenTTL(t *testing.T) {
	a := AuthConfig{TokenTTLHours: 0}
	if a.TokenTTL() != 24*time.Hour {
		t.Fatalf("0 应兜底 24h，得到 %v", a.TokenTTL())
	}
	a = AuthConfig{TokenTTLHours: 5}
	if a.TokenTTL() != 5*time.Hour {
		t.Fatalf("5 应得到 5h，得到 %v", a.TokenTTL())
	}
	a = AuthConfig{TokenTTLHours: -3}
	if a.TokenTTL() != 24*time.Hour {
		t.Fatalf("负数应兜底 24h，得到 %v", a.TokenTTL())
	}
}

func Test目录推导与EnsureDirs(t *testing.T) {
	cfg := defaults()
	if got := cfg.WorkspacesDir(); got != filepath.Join("data", "workspaces") {
		t.Fatalf("WorkspacesDir = %q", got)
	}
	if got := cfg.RunsDir(); got != filepath.Join("data", "runtime", "runs") {
		t.Fatalf("RunsDir = %q", got)
	}
	if got := cfg.ReportsDir(); got != filepath.Join("data", "runtime", "reports") {
		t.Fatalf("ReportsDir = %q", got)
	}

	cfg.Workspace.Root = filepath.Join(t.TempDir(), "ws")
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs 失败: %v", err)
	}
	for _, d := range []string{cfg.WorkspacesDir(), cfg.RunsDir(), cfg.ReportsDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Fatalf("目录 %s 未创建: %v", d, err)
		}
	}
}
