// Package jsonx 提供可跨数据库驱动使用的 JSON 字段类型。
//
// 为什么需要它：
//
//	平台需要在 MySQL 8 与 SQLite（本地开发）两种驱动下共用同一套 GORM 模型。
//	MySQL 有原生 JSON 类型，SQLite 没有。若使用 gorm.io/datatypes 之类依赖，
//	会额外引入模块并绑定其为 GORM 插件。
//
//	因此这里自己实现最小可用的 driver.Valuer + sql.Scanner：
//	**统一以 TEXT 存储，读写时由本包负责 JSON 编解码**。
//	（设计说明见 docs/数据库设计.md 第 0 节）
//
// 约定：
//
//   - 写入 NULL 与零值均落库为 NULL，避免空对象与 NULL 语义混淆；
//     读取到 NULL / 空串 / 非法 JSON 时返回零值且**不报错**（防御式，
//     因为历史数据可能由更早的版本写入）。
package jsonx

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Map 是 JSON object 字段。
type Map map[string]any

// Value 实现 driver.Valuer。
func (m Map) Value() (driver.Value, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(map[string]any(m))
	if err != nil {
		return nil, fmt.Errorf("jsonx.Map 序列化失败: %w", err)
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner。
func (m *Map) Scan(src any) error {
	raw, err := toBytes(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		*m = nil
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		// 防御式：不因历史脏数据让整个查询失败。
		*m = nil
		return nil
	}
	*m = out
	return nil
}

// Any 是任意 JSON 值字段（对象 / 数组 / 标量均可）。
type Any struct {
	Val any
}

// Value 实现 driver.Valuer。
func (a Any) Value() (driver.Value, error) {
	if a.Val == nil {
		return nil, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return nil, fmt.Errorf("jsonx.Any 序列化失败: %w", err)
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner。
func (a *Any) Scan(src any) error {
	raw, err := toBytes(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		a.Val = nil
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		a.Val = nil
		return nil
	}
	a.Val = out
	return nil
}

// MarshalJSON 让 Any 在 API 响应里直接展开为原始 JSON 值，而不是 {"Val": ...}。
func (a Any) MarshalJSON() ([]byte, error) {
	if a.Val == nil {
		return []byte("null"), nil
	}
	return json.Marshal(a.Val)
}

// UnmarshalJSON 支持从请求体直接反序列化。
func (a *Any) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		a.Val = nil
		return nil
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	a.Val = out
	return nil
}

// MarshalYAML 让 Any 在 YAML 中展开为原始内容。
//
// 没有这个方法时 yaml.v3 会把包装结构序列化成 `val: {...}`，
// 直接毁掉生成的用例文件——而编译器产出的 YAML 是要交给引擎执行的。
func (a Any) MarshalYAML() (any, error) { return a.Val, nil }

// UnmarshalYAML 支持从 YAML 反序列化。
func (a *Any) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Kind == yaml.ScalarNode && value.Tag == "!!null" {
		a.Val = nil
		return nil
	}
	var out any
	if err := value.Decode(&out); err != nil {
		return err
	}
	// yaml.v3 会把 object 解成 map[string]any，数组解成 []any，
	// 与 json 的行为一致，无需额外归一化。
	a.Val = out
	return nil
}

// MarshalYAML 让 Map 在 YAML 中保持 object 形态。
func (m Map) MarshalYAML() (any, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return map[string]any(m), nil
}

// Slice 是泛型 JSON 数组字段。
//
// 用法：
//
//	type Step struct {
//	    Extract  jsonx.Slice[ExtractItem]  `gorm:"type:text"`
//	    Validate jsonx.Slice[AssertItem]   `gorm:"type:text"`
//	}
type Slice[T any] []T

// Value 实现 driver.Valuer。
func (s Slice[T]) Value() (driver.Value, error) {
	if len(s) == 0 {
		return nil, nil
	}
	b, err := json.Marshal([]T(s))
	if err != nil {
		return nil, fmt.Errorf("jsonx.Slice 序列化失败: %w", err)
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner。
func (s *Slice[T]) Scan(src any) error {
	raw, err := toBytes(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		*s = nil
		return nil
	}
	var out []T
	if err := json.Unmarshal(raw, &out); err != nil {
		*s = nil
		return nil
	}
	*s = out
	return nil
}

// MarshalJSON 保证 nil 切片序列化为 []，前端不必处理 null。
func (s Slice[T]) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]T(s))
}

// toBytes 把驱动返回的各种形态统一成字节切片。
func toBytes(src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return []byte(v), nil
	default:
		// 少数驱动可能直接返回已解析的对象。
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("无法识别数据库返回的 JSON 字段类型 %T: %w", src, err)
		}
		return b, nil
	}
}
