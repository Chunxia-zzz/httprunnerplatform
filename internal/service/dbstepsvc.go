package service

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// per-run token 注册表（R1）
//
// db 步骤编译为「平台回环端点 POST /dbstep」，token 是这条 SQL 通道的唯一凭证：
// 执行器准备工作区时生成并写入该 run 的 .env，run 终态即关闭。
// 进程内实现：平台重启 = 通道全关，行为可接受（重启本就打断执行）。
// ---------------------------------------------------------------------------

type dbstepEntry struct {
	token     string
	projectID uint64
}

type dbstepRegistry struct {
	mu    sync.RWMutex
	byRun map[uint64]dbstepEntry
	// ephemeralSeq 是调试通道的合成 runID 计数器：从最大值往下走，永不与真实 run 冲突。
	ephemeralSeq uint64
}

func newDBStepRegistry() *dbstepRegistry {
	return &dbstepRegistry{byRun: map[uint64]dbstepEntry{}, ephemeralSeq: ^uint64(0)}
}

func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("dbstep-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Register 登记一个 run 的 dbstep 通道，返回本次 token。
func (r *dbstepRegistry) Register(runID, projectID uint64) string {
	tok := randomToken()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byRun[runID] = dbstepEntry{token: tok, projectID: projectID}
	return tok
}

// RegisterEphemeral 为调试合成用例登记临时通道（不经 run 表），
// 返回 token 与释放函数。runID 用递减的合成值，避免与真实 run 冲突。
func (r *dbstepRegistry) RegisterEphemeral(projectID uint64) (string, func()) {
	r.mu.Lock()
	id := r.ephemeralSeq
	r.ephemeralSeq--
	tok := randomToken()
	r.byRun[id] = dbstepEntry{token: tok, projectID: projectID}
	r.mu.Unlock()
	return tok, func() { r.Close(id) }
}

// Close 关闭 run 的通道（终态调用）。
func (r *dbstepRegistry) Close(runID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byRun, runID)
}

// Verify 校验 token，返回对应 run 与项目；无效/已关闭返回 false。
func (r *dbstepRegistry) Verify(token string) (runID, projectID uint64, ok bool) {
	if token == "" {
		return 0, 0, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for runID, e := range r.byRun {
		if e.token == token {
			return runID, e.projectID, true
		}
	}
	return 0, 0, false
}

// ---------------------------------------------------------------------------
// SQL 执行
// ---------------------------------------------------------------------------

// DBStepResult 是回环端点的响应体，也是调试台直执行的返回体。
// rows 的每个元素是 map（列名→值）；时间/二进制按字符串返回。
type DBStepResult struct {
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	Affected int64            `json:"affected"`
	SQLMs    int64            `json:"sql_ms"`
}

// dbStepRequest 是回环端点的请求体。
type DBStepRequest struct {
	Conn   string `json:"conn"`
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}

// DBStepService 执行 db 步骤的 SQL（回环端点与调试台共用）。
type DBStepService struct{ Deps }

// dbStepExec 执行一条 SQL：查询返回 rows，写操作返回 affected。
//
// 连接即开即用：sql.Open + Ping + Close，不做池化（产品规划 R7 再考虑）。
func (s *DBStepService) Exec(projectID uint64, req DBStepRequest) (*DBStepResult, error) {
	if strings.TrimSpace(req.SQL) == "" {
		return nil, errBadParam("SQL 不能为空")
	}
	// 直接查库（项目内按 name 唯一），不走跨服务引用
	var src model.DbSource
	if err := s.DB.Where("project_id = ? AND name = ?", projectID, req.Conn).First(&src).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("数据库连接 %q 不存在（本项目）", req.Conn)
		}
		return nil, errInternal("查询数据库连接失败", err)
	}
	dsn := buildDSN(&src)
	if dsn == "" {
		return nil, errBadParam("无法为驱动 %q 构造连接串（检查 host/port/dbname）", src.Driver)
	}
	db, err := sql.Open(src.Driver, dsn)
	if err != nil {
		return nil, errInternal("打开数据库连接失败: "+err.Error(), err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(30 * time.Second)
	if err := db.Ping(); err != nil {
		return nil, errInternal(fmt.Sprintf("数据库连接失败（%s）", src.Driver), err)
	}

	start := time.Now()
	res := &DBStepResult{}
	upper := strings.ToUpper(strings.TrimSpace(req.SQL))
	isQuery := strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") ||
		strings.HasPrefix(upper, "SHOW") || strings.HasPrefix(upper, "PRAGMA")

	if isQuery {
		rows, err := db.Query(req.SQL, req.Params...)
		if err != nil {
			return nil, errInternal("SQL 执行失败: "+err.Error(), err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, errInternal("读取列信息失败: %v", err)
		}
		res.Columns = cols
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, errInternal("读取行数据失败: "+err.Error(), err)
			}
			row := map[string]any{}
			for i, c := range cols {
				row[c] = normalizeDBValue(vals[i])
			}
			res.Rows = append(res.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return nil, errInternal("遍历结果集失败: "+err.Error(), err)
		}
	} else {
		// 参数占位符风格归一：mysql 用 ?，postgres 用 $n。统一入口收 ?，
		// postgres 驱动不认 ? —— 转换（按出现顺序）。
		sqlText := req.SQL
		args := req.Params
		if src.Driver == "postgres" {
			sqlText, args = convertPlaceholders(req.SQL, req.Params)
		}
		out, err := db.Exec(sqlText, args...)
		if err != nil {
			return nil, errInternal("SQL 执行失败: "+err.Error(), err)
		}
		affected, _ := out.RowsAffected()
		res.Affected = affected
	}
	res.SQLMs = time.Since(start).Milliseconds()
	return res, nil
}

// convertPlaceholders 把 `?` 占位符按顺序转成 postgres 的 $1..$n。
func convertPlaceholders(sqlText string, args []any) (string, []any) {
	var b strings.Builder
	n := 0
	for i := 0; i < len(sqlText); i++ {
		if sqlText[i] == '?' {
			n++
			b.WriteString("$" + itoa(n))
			continue
		}
		b.WriteByte(sqlText[i])
	}
	if len(args) > n {
		args = args[:n]
	}
	return b.String(), args
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// normalizeDBValue 把驱动的 []byte / time.Time 归一成 JSON 友好类型。
func normalizeDBValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	default:
		return t
	}
}

// buildDSN 按驱动构造连接串。空串表示无法构造（配置不完整）。
func buildDSN(src *model.DbSource) string {
	switch src.Driver {
	case "sqlite":
		return src.DBName
	case "mysql":
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&multiStatements=true",
			src.Username, src.Password, src.Host, src.Port, src.DBName)
	case "postgres":
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			src.Host, src.Port, src.Username, src.Password, src.DBName)
	}
	return ""
}

// ExecForDebug 供调试台直接执行（不经引擎）。
func (s *DBStepService) ExecForDebug(projectID uint64, req DBStepRequest) (*DBStepResult, error) {
	return s.Exec(projectID, req)
}

// DBStepParamsOf 从步骤的 db_params JSON 列解析参数数组。
func DBStepParamsOf(raw jsonx.Any) []any {
	if raw.Val == nil {
		return nil
	}
	switch t := raw.Val.(type) {
	case []any:
		return t
	default:
		var out []any
		b, err := json.Marshal(t)
		if err != nil {
			return nil
		}
		_ = json.Unmarshal(b, &out)
		return out
	}
}
