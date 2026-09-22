//go:build windows

package executor

import (
	"os/exec"
	"strconv"
	"syscall"
)

// newSysProcAttr 让子进程成为**新进程组的组长**。
//
// 这样 hrp 派生出的 `cmd.exe` / `python.exe` 都会留在同一组里，
// 强杀时能一次带干净。
func newSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killTree 终止整棵进程树。
//
// ⚠️ 这是 C7 的核心实现。实测 A6：shell 的 `timeout` 命令
// **杀不掉 hrp 派生出的 Windows 子进程**，实测中 hrp 曾挂起超过 2 分钟，
// 而外层 `timeout 90` 完全无效 —— 只能靠进程树级强杀。
//
// 用 `taskkill /T /F` 而不是 `Process.Kill()`：
// 后者只杀 hrp 本体，插件阶段派生的 python.exe 会变成孤儿进程继续占用端口与文件。
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	// /T 递归子进程，/F 强制
	_ = exec.Command("taskkill", "/T", "/F", "/PID", pid).Run()

	// 兜底：taskkill 不可用（被安全策略拦截等）时至少把主进程杀掉。
	// 这里忽略错误：进程可能已经被 taskkill 结束了。
	_ = cmd.Process.Kill()
}
