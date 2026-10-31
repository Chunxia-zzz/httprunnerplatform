package hrpclient

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeEngine 在临时目录里造一个「输出固定版本号的假引擎」。
//
// Windows 下用 .bat（实测 exec.Command 可直接执行绝对路径的批处理，
// 本机探针验证过）；Unix 下用可执行 shell 脚本。测试完全不依赖
// 机器上是否装了真 hrp，跨平台密闭。
func fakeEngine(t *testing.T, dir, name, output string) string {
	t.Helper()
	var path string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, name+".bat")
		content := "@echo off\r\n" + output + "\r\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		path = filepath.Join(dir, name)
		content := "#!/bin/sh\n" + output + "\n"
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestResolve显式路径优先 确认优先级链的第 1 环：显式配置压过一切。
//
// 刻意断言「显式路径坏了就报错，而不是悄悄回退」——
// 用户明确配置了路径时静默换一个引擎，比直接失败危险得多。
func TestResolve显式路径优先(t *testing.T) {
	t.Setenv(EnvBinaryPath, "")
	t.Setenv(EnvToolsRoot, "")

	dir := t.TempDir()
	good := fakeEngine(t, dir, "good", `echo hrp version v1.2.3`)

	// 合法显式路径：原样返回。
	got, err := Resolve(good)
	if err != nil || got != good {
		t.Fatalf("Resolve(%q) = %q, %v", good, got, err)
	}

	// 非法显式路径：必须报 ErrNotFound，即使环境变量里有可用的也不能回退。
	t.Setenv(EnvBinaryPath, good)
	if _, err := Resolve(filepath.Join(dir, "no-such")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("非法显式路径应报 ErrNotFound，得到 %v", err)
	}
	t.Setenv(EnvBinaryPath, "")
}

func TestResolve环境变量次之(t *testing.T) {
	t.Setenv(EnvToolsRoot, "")

	dir := t.TempDir()
	fake := fakeEngine(t, dir, "envhrp", `echo hrp version v1.2.3`)
	t.Setenv(EnvBinaryPath, fake)

	got, err := Resolve("")
	if err != nil || got != fake {
		t.Fatalf("Resolve 应命中环境变量，得到 %q, %v", got, err)
	}

	// 环境变量指向不存在的路径：报错而不是继续回退到约定路径。
	t.Setenv(EnvBinaryPath, filepath.Join(dir, "missing"))
	if _, err := Resolve(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("坏环境变量应报 ErrNotFound，得到 %v", err)
	}
}

func TestResolve约定路径兜底(t *testing.T) {
	t.Setenv(EnvBinaryPath, "")

	// 造一个 {tools}/bin/{BinaryName()} 的目录结构，让 EnvToolsRoot 命中。
	// Resolve 只 stat 文件不执行，空文件即可；文件名必须与 BinaryName()
	// 完全一致（Windows 下是 hrp.exe），否则约定路径不会认。
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(binDir, BinaryName())
	// 0755：Linux 下 isExecutable 检查 x 位；Windows 忽略权限位，无影响。
	if err := os.WriteFile(want, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvToolsRoot, root)

	got, err := Resolve("")
	if err != nil {
		t.Fatalf("约定路径应命中: %v", err)
	}
	if got != want {
		t.Fatalf("应解析到 %q，得到 %q", want, got)
	}
}

func TestVersion解析成功(t *testing.T) {
	dir := t.TempDir()
	// 版本号同时出现在 stdout 与 stderr 的形态都要能解析
	//（实测首次运行 hrp 会往 stderr 打日志行）。
	fake := fakeEngine(t, dir, "v1", `echo hrp version v4.3.6`)
	v, err := Version(fake)
	if err != nil {
		t.Fatalf("Version 失败: %v", err)
	}
	if v != "v4.3.6" {
		t.Fatalf("版本 = %q", v)
	}
}

func TestVersion解析失败(t *testing.T) {
	dir := t.TempDir()
	// 输出与 hrp version 正则不匹配：应报「无法解析」而不是空版本。
	fake := fakeEngine(t, dir, "garbage", `echo hello world`)
	if _, err := Version(fake); err == nil {
		t.Fatal("无法解析版本号时应报错")
	} else if !strings.Contains(err.Error(), "解析") {
		t.Fatalf("错误文案应说明是解析失败: %v", err)
	}
}

func TestCheck自检(t *testing.T) {
	dir := t.TempDir()
	fake := fakeEngine(t, dir, "check", `echo hrp version v9.9.9`)
	d, err := Check(fake)
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if d.BinaryPath != fake || d.Version != "v9.9.9" {
		t.Fatalf("Doctor = %+v", d)
	}
	if d.GOOS == "" || d.GOARCH == "" || d.GoVersion == "" {
		t.Fatalf("运行时信息缺失: %+v", d)
	}

	// 路径不存在时 Check 应原样带回 ErrNotFound。
	if _, err := Check(filepath.Join(dir, "nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应报 ErrNotFound，得到 %v", err)
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	// 目录不可执行。
	if isExecutable(dir) {
		t.Fatal("目录不应被视为可执行")
	}
	// 不存在的路径。
	if isExecutable(filepath.Join(dir, "ghost")) {
		t.Fatal("不存在的路径不应被视为可执行")
	}
	// 普通文件在 Windows 下一律可执行（无 x 位语义）；
	// 在 Unix 下没有 x 位则不可执行。
	f := filepath.Join(dir, "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := isExecutable(f); runtime.GOOS != "windows" && got {
		t.Fatal("Unix 下 0644 文件不应可执行")
	}
}
