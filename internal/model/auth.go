package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// 认证类型（F7）。
//
// `inherit` 只对**步骤**有意义：表示沿用环境配置；
// 环境层的空值等价于 none。
const (
	AuthTypeInherit = "inherit"
	AuthTypeNone    = "none"
	AuthTypeBearer  = "bearer"
	AuthTypeBasic   = "basic"
	AuthTypeAPIKey  = "api_key"
)

// AuthKeyIn 是 API Key 的放置位置。
const (
	AuthInHeader = "header"
	AuthInQuery  = "query"
)

// AuthConfig 是环境/步骤级认证配置（F7）。
//
// 存成 JSON 文本列而不是拆成 7 个列：认证是「一个整体配置」，
// 拆列会让「切换类型后旧字段残留」成为常态（改成 Bearer 后 basic 的密码还躺在库里）。
type AuthConfig struct {
	Type     string `json:"type"`
	Token    string `json:"token"`     // bearer
	Username string `json:"username"`  // basic
	Password string `json:"password"`  // basic
	KeyName  string `json:"key_name"`  // api_key
	KeyValue string `json:"key_value"` // api_key
	KeyIn    string `json:"key_in"`    // api_key：header / query
}

// IsZero 表示没有任何认证内容。
func (a AuthConfig) IsZero() bool {
	return a.Type == "" && a.Token == "" && a.Username == "" &&
		a.Password == "" && a.KeyName == "" && a.KeyValue == ""
}

// NormalizedType 归一类型：空值按 none 处理。
func (a AuthConfig) NormalizedType() string {
	if a.Type == "" {
		return AuthTypeNone
	}
	return a.Type
}

// Value 实现 driver.Valuer：空配置写空串（不写 "{}" 噪音）。
func (a AuthConfig) Value() (driver.Value, error) {
	if a.IsZero() {
		return "", nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner：空串 ↔ 零值。
func (a *AuthConfig) Scan(v any) error {
	switch t := v.(type) {
	case nil:
		*a = AuthConfig{}
		return nil
	case string:
		if strings.TrimSpace(t) == "" {
			*a = AuthConfig{}
			return nil
		}
		return json.Unmarshal([]byte(t), a)
	case []byte:
		if len(t) == 0 {
			*a = AuthConfig{}
			return nil
		}
		return json.Unmarshal(t, a)
	default:
		return fmt.Errorf("无法解析 auth 列：%T", v)
	}
}
