package logx

import (
	"bytes"
	"strings"
	"testing"
)

// capture 把全局 logger 的输出重定向到 buffer，测试结束后恢复为 JSON 模式。
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	SetOutput(&buf)
	t.Cleanup(func() { Init("info", true) })
	return &buf
}

func TestJSON字段名与引擎一致(t *testing.T) {
	// 字段名必须是 level / time / message —— 与 hrp 引擎的 zerolog 输出
	// 完全一致，才能放进同一个日志视图按 message 串联排查（包注释的约定）。
	Init("info", true)
	buf := capture(t)

	L().Info().Str("k", "v").Msg("hello")
	out := buf.String()
	for _, want := range []string{`"level":"info"`, `"message":"hello"`, `"k":"v"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %s：\n%s", want, out)
		}
	}
}

func TestLevelFilter(t *testing.T) {
	Init("warn", true)
	buf := capture(t)

	L().Debug().Msg("隐藏我")
	L().Info().Msg("也隐藏我")
	L().Warn().Msg("可见")
	L().Error().Msg("也可见")

	out := buf.String()
	if strings.Contains(out, "隐藏") {
		t.Fatalf("warn 级别不应输出 debug/info：\n%s", out)
	}
	if !strings.Contains(out, "可见") {
		t.Fatalf("warn/error 应输出：\n%s", out)
	}
}

func Test非法级别回退Info(t *testing.T) {
	// parseLevel 的防御式约定：非法值回退 info，而不是让服务起不来。
	Init("not-a-level", true)
	buf := capture(t)

	L().Debug().Msg("低于info")
	L().Info().Msg("info可见")

	out := buf.String()
	if strings.Contains(out, "低于info") {
		t.Fatalf("回退 info 后不应输出 debug：\n%s", out)
	}
	if !strings.Contains(out, "info可见") {
		t.Fatalf("info 应输出：\n%s", out)
	}
}

func Test大小写与空白容忍(t *testing.T) {
	// " DEBUG " / "Debug" 都应识别为 debug（ParseLevel 大小写敏感，先归一化）。
	Init("  DEBUG  ", true)
	buf := capture(t)

	L().Debug().Msg("debug可见")
	if !strings.Contains(buf.String(), "debug可见") {
		t.Fatalf("归一化后 debug 级别应输出：\n%s", buf.String())
	}
}
