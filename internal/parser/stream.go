package parser

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// stderr：引擎日志（JSON Lines）
// ---------------------------------------------------------------------------

// 引擎日志的事件名。
//
// 这些字符串是**从实测输出中逐字抄录**的，不是推断：
//
//	run testcase start / run testcase end
//	run step start / run step end
//	validate <checkExpr>
//
// 实测 F1 的额外结论：`validate` 行**不带 step 字段**，
// 只能靠「出现在 run step start 与 run step end 之间」来归属步骤。
const (
	evTestCaseStart = "run testcase start"
	evTestCaseEnd   = "run testcase end"
	evStepStart     = "run step start"
	evStepEnd       = "run step end"
	evValidatePfx   = "validate "
)

// logLine 是引擎单行 JSON 日志。
//
// 字段名逐字对齐实测输出（含带括号的 "elapsed(ms)"）。
// 全部使用指针或零值可判别类型，避免"字段缺失"与"零值"混淆。
type logLine struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Time    string `json:"time"`

	Testcase string `json:"testcase,omitempty"`
	Step     string `json:"step,omitempty"`
	Type     string `json:"type,omitempty"`

	Success   *bool          `json:"success,omitempty"`
	ElapsedMs *int64         `json:"elapsed(ms),omitempty"`
	ExportVar map[string]any `json:"exportVars,omitempty"`

	CheckExpr       string `json:"checkExpr,omitempty"`
	AssertMethod    string `json:"assertMethod,omitempty"`
	ExpectValue     any    `json:"expectValue,omitempty"`
	ExpectValueType string `json:"expectValueType,omitempty"`
	CheckValue      any    `json:"checkValue,omitempty"`
	CheckValueType  string `json:"checkValueType,omitempty"`
	Result          *bool  `json:"result,omitempty"`

	Error string `json:"error,omitempty"`
	Path  string `json:"path,omitempty"`
	Count *int   `json:"count,omitempty"`
}

// stepEvent 是一个步骤在引擎日志中的轨迹。
type stepEvent struct {
	Name      string
	Type      string // 形如 request-GET
	Started   bool
	Ended     bool
	Success   bool
	ElapsedMs int64
	Export    map[string]any
	Error     string
	// Validations 是出现在该步骤 start 与 end 之间的 validate 行（按出现顺序）。
	Validations []logLine
}

// parseStderr 逐个 JSON 行扫描 stderr，抽取用例与步骤事件。
//
// ⚠️ 必须**逐行 try-parse，解析失败即丢弃**。
// 引擎在 panic 时会在 stderr 尾部追加数百行 Go 栈回溯，
// 任何"取尾部若干行"的做法都会拿到垃圾数据。
func parseStderr(stderr string) (caseName string, steps []*stepEvent, testcaseStarted, testcaseEnded bool) {
	var cur *stepEvent

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			// panic 栈、Error: 前缀行、空行等一律跳过。
			continue
		}
		var l logLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			// 容忍：引擎可能在未来版本加入非 JSON 的诊断行。
			continue
		}

		switch {
		case l.Message == evTestCaseStart:
			testcaseStarted = true
			if l.Testcase != "" {
				caseName = l.Testcase
			}
		case l.Message == evTestCaseEnd:
			testcaseEnded = true
		case l.Message == evStepStart:
			cur = &stepEvent{Name: l.Step, Type: l.Type, Started: true}
			steps = append(steps, cur)
		case l.Message == evStepEnd:
			if cur == nil {
				// 理论上不会发生；保持防御以免整次解析失败。
				cur = &stepEvent{Name: l.Step, Type: l.Type, Started: true}
				steps = append(steps, cur)
			}
			cur.Ended = true
			if l.Success != nil {
				cur.Success = *l.Success
			}
			if l.ElapsedMs != nil {
				cur.ElapsedMs = *l.ElapsedMs
			}
			cur.Export = l.ExportVar
			cur.Error = l.Error
			cur = nil
		case strings.HasPrefix(l.Message, evValidatePfx):
			if cur != nil {
				cur.Validations = append(cur.Validations, l)
			}
		}
	}

	// 实测：panic 时 stderr 停在 `run step start`，不会有配对的 `run step end`。
	// 因此 `cur != nil` 残留即为"启动后未结束"的步骤。
	return caseName, steps, testcaseStarted, testcaseEnded
}

// ---------------------------------------------------------------------------
// stdout：请求/响应明细（纯文本）
// ---------------------------------------------------------------------------

var (
	reReqHeader = regexp.MustCompile(`^-{10,}\s*request\s*-{10,}$`)
	reResHeader = regexp.MustCompile(`^={10,}\s*response\s*={10,}$`)
	reBlockEnd  = regexp.MustCompile(`^-{20,}$`)
	reStatusLn  = regexp.MustCompile(`^(HTTP/\d(?:\.\d)?)\s+(\d{3})\s*(.*)$`)
	reReqLine   = regexp.MustCompile(`^([A-Z]+)\s+(\S+)\s+(HTTP/\S+)$`)

	// reExitLine 匹配引擎在 stdout 末尾打印的 `hrp exit N`。
	//
	// ⚠️ 它必须当作**块终止符**处理。
	// 实测：请求失败时 stdout 只有 request 块、没有 response 块、也没有结尾分隔线，
	// 于是这一行会被并进请求体 —— 用户看到的"请求体"就变成了 `hrp exit 1`。
	reExitLine = regexp.MustCompile(`^hrp exit \d+$`)
)

// stdoutPair 是 stdout 中解析出的一对报文快照。
type stdoutPair struct {
	Request  *RequestSnapshot
	Response *ResponseSnapshot
}

// parseStdout 按分隔符把 stdout 切成若干报文块。
//
// 实测格式（分隔符为固定长度的字符行）：
//
//	-------------------- request --------------------
//	GET /get/ HTTP/1.1
//	Host: 127.0.0.1:8899
//	<空行>
//	<body>
//	==================== response ====================
//	Connected via plaintext
//	HTTP/1.1 200 OK
//	<响应头>
//	<空行>
//	<body>
//	--------------------------------------------------
//
// 请求失败时只打印 request 块，没有 response 块也没有结尾分隔线。
func parseStdout(stdout string) []stdoutPair {
	lines := strings.Split(stdout, "\n")
	var pairs []stdoutPair

	const (
		stNone = iota
		stReq
		stRes
	)
	state := stNone

	var cur stdoutPair
	var buf []string

	flushReq := func() {
		if len(buf) > 0 {
			cur.Request = parseRequestBlock(buf)
			buf = nil
		}
	}
	flushRes := func() {
		if len(buf) > 0 {
			cur.Response = parseResponseBlock(buf)
			buf = nil
		}
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case reReqHeader.MatchString(trimmed):
			// 新块开始：上一次的 response 若还没落，先收尾。
			flushRes()
			if cur.Request != nil || cur.Response != nil {
				pairs = append(pairs, cur)
				cur = stdoutPair{}
			}
			state = stReq
			buf = nil
		case reResHeader.MatchString(trimmed):
			flushReq()
			state = stRes
			buf = nil
		case reBlockEnd.MatchString(trimmed) && state == stRes:
			flushRes()
			pairs = append(pairs, cur)
			cur = stdoutPair{}
			state = stNone
		case reExitLine.MatchString(trimmed):
			// `hrp exit N` 是 stdout 的最后一行，不属于任何报文块。
			// 请求失败时它紧跟请求块出现，必须在这里切断，否则会变成请求体。
			flushReq()
			flushRes()
			if cur.Request != nil || cur.Response != nil {
				pairs = append(pairs, cur)
				cur = stdoutPair{}
			}
			state = stNone
		default:
			if state != stNone {
				buf = append(buf, line)
			}
		}
	}

	// 收尾：请求失败时没有结尾分隔线。
	flushReq()
	flushRes()
	if cur.Request != nil || cur.Response != nil {
		pairs = append(pairs, cur)
	}
	return pairs
}

// parseRequestBlock 解析请求块。
func parseRequestBlock(lines []string) *RequestSnapshot {
	snap := &RequestSnapshot{Headers: map[string]string{}}
	idx := 0

	for ; idx < len(lines); idx++ {
		if m := reReqLine.FindStringSubmatch(strings.TrimSpace(lines[idx])); m != nil {
			snap.Method, snap.Path, snap.Proto = m[1], m[2], m[3]
			idx++
			break
		}
		if strings.TrimSpace(lines[idx]) != "" {
			// 首行不是请求行：可能是引擎改版，放弃结构化但仍保留原文。
			return &RequestSnapshot{Body: strings.Join(lines, "\n")}
		}
	}

	idx = parseHeaderLines(lines, idx, snap.Headers)
	// Host 头是重建最终 URL 的关键（实测 F9：引擎会自动补尾斜杠，
	// 只有请求行里的 path 才是真实发出的路径，必须配 Host 才能拼出完整地址）。
	// 这里不依赖"后面还有 body"这个条件，以免请求无 body 时拿不到 Host。
	if h, ok := snap.Headers["Host"]; ok {
		snap.Host = h
	}
	if idx < len(lines) {
		snap.Body = strings.TrimSpace(strings.Join(lines[idx:], "\n"))
	}
	return snap
}

// parseResponseBlock 解析响应块。
func parseResponseBlock(lines []string) *ResponseSnapshot {
	snap := &ResponseSnapshot{Headers: map[string]string{}}

	idx := 0
	// 状态行之前可能有一行传输层信息（如 "Connected via plaintext"）。
	var transport []string
	for ; idx < len(lines); idx++ {
		t := strings.TrimSpace(lines[idx])
		if m := reStatusLn.FindStringSubmatch(t); m != nil {
			snap.Proto = m[1]
			snap.StatusText = strings.TrimSpace(m[3])
			if n, err := strconv.Atoi(m[2]); err == nil {
				snap.StatusCode = n
			}
			idx++
			break
		}
		if t != "" {
			transport = append(transport, t)
		}
	}
	snap.Transport = strings.Join(transport, " ")

	idx = parseHeaderLines(lines, idx, snap.Headers)
	if idx < len(lines) {
		snap.Body = strings.TrimSpace(strings.Join(lines[idx:], "\n"))
	}
	return snap
}

// parseHeaderLines 从 idx 起解析 "Key: Value" 头，直到空行。
// 返回 body 起始下标。
func parseHeaderLines(lines []string, idx int, dst map[string]string) int {
	for ; idx < len(lines); idx++ {
		t := strings.TrimSpace(lines[idx])
		if t == "" {
			return idx + 1
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			// 不是头行，视为 body 起始。
			return idx
		}
		dst[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return idx
}
