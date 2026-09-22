package validator

import (
	"encoding/json"
	"fmt"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/parser"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// decodeRequest 把步骤的 request JSON 解成 RequestSpec。
//
// 与 compiler 包的同名函数刻意分开实现：两边的职责不同 ——
// 编译器关心"能不能渲染"，校验器关心"配置对不对"。
// 共用一份实现会让两边的错误信息互相迁就。
func decodeRequest(a jsonx.Any) (model.RequestSpec, error) {
	var rs model.RequestSpec
	if a.Val == nil {
		return rs, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return rs, fmt.Errorf("request 无法序列化: %w", err)
	}
	if err := json.Unmarshal(b, &rs); err != nil {
		return rs, fmt.Errorf("request 结构不合法: %w", err)
	}
	return rs, nil
}

// decodeHooks 把步骤的 hooks JSON 解成 Hooks，无内容时返回 nil。
func decodeHooks(a jsonx.Any) (*model.Hooks, error) {
	if a.Val == nil {
		return nil, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return nil, fmt.Errorf("hooks 无法序列化: %w", err)
	}
	var h model.Hooks
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("hooks 结构不合法: %w", err)
	}
	if len(h.Setup) == 0 && len(h.Teardown) == 0 {
		return nil, nil
	}
	return &h, nil
}

// decodeCaseConfig 把用例的 config JSON 解成 CaseConfig。
func decodeCaseConfig(a jsonx.Any) (*model.CaseConfig, error) {
	if a.Val == nil {
		return nil, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return nil, fmt.Errorf("config 无法序列化: %w", err)
	}
	var cc model.CaseConfig
	if err := json.Unmarshal(b, &cc); err != nil {
		return nil, fmt.Errorf("config 结构不合法: %w", err)
	}
	return &cc, nil
}

// parserAssertMethods 返回引擎支持的全部校验器方法。
//
// 真源在 parser 包（它实现了这些校验器对应的比较逻辑），
// 这里直接引用而不是复制一份清单 —— 两份清单迟早会不一致，
// 而"平台允许保存、引擎不认识"的组合会让用户在运行期才踩坑。
func parserAssertMethods() []string { return parser.ValidAssertMethods }
