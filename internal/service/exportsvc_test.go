package service

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

func exportSeed(t *testing.T, d Deps) uint64 {
	t.Helper()
	proj, err := New(d).Project.Create(CreateProjectReq{Code: "exp1", Name: "导出项目"}, 1)
	if err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	_, err = New(d).Environment.Create(proj.ID, EnvReq{
		Name: "测试环境", BaseURL: "http://127.0.0.1:8133", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("建环境失败: %v", err)
	}
	_, err = New(d).Case.Create(proj.ID, CaseReq{
		Code:   "case_ok",
		Name:   "健康用例",
		Status: "active",
		Config: reqAny(map[string]any{"verify": true}),
		Steps: []StepReq{{
			Seq: 1, StepType: model.StepRequest, Name: "步骤一",
			Request:  reqAny(map[string]any{"method": "GET", "url": "$base_url/login"}),
			Validate: []model.AssertItem{{Check: "status_code", Assert: "eq", Expect: 200}},
		}},
	}, 1)
	if err != nil {
		t.Fatalf("建用例失败: %v", err)
	}
	return proj.ID
}

func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析 zip 失败: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", f.Name, err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(content)
	}
	return out
}

func TestExport_完整项目(t *testing.T) {
	d := testDeps(t)
	pid := exportSeed(t, d)
	svc := NewExportService(d)

	data, filename, err := svc.Export(pid, 0)
	if err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	if !strings.HasPrefix(filename, "exp1-export-") {
		t.Fatalf("文件名应以项目 code 开头: %q", filename)
	}

	files := readZip(t, data)
	if _, ok := files["exp1/README.txt"]; !ok {
		t.Fatalf("缺 README.txt: %v", keysOf(files))
	}
	envContent, ok := files["exp1/.env"]
	if !ok {
		t.Fatalf("缺 .env: %v", keysOf(files))
	}
	if !strings.Contains(envContent, "base_url") {
		t.Fatalf(".env 应含 base_url: %q", envContent)
	}
	if _, ok := files["exp1/testcases/case_ok.yaml"]; !ok {
		t.Fatalf("缺用例 YAML: %v", keysOf(files))
	}
	// environments/ 存档：环境名含中文 → 清洗为下划线
	found := false
	for name := range files {
		if strings.HasPrefix(name, "exp1/environments/") && strings.HasSuffix(name, ".env") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 environments 存档: %v", keysOf(files))
	}
}

func TestExport_坏用例不阻断(t *testing.T) {
	d := testDeps(t)
	pid := exportSeed(t, d)

	// 引用不存在数据集的用例 → 渲染失败
	_, err := New(d).Case.Create(pid, CaseReq{
		Code:   "case_ghost",
		Name:   "幽灵引用用例",
		Status: "active",
		Config: reqAny(map[string]any{
			"verify":   true,
			"datasets": []map[string]any{{"name": "no_such_dataset", "limit": 0}},
		}),
		Steps: []StepReq{{
			Seq: 1, StepType: model.StepRequest, Name: "步骤一",
			Request: reqAny(map[string]any{"method": "GET", "url": "$base_url/x"}),
		}},
	}, 1)
	if err != nil {
		t.Fatalf("建坏用例失败: %v", err)
	}

	svc := NewExportService(d)
	data, _, err := svc.Export(pid, 0)
	if err != nil {
		t.Fatalf("含坏用例时 Export 仍应成功: %v", err)
	}
	files := readZip(t, data)
	readme := files["exp1/README.txt"]
	if !strings.Contains(readme, "case_ghost") {
		t.Fatalf("README 应记录坏用例: %q", readme)
	}
	if _, ok := files["exp1/testcases/case_ok.yaml"]; !ok {
		t.Fatal("健康用例仍应导出")
	}
	if _, ok := files["exp1/testcases/case_ghost.yaml"]; ok {
		t.Fatal("坏用例不应产出 YAML")
	}
}

func TestExport_CSV数据集(t *testing.T) {
	d := testDeps(t)
	svcSet := New(d)
	pid := exportSeed(t, d)

	// 建一个 CSV 数据集（ParamService 写盘 + 落库）
	_, err := svcSet.Param.Create(pid, ParamDatasetReq{
		Name:    "users",
		Source:  model.ParamSourceCSV,
		CsvName: "users.csv",
		CsvText: "username,password\nalice,pw1\n",
	})
	if err != nil {
		t.Fatalf("建数据集失败: %v", err)
	}

	svc := NewExportService(d)
	data, _, err := svc.Export(pid, 0)
	if err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	files := readZip(t, data)
	found := false
	for name, content := range files {
		if strings.HasPrefix(name, "exp1/data/") && strings.HasSuffix(name, ".csv") {
			if strings.Contains(content, "alice") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("导出包应含数据集 CSV: %v", keysOf(files))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
