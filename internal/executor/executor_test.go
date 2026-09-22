package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 参数组装
// ---------------------------------------------------------------------------

func TestBuildArgs(t *testing.T) {
	base := Options{CaseFile: "testcases/tc_1.yaml", GenHTMLReport: true}
	args := buildArgs(base, 300*time.Second)
	joined := strings.Join(args, " ")

	// 目标必须是**单个用例文件**，绝不能是目录（实测 A2：批量执行全有或全无）
	if !strings.HasPrefix(joined, "run testcases/tc_1.yaml ") {
		t.Fatalf("首个参数应为 run + 单用例文件，实际: %v", args)
	}
	for _, want := range []string{"--log-json", "--save-tests", "--gen-html-report", "--case-timeout 300"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少参数 %s: %v", want, args)
		}
	}
	// 目录分隔符要归一化成 /：引擎日志里会出现这个路径，统一风格便于比对
	if strings.Contains(joined, `\`) {
		t.Errorf("用例路径未归一化为正斜杠: %v", args)
	}
}

func TestFormatTimeoutSeconds(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{300 * time.Second, "300"},
		{90 * time.Second, "90"},
		{1200 * time.Millisecond, "1.2"},
		{500 * time.Millisecond, "0.5"},
	}
	for _, c := range cases {
		if got := formatTimeoutSeconds(c.in); got != c.want {
			t.Errorf("formatTimeoutSeconds(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildArgs_可选开关(t *testing.T) {
	// 默认不关报文明细：断言失败时它是唯一可用的失败线索（实测 F8）
	args := buildArgs(Options{CaseFile: "testcases/a.yaml"}, time.Second)
	if contains(args, "--log-requests-off") {
		t.Error("默认不应关闭请求/响应明细")
	}
	if contains(args, "--gen-html-report") {
		t.Error("未开启时不应传 --gen-html-report")
	}

	args = buildArgs(Options{CaseFile: "testcases/a.yaml", DisableRequestsLog: true}, time.Second)
	if !contains(args, "--log-requests-off") {
		t.Error("显式关闭时应传 --log-requests-off")
	}
}

func TestBuildArgs_超时时长可读(t *testing.T) {
	args := buildArgs(Options{CaseFile: "a.yaml"}, 90*time.Second)
	if !strings.Contains(strings.Join(args, " "), "--case-timeout 90") {
		t.Errorf("超时参数格式错误: %v", args)
	}
}

// ---------------------------------------------------------------------------
// 工作区与路径安全
// ---------------------------------------------------------------------------

func TestSafeSegment_拒绝路径穿越(t *testing.T) {
	bad := []string{"", "  ", "../etc/passwd", `a\b`, "a/b", "..", "a..b", "C:x"}
	for _, s := range bad {
		if _, err := safeSegment(s); err == nil {
			t.Errorf("safeSegment(%q) 应当报错", s)
		}
	}

	good := []string{"tc_login", "TC_1", "case-2", "用例"}
	for _, s := range good {
		if _, err := safeSegment(s); err != nil {
			t.Errorf("safeSegment(%q) 不应报错: %v", s, err)
		}
	}
}

func TestWorkspace_路径推导(t *testing.T) {
	ws, err := NewWorkspace(filepath.Join(t.TempDir(), "run1"))
	if err != nil {
		t.Fatal(err)
	}

	file, err := ws.CaseFile("tc_login")
	if err != nil {
		t.Fatal(err)
	}
	if file != "testcases/tc_login.yaml" {
		t.Errorf("用例相对路径错误: %q", file)
	}

	dir, err := ws.CaseArtifactsDir("tc_login")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, ws.ArtifactsRoot()) {
		t.Errorf("产物目录应在 artifacts/ 下: %q", dir)
	}

	if _, err := ws.CaseFile("../evil"); err == nil {
		t.Error("路径穿越必须被拒绝")
	}
}

func TestWorkspace_CopyFrom(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "testcases"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(project, ".env"), "base_url=http://127.0.0.1:8899\n")
	write(t, filepath.Join(project, "testcases", "a.yaml"), "config:\n    name: A\n")
	write(t, filepath.Join(project, "testcases", "b.yml"), "config:\n    name: B\n")
	// 这些都不该被复制
	write(t, filepath.Join(project, "testcases", "note.txt"), "x")
	write(t, filepath.Join(project, "testcases", ".env"), "x")
	if err := os.MkdirAll(filepath.Join(project, "testcases", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 运行残留也不能带过去
	write(t, filepath.Join(project, "results", "20260101000000", "summary.json"), "{}")

	ws, err := NewWorkspace(filepath.Join(t.TempDir(), "run1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.CopyFrom(project); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(ws.Root(), ".env")); err != nil {
		t.Error(".env 未复制")
	}
	for _, name := range []string{"a.yaml", "b.yml"} {
		if _, err := os.Stat(filepath.Join(ws.TestcasesDir(), name)); err != nil {
			t.Errorf("%s 未复制", name)
		}
	}
	// 引擎的白名单只有 .yml/.yaml/.json（实测 F5）
	for _, name := range []string{"note.txt", ".env"} {
		if _, err := os.Stat(filepath.Join(ws.TestcasesDir(), name)); err == nil {
			t.Errorf("不该复制 %s", name)
		}
	}
	// 上一批的运行产物绝不能带进新工作区（隔离的意义就在这里）
	if _, err := os.Stat(ws.ResultsDir()); err == nil {
		t.Error("不该复制 results/")
	}
}

func TestWorkspace_CopyFrom拒绝同目录(t *testing.T) {
	// 项目工作区 == 运行工作区 ⇒ 运行产物会互相覆盖（实测 A5）
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "testcases"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.CopyFrom(dir); err == nil {
		t.Fatal("项目工作区与运行工作区相同时必须报错")
	}
}

func TestWorkspace_PrepareForCase清空results(t *testing.T) {
	ws, err := NewWorkspace(filepath.Join(t.TempDir(), "run1"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws.ResultsDir(), "20260101000000", "summary.json"), "{}")

	if err := ws.PrepareForCase(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.ResultsDir()); err == nil {
		t.Error("results/ 应被清空，否则无法判断产物归属（时间戳只到秒）")
	}
}

// ---------------------------------------------------------------------------
// 产物收集
// ---------------------------------------------------------------------------

func TestCollectArtifacts_搬运summary与report(t *testing.T) {
	base := t.TempDir()
	ws, err := NewWorkspace(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(base, "art", "tc_1")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(ws.ResultsDir(), "20260101120000", "summary.json"), `{"success":true}`)
	write(t, filepath.Join(ws.ResultsDir(), "20260101120000", "report.html"), "<html></html>")

	out := &Outcome{}
	collectArtifacts(Options{ArtifactsDir: art}, ws.ResultsDir(), out)

	if string(out.SummaryJSON) != `{"success":true}` {
		t.Errorf("summary.json 未取到: %q", out.SummaryJSON)
	}
	if out.SummaryPath == "" || out.ReportPath == "" {
		t.Fatalf("产物路径未记录: %+v", out)
	}
	for _, p := range []string{out.SummaryPath, out.ReportPath} {
		if !strings.HasPrefix(p, art) {
			t.Errorf("产物应落在本次运行的产物目录: %q", p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("产物文件不存在: %q", p)
		}
	}
}

func TestCollectArtifacts_无产物时静默(t *testing.T) {
	base := t.TempDir()
	ws, _ := NewWorkspace(filepath.Join(base, "run"))
	art := filepath.Join(base, "art")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}

	// 断言失败时引擎完全不产出产物（实测 F8）——不能因此报错
	out := &Outcome{}
	collectArtifacts(Options{ArtifactsDir: art}, ws.ResultsDir(), out)

	if out.SummaryPath != "" || out.ReportPath != "" {
		t.Errorf("无产物时不应记录路径: %+v", out)
	}
	if len(out.SummaryJSON) != 0 {
		t.Error("无产物时 summary 应为空")
	}
}

func TestLatestResultsDir_取最近修改(t *testing.T) {
	dir := t.TempDir()
	if latestResultsDir(dir) != "" {
		t.Error("空目录应返回空串")
	}

	old := filepath.Join(dir, "20260101000000")
	write(t, filepath.Join(old, "summary.json"), "{}")
	// 把 old 的时间戳改早
	ts := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, ts, ts); err != nil {
		t.Fatal(err)
	}

	newer := filepath.Join(dir, "20260101000001")
	write(t, filepath.Join(newer, "summary.json"), "{}")

	if got := latestResultsDir(dir); got != newer {
		t.Errorf("应取最近修改的目录，got=%q want=%q", got, newer)
	}

	// 非目录条目应被忽略
	write(t, filepath.Join(dir, "stray.txt"), "x")
	if got := latestResultsDir(dir); got != newer {
		t.Errorf("文件条目不应参与比较: %q", got)
	}
}

// ---------------------------------------------------------------------------
// RunCase
// ---------------------------------------------------------------------------

func TestRunCase_二进制不存在(t *testing.T) {
	ws, err := NewWorkspace(filepath.Join(t.TempDir(), "run"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RunCase(context.Background(), Options{
		BinaryPath:   filepath.Join(t.TempDir(), "no-such-binary"),
		WorkspaceDir: ws.Root(),
		CaseFile:     "testcases/a.yaml",
	})
	if err != nil {
		t.Fatalf("启动失败应写在 Outcome 里而不是返回 error: %v", err)
	}
	if out.StartErr == nil {
		t.Fatal("应记录 StartErr")
	}
}

func TestRunCase_缺少必填参数直接报错(t *testing.T) {
	_, err := RunCase(context.Background(), Options{WorkspaceDir: t.TempDir()})
	if err == nil {
		t.Fatal("缺少二进制路径应返回 error")
	}

	_, err = RunCase(context.Background(), Options{BinaryPath: "x", WorkspaceDir: filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatal("工作区不存在应返回 error")
	}
}

func TestRunCase_双流分离且产物被收走(t *testing.T) {
	py := requirePython(t)
	base := t.TempDir()
	ws, err := NewWorkspace(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(base, "art")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}

	// 假引擎：往两条流各写一行（内容不同，用来验证没有混流），
	// 并按 hrp 的约定在 cwd 下产出 results/<ts>/summary.json 与 report.html。
	script := strings.Join([]string{
		`import os`,
		`print("STDOUT-ONLY")`,
		`print('{"level":"info","message":"run testcase start"}', file=__import__("sys").stderr)`,
		`d = os.path.join("results", "20260101120000")`,
		`os.makedirs(d, exist_ok=True)`,
		`open(os.path.join(d, "summary.json"), "w").write('{"success":true}')`,
		`open(os.path.join(d, "report.html"), "w").write("<html>r</html>")`,
	}, "; ")

	out, err := RunCase(context.Background(), Options{
		BinaryPath:   py,
		WorkspaceDir: ws.Root(),
		CaseFile:     "testcases/a.yaml",
		ArtifactsDir: art,
		Timeout:      30 * time.Second,
		debugArgs:    []string{"-c", script},
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.StartErr != nil {
		t.Fatalf("启动失败: %v", out.StartErr)
	}
	if out.ExitCode != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", out.ExitCode, out.Stderr)
	}
	// 两条流必须各自独立，不能混流
	if !strings.Contains(out.Stdout, "STDOUT-ONLY") {
		t.Errorf("stdout 未捕获: %q", out.Stdout)
	}
	if strings.Contains(out.Stderr, "STDOUT-ONLY") {
		t.Fatalf("两条流被合并了（解析会整体失效）: stderr=%q", out.Stderr)
	}
	if !strings.Contains(out.Stderr, "run testcase start") {
		t.Errorf("stderr 未捕获: %q", out.Stderr)
	}

	// 产物必须已经被搬进本次运行的产物目录，而不是留在工作区的 results/ 里
	if string(out.SummaryJSON) != `{"success":true}` {
		t.Errorf("summary.json 未取到: %q", out.SummaryJSON)
	}
	if out.ReportPath == "" || out.SummaryPath == "" {
		t.Fatalf("产物路径缺失: %+v", out)
	}
	for _, p := range []string{out.StdoutPath, out.StderrPath, out.SummaryPath, out.ReportPath} {
		if !strings.HasPrefix(p, art) {
			t.Errorf("产物应在本次运行目录下: %q", p)
		}
	}
	if !out.TimedOut && !out.Canceled {
		if out.DurationMs <= 0 {
			t.Error("耗时应为正数")
		}
	}
}

func TestRunCase_超时会强杀整棵进程树(t *testing.T) {
	// 这是 C7 的核心验证。
	//
	// 实测 A6：`--case-timeout` 不覆盖插件准备阶段，hrp 曾挂起超过 2 分钟；
	// 而 shell 的 `timeout` 命令**杀不掉 hrp 派生出的 Windows 子进程**。
	// 因此平台必须自己按进程树强杀，并且不能等引擎自己退出。
	py := requirePython(t)
	base := t.TempDir()
	ws, err := NewWorkspace(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "SLEPT-TO-COMPLETION")
	art := filepath.Join(base, "art")

	// 假引擎：先输出一行（验证部分输出不丢），再睡 60 秒，
	// 睡醒后写一个标记文件 —— 只要这个文件不存在，就说明进程确实被杀了。
	//
	// flush=True 是必须的：Python 在非终端环境下默认块缓冲，
	// 不 flush 的话那一行会留在它自己的缓冲区里，进程被杀就丢了。
	// （真实 hrp 写 stdout 是直接 write 系统调用，没有这个问题。）
	script := strings.Join([]string{
		`import time, sys, os`,
		`print("BEFORE-SLEEP", flush=True)`,
		`print('{"level":"info","message":"run step start"}', file=sys.stderr, flush=True)`,
		`time.sleep(60)`,
		`open(r"` + marker + `", "w").write("done")`,
	}, "; ")

	timeout := 1200 * time.Millisecond
	start := time.Now()
	out, err := RunCase(context.Background(), Options{
		BinaryPath:   py,
		WorkspaceDir: ws.Root(),
		CaseFile:     "testcases/a.yaml",
		ArtifactsDir: art,
		Timeout:      timeout,
		debugArgs:    []string{"-c", script},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if !out.TimedOut {
		t.Fatalf("应标记 TimedOut，实际: %+v", out)
	}

	// 必须在"超时 + 宽限期"附近返回，而不是傻等 60 秒。
	//
	// 宽限期的意义：平台给引擎传了同样的 --case-timeout，
	// 正常情况下引擎会自己超时退出（退出码 39）并留下产物；
	// 只有引擎彻底卡死时，平台才在 timeout+killGrace 处动手强杀。
	deadline := timeout + killGrace + 5*time.Second
	if elapsed > deadline {
		t.Fatalf("强杀未生效：耗时 %v 超过预期上限 %v（进程没被杀掉）", elapsed, deadline)
	}
	if elapsed < timeout {
		t.Fatalf("耗时 %v 小于设定的超时 %v，说明超时被提前触发了", elapsed, timeout)
	}

	// 被强杀的进程不可能写完标记文件
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("子进程跑完了 sleep —— 说明没有被杀掉（进程树强杀失效）")
	}

	// 被杀之前产生的输出必须保留下来（排查问题全靠它）
	if !strings.Contains(out.Stdout, "BEFORE-SLEEP") {
		t.Errorf("超时前的 stdout 丢失: %q", out.Stdout)
	}
	if !strings.Contains(out.Stderr, "run step start") {
		t.Errorf("超时前的 stderr 丢失: %q", out.Stderr)
	}
}

func TestRunCase_取消(t *testing.T) {
	py := requirePython(t)
	ws, _ := NewWorkspace(filepath.Join(t.TempDir(), "run"))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(800 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	out, err := RunCase(ctx, Options{
		BinaryPath:   py,
		WorkspaceDir: ws.Root(),
		CaseFile:     "testcases/a.yaml",
		Timeout:      120 * time.Second, // 超时远大于取消，确保是"取消"触发
		debugArgs:    []string{"-c", "import time; time.sleep(60)"},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if !out.Canceled {
		t.Fatalf("应标记 Canceled，实际: %+v", out)
	}
	if out.TimedOut {
		t.Error("取消不应同时标记超时")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("取消未生效，耗时 %v", elapsed)
	}
}

func TestRunCase_执行前清空results(t *testing.T) {
	py := requirePython(t)
	base := t.TempDir()
	ws, _ := NewWorkspace(filepath.Join(base, "run"))

	// 预置一个上一批运行的残留产物
	write(t, filepath.Join(ws.ResultsDir(), "20200101000000", "summary.json"), `{"stale":true}`)

	out, err := RunCase(context.Background(), Options{
		BinaryPath:   py,
		WorkspaceDir: ws.Root(),
		CaseFile:     "testcases/a.yaml",
		Timeout:      30 * time.Second,
		debugArgs:    []string{"-c", "print('ok')"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 假引擎不产出产物，所以 summary 必须为空 —— 不能把上一批的残留当成本次结果
	if len(out.SummaryJSON) != 0 {
		t.Fatalf("把上一批的残留产物当成了本次结果: %q（时间戳只到秒，会真的发生）", out.SummaryJSON)
	}
}

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func requirePython(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python")
	if err != nil {
		py, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("环境缺少 python，跳过需要假引擎的用例")
	}
	return py
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCaseEnv_遥测开关必须真的注入到子进程环境里 锁住实测 A20 的修法。
//
// 这条测试存在的理由：环境变量是「传错了也不报错」的典型 ——
// 拼错变量名、值写成 "1" / "TRUE"、或者忘了传，引擎都会继续跑，
// 只是悄悄多花 5 秒并往 stderr 里塞一条 error。
// 只有显式断言才拦得住。
func TestCaseEnv_遥测开关必须真的注入到子进程环境里(t *testing.T) {
	// 开启关闭遥测（平台默认）
	env := caseEnv(Options{DisableTelemetry: true})
	if !contains(env, "DISABLE_GA=true") {
		t.Error("缺少 DISABLE_GA=true：引擎做的是字符串相等比较，值必须是字面量 true")
	}
	if !contains(env, "DISABLE_SENTRY=true") {
		t.Error("缺少 DISABLE_SENTRY=true")
	}
	// NO_COLOR 与遥测无关，任何时候都要有（否则 stderr 会被 ANSI 颜色码污染）
	if !contains(env, "NO_COLOR=1") {
		t.Error("缺少 NO_COLOR=1，JSON Lines 解析会被颜色码打乱")
	}

	// 未开启时不能塞进去：用户若显式打开遥测，平台不该背着他改
	open := caseEnv(Options{DisableTelemetry: false})
	if contains(open, "DISABLE_GA=true") {
		t.Error("DisableTelemetry=false 时不应注入 DISABLE_GA")
	}
	if !contains(open, "NO_COLOR=1") {
		t.Error("NO_COLOR 与遥测开关无关，必须始终存在")
	}

	// 必须继承父进程环境（PATH 之类），不能是干净的数组
	if len(env) <= 3 {
		t.Errorf("子进程环境应继承父进程变量，实际只有 %d 条", len(env))
	}
}

// TestSetEnv_必须覆盖而不是追加 锁住一个 Windows 特有的坑。
//
// 环境里已有同名键时直接 append 会留下两份；Windows 的
// GetEnvironmentVariable 取第一个匹配项，于是平台注入的值会被旧值顶掉 ——
// 表现是"配置里明明关了遥测，却还是慢 3 秒"，而且完全不报错。
func TestSetEnv_必须覆盖而不是追加(t *testing.T) {
	env := []string{"PATH=/usr/bin", "DISABLE_GA=false", "HOME=/root", "DISABLE_GA=true"}
	got := setEnv(env, "DISABLE_GA", "true")

	n := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "DISABLE_GA=") {
			n++
			if kv != "DISABLE_GA=true" {
				t.Errorf("残留了旧值: %q", kv)
			}
		}
	}
	if n != 1 {
		t.Fatalf("DISABLE_GA 应恰好出现一次，实际 %d 次: %v", n, got)
	}
	// 其它变量必须原样保留
	for _, want := range []string{"PATH=/usr/bin", "HOME=/root"} {
		if !contains(got, want) {
			t.Errorf("丢失了无关变量 %s: %v", want, got)
		}
	}
}
