package service

import (
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
)

// PlanService 负责测试计划。
//
// 测试计划 = 「一组用例集 + 什么时候跑」。定时任务不是独立概念，
// 而是计划的一个属性（trigger_type=cron 时才生效）。
//
// 这个文件里真正需要注意的有四处：
//
//  1. **cron 在保存时就校验**，不等到调度器启动才失败。表达式写错是最常见
//     的错误，让它落在 POST /plans 的 40000 上，而不是变成一个永远不跑的计划。
//  2. **每个计划自带 IANA 时区**。不绑时区的后果很隐蔽：服务器跑在 UTC，
//     用户填 `0 9 * * *` 以为是早上 9 点，实际是 UTC 9 点。这类错误不报错，
//     只会让所有计划静默错位，发现它往往要等好几周。
//  3. 成员（PlanSuite）是**全量替换**，数组顺序即执行顺序 —— 与用例集成员同理。
//  4. 用例集被计划引用时不允许删除（SuiteService.Delete），避免悬空 ID。
type PlanService struct{ Deps }

// ---------------------------------------------------------------------------
// 视图与请求
// ---------------------------------------------------------------------------

// PlanView 是测试计划列表项。
type PlanView struct {
	ID          uint64 `json:"id"`
	ProjectID   uint64 `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	EnvID       uint64 `json:"env_id"`
	EnvName     string `json:"env_name,omitempty"`
	TriggerType string `json:"trigger_type"`
	CronExpr    string `json:"cron_expr"`
	// CronHuman 是 cron 表达式的中文说明，供前端在表达式旁边显示。
	// 不写这行的话，用户只能靠记忆判断 `*/15 9-18 * * 1-5` 到底是几分跑一次。
	CronHuman string `json:"cron_human"`
	// Timezone 与 NextFireAt 一起构成"它到底什么时候跑"的完整答案。
	// 只给 cron 表达式不给时区，等于只给了一半信息。
	Timezone   string  `json:"timezone"`
	NextFireAt *string `json:"next_fire_at"`
	Enabled    bool    `json:"enabled"`
	Timeout    int     `json:"timeout"`
	SuiteCount int     `json:"suite_count"`

	// --- 运行态：让"跳过"与"错过"可见 ---
	LastRunID      uint64  `json:"last_run_id"`
	LastFiredAt    *string `json:"last_fired_at"`
	LastMissedAt   *string `json:"last_missed_at"`
	LastSkipReason string  `json:"last_skip_reason"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// PlanSuiteView 是计划的用例集成员。
type PlanSuiteView struct {
	Seq       int    `json:"seq"`
	SuiteID   uint64 `json:"suite_id"`
	SuiteCode string `json:"suite_code"`
	SuiteName string `json:"suite_name"`
	CaseCount int    `json:"case_count"`
	Runnable  bool   `json:"runnable"`
	// SkipReason 说明这条成员为什么跑不了（用例集被删 / 没有成员）。
	SkipReason string `json:"skip_reason"`
}

// CreatePlanReq 是新建计划的请求。
type CreatePlanReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	EnvID       uint64 `json:"env_id"`
	TriggerType string `json:"trigger_type"`
	CronExpr    string `json:"cron_expr"`
	Timezone    string `json:"timezone"`
	Timeout     int    `json:"timeout"`
	Enabled     bool   `json:"enabled"`
	SuiteIDs    []uint64
}

// UpdatePlanReq 是修改计划的请求。字段与创建一致，全部可选式覆盖。
type UpdatePlanReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	EnvID       uint64 `json:"env_id"`
	TriggerType string `json:"trigger_type"`
	CronExpr    string `json:"cron_expr"`
	Timezone    string `json:"timezone"`
	Timeout     int    `json:"timeout"`
	Enabled     *bool  `json:"enabled"`
}

// SetPlanSuitesReq 是全量替换计划成员的请求。
type SetPlanSuitesReq struct {
	SuiteIDs []uint64 `json:"suite_ids"`
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

type PlanListQuery struct {
	Keyword string
	Enabled *bool
}

func (s *PlanService) List(projectID uint64, q PlanListQuery, page Page) ([]PlanView, int64, error) {
	tx := s.DB.Model(&model.TestPlan{}).Where("project_id = ?", projectID)
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		like := "%" + strings.ToLower(kw) + "%"
		tx = tx.Where("LOWER(name) LIKE ?", like)
	}
	if q.Enabled != nil {
		tx = tx.Where("enabled = ?", *q.Enabled)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计测试计划数量失败", err)
	}

	page = page.Normalize()
	var plans []model.TestPlan
	if err := tx.Order("id desc").Offset(page.Offset()).Limit(page.Limit()).
		Find(&plans).Error; err != nil {
		return nil, 0, errInternal("查询测试计划列表失败", err)
	}

	counts, err := s.suiteCounts(plans)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	views := make([]PlanView, 0, len(plans))
	for i := range plans {
		views = append(views, s.planView(&plans[i], counts[plans[i].ID], now))
	}
	return views, total, nil
}

// suiteCounts 批量统计各计划挂了几个用例集（避免 N+1）。
func (s *PlanService) suiteCounts(plans []model.TestPlan) (map[uint64]int, error) {
	out := make(map[uint64]int, len(plans))
	if len(plans) == 0 {
		return out, nil
	}
	ids := make([]uint64, 0, len(plans))
	for _, p := range plans {
		ids = append(ids, p.ID)
	}
	var rows []struct {
		PlanID uint64
		Cnt    int64
	}
	if err := s.DB.Model(&model.PlanSuite{}).
		Select("plan_id, count(*) as cnt").
		Where("plan_id IN ?", ids).
		Group("plan_id").Scan(&rows).Error; err != nil {
		return nil, errInternal("统计计划成员数失败", err)
	}
	for _, r := range rows {
		out[r.PlanID] = int(r.Cnt)
	}
	return out, nil
}

// Get 读取单个计划。
func (s *PlanService) Get(id uint64) (*model.TestPlan, error) { return loadPlan(s.DB, id) }

// View 读取单个计划的视图（含下次执行时间）。
func (s *PlanService) View(id uint64) (*PlanView, error) {
	p, err := loadPlan(s.DB, id)
	if err != nil {
		return nil, err
	}
	var n int64
	if err := s.DB.Model(&model.PlanSuite{}).Where("plan_id = ?", id).Count(&n).Error; err != nil {
		return nil, errInternal("统计计划成员数失败", err)
	}
	v := s.planView(p, int(n), time.Now())
	return &v, nil
}

// Suites 按 seq 返回计划的用例集成员。
func (s *PlanService) Suites(planID uint64) ([]PlanSuiteView, error) {
	// 先确认计划存在：不校验的话，GET /plans/9999/suites 会返回一个空数组，
	// 于是"计划不存在"和"计划没有成员"在响应上无法区分。
	if _, err := loadPlan(s.DB, planID); err != nil {
		return nil, err
	}
	var links []model.PlanSuite
	if err := s.DB.Where("plan_id = ?", planID).
		Order("seq asc, id asc").Find(&links).Error; err != nil {
		return nil, errInternal("查询计划成员失败", err)
	}
	if len(links) == 0 {
		return []PlanSuiteView{}, nil
	}

	ids := make([]uint64, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.SuiteID)
	}
	// 走 TestSuite 模型查询 ⇒ GORM 自动带上软删除条件 ⇒ 查不到的即"已删除"。
	var suites []model.TestSuite
	if err := s.DB.Where("id IN ?", ids).Find(&suites).Error; err != nil {
		return nil, errInternal("查询成员用例集失败", err)
	}
	byID := make(map[uint64]*model.TestSuite, len(suites))
	for i := range suites {
		byID[suites[i].ID] = &suites[i]
	}

	// 用例集没有成员就跑不了，这一点要在列表里就看出来 ——
	// 否则计划"跑成功了"但一条用例都没执行，是最难查的一类问题。
	caseCnt, err := s.suiteCaseCounts(ids)
	if err != nil {
		return nil, err
	}

	views := make([]PlanSuiteView, 0, len(links))
	for _, l := range links {
		v := PlanSuiteView{Seq: l.Seq, SuiteID: l.SuiteID}
		su, ok := byID[l.SuiteID]
		if !ok {
			v.SuiteCode = "(已删除)"
			v.SuiteName = "用例集已被删除"
			v.Runnable = false
			v.SkipReason = "用例集已被删除"
			views = append(views, v)
			continue
		}
		v.SuiteCode = su.Code
		v.SuiteName = su.Name
		v.CaseCount = caseCnt[su.ID]
		v.Runnable = v.CaseCount > 0
		if !v.Runnable {
			v.SkipReason = "用例集没有成员，执行时会被跳过"
		}
		views = append(views, v)
	}
	return views, nil
}

func (s *PlanService) suiteCaseCounts(suiteIDs []uint64) (map[uint64]int, error) {
	out := make(map[uint64]int, len(suiteIDs))
	if len(suiteIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SuiteID uint64
		Cnt     int64
	}
	if err := s.DB.Model(&model.SuiteCase{}).
		Select("suite_id, count(*) as cnt").
		Where("suite_id IN ?", suiteIDs).
		Group("suite_id").Scan(&rows).Error; err != nil {
		return nil, errInternal("统计用例集成员数失败", err)
	}
	for _, r := range rows {
		out[r.SuiteID] = int(r.Cnt)
	}
	return out, nil
}

func (s *PlanService) planView(p *model.TestPlan, suiteCount int, now time.Time) PlanView {
	v := PlanView{
		ID:             p.ID,
		ProjectID:      p.ProjectID,
		Name:           p.Name,
		Description:    p.Description,
		EnvID:          p.EnvID,
		TriggerType:    p.TriggerType,
		CronExpr:       p.CronExpr,
		Timezone:       p.Timezone,
		Enabled:        p.Enabled,
		Timeout:        p.Timeout,
		SuiteCount:     suiteCount,
		LastRunID:      p.LastRunID,
		LastSkipReason: p.LastSkipReason,
		CreatedAt:      p.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:      p.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if p.LastFiredAt != nil {
		str := p.LastFiredAt.Format("2006-01-02 15:04:05")
		v.LastFiredAt = &str
	}
	if p.LastMissedAt != nil {
		str := p.LastMissedAt.Format("2006-01-02 15:04:05")
		v.LastMissedAt = &str
	}
	if p.TriggerType == model.TriggerCron && p.CronExpr != "" {
		v.CronHuman = describeCronHuman(p.CronExpr)
		if t, err := nextFireAt(p.CronExpr, p.Timezone, now); err == nil {
			str := t.Format("2006-01-02 15:04:05")
			v.NextFireAt = &str
		}
	}
	return v
}

// ---------------------------------------------------------------------------
// 写操作
// ---------------------------------------------------------------------------

func (s *PlanService) Create(projectID uint64, req CreatePlanReq) (*model.TestPlan, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("测试计划名称不能为空")
	}
	trigger, cronExpr, tz, err := normalizeSchedule(req.TriggerType, req.CronExpr, req.Timezone)
	if err != nil {
		return nil, err
	}
	if req.Timeout < 0 {
		return nil, errBadParam("超时时间不能为负数")
	}
	if req.EnvID > 0 {
		if _, err := loadEnv(s.DB, req.EnvID); err != nil {
			return nil, err
		}
	}

	var exists int64
	if err := s.DB.Model(&model.TestPlan{}).
		Where("project_id = ? AND name = ?", projectID, name).Count(&exists).Error; err != nil {
		return nil, errInternal("检查计划名称是否重复失败", err)
	}
	if exists > 0 {
		return nil, errConflict("项目下已存在名为 %q 的测试计划", name)
	}

	p := &model.TestPlan{
		ProjectID:   projectID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		EnvID:       req.EnvID,
		TriggerType: trigger,
		CronExpr:    cronExpr,
		Timezone:    tz,
		Timeout:     req.Timeout,
		Enabled:     req.Enabled,
	}
	if err := s.DB.Create(p).Error; err != nil {
		return nil, errInternal("创建测试计划失败", err)
	}
	logx.L().Info().Uint64("plan_id", p.ID).Str("name", p.Name).
		Str("trigger", p.TriggerType).Str("cron", p.CronExpr).Msg("创建测试计划")

	if len(req.SuiteIDs) > 0 {
		if _, err := s.SetSuites(p.ID, req.SuiteIDs); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (s *PlanService) Update(id uint64, req UpdatePlanReq) (*model.TestPlan, error) {
	p, err := loadPlan(s.DB, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("测试计划名称不能为空")
	}
	trigger, cronExpr, tz, err := normalizeSchedule(req.TriggerType, req.CronExpr, req.Timezone)
	if err != nil {
		return nil, err
	}
	if req.Timeout < 0 {
		return nil, errBadParam("超时时间不能为负数")
	}
	if req.EnvID > 0 {
		if _, err := loadEnv(s.DB, req.EnvID); err != nil {
			return nil, err
		}
	}

	var exists int64
	if err := s.DB.Model(&model.TestPlan{}).
		Where("project_id = ? AND name = ? AND id <> ?", p.ProjectID, name, id).
		Count(&exists).Error; err != nil {
		return nil, errInternal("检查计划名称是否重复失败", err)
	}
	if exists > 0 {
		return nil, errConflict("项目下已存在名为 %q 的测试计划", name)
	}

	enabled := p.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if err := s.DB.Model(&model.TestPlan{}).Where("id = ?", id).Updates(map[string]any{
		"name":         name,
		"description":  strings.TrimSpace(req.Description),
		"env_id":       req.EnvID,
		"trigger_type": trigger,
		"cron_expr":    cronExpr,
		"timezone":     tz,
		"timeout":      req.Timeout,
		"enabled":      enabled,
	}).Error; err != nil {
		return nil, errInternal("更新测试计划失败", err)
	}

	p.Name = name
	p.Description = strings.TrimSpace(req.Description)
	p.EnvID = req.EnvID
	p.TriggerType = trigger
	p.CronExpr = cronExpr
	p.Timezone = tz
	p.Timeout = req.Timeout
	p.Enabled = enabled
	return p, nil
}

// Delete 删除计划（软删除）并清掉成员关联。
//
// 引用计数保护放在**用例集**那一侧（SuiteService.Delete），计划本身没有
// 被别的东西引用，删掉就是删掉。
func (s *PlanService) Delete(id uint64) error {
	if _, err := loadPlan(s.DB, id); err != nil {
		return err
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plan_id = ?", id).Delete(&model.PlanSuite{}).Error; err != nil {
			return errInternal("删除计划成员失败", err)
		}
		if err := tx.Delete(&model.TestPlan{}, id).Error; err != nil {
			return errInternal("删除测试计划失败", err)
		}
		return nil
	})
}

// SetSuites 全量替换计划的用例集成员，数组顺序即执行顺序。
func (s *PlanService) SetSuites(planID uint64, suiteIDs []uint64) ([]PlanSuiteView, error) {
	p, err := loadPlan(s.DB, planID)
	if err != nil {
		return nil, err
	}

	seen := make(map[uint64]struct{}, len(suiteIDs))
	for _, sid := range suiteIDs {
		if sid == 0 {
			return nil, errBadParam("成员列表里不能有空 ID")
		}
		if _, dup := seen[sid]; dup {
			return nil, errConflict("用例集 %d 被重复加入计划，同一个用例集不能出现两次", sid)
		}
		seen[sid] = struct{}{}
	}

	// 成员必须属于同一个项目。放到执行时才发现的话，
	// 用户会看到"计划跑了但少了几个用例集"却不知道原因。
	if len(suiteIDs) > 0 {
		var n int64
		if err := s.DB.Model(&model.TestSuite{}).
			Where("id IN ? AND project_id = ?", suiteIDs, p.ProjectID).Count(&n).Error; err != nil {
			return nil, errInternal("校验成员用例集失败", err)
		}
		if int(n) != len(suiteIDs) {
			return nil, errBadParam("成员里有 %d 个用例集不存在或不属于本项目（共提交 %d 个）",
				len(suiteIDs)-int(n), len(suiteIDs))
		}
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plan_id = ?", planID).Delete(&model.PlanSuite{}).Error; err != nil {
			return errInternal("清空计划成员失败", err)
		}
		if len(suiteIDs) == 0 {
			return nil
		}
		links := make([]model.PlanSuite, 0, len(suiteIDs))
		for i, sid := range suiteIDs {
			links = append(links, model.PlanSuite{PlanID: planID, SuiteID: sid, Seq: i + 1})
		}
		if err := tx.Create(&links).Error; err != nil {
			if isDuplicated(err) {
				return errConflict("用例集被重复加入计划")
			}
			return errInternal("写入计划成员失败", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Suites(planID)
}

// SetEnabled 单独开关计划。
//
// 与 Update 分开是为了让调度器不必 diff 整个计划就知道要不要重建任务：
// 一个专门的方法比"改一个字段要走完整覆盖"更不容易出错。
func (s *PlanService) SetEnabled(id uint64, enabled bool) (*model.TestPlan, error) {
	p, err := loadPlan(s.DB, id)
	if err != nil {
		return nil, err
	}
	if err := s.DB.Model(&model.TestPlan{}).Where("id = ?", id).
		Update("enabled", enabled).Error; err != nil {
		return nil, errInternal("更新计划开关失败", err)
	}
	p.Enabled = enabled
	logx.L().Info().Uint64("plan_id", id).Bool("enabled", enabled).Msg("计划开关已变更")
	return p, nil
}

// ---------------------------------------------------------------------------
// 调度辅助
// ---------------------------------------------------------------------------

// normalizeSchedule 校验并归一化触发方式与 cron 表达式。
//
// ⭐ 校验在**保存时**做，不等到调度器启动才失败：cron 表达式写错是最常见的
// 错误，让它落在 40000 上，而不是变成一个永远不跑的计划。
func normalizeSchedule(trigger, expr, tz string) (string, string, string, error) {
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	if trigger == "" {
		trigger = model.TriggerManual
	}
	switch trigger {
	case model.TriggerManual, model.TriggerCron:
	case model.TriggerCI:
		// CI 触发由 CI Token 那条路径设置，用户不能在计划里选它 ——
		// 选了会得到一个"配了但永远不会自己触发"的计划。
		return "", "", "", errBadParam("触发方式不能手动指定为 ci（那是 CI Token 触发时的记录值）")
	default:
		return "", "", "", errBadParam("触发方式只能是 manual 或 cron，收到 %q", trigger)
	}

	zone, err := normalizeTimezone(tz)
	if err != nil {
		return "", "", "", err
	}

	cronExpr := strings.TrimSpace(expr)
	if trigger == model.TriggerManual {
		// 手动计划不校验 cron：用户可能先建手动计划、之后再改成定时，
		// 此时 cron 字段为空是合法状态。
		return trigger, "", zone, nil
	}
	if cronExpr == "" {
		return "", "", "", errBadParam("定时计划的 cron 表达式不能为空")
	}
	if _, err := parseCron(cronExpr); err != nil {
		// 把引擎的报错原样带上：它是"第 3 段越界"这种具体信息，
		// 换成我们自己写的一句话反而更难排查。
		return "", "", "", errBadParam("cron 表达式 %q 非法：%v", cronExpr, err.Error())
	}
	return trigger, cronExpr, zone, nil
}

// normalizeTimezone 校验 IANA 时区名，空值回落 Asia/Shanghai。
func normalizeTimezone(tz string) (string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return defaultTimezone, nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", errBadParam("时区 %q 不是合法的 IANA 时区名（例如 Asia/Shanghai、UTC）", tz)
	}
	return tz, nil
}

// defaultTimezone 是新建计划的默认时区。
//
// 定成 Asia/Shanghai 而不是 UTC：这个平台的目标用户填 `0 9 * * *` 时
// 想的是"北京时间早上 9 点"。默认 UTC 会让绝大多数人的第一个定时计划错位 8 小时。
const defaultTimezone = "Asia/Shanghai"

// cronParser 只接受 5 段（分 时 日 月 周）。
//
// 不用 6 段带秒：测试计划最细到分钟已经足够，秒级只会让
// "为什么它在跑"更难解释，也会让"错过调度点"的判断变得没有意义。
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func parseCron(expr string) (cron.Schedule, error) {
	return cronParser.Parse(expr)
}

// nextFireAt 算出下一次触发的绝对时刻。
//
// ⭐ 时区在这里生效：先把 now 换到计划所在时区，再让 schedule 解释
// `0 9 * * *` —— 否则"9 点"会被按服务器时区解释，而服务器多半是 UTC。
func nextFireAt(expr, tz string, from time.Time) (time.Time, error) {
	sch, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	return sch.Next(from.In(loc)), nil
}

// ---------------------------------------------------------------------------
// 加载
// ---------------------------------------------------------------------------

func loadPlan(db *gorm.DB, id uint64) (*model.TestPlan, error) {
	var p model.TestPlan
	if err := db.First(&p, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("测试计划不存在（id=%d）", id)
		}
		return nil, errInternal("查询测试计划失败", err)
	}
	return &p, nil
}
