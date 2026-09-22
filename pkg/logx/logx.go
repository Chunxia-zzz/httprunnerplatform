// Package logx 统一平台日志。
//
// 选用 zerolog 而非标准库 log/slog 的原因：**与 hrp 引擎保持一致**。
// hrp 内部即用 zerolog，输出的 JSON 行字段为 level / time / message。
// 平台日志与引擎日志格式一致，才能放进同一个日志视图串联排查
// （引擎日志由 parser 解析后落库，平台日志可直接比对字段）。
package logx

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// 字段名与 hrp 引擎保持一致，便于日志检索时统一按 message 过滤。
const (
	FieldLevel   = "level"
	FieldTime    = "time"
	FieldMessage = "message"
)

var global zerolog.Logger

func init() {
	global = zerolog.New(os.Stdout).With().Timestamp().Logger()
}

// Init 初始化全局 logger。
//
// jsonOut 为 true 时输出 JSON 行（便于采集与 grep），
// 否则输出带颜色的控制台格式（本地开发更易读）。
func Init(level string, jsonOut bool) {
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.TimestampFieldName = FieldTime
	zerolog.LevelFieldName = FieldLevel
	zerolog.MessageFieldName = FieldMessage

	var w io.Writer = os.Stdout
	if !jsonOut {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05"}
	}

	global = zerolog.New(w).Level(parseLevel(level)).With().Timestamp().Logger()
}

// parseLevel 解析日志级别，非法值回退到 info。
func parseLevel(level string) zerolog.Level {
	lvl, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(level)))
	if err != nil {
		return zerolog.InfoLevel
	}
	return lvl
}

// L 返回全局 logger。
func L() *zerolog.Logger { return &global }

// SetOutput 替换输出目标，供测试捕获日志。
func SetOutput(w io.Writer) {
	global = global.Output(w)
}
