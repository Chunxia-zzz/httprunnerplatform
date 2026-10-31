package response

import (
	"errors"
	"testing"
)

func TestErrorMessage(t *testing.T) {
	plain := New(CodeBadParam, "参数错误")
	if got := plain.Error(); got != "[40000] 参数错误" {
		t.Fatalf("无内部原因时 Error() = %q", got)
	}

	root := errors.New("磁盘满")
	wrapped := Wrap(CodeInternal, "落库失败", root)
	got := wrapped.Error()
	if got != "[50000] 落库失败: 磁盘满" {
		t.Fatalf("带内部原因时 Error() = %q", got)
	}
}

func TestErrorUnwrap(t *testing.T) {
	root := errors.New("root cause")
	wrapped := Wrap(CodeInternal, "outer", root)
	// Unwrap 让 errors.Is / errors.As 能穿过业务错误定位内部原因，
	// 这是日志归因的依赖，不能只看字符串。
	if !errors.Is(wrapped, root) {
		t.Fatal("errors.Is 应穿透到内部原因")
	}
	var re *Error
	if !errors.As(wrapped, &re) || re.Code != CodeInternal {
		t.Fatal("errors.As 应识别出业务错误类型")
	}
}

func TestNewfAndWithData(t *testing.T) {
	e := Newf(CodeNotFound, "用例 %d 不存在", 42)
	if e.Message != "用例 42 不存在" {
		t.Fatalf("Message = %q", e.Message)
	}
	e2 := New(CodeValidateFail, "校验未通过").WithData([]FieldError{{Field: "url", Message: "必填"}})
	fields, ok := e2.Data.([]FieldError)
	if !ok || len(fields) != 1 || fields[0].Field != "url" {
		t.Fatalf("WithData 后 Data = %#v", e2.Data)
	}
	// WithData 返回自身（链式），而不是副本 —— 否则 e2 拿到的仍是没有 Data 的错误。
	if e2.Code != CodeValidateFail || e2.Message != "校验未通过" {
		t.Fatalf("链式调用后原字段被破坏: %#v", e2)
	}
}

// TestCodeUnique 确保业务码两两不同。
//
// 错误码表是前后端共同的契约（docs/接口契约.md 第 0.2 节），
// 两个常量悄悄撞号会让前端按码分流的逻辑错位，且编译器不会报。
func TestCodeUnique(t *testing.T) {
	codes := map[int]string{
		CodeOK:              "CodeOK",
		CodeBadParam:        "CodeBadParam",
		CodeConflict:        "CodeConflict",
		CodeNotFound:        "CodeNotFound",
		CodeInUse:           "CodeInUse",
		CodeInvalidIdent:    "CodeInvalidIdent",
		CodeVersionConflict: "CodeVersionConflict",
		CodeUnauthorized:    "CodeUnauthorized",
		CodeForbidden:       "CodeForbidden",
		CodeInternal:        "CodeInternal",
		CodeEngineDown:      "CodeEngineDown",
		CodeWorkspaceFail:   "CodeWorkspaceFail",
		CodeCompileFail:     "CodeCompileFail",
		CodeValidateFail:    "CodeValidateFail",
		CodeExecutorFail:    "CodeExecutorFail",
		CodeReportMissing:   "CodeReportMissing",
	}
	seen := map[int]string{}
	for code, name := range codes {
		if prev, dup := seen[code]; dup {
			t.Fatalf("业务码撞号: %s 与 %s 都是 %d", name, prev, code)
		}
		seen[code] = name
	}
	if len(codes) != 16 {
		t.Fatalf("错误码表数量 = %d（新增常量后请同步本测试）", len(codes))
	}
}
