package executor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Workspace 是一次执行的独立工作区。
//
// 为什么每次运行都要一份独立副本（实测 A5）：
//
//	引擎把产物写进 `<cwd>/results/<时间戳>/`，而时间戳**精度只到秒**。
//	如果多个运行共用同一个 cwd：
//	  · 同一秒内启动的两次运行会落到同一个产物目录、互相覆盖；
//	  · 更糟的是"执行前清空 results/"这个动作会互相踩 ——
//	    A 运行刚跑完、还没把产物搬走，B 运行起步就把 results/ 清了。
//
//	因此工作区**必须按运行隔离**，而不是按项目。
//
// 目录布局：
//
//	{root}/runtime/runs/{runID}/          ← cwd，hrp 在这里跑
//	├── .env                              ← 从项目工作区复制
//	├── testcases/*.yaml                  ← 从项目工作区复制
//	├── results/                          ← 引擎自己的产物（每用例执行前清空，跑完立刻搬走）
//	└── artifacts/<case_code>/            ← 平台搬过来的产物归属地
//	    ├── stdout.txt / stderr.txt
//	    ├── summary.json                  ← 可能不存在（断言失败时，实测 F8）
//	    └── report.html                   ← 可能不存在
type Workspace struct {
	root string
}

// NewWorkspace 创建一次运行的工作区。
func NewWorkspace(runRoot string) (*Workspace, error) {
	if strings.TrimSpace(runRoot) == "" {
		return nil, fmt.Errorf("工作区根目录为空")
	}
	ws := &Workspace{root: filepath.Clean(runRoot)}
	if err := os.MkdirAll(ws.Root(), 0o755); err != nil {
		return nil, fmt.Errorf("创建工作区失败: %w", err)
	}
	return ws, nil
}

// Root 返回工作区根目录，也就是 hrp 子进程的 cwd。
func (w *Workspace) Root() string { return w.root }

// TestcasesDir 返回用例目录（工作区内的相对路径是 testcases/）。
func (w *Workspace) TestcasesDir() string { return filepath.Join(w.root, "testcases") }

// ResultsDir 返回引擎自己的产物目录。
func (w *Workspace) ResultsDir() string { return filepath.Join(w.root, "results") }

// ArtifactsRoot 返回平台搬移产物后的归属目录。
func (w *Workspace) ArtifactsRoot() string { return filepath.Join(w.root, "artifacts") }

// CaseArtifactsDir 返回某个用例的产物目录。
//
// caseCode 会做路径穿越防护：它来自数据库，理论上可信，
// 但工作区路径一旦被穿越就会写到盘上任意位置，代价太大，不值得省这几行。
func (w *Workspace) CaseArtifactsDir(caseCode string) (string, error) {
	safe, err := safeSegment(caseCode)
	if err != nil {
		return "", err
	}
	return filepath.Join(w.ArtifactsRoot(), safe), nil
}

// CaseFile 返回某个用例在工作区内的**相对**路径（传给 hrp run 用）。
//
// 用相对路径而不是绝对路径：引擎以 cwd 为项目根，
// 相对路径能让引擎日志里出现的路径与实际工作区一致，排查时不会对不上。
func (w *Workspace) CaseFile(caseCode string) (string, error) {
	safe, err := safeSegment(caseCode)
	if err != nil {
		return "", err
	}
	return "testcases/" + safe + ".yaml", nil
}

// CopyFrom 把编译产物从项目持久工作区复制进来。
//
// 只复制运行必需的两种东西：`.env` 与 `testcases/`。
// 不复制 `results/` 之类的运行残留 —— 那正是要隔离的对象。
func (w *Workspace) CopyFrom(projectWorkspace string) error {
	projectWorkspace = filepath.Clean(projectWorkspace)
	if projectWorkspace == w.root {
		return fmt.Errorf("项目工作区与运行工作区相同（%s）：必须隔离，否则运行产物会互相覆盖", w.root)
	}

	if err := os.MkdirAll(w.TestcasesDir(), 0o755); err != nil {
		return fmt.Errorf("创建用例目录失败: %w", err)
	}

	// .env 缺失是可以接受的（引擎会用默认 base_url 空值），但用例目录必须存在
	envSrc := filepath.Join(projectWorkspace, ".env")
	if _, err := os.Stat(envSrc); err == nil {
		if err := copyFile(envSrc, filepath.Join(w.root, ".env")); err != nil {
			return fmt.Errorf("复制 .env 失败: %w", err)
		}
	}

	srcDir := filepath.Join(projectWorkspace, "testcases")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("读取项目用例目录失败: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !isCaseFile(e.Name()) {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(w.TestcasesDir(), e.Name())
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("复制用例 %s 失败: %w", e.Name(), err)
		}
	}
	return nil
}

// PrepareForCase 为下一个用例做准备。
//
// 清掉 results/：确保执行后这里最多只有一个时间戳目录，
// 从而能准确判断"哪些产物属于这一次执行"（时间戳只到秒，见 A5）。
func (w *Workspace) PrepareForCase() error {
	if err := os.RemoveAll(w.ResultsDir()); err != nil {
		return fmt.Errorf("清理 results 目录失败: %w", err)
	}
	return nil
}

// isCaseFile 判断是否是引擎会加载的用例文件。
//
// 引擎的白名单是 .yml / .yaml / .json（实测 F5）。
// 这里额外排除隐藏文件，避免把 `.env` 之类的误复制进 testcases/。
func isCaseFile(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yml", ".yaml", ".json":
		return true
	default:
		return false
	}
}

// safeSegment 校验并归一化一个用作路径片段的标识符。
//
// 这些标识符来自数据库（用例 code 等），理论上可信；
// 但工作区路径一旦被穿越就能写到盘上任意位置，代价太大，不值得省这几行。
func safeSegment(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("标识符为空，无法作为路径片段")
	}
	if strings.ContainsAny(s, `/\:`) {
		return "", fmt.Errorf("标识符 %q 含有非法路径字符", s)
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("标识符 %q 含有路径穿越片段", s)
	}
	return s, nil
}

// copyFile 逐字节复制。
//
// 不用 os.Rename：项目工作区与运行工作区可能落在不同盘符，
// 跨盘 rename 会失败。这里的数据量很小（几十 KB），复制足够。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
