package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// TestProject_创建会建出完整工作区目录树 覆盖唯一需要落盘的那条路径。
//
// 目录名沿用 hrp 官方脚手架约定：即使 M1 只用得到 testcases/，
// api/ suites/ data/ 也一并建好，用户往里放 .csv 数据文件时不会撞到"目录不存在"。
func TestProject_创建会建出完整工作区目录树(t *testing.T) {
	d := testDeps(t)
	p := seedProjectOnDisk(t, d)

	root := projectWorkspace(d.Cfg, p)
	for _, sub := range append([]string{"."}, projectSubDirs...) {
		path := filepath.Join(root, sub)
		fi, err := os.Stat(path)
		if err != nil {
			t.Errorf("目录未创建: %s (%v)", path, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s 不是目录", path)
		}
	}

	// WorkspacePath 要回写到库里：执行时需要用它推导项目持久层目录
	var got model.Project
	if err := d.DB.First(&got, p.ID).Error; err != nil {
		t.Fatalf("读取项目失败: %v", err)
	}
	if got.WorkspacePath != root {
		t.Errorf("workspace_path = %q, want %q", got.WorkspacePath, root)
	}
}

// TestProject_目录已存在时创建仍成功 保证幂等。
//
// 用户手工删掉工作区目录后重建项目、或目录被运维预先建好，
// 都不该让"建项目"这件事失败。
func TestProject_目录已存在时创建仍成功(t *testing.T) {
	d := testDeps(t)
	svc := New(d).Project

	first, err := svc.Create(CreateProjectReq{Code: "dupdir", Name: "第一次"}, 1)
	if err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	// 删掉项目记录但保留目录，模拟"目录还在、库里没有"
	if err := d.DB.Unscoped().Delete(&model.Project{}, first.ID).Error; err != nil {
		t.Fatalf("清理项目记录失败: %v", err)
	}
	if _, err := svc.Create(CreateProjectReq{Code: "dupdir", Name: "第二次"}, 1); err != nil {
		t.Fatalf("目录已存在时创建应当成功（MkdirAll 幂等），实际: %v", err)
	}
}

// TestProject_结构性校验 code 是工作区目录名，必须在入口严格限制。
func TestProject_结构性校验(t *testing.T) {
	d := testDeps(t)
	svc := New(d).Project

	_, err := svc.Create(CreateProjectReq{Code: "a/b", Name: "带斜杠"}, 1)
	wantCode(t, err, response.CodeInvalidIdent)

	_, err = svc.Create(CreateProjectReq{Code: "ok", Name: "  "}, 1)
	wantCode(t, err, response.CodeBadParam)

	if _, err := svc.Create(CreateProjectReq{Code: "ok", Name: "正常项目"}, 1); err != nil {
		t.Fatalf("正常创建失败: %v", err)
	}
	_, err = svc.Create(CreateProjectReq{Code: "ok", Name: "重复 code"}, 1)
	wantCode(t, err, response.CodeConflict)
}

// TestProject_code不可改 code 是工作区目录名，改名要连带迁移目录。
func TestProject_code不可改(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	_, err := New(d).Project.Update(p.ID, UpdateProjectReq{Code: "renamed", Name: "改过的名字"})
	wantCode(t, err, response.CodeBadParam)

	// 不带 code（编辑器只改别的字段）应当被接受
	got, err := New(d).Project.Update(p.ID, UpdateProjectReq{Name: "改过的名字"})
	if err != nil {
		t.Fatalf("只改名称时不应要求带 code: %v", err)
	}
	if got.Name != "改过的名字" || got.Code != p.Code {
		t.Errorf("更新结果错误: name=%q code=%q", got.Name, got.Code)
	}
}

// TestProject_有执行在跑时不能删 否则会跑出"孤儿结果"。
func TestProject_有执行在跑时不能删(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Project

	running := model.RunRecord{
		ProjectID: p.ID, TargetType: model.TargetCase, TargetName: "x",
		Status: model.RunRunning,
	}
	if err := d.DB.Create(&running).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	wantCode(t, svc.Delete(p.ID), response.CodeInUse)

	// 执行收尾后即可删除
	if err := d.DB.Model(&model.RunRecord{}).Where("id = ?", running.ID).
		Update("status", model.RunSuccess).Error; err != nil {
		t.Fatalf("更新执行状态失败: %v", err)
	}
	if err := svc.Delete(p.ID); err != nil {
		t.Fatalf("执行结束后应当可以删除: %v", err)
	}
	if _, err := svc.Get(p.ID); err == nil {
		t.Error("软删除后不应再能读到项目")
	}
}
