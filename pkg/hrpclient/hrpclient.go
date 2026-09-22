// Package hrpclient 封装对 hrp 引擎二进制的调用。
//
// 设计约束（来自方案文档第三章「引擎事实基线」）：
//
//   - 平台**不引入** hrp 的 Go 库。hrp 的 go.mod 直接依赖 gocv.io/x/gocv（OpenCV，
//     需要 CGO），且 hrp/config.go 引入 hrp/pkg/uixt，会传递性拖入 OpenCV/CGO/gRPC
//     /Prometheus 一整套重依赖，导致交叉编译失效与二进制膨胀。因此平台只通过
//     子进程与引擎交互。
//
//   - 引擎日志写在 **stderr**（`hrp/cmd/root.go` 的 initLogger：JSON 模式与非 JSON
//     模式的 writer 都是 os.Stderr），stdout 只有少量文本输出。调用方必须捕获
//     stderr 才能拿到结构化结果。
//
//   - 引擎以**当前工作目录**作为项目根目录（`hrp/internal/env/env.go` 中
//     RootDir = os.Getwd()），执行时必须显式设置 cmd.Dir，否则用例中的相对路径
//     引用（api/、data/*.csv）全部失效。
//
//   - 引擎会给进程设置分区间退出码（`hrp/internal/code/code.go`），平台据此做
//     失败归因，见 pkg/hrpclient 的 ExitCode 相关定义（后续里程碑补充）。
package hrpclient

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ErrNotFound 表示未能定位到 hrp 二进制。
var ErrNotFound = errors.New("hrp binary not found")

// EnvBinaryPath 是注入 hrp 二进制路径的环境变量名。
const EnvBinaryPath = "HRP_BINARY_PATH"

// EnvToolsRoot 是工具链根目录的环境变量名，用于推导约定路径。
const EnvToolsRoot = "HRP_PLATFORM_TOOLS"

var versionRe = regexp.MustCompile(`hrp version\s+(\S+)`)

// BinaryName 返回当前平台的 hrp 可执行文件名。
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "hrp.exe"
	}
	return "hrp"
}

// candidatePaths 返回约定俗成的候选位置（不含显式配置与环境变量）。
//
// 约定：工具链放在仓库**之外**，避免把二进制提交进 Git。
// 默认目录为仓库同级的 httprunnerplatform-tools/bin/。
func candidatePaths() []string {
	var roots []string
	if r := os.Getenv(EnvToolsRoot); r != "" {
		roots = append(roots, r)
	}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(wd), "httprunnerplatform-tools"))
	}

	name := BinaryName()
	paths := make([]string, 0, len(roots))
	for _, r := range roots {
		paths = append(paths, filepath.Join(r, "bin", name))
	}
	return paths
}

// Resolve 定位 hrp 二进制，优先级从高到低：
//
//  1. explicit（配置文件中的 engine.binary_path）
//  2. 环境变量 HRP_BINARY_PATH
//  3. 约定路径 {HRP_PLATFORM_TOOLS}/bin/ 或 仓库同级 httprunnerplatform-tools/bin/
//  4. 系统 PATH
func Resolve(explicit string) (string, error) {
	if explicit != "" {
		if isExecutable(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("%w: 配置指定的路径不可执行: %s", ErrNotFound, explicit)
	}

	if p := os.Getenv(EnvBinaryPath); p != "" {
		if isExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("%w: 环境变量 %s 指向的路径不可执行: %s", ErrNotFound, EnvBinaryPath, p)
	}

	for _, p := range candidatePaths() {
		if isExecutable(p) {
			return p, nil
		}
	}

	if p, err := exec.LookPath(BinaryName()); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("%w: 已尝试 配置项 / %s / 约定路径 %v / 系统 PATH",
		ErrNotFound, EnvBinaryPath, candidatePaths())
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// Version 执行 `hrp -v` 并解析出版本号（例如 "v4.3.6"）。
//
// 实测：`hrp -v` 把 "hrp version v4.3.6" 写到 **stdout**，stderr 通常为空
// （首次运行会因创建 ~/.hrp/x509 目录而在 stderr 输出一行 JSON 日志）。
// 因此这里同时捕获两个流，避免首次运行解析失败。
func Version(binPath string) (string, error) {
	cmd := exec.Command(binPath, "-v")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	combined := stdout.String() + "\n" + stderr.String()

	if m := versionRe.FindStringSubmatch(combined); len(m) == 2 {
		return m[1], nil
	}
	if runErr != nil {
		return "", fmt.Errorf("执行 %s -v 失败: %w", binPath, runErr)
	}
	return "", fmt.Errorf("无法从输出中解析 hrp 版本号: %q", strings.TrimSpace(combined))
}

// Doctor 汇总一次引擎可用性自检结果，供启动前预检与 CLI 子命令使用。
type Doctor struct {
	BinaryPath string
	Version    string
	GOOS       string
	GOARCH     string
	GoVersion  string
}

// Check 定位并探测 hrp，返回自检结果。
func Check(explicitPath string) (*Doctor, error) {
	bin, err := Resolve(explicitPath)
	if err != nil {
		return nil, err
	}
	v, err := Version(bin)
	if err != nil {
		return nil, err
	}
	return &Doctor{
		BinaryPath: bin,
		Version:    v,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		GoVersion:  runtime.Version(),
	}, nil
}
