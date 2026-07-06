//go:build !windows

package update

import (
	"fmt"
	"os"
	"syscall"
)

// Restart 在 Linux/macOS 上直接使用 syscall.Exec 替换当前进程，
// 避免 os.Executable() 在二进制被替换后指向 /proc/self/exe 导致竞态。
// 工作目录、环境变量和命令行参数都会保留。
func Restart() error {
	// 使用 /proc/self/exe 在 exec 瞬间会解析到文件系统上的当前二进制，
	// 但 selfupdate 已经完成了替换，所以解析到的是新二进制。
	// 更安全的方式是用 os.Args[0]（如果它是绝对/相对路径），
	// 对 Linux 部署场景通常是足够的。
	exe := os.Args[0]
	if _, err := os.Stat(exe); err != nil {
		// os.Args[0] 不是可访问路径时，退回到 os.Executable() 再试一次。
		fallback, ferr := os.Executable()
		if ferr != nil {
			return fmt.Errorf("get executable: %w", ferr)
		}
		exe = fallback
	}

	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("exec new binary: %w", err)
	}
	return nil // unreachable
}
