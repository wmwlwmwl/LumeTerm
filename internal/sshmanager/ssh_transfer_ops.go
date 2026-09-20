package sshmanager

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"lumeterm/internal/mcpserver"
	"lumeterm/internal/transfer"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"
)

func (m *SSHManager) BeginChunkedUploadTask(sessionId string, remoteDir string, maxClients int) (string, error) {
	return m.transferService.BeginChunkedUploadTask(sessionId, remoteDir, maxClients)
}

func (m *SSHManager) BeginChunkedUploadFile(taskID string, relativePath string, size int64, totalChunks int) (string, error) {
	return m.transferService.BeginChunkedUploadFile(taskID, relativePath, size, totalChunks)
}

func (m *SSHManager) UploadChunkBase64(taskID string, fileID string, chunkIndex int, offset int64, base64Content string) error {
	return m.transferService.UploadChunkBase64(taskID, fileID, chunkIndex, offset, base64Content)
}

func (m *SSHManager) CompleteChunkedUploadFile(taskID string, fileID string) error {
	return m.transferService.CompleteChunkedUploadFile(taskID, fileID)
}

func (m *SSHManager) AbortChunkedUploadFile(taskID string, fileID string) error {
	return m.transferService.AbortChunkedUploadFile(taskID, fileID)
}

func (m *SSHManager) FinishChunkedUploadTask(taskID string) error {
	return m.transferService.FinishChunkedUploadTask(taskID)
}

func (m *SSHManager) AbortChunkedUploadTask(taskID string) error {
	return m.transferService.AbortChunkedUploadTask(taskID)
}

func (m *SSHManager) DownloadFileToLocal(sessionId string, downloadID string, remotePath string, localPath string, optionsJSON string) error {
	return m.transferService.DownloadFileToLocal(sessionId, downloadID, remotePath, localPath, optionsJSON)
}

func (m *SSHManager) DownloadDirectoryToLocal(sessionId string, downloadID string, remotePath string, localRoot string, optionsJSON string) error {
	return m.transferService.DownloadDirectoryToLocal(sessionId, downloadID, remotePath, localRoot, optionsJSON)
}

func (m *SSHManager) DownloadDirectoryCompressed(sessionId string, downloadID string, remotePath string, localRoot string, optionsJSON string) error {
	return m.transferService.DownloadDirectoryCompressed(sessionId, downloadID, remotePath, localRoot, optionsJSON)
}

func (m *SSHManager) AbortDownloadTransfer(identifier string) error {
	return m.transferService.AbortDownloadTransfer(identifier)
}

func (m *SSHManager) UploadLocalPathsCompressed(sessionId string, uploadID string, maxConcurrent int, localPaths []string, remoteDir string) error {
	return m.transferService.UploadLocalPathsCompressed(sessionId, uploadID, maxConcurrent, localPaths, remoteDir)
}

func (m *SSHManager) AutoRepairCompressedUploadTargets(sessionId string, localPaths []string, remoteDir string) error {
	return m.transferService.AutoRepairCompressedUploadTargets(sessionId, localPaths, remoteDir)
}

func (m *SSHManager) AbortCompressedUpload(identifier string) error {
	return m.transferService.AbortCompressedUpload(identifier)
}

func (m *SSHManager) TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	return m.transferService.TransferFileContext(ctx, sessionID, request)
}

func (m *SSHManager) ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	return m.transferService.ListTransfersContext(ctx, sessionID)
}

func (m *SSHManager) PreviewDownloadConflicts(sessionId string, remotePath string, localPath string, isDirectory bool) ([]map[string]interface{}, error) {
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return nil, err
	}
	return transfer.PreviewDownloadConflicts(sftpClient, remotePath, localPath, isDirectory)
}

func (m *SSHManager) CopyItem(sessionId string, srcPath string, dstPath string) error {
	return m.CopyItemContext(context.Background(), sessionId, srcPath, dstPath)
}

// CopyItemContext 在同一台服务器内复制文件或目录。
// 优先走 shell：直接在服务器执行 cp -a，数据只在远端本地磁盘流动，不走网络，
// 远快于 SFTP 逐块读写。-a 保留权限/属主/时间戳，递归复制目录，并保留符号链接（不跟随）。
// 无 shell 会话时（未来纯 SFTP 连接）进入 SFTP 预留分支：SFTP 无原生递归复制，当前版本尚未实现，返回错误占位。
func (m *SSHManager) CopyItemContext(ctx context.Context, sessionId string, srcPath string, dstPath string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	if isDangerousPath(srcPath) || isDangerousPath(dstPath) {
		return fmt.Errorf("refusing to copy dangerous path")
	}
	// Local sessions (WSL/PowerShell) have an embedded SFTP-only server with no
	// shell channel, so cp -a would fail. Copy via SFTP instead.
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		sftpClient, err := m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
		return transfer.CopyViaSFTP(sftpClient, srcPath, dstPath)
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	if client != nil {
		return m.execRemoteCmdLong(ctx, sessionId, fmt.Sprintf("cp -a %s %s", shellQuotePath(srcPath), shellQuotePath(dstPath)))
	}
	return fmt.Errorf("当前连接暂不支持复制操作(无可用 shell 会话)")
}

func (m *SSHManager) MoveItem(sessionId string, srcPath string, dstPath string) error {
	return m.MoveItemContext(context.Background(), sessionId, srcPath, dstPath)
}

// MoveItemContext 在同一台服务器内移动文件或目录。
// 优先走 shell：直接在服务器执行 mv，同文件系统上仅改 inode 引用（瞬时），跨文件系统时
// 由 mv 自动完成 cp + rm，数据只在远端本地流动。
// 无 shell 会话时（未来纯 SFTP 连接）进入 SFTP 预留分支：可用 sftpClient.Rename 兜底同文件系统移动，当前版本尚未实现，返回错误占位。
func (m *SSHManager) MoveItemContext(ctx context.Context, sessionId string, srcPath string, dstPath string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	if isDangerousPath(srcPath) || isDangerousPath(dstPath) {
		return fmt.Errorf("refusing to move dangerous path")
	}
	// Local sessions (WSL/PowerShell) have an embedded SFTP-only server with no
	// shell channel, so mv would fail. Move via SFTP Rename instead.
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		sftpClient, err := m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
		// Prefer PosixRename (atomic); fall back to Rename if unsupported.
		if err := sftpClient.PosixRename(srcPath, dstPath); err != nil {
			return sftpClient.Rename(srcPath, dstPath)
		}
		return nil
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	if client != nil {
		return m.execRemoteCmdLong(ctx, sessionId, fmt.Sprintf("mv %s %s", shellQuotePath(srcPath), shellQuotePath(dstPath)))
	}
	return fmt.Errorf("当前连接暂不支持移动操作(无可用 shell 会话)")
}

// progressReader wraps an io.Reader and emits progress events via Wails.
type progressReader struct {
	io.Reader
	ctx       context.Context
	eventName string
	total     int64
	lastEmit  time.Time
}

func (p *progressReader) emit(current int64) {
	pct := float64(0)
	if p.total > 0 {
		pct = float64(current) / float64(p.total) * 100
		if pct > 100 {
			pct = 100
		}
	}
	if p.ctx != nil {
		runtime.EventsEmit(p.ctx, p.eventName, pct)
	}
}

// progressWriter 只累加原子计数，不在 Write 内触发 Wails 事件。
// 传输数据流水线（尤其 sftp 的 File.WriteTo 串行 Reduce 阶段）以此 Write 为唯一出口，
// 在其中做同步 IPC 会冻结整条流水线。
type progressWriter struct {
	io.Writer
	copied atomic.Int64
}

func (p *progressWriter) Write(data []byte) (int, error) {
	n, err := p.Writer.Write(data)
	if n > 0 {
		p.copied.Add(int64(n))
	}
	return n, err
}

// copyWithProgress 复制数据并通过 Wails 事件报告进度
func (m *SSHManager) copyWithProgress(dst io.Writer, src io.Reader, sessionId string, totalSize int64) error {
	tracker := &progressReader{
		ctx:       m.ctx,
		eventName: "transfer-progress-" + sessionId,
		total:     totalSize,
		lastEmit:  time.Now(),
	}
	writer := &progressWriter{Writer: dst}
	reporterDone := make(chan struct{})
	reporterFinished := make(chan struct{})
	go func() {
		defer close(reporterFinished)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		lastReported := int64(-1)
		for {
			select {
			case <-reporterDone:
				return
			case <-ticker.C:
				current := writer.copied.Load()
				if current == lastReported {
					continue
				}
				lastReported = current
				tracker.emit(current)
			}
		}
	}()
	_, err := io.Copy(writer, src)
	close(reporterDone)
	<-reporterFinished
	tracker.emit(writer.copied.Load())
	if err == nil && totalSize > 0 && writer.copied.Load() < totalSize {
		// 与 transfer 包下载路径相同的截断守卫(issue #334)
		return fmt.Errorf("transfer incomplete: copied %d of %d bytes", writer.copied.Load(), totalSize)
	}
	return err
}

func (m *SSHManager) UploadFile(sessionId string, localPath string, remotePath string) error {
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}

	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	destPath := filepath.ToSlash(filepath.Join(remotePath, filepath.Base(localPath)))
	dst, err := sftpClient.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	var totalSize int64
	if stat, err := src.Stat(); err == nil {
		totalSize = stat.Size()
	}
	return m.copyWithProgress(dst, src, sessionId, totalSize)
}

// UploadDir recursively uploads a local directory to a remote path
func (m *SSHManager) UploadDir(sessionId string, localDir string, remoteDir string) error {
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}

	remoteDir = filepath.ToSlash(remoteDir)

	return filepath.Walk(localDir, func(localPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(localDir, localPath)
		if err != nil {
			return err
		}

		remotePath := filepath.ToSlash(filepath.Join(remoteDir, relPath))

		if info.IsDir() {
			return sftpClient.MkdirAll(remotePath)
		}

		src, err := os.Open(localPath)
		if err != nil {
			return err
		}

		dst, err := sftpClient.Create(remotePath)
		if err != nil {
			src.Close()
			return err
		}

		var totalSize int64
		if stat, err := src.Stat(); err == nil {
			totalSize = stat.Size()
		}

		copyErr := m.copyWithProgress(dst, src, sessionId, totalSize)
		closeSrcErr := src.Close()
		closeDstErr := dst.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeSrcErr != nil {
			return closeSrcErr
		}
		return closeDstErr
	})
}

// UploadFileContent uploads file content from memory to a remote path
func (m *SSHManager) UploadFileContent(sessionId string, fileName string, remoteDir string, content []byte) error {
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}

	destPath := filepath.ToSlash(filepath.Join(remoteDir, fileName))
	dst, err := sftpClient.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = dst.Write(content)
	return err
}

// UploadFileContentBase64 通过 base64 编码上传文件内容，避免前端将 Uint8Array
// 展开为普通 Array 导致的内存爆炸（8-16 倍开销）。base64 仅 1.33 倍开销。
func (m *SSHManager) UploadFileContentBase64(sessionId string, fileName string, remoteDir string, base64Content string) error {
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}

	content, err := base64.StdEncoding.DecodeString(base64Content)
	if err != nil {
		return fmt.Errorf("base64 解码失败: %w", err)
	}

	destPath := filepath.ToSlash(filepath.Join(remoteDir, fileName))
	dst, err := sftpClient.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	return m.copyWithProgress(dst, bytes.NewReader(content), sessionId, int64(len(content)))
}

func (m *SSHManager) DownloadFile(sessionId string, remotePath string, localPath string) error {
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}

	src, err := sftpClient.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	var totalSize int64
	if stat, err := src.Stat(); err == nil {
		totalSize = stat.Size()
	}
	return m.copyWithProgress(dst, src, sessionId, totalSize)
}

func (m *SSHManager) CompressItem(sessionId string, remotePath string) error {
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}

	dir := filepath.Dir(remotePath)
	base := filepath.Base(remotePath)
	archiveName := base + ".tar.gz"

	dir = strings.ReplaceAll(dir, "\\", "/")
	cmd := fmt.Sprintf("cd %s && tar -czf %s %s", shellQuotePath(dir), shellQuotePath(archiveName), shellQuotePath(base))

	out, err := m.executeCmdWithClient(client, cmd)
	if err != nil {
		return fmt.Errorf("compress failed: %w, output: %s", err, out)
	}
	return nil
}

func (m *SSHManager) previewSmartUncompressItem(client *ssh.Client, sftpClient *sftp.Client, remotePath string) (smartUncompressPlan, string, string, error) {
	if client == nil {
		return smartUncompressPlan{}, "", "", fmt.Errorf("client not found")
	}
	remoteDir := strings.ReplaceAll(filepath.Dir(remotePath), "\\", "/")
	base := filepath.Base(remotePath)
	listCmd, err := buildSmartUncompressListCommand(remoteDir, base)
	if err != nil {
		return smartUncompressPlan{}, "", "", err
	}
	members := []string{smartUncompressTargetBaseName(base)}
	if listCmd != "" {
		out, runErr := m.executeCmdWithClient(client, listCmd)
		if runErr != nil {
			return smartUncompressPlan{}, "", "", fmt.Errorf("list archive members failed: %w, output: %s", runErr, out)
		}
		members = parseSmartUncompressArchiveMembers(out)
	}
	return buildSmartUncompressPlan(remoteDir, base, members, sftpClient), remoteDir, base, nil
}

func (m *SSHManager) PreviewSmartUncompressItem(sessionId string, remotePath string) (map[string]interface{}, error) {
	client, sftpClient, err := m.GetClientEntry(sessionId)
	if err != nil {
		return nil, err
	}
	if sftpClient == nil {
		sftpClient, err = m.GetSFTPClient(sessionId)
		if err != nil {
			return nil, err
		}
	}
	plan, _, _, err := m.previewSmartUncompressItem(client, sftpClient, remotePath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"mode":         plan.Mode,
		"reason":       plan.Reason,
		"targetName":   plan.TargetName,
		"targetPath":   plan.TargetPath,
		"targetKind":   plan.TargetKind,
		"targetExists": plan.TargetExists,
	}, nil
}

func (m *SSHManager) UncompressItem(sessionId string, remotePath string) error {
	return m.UncompressItemWithStrategy(sessionId, remotePath, smartUncompressConflictStrategyAutoRename)
}

func (m *SSHManager) InstallUnzip(sessionId string) error {
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	cmd := `if command -v unzip >/dev/null 2>&1; then exit 0; fi
if [ "$(id -u)" -eq 0 ]; then SUDO="";
elif command -v sudo >/dev/null 2>&1; then SUDO="sudo -n";
else echo "root privileges or passwordless sudo are required" >&2; exit 1;
fi
if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive $SUDO apt-get update && DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y unzip
elif command -v dnf >/dev/null 2>&1; then $SUDO dnf install -y unzip
elif command -v yum >/dev/null 2>&1; then $SUDO yum install -y unzip
elif command -v apk >/dev/null 2>&1; then $SUDO apk add unzip
elif command -v zypper >/dev/null 2>&1; then $SUDO zypper --non-interactive install unzip
elif command -v pacman >/dev/null 2>&1; then $SUDO pacman -Sy --noconfirm unzip
else echo "no supported package manager found" >&2; exit 1
fi
command -v unzip >/dev/null 2>&1`
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	out, err := runCommandWithSessionContext(context.Background(), session, cmd, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("install unzip failed: %w, output: %s", err, out)
	}
	return nil
}

func (m *SSHManager) UncompressUploadedArchive(sessionId string, remotePath string) error {
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}

	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("missing remote path")
	}
	remoteDir := pathpkg.Dir(remotePath)
	base := pathpkg.Base(remotePath)
	safeDir := shellQuotePath(remoteDir)
	safeBase := shellQuotePath(base)

	var cmd string
	lowerBase := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lowerBase, ".zip"):
		cmd = fmt.Sprintf("cd %s && unzip -o %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".tar.gz") || strings.HasSuffix(lowerBase, ".tgz"):
		cmd = fmt.Sprintf("cd %s && tar -xzf %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".tar"):
		cmd = fmt.Sprintf("cd %s && tar -xf %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".tar.bz2") || strings.HasSuffix(lowerBase, ".tbz2"):
		cmd = fmt.Sprintf("cd %s && tar -xjf %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".gz"):
		cmd = fmt.Sprintf("cd %s && gunzip -f -k %s", safeDir, safeBase)
	default:
		return fmt.Errorf("unsupported archive format")
	}

	out, err := m.executeCmdWithClient(client, cmd)
	if err != nil {
		return fmt.Errorf("uncompress uploaded archive failed: %w, output: %s", err, out)
	}
	return nil
}

func (m *SSHManager) UncompressItemWithStrategy(sessionId string, remotePath string, conflictStrategy string) error {
	client, sftpClient, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	if sftpClient == nil {
		sftpClient, err = m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
	}

	plan, remoteDir, base, err := m.previewSmartUncompressItem(client, sftpClient, remotePath)
	if err != nil {
		return err
	}

	effectiveStrategy := normalizeSmartUncompressConflictStrategy(conflictStrategy)
	targetPath := plan.TargetPath
	if plan.Mode == smartUncompressModeFolder {
		if plan.TargetExists {
			switch effectiveStrategy {
			case smartUncompressConflictStrategyOverwrite:
				if plan.TargetKind != "directory" {
					return fmt.Errorf("smart uncompress target exists and is not a directory")
				}
			case smartUncompressConflictStrategyAutoRename:
				_, nextTargetPath, renameErr := resolveSmartUncompressUniqueTargetPath(sftpClient, remoteDir, plan.TargetName)
				if renameErr != nil {
					return renameErr
				}
				targetPath = nextTargetPath
			default:
				return fmt.Errorf("smart uncompress target exists")
			}
		}
		if err := sftpClient.MkdirAll(targetPath); err != nil {
			return err
		}
	}

	safeDir := shellQuotePath(remoteDir)
	safeBase := shellQuotePath(base)
	safeTargetPath := shellQuotePath(targetPath)

	var cmd string
	lowerBase := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lowerBase, ".zip"):
		if plan.Mode == smartUncompressModeFolder {
			cmd = fmt.Sprintf("cd %s && unzip -o %s -d %s", safeDir, safeBase, safeTargetPath)
		} else {
			cmd = fmt.Sprintf("cd %s && unzip -o %s", safeDir, safeBase)
		}
	case strings.HasSuffix(lowerBase, ".tar.gz") || strings.HasSuffix(lowerBase, ".tgz"):
		if plan.Mode == smartUncompressModeFolder {
			cmd = fmt.Sprintf("cd %s && tar -xzf %s -C %s", safeDir, safeBase, safeTargetPath)
		} else {
			cmd = fmt.Sprintf("cd %s && tar -xzf %s", safeDir, safeBase)
		}
	case strings.HasSuffix(lowerBase, ".tar"):
		if plan.Mode == smartUncompressModeFolder {
			cmd = fmt.Sprintf("cd %s && tar -xf %s -C %s", safeDir, safeBase, safeTargetPath)
		} else {
			cmd = fmt.Sprintf("cd %s && tar -xf %s", safeDir, safeBase)
		}
	case strings.HasSuffix(lowerBase, ".tar.bz2") || strings.HasSuffix(lowerBase, ".tbz2"):
		if plan.Mode == smartUncompressModeFolder {
			cmd = fmt.Sprintf("cd %s && tar -xjf %s -C %s", safeDir, safeBase, safeTargetPath)
		} else {
			cmd = fmt.Sprintf("cd %s && tar -xjf %s", safeDir, safeBase)
		}
	case strings.HasSuffix(lowerBase, ".gz"):
		cmd = fmt.Sprintf("cd %s && gunzip -f -k %s", safeDir, safeBase)
	default:
		return fmt.Errorf("unsupported archive format")
	}

	out, err := m.executeCmdWithClient(client, cmd)
	if err != nil {
		return fmt.Errorf("uncompress failed: %w, output: %s", err, out)
	}
	return nil
}
