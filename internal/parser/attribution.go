package parser

import (
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// ---------------------------------------------------------------------------
// 引擎退出码
//
// ⚠️ 这些常量是**逐码抄录**自官方源码 hrp/internal/code/code.go（v4.3.6），
// 不是按区间猜测的。之所以要逐码，是因为猜想区间会犯实质错误 ——
// 例如 38/39 分别住在 runner 区间里，但它们其实是
// InterruptError（中断）与 TimeoutError（超时），
// 按"runner 区间 = 执行机问题"一刀切就会把两者都误判成 ops。
//
// 同一份源码还解释了一个关键现象：
//
//	func GetErrorCode(err error) (errCode int) {
//	    if err == nil {
//	        return Success          // ← 提前 return，**跳过了下面的打印**
//	    }
//	    ...
//	    fmt.Printf("hrp exit %d\n", errCode)
//	    return
//	}
//
// 因此 `hrp exit N` **只在退出码非 0 时出现**。
// 用「stdout 是否含 hrp exit」判断"是否正常退出"会把成功判成异常，
// 正确用法见 Result.CleanExit 的赋值处。
// ---------------------------------------------------------------------------
const (
	exitSuccess     = 0
	exitGeneralFail = 1

	// environment [2, 10)：源码里**只定义了 9**，2-8 是预留但未分配。
	//
	// ⚠️ 这澄清了一个关键事实：**退出码 2 不是"环境错误码"，而是 Go panic 的固定退出码。**
	// 断言失败会 panic（实测 F8），于是 2 出现在这里 —— 纯属 runtime 行为，
	// 不是引擎的分类契约。
	//
	// 因此对"exit=2 且 stderr 无 panic"这种情形，平台**不猜测**：
	// 归为 unknown，提示用户查看原始日志。宁可不判，也不要把
	// 被测系统的问题错判成执行机的问题。
	exitInvalidPyVenv = 9

	// loader [10, 20)
	exitLoadFileMin = 10 // LoadFileError

	// parser [20, 30)
	exitParseErrorMin = 20
	exitParseErrorMax = 24

	// runner [30, 40)
	exitInitPluginFailed = 31
	exitBuildGoPlugin    = 32
	exitBuildPyPlugin    = 33
	exitInterrupted      = 38
	exitEngineTimeout    = 39

	// 移动端 / CV 区间起点
	exitMobileMin = 50
	exitMobileMax = 90
)

// 请求失败时的错误文本特征（实测 F13）。
//
// ⚠️ 这是本轮实测最重要的修正：**请求级超时与连接失败同为退出码 1**
// （`GeneralFail` 是引擎的"兜底码"，任何未预定义的错误都落在这里），
// 只看退出码无法区分，必须下沉到错误文本。
//
// 对照样本（两条都是 exit 1）：
//
//	超时     context deadline exceeded (Client.Timeout exceeded while awaiting headers)
//	连接失败 dial tcp 127.0.0.1:9999: connectex: No connection could be made ...
var timeoutMarkers = []string{
	"context deadline exceeded",
	"client.timeout exceeded",
	"i/o timeout",
	"timeout awaiting response headers",
	"tls handshake timeout",
}

// environmentMarkers 是网络/环境类错误的特征。
//
// ⚠️ 顺序敏感：必须先判 timeout 再判环境。
// `tls handshake timeout` 同时含 "timeout" 与环境特征，
// 顺序颠倒就会被误判成环境错误。
var environmentMarkers = []string{
	"dial tcp",
	"connectex",
	"connection refused",
	"connection reset",
	"no such host",
	"no route to host",
	"server misbehaving",
	"certificate",
	"tls:",
	"eof",
}

// ClassifyInput 是归因判别的输入。
type ClassifyInput struct {
	ExitCode int
	// Panic 表示 stderr 中出现了 `panic:`。
	Panic bool
	// TimedOut / Canceled 由执行器标记，优先级最高。
	TimedOut bool
	Canceled bool
	// ErrorText 是 stderr 中最后一条 error 级日志的 error 字段。
	//
	// 用途：区分同为 exit=1 的「请求超时」与「连接失败」（实测 F13）。
	// 允许为空；为空时退化为"只要 exit=1 就按环境错误处理"。
	ErrorText string
	// CaseStarted 表示引擎日志里确实出现过 `run testcase start`。
	//
	// ⚠️ **调用方必须显式赋值**。它存在的原因见下面的"假绿"分支：
	// 退出码为 0 不代表用例跑过。
	//
	// 这里刻意用正向命名（而不是 `CaseMissing`）：反向命名的零值恰好是
	// "正常"，一旦哪里忘记赋值，假绿检测就会被静默跳过 ——
	// 而假绿是本系统最不能漏的一类问题。正向命名的零值会直接让测试变红。
	CaseStarted bool
}

// Classify 判定失败归因。
//
// **判别顺序不可调换**（详见 docs/数据库设计.md 第 4 节）：
//
//	平台侧判定（取消/超时） > 成功 > panic > 退出码逐码映射
//
// panic 必须排在退出码之前：断言失败会触发 Go panic，退出码固定为 2，
// 而 2 恰好落在引擎自己的「環境」区间里 —— 那是 Go runtime 的固定行为，
// 不是引擎的设计契约。只看退出码必然把"被测系统的锅"算成"环境的锅"。
func Classify(in ClassifyInput) string {
	// 平台侧原因优先：被终止/超时是平台的判定，不是引擎的。
	if in.Canceled {
		return model.AttrCanceled
	}
	if in.TimedOut {
		return model.AttrTimeout
	}

	// ⭐ 假绿优先于 exit=0（实测 F5）。
	//
	// 畸形用例文件会被引擎**静默丢弃**：不执行、不报错、不告警，退出码 0。
	// 若按 exit=0 判"通过"，用户会得到一份绿色的假象，而实际上
	// 一个请求都没发出去 —— 这比"报错"危险得多，也是最容易失去信任的场景。
	if in.ExitCode == exitSuccess && !in.CaseStarted {
		return model.AttrCaseIssue
	}

	if in.ExitCode == exitSuccess {
		return model.AttrPass
	}

	// ⭐ 第一判据：panic ⇒ 断言失败 ⇒ 被测系统
	// 引擎在断言失败时会 panic（实测 F8），这是实测确认的唯一识别方式。
	if in.Panic {
		return model.AttrSystemUnderTest
	}

	switch {
	case in.ExitCode == exitGeneralFail:
		// 引擎的兜底码：任何未预定义错误都落在这里，
		// 至少包含「请求超时」与「连接失败」两类完全不同的故障，
		// 只能靠错误文本分流（实测 F13）。
		return classifyRequestFailure(in.ErrorText)

	case in.ExitCode == exitInvalidPyVenv,
		in.ExitCode == exitInitPluginFailed,
		in.ExitCode == exitBuildGoPlugin,
		in.ExitCode == exitBuildPyPlugin:
		// Python venv 未就绪 / 插件初始化或构建失败 —— 执行机环境问题
		return model.AttrOps

	case in.ExitCode == exitInterrupted:
		// 38 InterruptError：引擎自己收到了中断信号
		return model.AttrCanceled

	case in.ExitCode == exitEngineTimeout:
		// 39 TimeoutError：引擎自身的超时机制，指"用例跑太久"
		return model.AttrTimeout

	case in.ExitCode >= exitLoadFileMin && in.ExitCode < exitParseErrorMin:
		// 10-18：加载期问题 —— 文件/JSON/YAML/.env/CSV 格式、
		// 用例格式、扩展名、引用文件缺失、插件文件非法。
		// 全部属于"用例或其引用材料有问题"，用户改文件即可解决。
		return model.AttrCaseIssue

	case in.ExitCode >= exitParseErrorMin && in.ExitCode <= exitParseErrorMax:
		// 20-24：解析期问题 —— 解析失败、变量未定义、函数解析或调用失败
		return model.AttrCaseIssue

	case in.ExitCode >= exitMobileMin && in.ExitCode < exitMobileMax:
		// 50-85：iOS / Android / UI 自动化 / CV，本平台不加载这类用例。
		// 归为用例问题而不是"未知"，是为了给出可执行的下一步：
		// 用户应该知道"这条用例用到了平台不支持的能力"。
		return model.AttrCaseIssue

	default:
		return model.AttrUnknown
	}
}

// classifyRequestFailure 在 exit=1（兜底码）内做二级分流。
//
// 判别顺序：timeout → environment → 兜底 environment。
// 兜底的取舍：exit=1 的绝大多数成因是"请求没能成功发出或收到"，
// 归到环境比归到未知对用户更有指导性；且平台会同时展示原始错误文本，
// 用户不会被一个不精确的标签误导太久。
func classifyRequestFailure(errText string) string {
	if errText == "" {
		return model.AttrEnvironment
	}
	low := strings.ToLower(errText)

	for _, m := range timeoutMarkers {
		if strings.Contains(low, m) {
			return model.AttrTimeout
		}
	}
	for _, m := range environmentMarkers {
		if strings.Contains(low, m) {
			return model.AttrEnvironment
		}
	}
	return model.AttrEnvironment
}

// StatusFromAttribution 把归因映射到用例结果状态。
//
// 语义区分：
//   - pass                                  → 通过
//   - system_under_test                     → 失败（用例跑完了，是被测行为不符预期）
//   - 其余（环境/用例/运维/超时/取消/未知）  → 错误（用例根本没跑完，属平台侧问题）
//
// 这个区分直接对应方案 13.2 的核心洞察：把"谁的问题"告诉用户。
func StatusFromAttribution(attr string) string {
	switch attr {
	case model.AttrPass:
		return model.StatusPass
	case model.AttrSystemUnderTest:
		return model.StatusFail
	default:
		return model.StatusError
	}
}

// StepStatus 判定**单个步骤**的结果状态。
//
// 判据来自实测 F8 的一个推论，值得单独说明：
//
//	断言失败 → panic → 引擎停在 `run step start`，**不会有配对的 `run step end`**。
//
// 换句话说，一个**带 `run step end` 且 success=false 的步骤，其失败原因
// 一定不是断言不通过**（否则它根本走不到 end），而是请求失败、变量未定义、
// hook 抛错之类的"步骤没能正常跑完"。
//
// 因此：
//
//	ended && !success  → error（步骤没跑完，平台侧问题）
//	!ended             → fail （走到了断言阶段，是被测行为不符预期）
func StepStatus(ended bool, success bool) string {
	switch {
	case ended && success:
		return model.StatusPass
	case ended:
		return model.StatusError
	default:
		return model.StatusFail
	}
}
