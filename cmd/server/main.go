// Command server 是 httprunnerplatform 的入口。
//
// 子命令：
//
//	version  打印平台版本
//	doctor   引擎与运行环境自检
//	migrate  执行数据库迁移与初始化
//	server   启动 HTTP 服务
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/repo"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/webui"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/hrpclient"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
)

// version 由构建时注入：go build -ldflags "-X main.version=<ver>"
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// globalFlags 是所有子命令共享的命令行参数。
type globalFlags struct {
	configPath string
	hrpPath    string
}

func newRootCmd() *cobra.Command {
	g := &globalFlags{}

	root := &cobra.Command{
		Use:   "httprunnerplatform",
		Short: "基于 HttpRunner 的接口自动化测试平台",
		Long: `httprunnerplatform —— 基于 HttpRunner 引擎的接口自动化测试平台。

平台不引入 hrp 的 Go 库，通过子进程调用 hrp v4.3.6 二进制执行用例；
用例以结构化元数据存放于数据库，执行时单向编译为 hrp 工作区。

执行模型有一条硬约束（来自引擎实测）：**一个用例一个独立子进程**。
原因是 hrp 在断言失败时会 panic 并终止整个进程，目录模式下会导致
整批结果丢失。详见 docs/引擎实测记录.md。`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&g.configPath, "config", "c", "",
		"配置文件路径；留空则依次尝试 环境变量 "+config.EnvConfigPath+" 与 configs/config.yaml")
	root.PersistentFlags().StringVar(&g.hrpPath, "hrp", "",
		"hrp 二进制路径；留空则回退到环境变量 "+hrpclient.EnvBinaryPath+" 与约定路径")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newDoctorCmd(g))
	root.AddCommand(newMigrateCmd(g))
	root.AddCommand(newServerCmd(g))

	return root
}

// loadConfig 加载配置并初始化日志。
func loadConfig(g *globalFlags) (*config.Config, error) {
	cfg, err := config.Load(g.configPath)
	if err != nil {
		return nil, err
	}
	// --hrp 优先级最高，覆盖配置里的 engine.binary_path。
	if g.hrpPath != "" {
		cfg.Engine.BinaryPath = g.hrpPath
	}
	logx.Init(cfg.Log.Level, cfg.Log.JSON)
	return cfg, nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印平台版本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "httprunnerplatform %s\n", version)
			return nil
		},
	}
}

func newDoctorCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "引擎与运行环境自检",
		Long: `定位 hrp 二进制、探测其版本，并检查工作区与数据库配置。

建议在启动服务前先跑一次；引擎或数据库不可用是最常见的启动失败原因。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "httprunnerplatform %s\n", version)
			fmt.Fprintf(out, "运行环境 %s/%s  %s\n\n", runtime.GOOS, runtime.GOARCH, runtime.Version())

			// 1) 引擎
			d, err := hrpclient.Check(g.hrpPath)
			if err != nil {
				fmt.Fprintf(out, "[FAIL] 引擎自检失败\n       %v\n\n", err)
				fmt.Fprintf(out, "提示：下载 hrp v4.3.6 发布包，解压后放到 <工具目录>/bin/ 下，\n")
				fmt.Fprintf(out, "      或设置环境变量 %s 指向该二进制。\n\n", hrpclient.EnvBinaryPath)
			} else {
				fmt.Fprintf(out, "[ OK ] hrp 版本  %s (引擎内置 %s)\n", d.Version, d.GoVersion)
				fmt.Fprintf(out, "[ OK ] hrp 路径  %s\n\n", d.BinaryPath)
			}

			// 2) 配置与数据库
			cfg, err := config.Load(g.configPath)
			if err != nil {
				fmt.Fprintf(out, "[FAIL] 配置加载失败\n       %v\n", err)
				return err
			}
			fmt.Fprintf(out, "[ OK ] 数据库    driver=%s\n", cfg.Database.Driver)

			db, err := repo.Open(cfg.Database, false)
			if err != nil {
				fmt.Fprintf(out, "[FAIL] 数据库连接失败\n       %v\n", err)
				return err
			}
			fmt.Fprintf(out, "[ OK ] 数据库连接正常\n\n")

			// 3) 工作区目录
			dirs := []struct{ path, desc string }{
				{cfg.WorkspacesDir(), "持久层：用例源码（.env / testcases）"},
				{cfg.RunsDir(), "运行层：每次执行的隔离工作区"},
				{cfg.ReportsDir(), "运行层：HTML 报告归档"},
			}
			for _, it := range dirs {
				if fi, statErr := os.Stat(it.path); statErr == nil && fi.IsDir() {
					fmt.Fprintf(out, "[ OK ] 目录      %-28s %s\n", it.path, it.desc)
				} else {
					fmt.Fprintf(out, "[WARN] 目录缺失  %-28s %s（启动时自动创建）\n", it.path, it.desc)
				}
			}

			// 4) 数据表
			var missing []string
			for _, m := range model.AllModels() {
				if !db.Migrator().HasTable(m) {
					missing = append(missing, db.NamingStrategy.TableName(fmt.Sprintf("%T", m)))
				}
			}
			if len(missing) > 0 {
				fmt.Fprintf(out, "\n[WARN] 有 %d 张表未创建，请执行 migrate：%v\n", len(missing), missing)
			} else {
				fmt.Fprintf(out, "\n[ OK ] 数据表    已就绪\n")
			}
			return nil
		},
	}
}

func newMigrateCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "执行数据库迁移与初始化",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}

			db, err := repo.Open(cfg.Database, false)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "正在迁移数据库表结构 ...")
			if err := repo.Migrate(db); err != nil {
				return err
			}
			fmt.Fprintln(out, "正在写入初始数据 ...")
			if err := repo.Seed(db); err != nil {
				return err
			}

			fmt.Fprintf(out, "迁移完成（driver=%s）。\n", cfg.Database.Driver)
			fmt.Fprintf(out, "默认管理员账号：%s / %s（请尽快修改密码）\n",
				repo.DefaultAdminUser, repo.DefaultAdminPassword)
			return nil
		},
	}
}

func newServerCmd(g *globalFlags) *cobra.Command {
	var migrateFirst bool

	cmd := &cobra.Command{
		Use:   "server",
		Short: "启动 HTTP 服务",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}

			db, err := repo.Open(cfg.Database, cfg.Server.Mode == "debug")
			if err != nil {
				return err
			}

			if migrateFirst || cfg.Server.AutoMigrate {
				if err := repo.Migrate(db); err != nil {
					return err
				}
				if err := repo.Seed(db); err != nil {
					return err
				}
			}

			// 引擎可用性问题只告警不阻断启动：
			// 平台仍可用于编辑用例，只是无法执行。前端会显示醒目告警。
			if d, err := hrpclient.Check(cfg.Engine.BinaryPath); err != nil {
				logx.L().Warn().Err(err).Msg("引擎不可用，执行功能将不可用")
			} else {
				logx.L().Info().
					Str("hrp_version", d.Version).
					Str("hrp_path", d.BinaryPath).
					Msg("引擎就绪")
			}

			sessions := auth.NewStore(cfg.Auth.TokenTTL())

			// 内嵌前端。加载失败不阻断启动：后端 API 仍可用，
			// 前端可以退回开发服务器（npm run dev）。
			ui, err := webui.Load()
			if err != nil {
				logx.L().Warn().Err(err).Msg("内嵌前端不可用，将只提供 API")
			} else {
				logx.L().Info().Str("ui", ui.Describe()).Msg("前端资源就绪")
			}

			// services 由这里持有而不是交给 NewRouter 内部构造：
			// 运行服务维护着在跑执行的取消句柄，进程退出时必须由**同一个实例**
			// 负责把 hrp 子进程收干净（实测 A6：被强杀的 hrp 会留下挂起的子进程）。
			svcs := service.New(service.Deps{DB: db, Cfg: cfg})
			router := api.NewRouter(&api.Deps{
				Cfg:      cfg,
				DB:       db,
				Sessions: sessions,
				Services: svcs,
				WebUI:    ui,
			})

			srv := &http.Server{
				Addr:              cfg.Server.Addr(),
				Handler:           router,
				ReadHeaderTimeout: 15 * time.Second,
				// 不设置 WriteTimeout：后续里程碑会加入 WebSocket（长连接）。
				// 单请求级别的超时由执行器负责，而不是 HTTP 层。
			}

			// 会话清理
			gcCtx, stopGC := context.WithCancel(context.Background())
			defer stopGC()
			go func() {
				t := time.NewTicker(10 * time.Minute)
				defer t.Stop()
				for {
					select {
					case <-gcCtx.Done():
						return
					case <-t.C:
						if n := sessions.GC(); n > 0 {
							logx.L().Debug().Int("cleaned", n).Msg("清理过期会话")
						}
					}
				}
			}()

			errCh := make(chan error, 1)
			go func() {
				logx.L().Info().Str("addr", srv.Addr).Str("mode", cfg.Server.Mode).Msg("服务已启动")
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errCh <- err
				}
			}()

			quit := make(chan os.Signal, 1)
			signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

			select {
			case err := <-errCh:
				return fmt.Errorf("服务启动失败: %w", err)
			case sig := <-quit:
				logx.L().Info().Str("signal", sig.String()).Msg("收到退出信号，开始优雅关闭")
			}

			// 先停 HTTP（不再接新请求），再收执行：顺序反过来会让
			// 已经进来的运行请求在服务关闭中途失败。
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				logx.L().Warn().Err(err).Msg("优雅关闭 HTTP 服务失败")
			}
			svcs.Shutdown()
			logx.L().Info().Msg("服务已关闭")
			return nil
		},
	}

	cmd.Flags().BoolVar(&migrateFirst, "migrate", false, "启动前强制执行一次数据库迁移")
	return cmd
}
