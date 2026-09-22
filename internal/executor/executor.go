// Package executor 负责把「一个用例」变成「一次 hrp 子进程执行」，并收齐产物。
//
// 设计由三条实测结论直接决定：
//
//  1. **必须一个用例一个子进程**（实测 A2）。
//     `hrp run <目录>` 是全有或全无的：一条断言失败 panic 会让后续用例
//     完全不执行、且整批结果丢失。因此平台绝不批量执行。
//
//  2. **必须进程树级别地超时与强杀**（实测 A6）。
//     `--case-timeout` 只管用例执行时长，**不覆盖插件准备阶段**；
//     而 shell 的 `timeout` 杀不掉 hrp 派生出的 Windows 子进程。
//     实测中 hrp 曾挂起超过 2 分钟且无人能停。
//
//  3. **必须每次运行用独立工作区**（实测 A5）。
//     引擎把产物写进 `<cwd>/results/<时间戳>/`，而**时间戳精度只到秒** ——
//     同一秒内的两次运行会复用同一目录互相覆盖。
//
// 因此这里的策略是：
//
//	执行前清空 results/ → 执行 → 立刻把 summary.json / report.html 搬进
//	本次运行专属的产物目录。绝不能等"跑完一批再收产物"。
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout 是单用例的默认超时。
//
// 取值理由：一个正常的接口用例在本地 1~3 秒完成（实测启动开销约 1.4 秒）。
// 300 秒足够覆盖慢接口，又不会让卡死的用例占用执行槽位过久。
const DefaultTimeout = 300 * time.Second

// killGrace 是平台强杀的宽限期。
//
// 我们给引擎的 `--case-timeout` 传**与 timeout 相同的值**，
// 平台自己的硬超时则晚 killGrace 触发。这样正常情况下由引擎自己超时退出
// （退出码 39，日志与产物都在），平台只在引擎彻底卡死时才动手。
const killGrace = 5 * time.Second

// Options 描述一次用例执行。
type Options struct {
	// BinaryPath 是 hrp 可执行文件（由 pkg/hrpclient.Resolve 定位）。
	BinaryPath string
	// WorkspaceDir 是该次运行的独立工作区，同时作为子进程的 cwd。
	//
	// ⚠️ 必须每次运行独立，不能复用（实测 A5：产物按秒级时间戳落盘）。
	WorkspaceDir string
	// CaseFile 是工作区内的相对路径，例如 testcases/tc_login.yaml。
	//
	// 用相对路径而不是绝对路径：引擎以 cwd 为项目根，
	// 相对路径能让日志里出现的路径与实际工作区一致，便于排查。
	CaseFile string
	// Timeout 是单用例超时，0 表示用 DefaultTimeout。
	Timeout time.Duration
	// ArtifactsDir 是本次用例的产物目录（summary.json / report.html / 双流日志）。
	ArtifactsDir string
	// GenHTMLReport 是否产出 report.html。M1 默认开启。
	GenHTMLReport bool
	// DisableRequestsLog 为 true 时加 `--log-requests-off`。
	//
	// 默认**不要关**：断言失败时引擎不产出 summary.json，
	// stdout 的请求/响应明细就成了唯一可用的失败线索（实测 F8）。
	DisableRequestsLog bool
	// DisableTelemetry 为 true 时向引擎进程注入 DISABLE_GA / DISABLE_SENTRY。
	//
	// 这两个变量是 hrp **唯一**的遥测开关，v4.3.6 源码：
	//
	//	hrp/internal/sdk/ga4.go:    if env.DISABLE_GA == "true"     { return }
	//	hrp/internal/sdk/sentry.go: if env.DISABLE_SENTRY == "true" { return }
	//
	// 而且它们是在**包级 var 的初始化阶段**读取的（`env.DISABLE_GA = os.Getenv(...)`），
	// 所以只能在子进程环境里传 —— 进程起来之后再改没有任何作用。
	//
	// 为什么平台必须默认关掉（两笔账）：
	//
	//  1. GA4 的 HTTP client 超时是 5 秒，内网/无外网环境下必然耗满，
	//     直接体现在「用例耗时」里（实测：同一条用例 1.4s → 6.9s）。
	//  2. 上报失败是用 `log.Error()` 打出来的，在 stderr 里与"用例失败"
	//     完全同形，于是**一条成功的用例会挂着一条错误摘要**。
	//     光靠 parser 过滤是治标，源头掐掉才是治本（见实测 A20）。
	DisableTelemetry bool

	// debugArgs 覆盖默认的 hrp 参数列表，**仅供同包测试注入**。
	//
	// 存在的理由：超时强杀是 C7 的核心，必须有一个"会挂住的子进程"才能验证。
	// 而真实的挂起场景需要 hrp + 慢接口，无法在普通单测里低成本构造。
	// 生产代码不应设置这个字段。
	debugArgs []string
}

// Outcome 是一次执行的原始结果。
//
// 这个结构刻意保持"未经加工"：只负责把两条流、退出码和产物原样带回来，
// 归因与结构化交给 parser 包。执行器不解释引擎的行为，只搬运证据。
type Outcome struct {
	ExitCode int
	// Stdout / Stderr 是两条流的完整原文，**未做任何裁剪**。
	// parser 依赖 stderr 尾部的 Go 栈回溯来判定 panic，裁剪会毁掉归因。
	Stdout string
	Stderr string
	// SummaryJSON 是 summary.json 原文；断言失败时为空（实测 F8）。
	SummaryJSON []byte
	// ReportPath 是搬进产物目录后的 report.html 路径，未产出时为空。
	ReportPath string
	// SummaryPath 同理。
	SummaryPath string
	// StdoutPath / StderrPath 是双流落盘后的路径，供"查看原始日志"使用。
	StdoutPath string
	StderrPath string

	DurationMs int64
	// TimedOut 表示被**平台**强杀（引擎自己超时退出时为 false，但退出码是 39）。
	TimedOut bool
	// Canceled 表示被用户取消。
	Canceled bool
	// StartErr 非空表示进程根本没起来（二进制不存在、cwd 不存在等）。
	StartErr error
}

// ErrStart 表示子进程未能启动。
var ErrStart = errors.New("executor: 无法启动引擎进程")

// RunCase 执行单个用例并收齐产物。
//
// ctx 被取消时按"用户取消"处理，并强杀整棵进程树。
// 本函数**不返回 error 表示用例失败** —— 用例失败是正常结果，写在 Outcome 里。
// 只有"平台侧无法完成执行"（进程起不来、产物目录不可写）才返回 error。
func RunCase(ctx context.Context, opt Options) (*Outcome, error) {
	if opt.BinaryPath == "" {
		return nil, fmt.Errorf("%w: 未指定 hrp 二进制路径", ErrStart)
	}
	if opt.WorkspaceDir == "" {
		return nil, fmt.Errorf("%w: 未指定工作区", ErrStart)
	}
	if opt.CaseFile == "" {
		return nil, fmt.Errorf("%w: 未指定用例文件", ErrStart)
	}
	if fi, err := os.Stat(opt.WorkspaceDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: 工作区不可用 %s", ErrStart, opt.WorkspaceDir)
	}

	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	if opt.ArtifactsDir != "" {
		if err := os.MkdirAll(opt.ArtifactsDir, 0o755); err != nil {
			return nil, fmt.Errorf("创建产物目录失败: %w", err)
		}
	}

	// 执行前清空 results/：确保执行后这里最多只有一个时间戳目录，
	// 从而能准确地把「这一次」的产物拿走（时间戳只到秒，见 A5）。
	resultsDir := filepath.Join(opt.WorkspaceDir, "results")
	if err := os.RemoveAll(resultsDir); err != nil {
		return nil, fmt.Errorf("清理工作区 results 目录失败: %w", err)
	}

	args := opt.debugArgs
	if args == nil {
		args = buildArgs(opt, timeout)
	}

	// 平台自己的硬超时比引擎的 --case-timeout 晚一点，
	// 让引擎有机会自己超时退出（退出码 39），那样日志与产物都还在。
	runCtx, cancel := context.WithTimeout(ctx, timeout+killGrace)
	defer cancel()

	cmd := exec.Command(opt.BinaryPath, args...)
	cmd.Dir = opt.WorkspaceDir
	cmd.Env = caseEnv(opt)
	cmd.SysProcAttr = newSysProcAttr()

	// 双流分别捕获：内容完全不同（stderr 是 JSON 日志，stdout 是报文明细），
	// 合并就毁掉解析（实测 F1）。
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Start()
	if runErr != nil {
		return &Outcome{
			StartErr:   fmt.Errorf("%w: %v", ErrStart, runErr),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	// 等待结束，或超时/被取消后强杀。
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var (
		timedOut bool
		canceled bool
	)
	select {
	case runErr = <-waitDone:
	case <-runCtx.Done():
		// 平台侧收口：强杀整棵进程树
		timedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
		canceled = errors.Is(runCtx.Err(), context.Canceled)
		killTree(cmd)
		// 给 Wait 一点时间回收；拿不到也不阻塞整体流程
		select {
		case runErr = <-waitDone:
		case <-time.After(10 * time.Second):
			runErr = fmt.Errorf("进程树已强杀但 Wait 未返回")
		}
	}

	duration := time.Since(start).Milliseconds()
	out := &Outcome{
		ExitCode:   exitCodeOf(cmd, runErr),
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: duration,
		TimedOut:   timedOut,
		Canceled:   canceled,
	}

	// 双流落盘，供「查看原始日志」
	if opt.ArtifactsDir != "" {
		out.StdoutPath = writeIfAny(filepath.Join(opt.ArtifactsDir, "stdout.txt"), out.Stdout)
		out.StderrPath = writeIfAny(filepath.Join(opt.ArtifactsDir, "stderr.txt"), out.Stderr)
	}

	// 收产物：必须立刻搬走（见 A5）
	collectArtifacts(opt, resultsDir, out)

	return out, nil
}

// caseEnv 组装子进程的环境变量。
//
// 拆成独立函数是为了能被单测直接断言：环境变量是"传错了也不会报错"的那种输入，
// 只有显式验证才靠得住（DISABLE_GA 的值必须是小写的 "true"，
// 引擎做的是字符串相等比较 `== "true"`，写成 "1" / "TRUE" 都无效）。
func caseEnv(opt Options) []string {
	// NO_COLOR：让引擎不要在 stderr 里插 ANSI 颜色码，
	// 否则 JSON Lines 解析会被颜色码打乱。
	env := append(os.Environ(), "NO_COLOR=1")
	if opt.DisableTelemetry {
		env = setEnv(env, "DISABLE_GA", "true")
		env = setEnv(env, "DISABLE_SENTRY", "true")
	}
	return env
}

// setEnv 在环境变量数组里覆盖式设置一个键。
//
// 不能直接 append：环境里已经存在同名键时，Windows 上 `GetEnvironmentVariable`
// 取的是**第一个**匹配项，于是"平台注入的值"会被父进程的旧值顶掉 ——
// 表现就是"配置里明明关了遥测，却还是慢 3 秒"。
// 所以先删掉所有同名项再追加。
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return append(out, prefix+value)
}

// buildArgs 组装 hrp run 的参数。
func buildArgs(opt Options, timeout time.Duration) []string {
	args := []string{
		"run", filepath.ToSlash(opt.CaseFile),
		"--log-json",   // 结构化日志，解析器依赖它
		"--save-tests", // 恒定开启：summary.json 是主要的权威数据源
		"--case-timeout", formatTimeoutSeconds(timeout),
	}
	if opt.GenHTMLReport {
		args = append(args, "--gen-html-report")
	}
	if opt.DisableRequestsLog {
		args = append(args, "--log-requests-off")
	}
	return args
}

// formatTimeoutSeconds 把时长格式化成 `--case-timeout` 要的秒数。
//
// 按 32 位精度格式化，因为该标志在 hrp 里就是 float32：
// 若按 64 位格式化，1.2 秒会写成 "1.2000000476837158"，
// 日志里很难看，也容易让人以为平台把超时算错了。
func formatTimeoutSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'g', -1, 32)
}

// writeIfAny 在内容非空时写文件，返回路径。
func writeIfAny(path, content string) string {
	if content == "" {
		return ""
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ""
	}
	return path
}

// collectArtifacts 把 results/<ts>/ 下的产物搬进本次运行的产物目录。
//
// 必须**执行后立刻**做：时间戳精度只到秒，等一批跑完再收，
// 同一秒内的多次运行会互相覆盖（实测 A5）。
func collectArtifacts(opt Options, resultsDir string, out *Outcome) {
	if opt.ArtifactsDir == "" {
		return
	}

	dir := latestResultsDir(resultsDir)
	if dir == "" {
		return
	}

	summary := filepath.Join(dir, "summary.json")
	if b, err := os.ReadFile(summary); err == nil {
		out.SummaryJSON = b
		dst := filepath.Join(opt.ArtifactsDir, "summary.json")
		if err := os.WriteFile(dst, b, 0o644); err == nil {
			out.SummaryPath = dst
		}
	}

	report := filepath.Join(dir, "report.html")
	if _, err := os.Stat(report); err == nil {
		dst := filepath.Join(opt.ArtifactsDir, "report.html")
		if err := copyFile(report, dst); err == nil {
			out.ReportPath = dst
		}
	}
}

// latestResultsDir 返回 results/ 下最近修改的子目录；没有则返回空串。
//
// 正常情况下因为执行前清过目录，这里只会有一个候选。
// 仍然取"最近修改"而不是"唯一"是为了容错：
// 万一 hrp 生成的目录名不是纯时间戳，也不会因此拿不到产物。
func latestResultsDir(resultsDir string) string {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return ""
	}
	best := ""
	var bestMod time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best = filepath.Join(resultsDir, e.Name())
			bestMod = info.ModTime()
		}
	}
	return best
}

// exitCodeOf 取出退出码。
//
// 注意三种情况要区分开：
//   - 正常结束 → cmd.ProcessState.ExitCode()
//   - 被信号杀死（unix）→ ExitCode() 返回 -1
//   - 被平台强杀 → 同样是 -1
//
// 归因不依赖这里的取值是否精确，因为 TimedOut / Canceled 会先于退出码被判别。
func exitCodeOf(cmd *exec.Cmd, waitErr error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if waitErr != nil {
		return -1
	}
	return 0
}
