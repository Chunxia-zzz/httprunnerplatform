package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/repo"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// testWorkRoot 是所有测试工作区目录的父目录，由 TestMain 统一清理。
//
// 刻意不使用 t.TempDir()：Windows 上删除临时目录时会为仍被占用的
// 句柄做退避重试，逐测试付这份成本毫无意义（实测每个测试要多花约 0.8 秒）。
var (
	testWorkRoot string
	testDBCount  atomic.Uint64
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hrp-service-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建测试工作目录失败:", err)
		os.Exit(1)
	}

	code := func() int {
		defer func() {
			// 清理失败不算错误（Windows 上句柄可能还没被释放），
			// 但要说出来，避免临时目录悄悄堆积。
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintln(os.Stderr, "清理测试工作目录失败（可忽略）:", err)
			}
		}()
		testWorkRoot = dir
		return m.Run()
	}()
	os.Exit(code)
}

// testDeps 构造一套隔离的依赖：每个测试一份独立的内存数据库 + 独立工作区目录。
//
// 为什么数据库在内存里（`mode=memory&cache=shared`）：
//
//	实测同一套 16 张表的 AutoMigrate，落到文件要 2.8 秒，放内存只要 4 毫秒 ——
//	SQLite 每个 DDL 都要 fsync 落盘，差的是三个数量级。
//	于是这里可以"每个测试现场建库"，既隔离得彻底，又不必搞模板文件复制，
//	更没有残留文件要清理。
//
// 为什么仍然用真实的 repo.Open + repo.Migrate 而不是替身：
//
//	平台与数据库的耦合点全在 GORM 的标签、时区与 JSON 编码上，
//	用替身测出来的"绿"没有意义 —— jsonx 的 Scan/Value、
//	以及布尔列的 default 陷阱都是从这个层面暴露出来的。
func testDeps(t *testing.T) Deps {
	t.Helper()

	cfg := &config.Config{}
	// 工作区根目录整个包共用一个，且**不预先创建任何子目录**。
	//
	// 两个都是刻意的：本机删除目录约需 55ms/个（Go 的 os.RemoveAll 与
	// 原生 rmdir /s /q 实测一样慢，是文件系统本身的开销，不是重试逻辑）。
	// 若每个测试都铺一套 data/workspaces/... 的目录树，光是收尾就要 8 秒。
	// 而绝大多数测试（校验、渲染、SQL 语义）根本不落盘 —— 那就不该为它们建目录。
	//
	// 需要真在盘上跑的测试请改用 seedProjectOnDisk，它会走完整的
	// ProjectService.Create（含建目录）并让每个测试拿到自己的目录树。
	cfg.Workspace.Root = filepath.Join(testWorkRoot, "data")
	cfg.Engine.DefaultCaseTimeout = 30
	cfg.Engine.MaxConcurrency = 2
	cfg.Engine.GenHTMLReport = true

	// cache=shared 是必需的：GORM 连接池会开多条连接，
	// 不共享缓存的话每条连接会各自看到一个空的库。
	dsn := fmt.Sprintf("file:hrp-test-%d?mode=memory&cache=shared", testDBCount.Add(1))
	db, err := repo.Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}, false)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := repo.Migrate(db); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return Deps{DB: db, Cfg: cfg}
}

// seedProject 建一个项目并返回，**不落盘**。
//
// 刻意绕过 ProjectService.Create 直接写库：那条路径会创建工作区目录树，
// 而本机文件系统的目录创建/删除很贵（见 testDeps 的说明）。
// 现有测试全都只关心库里的语义，不需要盘上有东西。
// 需要真实目录树的测试请用 seedProjectOnDisk。
func seedProject(t *testing.T, d Deps) *model.Project {
	t.Helper()
	p := &model.Project{
		Code:          fmt.Sprintf("demo%d", testDBCount.Add(1)),
		Name:          "演示项目",
		HrpVersion:    "v4.3.6",
		WorkspacePath: filepath.Join(d.Cfg.WorkspacesDir(), "demo"),
		OwnerID:       1,
	}
	if err := d.DB.Create(p).Error; err != nil {
		t.Fatalf("创建项目失败: %v", err)
	}
	return p
}

// seedProjectOnDisk 走完整的 ProjectService.Create，用于覆盖"建项目要建目录"。
func seedProjectOnDisk(t *testing.T, d Deps) *model.Project {
	t.Helper()
	p, err := New(d).Project.Create(CreateProjectReq{
		Code: fmt.Sprintf("disk%d", testDBCount.Add(1)),
		Name: "落盘项目",
	}, 1)
	if err != nil {
		t.Fatalf("创建项目失败: %v", err)
	}
	return p
}

// mustCase 建一个用例，失败即终止测试。
func mustCase(t *testing.T, d Deps, projectID uint64, req CaseReq) *CaseDetail {
	t.Helper()
	detail, err := New(d).Case.Create(projectID, req, 1)
	if err != nil {
		t.Fatalf("创建用例 %q 失败: %v", req.Name, err)
	}
	return detail
}

// reqAny 构造步骤的 request 字段。
func reqAny(m map[string]any) jsonx.Any { return jsonx.Any{Val: m} }

// TestModel_布尔零值必须能落库 守护一个会**静默改数据**的 GORM 行为。
//
// 字段一旦带 `default:` 标签，GORM 在 INSERT 时会把该列整个排除，
// 交给数据库默认值兜底 —— 即使应用层传的是显式的 false。
// 结果是 false 被写成 true，且没有任何报错。
//
// 两个已知的真实受害者：
//
//	test_step.enabled      false → true  用户禁用的步骤继续执行
//	case_result.clean_exit false → true  被强杀的执行看起来"正常退出"
//
// 这类错误的共同特征是"错得更正常"，所以必须有测试盯着，
// 而不是靠写模型的时候记得住（见 internal/model/model.go 顶部的约定）。
func TestModel_布尔零值必须能落库(t *testing.T) {
	d := testDeps(t)

	// 1) 用例步骤的 enabled
	p := seedProject(t, d)
	off := false
	detail := mustCase(t, d, p.ID, CaseReq{
		Code: "tc_flag", Name: "布尔零值",
		Steps: []StepReq{{
			Seq: 1, StepType: model.StepRequest, Name: "被禁用的步骤",
			Request: reqAny(map[string]any{"url": "$base_url/x"}),
			Enabled: &off,
		}},
	})
	var step model.TestStep
	if err := d.DB.First(&step, detail.Steps[0].ID).Error; err != nil {
		t.Fatalf("读取步骤失败: %v", err)
	}
	if step.Enabled {
		t.Error("test_step.enabled：写入 false 后读回来是 true —— GORM 的 default 标签又把零值剔掉了")
	}

	// 2) 用例结果的 clean_exit（断言失败/被强杀时为 false）
	var run model.RunRecord
	run.ProjectID = p.ID
	run.TargetType = model.TargetCase
	run.TargetName = "x"
	run.Status = model.RunError
	if err := d.DB.Create(&run).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	cr := model.CaseResult{
		RunID: run.ID, CaseCode: "tc_flag", Status: model.StatusFail,
		Panic: true, CleanExit: false, Attribution: model.AttrSystemUnderTest,
	}
	if err := d.DB.Create(&cr).Error; err != nil {
		t.Fatalf("创建用例结果失败: %v", err)
	}
	var got model.CaseResult
	if err := d.DB.First(&got, cr.ID).Error; err != nil {
		t.Fatalf("读取用例结果失败: %v", err)
	}
	if got.CleanExit {
		t.Error("case_result.clean_exit：写入 false 后读回来是 true —— 被强杀的执行会被记成正常退出")
	}
	if !got.Panic {
		t.Error("case_result.panic 应保持 true")
	}

	// 3) 用户启用状态（M1 已有登录，M2 会用它做禁用账号）
	u := model.User{Username: "u_disabled", Password: "x", Enabled: false}
	if err := d.DB.Create(&u).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	var gu model.User
	if err := d.DB.First(&gu, u.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if gu.Enabled {
		t.Error("user.enabled：写入 false 后读回来是 true")
	}
}

// simpleCaseReq 返回一条形状正确的最小用例。
//
// url 用 $base_url 前缀是刻意的：`base_url` 由环境提供、引擎必然注入，
// 因此这条用例在任何环境下都能通过静态校验 —— 它代表"用户能写出的
// 最正常的一条用例"，用来验证正常路径不该被误报。
func simpleCaseReq(code, name, path string) CaseReq {
	return CaseReq{
		Code: code,
		Name: name,
		Steps: []StepReq{{
			Seq:      1,
			StepType: model.StepRequest,
			Name:     "发一个请求",
			Request:  reqAny(map[string]any{"method": "GET", "url": "$base_url" + path}),
			Validate: []model.AssertItem{
				{Check: "status_code", Assert: "eq", Expect: float64(200)},
			},
		}},
	}
}
