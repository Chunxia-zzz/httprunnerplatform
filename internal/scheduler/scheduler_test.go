package scheduler

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/repo"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"gorm.io/gorm"
)

// 本文件覆盖调度器的三条硬规则：
//
//  1. 时区换算（最容易静默出错的一处）；
//  2. 重入保护 —— 上一轮没跑完就该跳过，并留下痕迹；
//  3. 不补跑但记录 —— 跳过必须写进 LastMissedAt / LastSkipReason。
//
// 真正"到点跑一批用例"的路径依赖 hrp 二进制，不在这里测。

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:sched-test-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := repo.Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}, false)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := repo.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newTestScheduler(t *testing.T) (*Scheduler, *gorm.DB) {
	t.Helper()
	db := testDB(t)
	cfg := &config.Config{}
	cfg.Workspace.Root = filepath.Join(t.TempDir(), "data")
	svcs := service.New(service.Deps{DB: db, Cfg: cfg})
	s := New(db, svcs.Run)
	t.Cleanup(s.Stop)
	return s, db
}

func seedPlan(t *testing.T, db *gorm.DB, name, cron, tz string, enabled bool) *model.TestPlan {
	t.Helper()
	p := &model.TestPlan{
		Name:        name,
		TriggerType: model.TriggerCron,
		CronExpr:    cron,
		Timezone:    tz,
		Enabled:     enabled,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("创建计划失败: %v", err)
	}
	return p
}

// ---------------------------------------------------------------------------
// 时区换算
// ---------------------------------------------------------------------------

// ⭐ 这条是全包最重要的一处：时区不生效的话，所有计划会静默错位，
// 而且不会报错 —— 发现它往往要等好几周。
func Test时区换算_同样的表达式在不同时区落在不同时刻(t *testing.T) {
	s, _ := newTestScheduler(t)

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("本机没有 Asia/Shanghai 时区数据: %v", err)
	}
	utc := time.UTC

	// 同一"现在"，两个时区的下一个 9 点
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, utc)

	a := s.nextOf("0 9 * * *", "Asia/Shanghai", now)
	b := s.nextOf("0 9 * * *", "UTC", now)
	if a.IsZero() || b.IsZero() {
		t.Fatalf("解析失败: a=%v b=%v", a, b)
	}
	// 北京时间 9 点 = UTC 1 点 ⇒ 上海的那次比 UTC 的早 8 小时
	if a.Sub(b) != -8*time.Hour {
		t.Errorf("上海与 UTC 的下次触发相差 %v, want -8h（a=%v b=%v）", a.Sub(b), a, b)
	}
	if got := a.In(shanghai).Format("15:04"); got != "09:00" {
		t.Errorf("上海时区的触发时刻 = %s, want 09:00", got)
	}
	if got := b.In(utc).Format("15:04"); got != "09:00" {
		t.Errorf("UTC 时区的触发时刻 = %s, want 09:00", got)
	}
}

// nextOf 是测试专用的快捷方式：走与生产同一条 Schedule 构造路径。
func (s *Scheduler) nextOf(expr, tz string, from time.Time) time.Time {
	sch, err := cronParser.Parse(expr)
	if err != nil {
		return time.Time{}
	}
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	return tzSchedule{Schedule: sch, loc: loc}.Next(from)
}

// ---------------------------------------------------------------------------
// 装载与对账
// ---------------------------------------------------------------------------

func Test对账_只装载启用的定时计划(t *testing.T) {
	s, db := newTestScheduler(t)

	on := seedPlan(t, db, "启用", "0 9 * * *", "Asia/Shanghai", true)
	seedPlan(t, db, "禁用", "0 10 * * *", "Asia/Shanghai", false)
	seedPlan(t, db, "手动", "", "Asia/Shanghai", true)
	// 定时但没有表达式：库里被手工改坏时的兜底，不能让整个对账挂掉
	seedPlan(t, db, "空cron", "", "Asia/Shanghai", true)
	db.Model(&model.TestPlan{}).Where("id = ?", 4).Update("trigger_type", model.TriggerCron)

	s.sync()
	if len(s.entries) != 1 {
		t.Fatalf("装载的定时任务 = %d, want 1", len(s.entries))
	}
	if _, ok := s.entries[on.ID]; !ok {
		t.Errorf("启用的计划 %d 没有被装载", on.ID)
	}
}

func Test对账_改了cron或时区要重建(t *testing.T) {
	s, db := newTestScheduler(t)
	p := seedPlan(t, db, "会改", "0 9 * * *", "Asia/Shanghai", true)

	s.sync()
	first := s.entries[p.ID]

	// 只改时区也要重建：时区变了但表达式没变，
	// 若只比较 cron 表达式，它会继续按旧时区跑。
	if err := db.Model(&model.TestPlan{}).Where("id = ?", p.ID).
		Update("timezone", "UTC").Error; err != nil {
		t.Fatalf("改时区失败: %v", err)
	}
	s.sync()
	if s.entries[p.ID] == first {
		t.Error("时区改了但任务没有被重建（会继续按旧时区跑）")
	}
	if s.specOf[p.ID] != "0 9 * * *|UTC" {
		t.Errorf("specOf = %q, want %q", s.specOf[p.ID], "0 9 * * *|UTC")
	}

	// 配置没变时不该重建 —— 否则每 30 秒就会把所有任务重挂一遍
	again := s.entries[p.ID]
	s.sync()
	if s.entries[p.ID] != again {
		t.Error("配置没变却重建了任务")
	}
}

func Test对账_删除与禁用要撤掉任务(t *testing.T) {
	s, db := newTestScheduler(t)
	a := seedPlan(t, db, "会删", "0 9 * * *", "Asia/Shanghai", true)
	b := seedPlan(t, db, "会禁用", "0 10 * * *", "Asia/Shanghai", true)

	s.sync()
	if len(s.entries) != 2 {
		t.Fatalf("装载 = %d, want 2", len(s.entries))
	}

	if err := db.Delete(&model.TestPlan{}, a.ID).Error; err != nil {
		t.Fatalf("删除计划失败: %v", err)
	}
	if err := db.Model(&model.TestPlan{}).Where("id = ?", b.ID).
		Update("enabled", false).Error; err != nil {
		t.Fatalf("禁用计划失败: %v", err)
	}
	s.sync()
	if len(s.entries) != 0 {
		t.Errorf("撤掉后仍留有 %d 个任务（会继续触发已经删掉的计划）", len(s.entries))
	}
	if len(s.specOf) != 0 {
		t.Errorf("specOf 残留 %d 条", len(s.specOf))
	}
}

func Test对账_非法表达式不会让整个对账停下(t *testing.T) {
	s, db := newTestScheduler(t)
	ok := seedPlan(t, db, "正常", "0 9 * * *", "Asia/Shanghai", true)
	// 手工把库改坏：保存时有校验，但库可能被直接改过
	db.Model(&model.TestPlan{}).Where("id = ?", ok.ID).Update("cron_expr", "不是cron")

	bad := seedPlan(t, db, "坏", "不是cron", "Asia/Shanghai", true)
	s.sync()

	if len(s.entries) != 0 {
		t.Errorf("非法表达式被装载了 %d 条", len(s.entries))
	}
	// 关键是不能 panic，也不能把 sync 卡住 —— 走到这里就算过
	_ = bad
}

// ---------------------------------------------------------------------------
// 触发
// ---------------------------------------------------------------------------

func Test触发_上一轮还在跑就跳过并记下原因(t *testing.T) {
	s, db := newTestScheduler(t)
	p := seedPlan(t, db, "重入", "0 9 * * *", "Asia/Shanghai", true)

	// 挂一个用例集成员，让它能走到"启动执行"这一步
	su := &model.TestSuite{ProjectID: 1, Code: "s1", Name: "用例集"}
	if err := db.Create(su).Error; err != nil {
		t.Fatalf("创建用例集失败: %v", err)
	}
	if err := db.Create(&model.PlanSuite{PlanID: p.ID, SuiteID: su.ID, Seq: 1}).Error; err != nil {
		t.Fatalf("挂成员失败: %v", err)
	}

	// 造一条"仍在跑"的执行记录，并让计划指向它
	run := &model.RunRecord{ProjectID: 1, TargetType: model.TargetSuite, Status: model.RunRunning}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	if err := db.Model(&model.TestPlan{}).Where("id = ?", p.ID).
		Update("last_run_id", run.ID).Error; err != nil {
		t.Fatalf("回写 last_run_id 失败: %v", err)
	}

	s.fire(p.ID)

	var got model.TestPlan
	if err := db.First(&got, p.ID).Error; err != nil {
		t.Fatalf("读回计划失败: %v", err)
	}
	if got.LastSkipReason != SkipReasonStillRunning {
		t.Errorf("last_skip_reason = %q, want %q", got.LastSkipReason, SkipReasonStillRunning)
	}
	if got.LastMissedAt == nil {
		t.Error("跳过了却没记 LastMissedAt —— 静默跳过的定时任务是最难发现的一类故障")
	}
	if got.LastFiredAt != nil {
		t.Error("被跳过的调度点不该回写 LastFiredAt（它并没有真的触发）")
	}
	// ⭐ 服务重启后进程内的 inflight 会清空，上面那条 DB 判断是唯一的兜底。
	// 这也是为什么重入保护要做两道：只靠进程内状态的话，重启一次就失守了。
}

func Test触发_没有成员也要留下痕迹(t *testing.T) {
	s, db := newTestScheduler(t)
	p := seedPlan(t, db, "空计划", "0 9 * * *", "Asia/Shanghai", true)

	s.fire(p.ID)

	var got model.TestPlan
	if err := db.First(&got, p.ID).Error; err != nil {
		t.Fatalf("读回计划失败: %v", err)
	}
	if got.LastSkipReason != SkipReasonNoSuite {
		t.Errorf("last_skip_reason = %q, want %q", got.LastSkipReason, SkipReasonNoSuite)
	}
	if got.LastMissedAt == nil {
		t.Error("空计划被跳过时也要记 LastMissedAt")
	}
}

func Test触发_已禁用或已改为手动的计划不触发(t *testing.T) {
	s, db := newTestScheduler(t)
	p := seedPlan(t, db, "关掉了", "0 9 * * *", "Asia/Shanghai", false)

	s.fire(p.ID)

	var got model.TestPlan
	_ = db.First(&got, p.ID).Error
	if got.LastSkipReason != "" || got.LastFiredAt != nil {
		t.Errorf("禁用的计划不该被触发：skip=%q fired=%v", got.LastSkipReason, got.LastFiredAt)
	}

	// 改成手动触发的计划同样不该被调度器触发
	if err := db.Model(&model.TestPlan{}).Where("id = ?", p.ID).
		Updates(map[string]any{"enabled": true, "trigger_type": model.TriggerManual}).Error; err != nil {
		t.Fatalf("改触发方式失败: %v", err)
	}
	s.fire(p.ID)
	_ = db.First(&got, p.ID).Error
	if got.LastFiredAt != nil {
		t.Error("手动计划不该被调度器触发")
	}
}

func Test触发_用例集起不来不影响同计划里的其它用例集(t *testing.T) {
	s, db := newTestScheduler(t)
	p := seedPlan(t, db, "部分失败", "0 9 * * *", "Asia/Shanghai", true)
	proj := &model.Project{Code: "p1", Name: "项目", HrpVersion: "v4.3.6"}
	if err := db.Create(proj).Error; err != nil {
		t.Fatalf("创建项目失败: %v", err)
	}
	if err := db.Model(&model.TestPlan{}).Where("id = ?", p.ID).
		Update("project_id", proj.ID).Error; err != nil {
		t.Fatalf("回写项目失败: %v", err)
	}

	// 两个空的用例集：StartSuite 会以"没有成员"拒绝，
	// 但计划本身仍要记下"触发过了"。
	for i, code := range []string{"s1", "s2"} {
		su := &model.TestSuite{ProjectID: proj.ID, Code: code, Name: "用例集" + code}
		if err := db.Create(su).Error; err != nil {
			t.Fatalf("创建用例集失败: %v", err)
		}
		if err := db.Create(&model.PlanSuite{PlanID: p.ID, SuiteID: su.ID, Seq: i + 1}).Error; err != nil {
			t.Fatalf("挂成员失败: %v", err)
		}
	}

	s.fire(p.ID)

	var got model.TestPlan
	if err := db.First(&got, p.ID).Error; err != nil {
		t.Fatalf("读回计划失败: %v", err)
	}
	if got.LastFiredAt == nil {
		t.Error("计划确实触发了，应该回写 LastFiredAt（哪怕每个用例集都起不来）")
	}
	if got.LastSkipReason != "" {
		t.Errorf("last_skip_reason = %q, want 空", got.LastSkipReason)
	}
}
