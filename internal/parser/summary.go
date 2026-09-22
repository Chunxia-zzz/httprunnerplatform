package parser

import "encoding/json"

// summaryDoc 对应 hrp `-s` 产出的 results/<时间戳>/summary.json。
//
// 字段名逐字对齐实测输出。注意三处容易被写错的地方：
//
//   - stat.testcases 用 total/success/fail，而 stat.teststeps 用 total/successes/failures
//     （引擎自身的不一致，照抄即可）
//   - records[].start_time 是 Unix **秒**
//   - details[].root_dir **恒为空字符串**，结构里没有任何源文件路径，
//     因此无法把结果回填到具体用例文件 —— 这正是平台采用
//     「一个用例一个子进程」的原因（见 docs/引擎实测记录.md A2/F11）
type summaryDoc struct {
	Success  bool            `json:"success"`
	Stat     summaryStat     `json:"stat"`
	Time     summaryTime     `json:"time"`
	Platform PlatformInfo    `json:"platform"`
	Details  []summaryDetail `json:"details"`
}

type summaryStat struct {
	TestCases struct {
		Total   int `json:"total"`
		Success int `json:"success"`
		Fail    int `json:"fail"`
	} `json:"testcases"`
	TestSteps struct {
		Total     int `json:"total"`
		Successes int `json:"successes"`
		Failures  int `json:"failures"`
	} `json:"teststeps"`
}

type summaryTime struct {
	StartAt  string  `json:"start_at"`
	Duration float64 `json:"duration"`
}

type summaryDetail struct {
	Name    string      `json:"name"`
	Success bool        `json:"success"`
	Stat    summaryStat `json:"stat"`
	Time    summaryTime `json:"time"`
	InOut   struct {
		ConfigVars map[string]any `json:"config_vars"`
		ExportVars map[string]any `json:"export_vars"`
	} `json:"in_out"`
	Records []summaryRecord `json:"records"`
	RootDir string          `json:"root_dir"`
}

type summaryRecord struct {
	Name      string `json:"name"`
	StartTime int64  `json:"start_time"`
	StepType  string `json:"step_type"`
	Success   bool   `json:"success"`
	ElapsedMs int64  `json:"elapsed_ms"`
	Data      struct {
		Success  bool `json:"success"`
		ReqResps struct {
			Request struct {
				Headers map[string]string `json:"headers"`
				Method  string            `json:"method"`
				URL     string            `json:"url"`
			} `json:"request"`
			Response struct {
				Body       string            `json:"body"`
				Cookies    map[string]string `json:"cookies"`
				Headers    map[string]string `json:"headers"`
				Proto      string            `json:"proto"`
				StatusCode int               `json:"status_code"`
			} `json:"response"`
		} `json:"req_resps"`
		Validators []struct {
			Check       string `json:"check"`
			Assert      string `json:"assert"`
			Expect      any    `json:"expect"`
			CheckValue  any    `json:"check_value"`
			CheckResult string `json:"check_result"`
			Msg         string `json:"msg"`
		} `json:"validators"`
	} `json:"data"`
	ContentSize int64 `json:"content_size"`

	// Attachments 在**步骤失败时**承载错误文本（实测）。
	//
	// 成功与失败的步骤在 summary.json 里是两种形状，互斥出现：
	//
	//	成功：[data.req_resps + data.validators]   有 data，无 attachments
	//	失败：[attachments: "do request failed: ..."] 无 data，有 attachments
	//
	// 这是失败步骤在 summary 里的**唯一**错误来源，因此必须取用，
	// 否则「连接被拒」这类失败会只剩一个 success=false 而没有任何原因。
	Attachments string `json:"attachments"`
}

// ErrorText 返回该步骤的错误描述（仅失败步骤有）。
func (r summaryRecord) ErrorText() string {
	if r.Success {
		return ""
	}
	return r.Attachments
}

// parseSummary 解析 summary.json。解析失败返回 nil（不是致命错误）。
//
// 防御式：summary.json 的格式没有版本化承诺（方案 R2），
// 任何解析问题都不应让整次执行的解析失败 —— 因为 stdout/stderr 仍在。
func parseSummary(raw []byte) *summaryDoc {
	var doc summaryDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	return &doc
}
