//go:build linux

package platformupdate

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LaunchInstaller 启动安装向导（Unix 下直接执行）
func LaunchInstaller(targetPath string) error {
	cmd := exec.Command(targetPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start setup: %w", err)
	}
	return nil
}

// Restart 重启应用
func Restart(exePath string) error {
	cmd := exec.Command(exePath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to restart application: %w", err)
	}
	return nil
}

// InstallDeb 以提权方式安装 .deb 包。
// 通过 pkexec（优先）或 sudo 执行 dpkg -i。
func InstallDeb(debPath string) error {
	// 优先使用 pkexec（显示友好的图形鉴权对话框）
	cmd := exec.Command("pkexec", "dpkg", "-i", debPath)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		// pkexec 不可用时回退到 sudo（终端内输入密码）
		cmd = exec.Command("sudo", "dpkg", "-i", debPath)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install deb package: neither pkexec nor sudo is available, or user cancelled: %w", err)
		}
	}
	return nil
}

// InstallRPM 以提权方式安装 .rpm 包。
// 通过 pkexec（优先）或 sudo 执行 rpm -Uvh。
func InstallRPM(rpmPath string) error {
	cmd := exec.Command("pkexec", "rpm", "-Uvh", rpmPath)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("sudo", "rpm", "-Uvh", rpmPath)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install rpm package: neither pkexec nor sudo is available, or user cancelled: %w", err)
		}
	}
	return nil
}

func InstallDMG(_, _ string) error {
	return fmt.Errorf("dmg packages are not supported on Linux")
}

func quoteShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// ApplyElevated 以提权方式执行便携版热替换。
// 当 LumeTerm 安装在 /usr/bin/ 等系统目录时，普通用户无写入权限，
// 需要借助 pkexec（优先）或 sudo 执行替换操作。
// 同时，只提权做文件替换，新程序以普通用户权限启动（更安全）。
func ApplyElevated(targetPath, exePath string) error {
	cmdStr := fmt.Sprintf(`mv -f -- %s %s && mv -f -- %s %s`,
		quoteShellArg(exePath), quoteShellArg(exePath+".old"), quoteShellArg(targetPath), quoteShellArg(exePath))

	// 优先使用 pkexec（显示友好的图形鉴权对话框）
	cmd := exec.Command("pkexec", "sh", "-c", cmdStr)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		// pkexec 不可用时回退到 sudo（终端内输入密码）
		cmd = exec.Command("sudo", "sh", "-c", cmdStr)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("elevated update failed: neither pkexec nor sudo is available, or user cancelled: %w", err)
		}
	}

	// 替换成功，以普通用户权限重启应用
	if err := Restart(exePath); err != nil {
		return fmt.Errorf("elevated update succeeded but failed to restart: %w", err)
	}
	return nil
}
