package service

import (
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// baselineSeed 造一条用例 + 若干次执行历史，返回 (projectID, caseID)。
//
// 历史（从旧到新）：
//
//	run1: pass（120ms, 2 步全过）
//	run2: pass（150ms, 2 步全过）
//	run3: fail（300ms, 1 过 1 败）   ← 从这里开始变坏
//	run4: fail（320ms, 0 过 2 败）
//	run5: pass（130ms, 2 步全过）   ← 恢复
func baselineSeed(t *testing.T, d Deps) (uint64, uint64) {
	t.Helper()
	proj := seedProject(t, d)
	tc := &model.TestCase{
		ProjectID: proj.ID,
		Code:      "case_baseline",
		Name:      "基线对比用例",
		Status:    model.CaseStatusActive,
	}
	if err := d.DB.Create(tc).Error; err != nil {
		t.Fatalf("create case: %v", err)
	}

	now := time.Now()
	mk := func(i int, crStatus, runStatus string, dur int64, stepPassed, stepFailed int) {
		finished := now.Add(-time.Duration(5-i) * time.Hour)
		run := &model.RunRecord{
			Base:        model.Base{CreatedAt: finished},
			ProjectID:   proj.ID,
			TargetType:  model.TargetCase,
			TargetID:    tc.ID,
			TargetName:  tc.Name,
			Status:      runStatus,
			TriggerType: model.TriggerManual,
			FinishedAt:  &finished,
		}
		if err := d.DB.Create(run).Error; err != nil {
			t.Fatalf("create run: %v", err)
		}
		cr := &model.CaseResult{
			Base:       model.Base{CreatedAt: finished},
			RunID:      run.ID,
			CaseID:     tc.ID,
			CaseCode:   tc.Code,
			ConfigName: tc.Name,
			Status:     crStatus,
			DurationMs: dur,
			StepTotal:  stepPassed + stepFailed,
			StepPassed: stepPassed,
		}
		if err := d.DB.Create(cr).Error; err != nil {
			t.Fatalf("create case result: %v", err)
		}
		// 铺步骤结果：stepPassed 条 pass + stepFailed 条 fail。
		for s := 0; s < stepPassed; s++ {
			st := &model.StepResult{
				RunID:        run.ID,
				CaseResultID: cr.ID,
				CaseCode:     tc.Code,
				Seq:          s + 1,
				StepName:     "ok-step",
				Status:       model.StatusPass,
			}
			if err := d.DB.Create(st).Error; err != nil {
				t.Fatalf("create step: %v", err)
			}
		}
		for s := 0; s < stepFailed; s++ {
			st := &model.StepResult{
				RunID:        run.ID,
				CaseResultID: cr.ID,
				CaseCode:     tc.Code,
				Seq:          stepPassed + s + 1,
				StepName:     "bad-step",
				Status:       model.StatusFail,
			}
			if err := d.DB.Create(st).Error; err != nil {
				t.Fatalf("create step: %v", err)
			}
		}
	}

	mk(1, model.StatusPass, model.RunSuccess, 120, 2, 0)
	mk(2, model.StatusPass, model.RunSuccess, 150, 2, 0)
	mk(3, model.StatusFail, model.RunFailed, 300, 1, 1)
	mk(4, model.StatusFail, model.RunFailed, 320, 0, 2)
	mk(5, model.StatusPass, model.RunSuccess, 130, 2, 0)

	return proj.ID, tc.ID
}

func TestBaseline_最近N次从旧到新(t *testing.T) {
	d := testDeps(t)
	pid, cid := baselineSeed(t, d)
	svc := NewBaselineService(d)

	res, err := svc.Baseline(BaselineQuery{ProjectID: pid, CaseID: cid, Limit: 5})
	if err != nil {
		t.Fatalf("baseline err: %v", err)
	}
	if res.TotalRuns != 5 {
		t.Fatalf("total 应为 5，实际 %d", res.TotalRuns)
	}
	if len(res.Runs) != 5 {
		t.Fatalf("runs 应为 5，实际 %d", len(res.Runs))
	}
	// 从旧到新：第 0 项 pass(120ms)，最后一项 pass(130ms)。
	if res.Runs[0].DurationMs != 120 || res.Runs[0].Status != model.StatusPass {
		t.Fatalf("第 0 项（最早）应为 pass/120ms，实际 %+v", res.Runs[0])
	}
	if res.Runs[4].DurationMs != 130 {
		t.Fatalf("最后一项（最新）应为 130ms，实际 %d", res.Runs[4].DurationMs)
	}
	// 第 0 项无 Delta。
	if res.Runs[0].Delta != nil {
		t.Fatalf("最早一项不应有 Delta，实际 %+v", res.Runs[0].Delta)
	}
}

func TestBaseline_变坏与恢复信号(t *testing.T) {
	d := testDeps(t)
	pid, cid := baselineSeed(t, d)
	svc := NewBaselineService(d)

	res, err := svc.Baseline(BaselineQuery{ProjectID: pid, CaseID: cid, Limit: 5})
	if err != nil {
		t.Fatalf("baseline err: %v", err)
	}
	runs := res.Runs
	// runs[2]：pass(150) → fail(300)，应标记 broke。
	if !runs[2].Delta.Broke {
		t.Fatalf("run3 应标记 broke，实际 %+v", runs[2].Delta)
	}
	if runs[2].Delta.Recovered {
		t.Fatalf("run3 不应标记 recovered")
	}
	if runs[2].Delta.DurDeltaMs != 150 {
		t.Fatalf("run3 耗时差应为 150（300-150），实际 %d", runs[2].Delta.DurDeltaMs)
	}
	// runs[4]：fail(320) → pass(130)，应标记 recovered。
	if !runs[4].Delta.Recovered {
		t.Fatalf("run5 应标记 recovered，实际 %+v", runs[4].Delta)
	}
	if runs[4].Delta.Broke {
		t.Fatalf("run5 不应标记 broke")
	}
	// runs[3]：fail → fail，既非 broke 也非 recovered。
	if runs[3].Delta.Broke || runs[3].Delta.Recovered {
		t.Fatalf("run4 不应有 broke/recovered，实际 %+v", runs[3].Delta)
	}
}

func TestBaseline_步骤失败数聚合(t *testing.T) {
	d := testDeps(t)
	pid, cid := baselineSeed(t, d)
	svc := NewBaselineService(d)

	res, err := svc.Baseline(BaselineQuery{ProjectID: pid, CaseID: cid, Limit: 5})
	if err != nil {
		t.Fatalf("baseline err: %v", err)
	}
	runs := res.Runs
	if runs[2].StepFailed != 1 || runs[2].StepPassed != 1 {
		t.Fatalf("run3 应 1 过 1 败，实际 passed=%d failed=%d", runs[2].StepPassed, runs[2].StepFailed)
	}
	if runs[3].StepFailed != 2 || runs[3].StepPassed != 0 {
		t.Fatalf("run4 应 0 过 2 败，实际 passed=%d failed=%d", runs[3].StepPassed, runs[3].StepFailed)
	}
}

func TestBaseline_Limit截断(t *testing.T) {
	d := testDeps(t)
	pid, cid := baselineSeed(t, d)
	svc := NewBaselineService(d)

	res, err := svc.Baseline(BaselineQuery{ProjectID: pid, CaseID: cid, Limit: 2})
	if err != nil {
		t.Fatalf("baseline err: %v", err)
	}
	if res.TotalRuns != 5 {
		t.Fatalf("total 应仍为 5（截断不影响总数），实际 %d", res.TotalRuns)
	}
	if len(res.Runs) != 2 {
		t.Fatalf("runs 应截断为 2，实际 %d", len(res.Runs))
	}
	// 最近 2 次是 run4(fail/320) 与 run5(pass/130)。
	if res.Runs[0].DurationMs != 320 || res.Runs[1].DurationMs != 130 {
		t.Fatalf("最近 2 次应为 320 与 130，实际 %d / %d", res.Runs[0].DurationMs, res.Runs[1].DurationMs)
	}
}

func TestBaseline_无历史(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	tc := &model.TestCase{
		ProjectID: proj.ID,
		Code:      "case_empty",
		Name:      "无历史用例",
		Status:    model.CaseStatusActive,
	}
	if err := d.DB.Create(tc).Error; err != nil {
		t.Fatalf("create case: %v", err)
	}

	svc := NewBaselineService(d)
	res, err := svc.Baseline(BaselineQuery{ProjectID: proj.ID, CaseID: tc.ID})
	if err != nil {
		t.Fatalf("baseline err: %v", err)
	}
	if res.TotalRuns != 0 || len(res.Runs) != 0 {
		t.Fatalf("无历史应返回空 runs，实际 total=%d runs=%d", res.TotalRuns, len(res.Runs))
	}
}

func TestBaseline_跨项目拒绝(t *testing.T) {
	d := testDeps(t)
	pid, cid := baselineSeed(t, d)
	other := seedProject(t, d)

	svc := NewBaselineService(d)
	_, err := svc.Baseline(BaselineQuery{ProjectID: other.ID, CaseID: cid})
	if err == nil {
		t.Fatalf("用错误 project_id 查询应报错，实际通过（pid=%d 应查 cid=%d 失败）", pid, cid)
	}
}
