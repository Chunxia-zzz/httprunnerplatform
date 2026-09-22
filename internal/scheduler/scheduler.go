// Package scheduler 是测试计划的进程内定时调度器。
//
// 三条硬规则（设计文档 3.4）：
//
//  1. **重入保护**：上一轮还没跑完、下一个调度点到了 → 跳过，并记下原因。
//     不做的话，一个跑 40 分钟的用例集配 `*/5 * * * *` 会把执行堆成山，
//     而且每一条都在抢 max_concurrency 的槽位。
//  2. **不补跑错过的调度点**，但记录 LastMissedAt。
//     保证"错过"这件事不被静默吞掉 —— 静默跳过的定时任务是运维里
//     最难发现的一类故障：它不会报错，只会让该跑的没跑。
//  3. **只在单实例下成立**：多实例部署时每个实例都会触发同一个计划。
//     M2 按单实例做，这条必须写进部署文档。以后要多实例，靠数据库行级锁
//     抢"调度权"解决，不在本次范围。
package scheduler

import (
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
)

// 跳过原因。写进 TestPlan.LastSkipReason，前端原样展示。
const (
	SkipReasonStillRunning = "上一轮仍在执行"
	SkipReasonNoSuite      = "计划没有成员"
	SkipReasonTriggerFail  = "触发失败"
)

// syncEvery 是对账间隔。
//
// 计划被增删改之后最多 30 秒生效。选 30 秒而不是立即重建，是因为
// 调度器不打算让 API 层"记得通知我" —— 依赖调用方记得调 Reload 的设计
// 一定会漏，而对账是自愈的：即使漏了一次，下一轮也会纠正。
const syncEvery = 30 * time.Second

type Scheduler struct {
	db   *gorm.DB
	runs *service.RunService

	cron    *cron.Cron
	mu      sync.Mutex
	entries map[uint64]cron.EntryID
	// specOf 记录每条 entry 生效时的 "cron|tz"，用于判断配置是否被改过。
	// 不用 cron 表达式单独比较：改了时区也要重建，否则会继续按旧时区跑。
	specOf map[uint64]string

	// inflight 是本进程内"正在触发"的计划集合。
	// DB 里的 LastRunID 也能判断，但那要等执行记录写库；
	// 这个 map 用来挡住同一轮里的并发触发，两者是互补关系。
	inflight sync.Map

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New 构造调度器。Start 之前不会有任何动作。
func New(db *gorm.DB, runs *service.RunService) *Scheduler {
	// ⚠️ 用 UTC 作为 cron 的时钟，时区由 tzSchedule 逐条换算。
	// 反过来（给 Cron 设一个业务时区）会让所有计划共用同一个时区，
	// 那正是 3.3 要避免的那类静默错位。
	c := cron.New(cron.WithLocation(time.UTC))
	return &Scheduler{
		db:      db,
		runs:    runs,
		cron:    c,
		entries: make(map[uint64]cron.EntryID),
		specOf:  make(map[uint64]string),
		stopCh:  make(chan struct{}),
	}
}

// Start 装载全部定时计划并启动对账循环。
func (s *Scheduler) Start() {
	s.sync()
	s.cron.Start()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(syncEvery)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.sync()
			}
		}
	}()
	logx.L().Info().Msg("测试计划调度器已启动")
}

// Stop 停止调度并等待对账循环退出。
//
// 用 Stop 而不是直接 Stop 掉 cron：cron.Stop 会等正在跑的 job 结束，
// 这正是我们要的 —— 正在触发的计划不应该因为服务要关了就跑一半。
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		ctx := s.cron.Stop()
		// 最多等 5 秒：卡住的 job 不能把服务退出一直拖着。
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			logx.L().Warn().Msg("等待调度任务收尾超时，强制退出")
		}
		s.wg.Wait()
		logx.L().Info().Msg("测试计划调度器已停止")
	})
}

// sync 把 DB 里的定时计划与 cron 里的任务对账一次。
func (s *Scheduler) sync() {
	var plans []model.TestPlan
	// 一次把 trigger_type=cron 的计划全取回来（含已禁用的）：
	// "禁用"要在这一层判断，才能走到下面"撤掉已删除/禁用/改成手动的计划"。
	if err := s.db.Where("trigger_type = ?", model.TriggerCron).Find(&plans).Error; err != nil {
		logx.L().Error().Err(err).Msg("装载定时计划失败")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	alive := make(map[uint64]struct{}, len(plans))
	for i := range plans {
		p := plans[i]
		if !p.Enabled || p.CronExpr == "" {
			continue
		}
		alive[p.ID] = struct{}{}
		spec := p.CronExpr + "|" + p.Timezone
		if old, ok := s.specOf[p.ID]; ok && old == spec {
			continue // 配置没变，保持原 entry
		}
		// 配置变了（或首次装载）：先撤旧的再加新的。
		if old, ok := s.entries[p.ID]; ok {
			s.cron.Remove(old)
			delete(s.entries, p.ID)
		}
		sch, err := cronParser.Parse(p.CronExpr)
		if err != nil {
			// 保存时已经校验过，走到这里说明库里躺着一条非法表达式
			// （例如手工改过库）。记一条错误就行，不能让整个对账停下来。
			logx.L().Error().Err(err).
				Uint64("plan_id", p.ID).Str("cron", p.CronExpr).
				Msg("定时计划无法装载，已跳过")
			continue
		}
		loc := time.UTC
		if p.Timezone != "" {
			if l, err := time.LoadLocation(p.Timezone); err == nil {
				loc = l
			} else {
				logx.L().Warn().Str("tz", p.Timezone).Uint64("plan_id", p.ID).
					Msg("计划的时区无法识别，按 UTC 调度")
			}
		}
		id := s.cron.Schedule(tzSchedule{Schedule: sch, loc: loc}, cron.FuncJob(s.job(p.ID)))
		s.entries[p.ID] = id
		s.specOf[p.ID] = spec
	}

	// 撤掉已经删除 / 禁用 / 改成手动的计划
	for id, entry := range s.entries {
		if _, ok := alive[id]; ok {
			continue
		}
		s.cron.Remove(entry)
		delete(s.entries, id)
		delete(s.specOf, id)
	}
}

// job 返回一个触发计划的闭包。
//
// 闭包里**重新读一次计划**而不是用装载时的快照：计划在这 30 秒里可能
// 已经被改过甚至删掉了，按快照触发会跑出一个用户已经不要的配置。
func (s *Scheduler) job(planID uint64) func() {
	return func() {
		s.fire(planID)
	}
}

// fire 触发一次计划。
func (s *Scheduler) fire(planID uint64) {
	var p model.TestPlan
	if err := s.db.First(&p, planID).Error; err != nil {
		logx.L().Warn().Err(err).Uint64("plan_id", planID).Msg("定时计划已不存在，跳过本次触发")
		return
	}
	if !p.Enabled || p.TriggerType != model.TriggerCron {
		return
	}

	// 规则 1：重入保护。
	if _, busy := s.inflight.Load(planID); busy {
		s.markSkipped(&p, SkipReasonStillRunning)
		return
	}
	if s.lastRunStillGoing(&p) {
		s.markSkipped(&p, SkipReasonStillRunning)
		return
	}

	s.inflight.Store(planID, struct{}{})
	defer s.inflight.Delete(planID)

	var links []model.PlanSuite
	if err := s.db.Where("plan_id = ?", planID).Order("seq asc, id asc").
		Find(&links).Error; err != nil {
		logx.L().Error().Err(err).Uint64("plan_id", planID).Msg("读取计划成员失败")
		s.markSkipped(&p, SkipReasonTriggerFail)
		return
	}
	if len(links) == 0 {
		// 没有成员不算"错过调度点"，但也要留下痕迹：
		// 一个配好了却永远不跑的计划，和跑了的看起来一模一样。
		s.markSkipped(&p, SkipReasonNoSuite)
		return
	}

	now := time.Now()
	var lastRunID uint64
	fired := 0
	for _, l := range links {
		run, err := s.runs.StartSuite(service.StartRunRequest{
			ProjectID:   p.ProjectID,
			TargetType:  model.TargetSuite,
			TargetID:    l.SuiteID,
			EnvID:       p.EnvID,
			TriggerType: model.TriggerCron,
			PlanID:      p.ID,
		}, 0)
		if err != nil {
			// ⭐ 一个用例集起不来不该让同计划里的其它用例集跟着不跑 ——
			// 用例集之间本来就该失败隔离，这正是"计划触发产生 N 条执行记录"
			// 而不是合并成一条的理由（设计文档 3.5）。
			logx.L().Error().Err(err).
				Uint64("plan_id", p.ID).Uint64("suite_id", l.SuiteID).
				Msg("计划内的用例集启动失败，继续下一个")
			continue
		}
		lastRunID = run.ID
		fired++
	}

	updates := map[string]any{
		"last_fired_at":    &now,
		"last_missed_at":   nil,
		"last_skip_reason": "",
	}
	if lastRunID > 0 {
		updates["last_run_id"] = lastRunID
	}
	if err := s.db.Model(&model.TestPlan{}).Where("id = ?", p.ID).Updates(updates).Error; err != nil {
		logx.L().Error().Err(err).Uint64("plan_id", p.ID).Msg("回写计划触发状态失败")
	}
	logx.L().Info().
		Uint64("plan_id", p.ID).Str("plan", p.Name).
		Int("suites", fired).Int("members", len(links)).
		Msg("定时计划已触发")
}

// lastRunStillGoing 判断上一次触发的执行是否还没结束。
//
// 只看最后一条执行记录：计划是串行启动的，最后一条没结束 ⇒ 这一轮还没跑完。
// 服务重启后进程内的 inflight 会清空，这条 DB 判断是唯一的兜底。
func (s *Scheduler) lastRunStillGoing(p *model.TestPlan) bool {
	if p.LastRunID == 0 {
		return false
	}
	var st string
	err := s.db.Model(&model.RunRecord{}).Where("id = ?", p.LastRunID).
		Limit(1).Pluck("status", &st).Error
	if err != nil || st == "" {
		return false
	}
	return st == model.RunQueued || st == model.RunRunning
}

// markSkipped 记下一次"调度点到了但没真正触发"。
//
// 不补跑（规则 2），但必须留痕：静默跳过的定时任务不会报错，
// 只会让该跑的没跑，而发现它往往要等好几周。
func (s *Scheduler) markSkipped(p *model.TestPlan, reason string) {
	now := time.Now()
	if err := s.db.Model(&model.TestPlan{}).Where("id = ?", p.ID).Updates(map[string]any{
		"last_missed_at":   &now,
		"last_skip_reason": reason,
	}).Error; err != nil {
		logx.L().Error().Err(err).Uint64("plan_id", p.ID).Msg("回写计划跳过状态失败")
	}
	logx.L().Warn().
		Uint64("plan_id", p.ID).Str("plan", p.Name).Str("reason", reason).
		Msg("定时计划的调度点被跳过（不补跑）")
}

// ---------------------------------------------------------------------------
// 时区换算
// ---------------------------------------------------------------------------

// tzSchedule 把一个 cron Schedule 绑定到指定时区。
//
// robfig/cron 的 Cron 只有一个全局 location，而每个计划各有自己的时区。
// 做法是把传入的时间先换到计划时区，让 schedule 按那个时区的"几点几分"
// 来解释表达式，再把结果换算回绝对时刻。
type tzSchedule struct {
	cron.Schedule
	loc *time.Location
}

func (t tzSchedule) Next(now time.Time) time.Time {
	return t.Schedule.Next(now.In(t.loc)).UTC()
}

// cronParser 与 service 侧保存时用的规格保持一致：5 段（分 时 日 月 周）+ 描述符。
//
// 两处必须一致，否则会出现"保存时校验通过、装载时解析失败"的计划 ——
// 那是最难查的一类：它不会报错，只是永远不跑。
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
