package jsonx

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Map
// ---------------------------------------------------------------------------

func TestMapValue(t *testing.T) {
	tests := []struct {
		name    string
		in      Map
		wantNil bool
	}{
		{"nil 落库为 NULL", nil, true},
		{"空 map 落库为 NULL", Map{}, true},
		{"非空正常序列化", Map{"a": 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.in.Value()
			if err != nil {
				t.Fatalf("Value 失败: %v", err)
			}
			if tt.wantNil && v != nil {
				t.Fatalf("期望 NULL，得到 %v", v)
			}
			if !tt.wantNil {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("期望 string，得到 %T", v)
				}
				if s != `{"a":1}` {
					t.Fatalf("序列化结果 = %q", s)
				}
			}
		})
	}
}

func TestMapScan(t *testing.T) {
	// 正常往返：[]byte 与 string 两种驱动形态都必须支持。
	for name, src := range map[string]any{
		"[]byte 形态": []byte(`{"x":"y","n":1}`),
		"string 形态": `{"x":"y","n":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			var m Map
			if err := m.Scan(src); err != nil {
				t.Fatalf("Scan 失败: %v", err)
			}
			if m["x"] != "y" {
				t.Fatalf("x = %v", m["x"])
			}
			if n, ok := m["n"].(float64); !ok || n != 1 {
				t.Fatalf("n = %v (%T)", m["n"], m["n"])
			}
		})
	}

	// 防御式：NULL / 空串 / 非法 JSON 一律返回零值且不报错。
	// 这是包注释里的明确约定 —— 历史数据可能由更早版本写入，
	// 不允许一条脏数据让整个查询失败。
	for name, src := range map[string]any{
		"NULL":    nil,
		"空串":      "",
		"非法 JSON": []byte(`{oops`),
	} {
		t.Run(name, func(t *testing.T) {
			var m Map
			if err := m.Scan(src); err != nil {
				t.Fatalf("Scan 不应报错（防御式约定）: %v", err)
			}
			if len(m) != 0 {
				t.Fatalf("期望零值，得到 %v", m)
			}
		})
	}
}

// TestMapScanOtherType 覆盖 toBytes 的 default 分支：
// 少数驱动可能直接返回已解析的对象。
func TestMapScanOtherType(t *testing.T) {
	var m Map
	if err := m.Scan(map[string]any{"k": "v"}); err != nil {
		t.Fatalf("Scan 失败: %v", err)
	}
	if m["k"] != "v" {
		t.Fatalf("k = %v", m["k"])
	}
}

func TestMapMarshalYAML(t *testing.T) {
	b, err := yaml.Marshal(struct {
		M Map `yaml:"m"`
	}{M: Map{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "m:\n    a: 1\n" {
		t.Fatalf("YAML 输出 = %q", b)
	}

	// 空 map 必须落成 null，不能是 {} —— 否则编译器产出的
	// 用例 YAML 里会出现一个空对象，与「未设置」无法区分。
	b, err = yaml.Marshal(struct {
		M Map `yaml:"m"`
	}{M: Map{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "m: null\n" {
		t.Fatalf("空 map 的 YAML 输出 = %q", b)
	}
}

// ---------------------------------------------------------------------------
// Any
// ---------------------------------------------------------------------------

func TestAnyValue(t *testing.T) {
	a := Any{}
	if v, err := a.Value(); err != nil || v != nil {
		t.Fatalf("nil 应落库为 NULL，得到 %v %v", v, err)
	}
	a = Any{Val: []any{"x", 1}}
	v, err := a.Value()
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v.(string); !ok || s != `["x",1]` {
		t.Fatalf("序列化结果 = %#v", v)
	}
}

func TestAnyScanAndMarshalJSON(t *testing.T) {
	var a Any
	if err := a.Scan(`{"k":1}`); err != nil {
		t.Fatal(err)
	}
	// MarshalJSON 必须展开为原始值，不能是 {"Val":{...}}。
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"k":1}` {
		t.Fatalf("MarshalJSON = %s", b)
	}

	// nil 的 Any 序列化为 null；"null" 输入反序列化为 nil。
	b, _ = json.Marshal(Any{})
	if string(b) != "null" {
		t.Fatalf("nil Any 的 MarshalJSON = %s", b)
	}
	var a2 Any
	if err := a2.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatal(err)
	}
	if a2.Val != nil {
		t.Fatalf("null 输入后 Val = %v", a2.Val)
	}
	// 非法 JSON 应该报错（这是请求体入口，不是历史数据，不能静默吞）。
	if err := a2.UnmarshalJSON([]byte("{bad")); err == nil {
		t.Fatal("非法 JSON 输入应报错")
	}
	// 防御式：脏数据落库读出为零值不报错。
	var a3 Any
	if err := a3.Scan([]byte("not-json")); err != nil || a3.Val != nil {
		t.Fatalf("脏数据应得到零值不报错: %v %v", a3.Val, err)
	}
}

func TestAnyYAMLRoundtrip(t *testing.T) {
	type holder struct {
		Val Any `yaml:"val"`
	}
	in := holder{Val: Any{Val: map[string]any{"a": 1}}}
	b, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	// MarshalYAML 保证展开为原始内容，绝不出现 "val: val:" 这种包装灾难。
	if string(b) != "val:\n    a: 1\n" {
		t.Fatalf("YAML 输出 = %q", b)
	}
	var out holder
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	m, ok := out.Val.Val.(map[string]any)
	if !ok || m["a"] != 1 {
		t.Fatalf("往返后 Val = %#v", out.Val.Val)
	}

	// null 节点反序列化为 nil。
	var out2 holder
	if err := yaml.Unmarshal([]byte("val: null\n"), &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Val.Val != nil {
		t.Fatalf("null 节点后 Val = %#v", out2.Val.Val)
	}
}

// ---------------------------------------------------------------------------
// Slice
// ---------------------------------------------------------------------------

func TestSliceValueAndScan(t *testing.T) {
	type item struct {
		K string `json:"k"`
	}
	s := Slice[item]{{K: "a"}, {K: "b"}}
	v, err := s.Value()
	if err != nil {
		t.Fatal(err)
	}
	if str, ok := v.(string); !ok || str != `[{"k":"a"},{"k":"b"}]` {
		t.Fatalf("序列化结果 = %#v", v)
	}

	var out Slice[item]
	if err := out.Scan([]byte(`[{"k":"a"},{"k":"b"}]`)); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1].K != "b" {
		t.Fatalf("Scan 后 = %#v", out)
	}

	// 空切片落库 NULL、脏数据零值不报错。
	empty := Slice[item]{}
	if v, _ := empty.Value(); v != nil {
		t.Fatalf("空切片应落 NULL，得到 %v", v)
	}
	var dirty Slice[item]
	if err := dirty.Scan([]byte("bad")); err != nil || len(dirty) != 0 {
		t.Fatalf("脏数据应零值不报错: %v", err)
	}
}

func TestSliceMarshalJSONNil(t *testing.T) {
	// nil 切片必须序列化为 []，前端不必处理 null —— 包注释的明确契约。
	b, err := json.Marshal(Slice[int]{})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("nil Slice 的 MarshalJSON = %s", b)
	}
	b, _ = json.Marshal(Slice[int]{1, 2})
	if string(b) != "[1,2]" {
		t.Fatalf("非空 Slice 的 MarshalJSON = %s", b)
	}
}
