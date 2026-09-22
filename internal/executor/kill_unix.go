//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

// newSysProcAttr 让子进程成为新进程组的组长。
//
// 有了独立的进程组，就能用 `kill(-pgid)` 一次干掉整棵进程树，
// 而不是只杀 hrp 本体、把派生出的 python 留在后台。
func newSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killTree 终止整棵进程树（POSIX 实现）。
//
// 与 Windows 版对应：用负 PID 向整个进程组发 SIGKILL。
// 这在 Linux/macOS 上是可靠的，也是平台部署到服务器时的主路径。
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// 负号表示"进程组"：Setpgid 已让子进程自成一组，组长 PID == 子进程 PID
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// 兜底：进程组可能已经不存在（子进程先退出了），再单杀一次
		_ = cmd.Process.Kill()
		return
	}
	_ = cmd.Process.Kill()
}
