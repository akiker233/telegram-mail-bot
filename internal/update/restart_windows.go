//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
)

// Restart 用新二进制启动一个子进程，然后退出当前进程。
// Windows 上使用 exec.Command，因为 syscall.Exec 在 Windows 上不可用。
func Restart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Dir = getWD()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new process: %w", err)
	}
	os.Exit(0)
	return nil // unreachable
}

func getWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
