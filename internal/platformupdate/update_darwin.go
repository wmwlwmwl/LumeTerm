//go:build darwin

package platformupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// LaunchInstaller opens the downloaded disk image with Finder. This mirrors
// the Windows installer flow: hand the platform package to the OS, then let
// UpdateApp exit the old application.
func LaunchInstaller(targetPath string) error {
	if err := exec.Command("open", targetPath).Start(); err != nil {
		return fmt.Errorf("failed to open macOS disk image: %w", err)
	}
	return nil
}

// Restart is only a fallback for non-DMG update paths. Released macOS
// builds use a DMG and therefore go through LaunchInstaller.
func Restart(exePath string) error {
	if err := exec.Command("open", exePath).Start(); err != nil {
		return fmt.Errorf("failed to restart application: %w", err)
	}
	return nil
}

func InstallDeb(_ string) error {
	return fmt.Errorf("deb packages are not supported on macOS")
}

func InstallRPM(_ string) error {
	return fmt.Errorf("rpm packages are not supported on macOS")
}

// InstallDMG mounts the verified DMG and starts a detached updater.
// The updater waits for this process to exit, backs up and replaces the
// application bundle, opens the new application, and rolls back on failure.
func InstallDMG(dmgPath, exePath string) error {
	const appMarker = ".app/Contents/MacOS/"
	markerIndex := strings.Index(exePath, appMarker)
	if markerIndex < 0 {
		return fmt.Errorf("cannot locate the current macOS application bundle")
	}

	targetApp := exePath[:markerIndex+len(".app")]
	// A bundle launched directly from a DMG is read-only. Install the update
	// into /Applications in that case.
	if strings.HasPrefix(targetApp, "/Volumes/") {
		targetApp = filepath.Join("/Applications", filepath.Base(targetApp))
	}

	targetParent := filepath.Dir(targetApp)
	writeProbe, err := os.MkdirTemp(targetParent, ".lumeterm-update-write-test-")
	if err != nil {
		return fmt.Errorf("application directory is not writable: %w", err)
	}
	os.Remove(writeProbe)

	mountPoint, err := os.MkdirTemp("", "lumeterm-update-mount-")
	if err != nil {
		return fmt.Errorf("failed to create DMG mount point: %w", err)
	}
	mounted := false
	defer func() {
		if !mounted {
			os.Remove(mountPoint)
		}
	}()

	attach := exec.Command("hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mountPoint, dmgPath)
	if output, err := attach.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to mount update DMG: %w: %s", err, strings.TrimSpace(string(output)))
	}
	mounted = true

	detachOnError := func() {
		exec.Command("hdiutil", "detach", mountPoint).Run()
		os.Remove(mountPoint)
		mounted = false
	}

	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		detachOnError()
		return fmt.Errorf("failed to inspect update DMG: %w", err)
	}
	var sourceApp string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
			sourceApp = filepath.Join(mountPoint, entry.Name())
			break
		}
	}
	if sourceApp == "" {
		detachOnError()
		return fmt.Errorf("update DMG does not contain an application bundle")
	}

	if output, err := exec.Command("codesign", "--verify", "--deep", "--strict", sourceApp).CombinedOutput(); err != nil {
		detachOnError()
		return fmt.Errorf("update application signature verification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	updaterScript := `#!/bin/sh
set -u
TARGET="$1"
SOURCE="$2"
MOUNT="$3"
DMG="$4"
OLD_PID="$5"
BACKUP="${TARGET}.old"

cleanup() {
  /usr/bin/hdiutil detach "$MOUNT" >/dev/null 2>&1 || true
  /bin/rmdir "$MOUNT" >/dev/null 2>&1 || true
  /bin/rm -f "$DMG" "$0"
}
trap cleanup EXIT

while /bin/kill -0 "$OLD_PID" >/dev/null 2>&1; do
  /bin/sleep 0.2
done

/bin/rm -rf "$BACKUP"
if [ -e "$TARGET" ]; then
  /bin/mv "$TARGET" "$BACKUP" || exit 1
fi

if /usr/bin/ditto "$SOURCE" "$TARGET"; then
  if /usr/bin/open -n "$TARGET"; then
    /bin/rm -rf "$BACKUP"
    exit 0
  fi
fi

/bin/rm -rf "$TARGET"
if [ -e "$BACKUP" ]; then
  /bin/mv "$BACKUP" "$TARGET"
  /usr/bin/open -n "$TARGET" >/dev/null 2>&1 || true
fi
exit 1
`

	scriptFile, err := os.CreateTemp("", "lumeterm-updater-*.sh")
	if err != nil {
		detachOnError()
		return fmt.Errorf("failed to create updater script: %w", err)
	}
	scriptPath := scriptFile.Name()
	if _, err := scriptFile.WriteString(updaterScript); err != nil {
		scriptFile.Close()
		os.Remove(scriptPath)
		detachOnError()
		return fmt.Errorf("failed to write updater script: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		os.Remove(scriptPath)
		detachOnError()
		return fmt.Errorf("failed to close updater script: %w", err)
	}
	if err := os.Chmod(scriptPath, 0700); err != nil {
		os.Remove(scriptPath)
		detachOnError()
		return fmt.Errorf("failed to prepare updater script: %w", err)
	}

	logPath := filepath.Join(os.TempDir(), "LumeTerm-update.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Remove(scriptPath)
		detachOnError()
		return fmt.Errorf("failed to open updater log: %w", err)
	}

	cmd := exec.Command(scriptPath, targetApp, sourceApp, mountPoint, dmgPath, strconv.Itoa(os.Getpid()))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		os.Remove(scriptPath)
		detachOnError()
		return fmt.Errorf("failed to start macOS updater: %w", err)
	}
	logFile.Close()

	// The detached updater owns the mount and temporary files from here.
	return nil
}

func ApplyElevated(_, _ string) error {
	return fmt.Errorf("executable replacement is not supported on macOS; use the DMG release")
}
