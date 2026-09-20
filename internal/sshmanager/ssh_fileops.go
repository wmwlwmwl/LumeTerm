package sshmanager

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

func normalizeRemotePath(input string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(input, "\\", "/"))
	if trimmed == "" {
		return "/"
	}
	cleaned := pathpkg.Clean(trimmed)
	if cleaned == "." || cleaned == "" {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + strings.TrimLeft(cleaned, "/")
	}
	return cleaned
}

func remoteParentPath(input string) string {
	normalized := normalizeRemotePath(input)
	parent := pathpkg.Dir(normalized)
	if parent == "." || parent == "" {
		return "/"
	}
	return normalizeRemotePath(parent)
}

func isRemotePathNotFound(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist")
}

func (m *SSHManager) ResolveDirectoryPath(sessionId string, inputPath string) (string, error) {
	normalizedPath := normalizeRemotePath(inputPath)
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return "", err
	}

	if info, statErr := sftpClient.Stat(normalizedPath); statErr == nil && info != nil {
		if info.IsDir() {
			return normalizedPath, nil
		}
		return remoteParentPath(normalizedPath), nil
	} else if !isRemotePathNotFound(statErr) {
		return normalizedPath, nil
	}

	candidate := remoteParentPath(normalizedPath)
	for candidate != normalizedPath {
		info, statErr := sftpClient.Stat(candidate)
		if statErr == nil && info != nil {
			if info.IsDir() {
				return candidate, nil
			}
			return remoteParentPath(candidate), nil
		}
		if !isRemotePathNotFound(statErr) {
			return candidate, nil
		}
		nextCandidate := remoteParentPath(candidate)
		if nextCandidate == candidate {
			break
		}
		candidate = nextCandidate
	}

	return "/", nil
}

func (m *SSHManager) ListDir(sessionId string, path string) ([]map[string]interface{}, error) {
	return m.ListDirContext(context.Background(), sessionId, path)
}

func (m *SSHManager) ListDirContext(ctx context.Context, sessionId string, path string) ([]map[string]interface{}, error) {
	if err := ensureContextActive(ctx); err != nil {
		return nil, err
	}
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return nil, err
	}

	files, err := sftpClient.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if err := ensureContextActive(ctx); err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	// 目录型符号链接目标解析任务: 先收集, 再并发补判. 密集符号链接目录(如 busybox/Alpine
	// 的 /bin /sbin, 可能数百个指向 busybox 的链接)若串行 Stat 会造成成倍往返延迟.
	type symlinkDirResolveTarget struct {
		index    int
		fullPath string
	}
	var pendingSymlinkTargets []symlinkDirResolveTarget
	for _, f := range files {
		if err := ensureContextActive(ctx); err != nil {
			return nil, err
		}
		permStr := f.Mode().String()
		modeNumeric := fmt.Sprintf("%o", f.Mode().Perm())

		uid := "-"
		gid := "-"
		if stat, ok := f.Sys().(interface{ GetUID() uint32 }); ok {
			uid = fmt.Sprintf("%d", stat.GetUID())
		}
		if stat, ok := f.Sys().(interface{ GetGID() uint32 }); ok {
			gid = fmt.Sprintf("%d", stat.GetGID())
		}

		isSymlink := f.Mode()&os.ModeSymlink != 0
		results = append(results, map[string]interface{}{
			"name":        f.Name(),
			"isDirectory": f.IsDir(),
			"isSymlink":   isSymlink,
			"size":        f.Size(),
			"modifyTime":  f.ModTime().Format(time.RFC3339),
			"permission":  permStr,
			"mode":        modeNumeric,
			"uid":         uid,
			"gid":         gid,
		})
		// 符号链接先入列表, isDirectory 暂用链接自身类型(恒为 false), 稍后并发跟随链接补判.
		// permission/mode 保留链接原值, 前端据此显示链接图标.
		if isSymlink && !f.IsDir() {
			pendingSymlinkTargets = append(pendingSymlinkTargets, symlinkDirResolveTarget{
				index:    len(results) - 1,
				fullPath: pathpkg.Join(path, f.Name()),
			})
		}
	}
	// 并发补判目录型符号链接: sftp.Client 支持并发调用, 用带上限的信号量控制并发度, 把 N 次
	// 串行往返压成 N/并发 批次. 每个 worker 只写各自 results[index] 这一个独立 map, 无共享
	// map 竞态. 目标是目录才改 isDirectory; broken/无权限链接 Stat 失败保持非目录. 纯 SFTP,
	// 不依赖 shell, 跨所有 Unix SSH 系统语义一致, 未来 SFTP-only 模式同样兼容.
	if len(pendingSymlinkTargets) > 0 {
		const maxConcurrentSymlinkResolves = 8
		concurrency := maxConcurrentSymlinkResolves
		if len(pendingSymlinkTargets) < concurrency {
			concurrency = len(pendingSymlinkTargets)
		}
		semaphore := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for _, target := range pendingSymlinkTargets {
			if ensureContextActive(ctx) != nil {
				break
			}
			wg.Add(1)
			semaphore <- struct{}{}
			go func(resolveTarget symlinkDirResolveTarget) {
				defer wg.Done()
				defer func() { <-semaphore }()
				if info, statErr := sftpClient.Stat(resolveTarget.fullPath); statErr == nil && info != nil && info.IsDir() {
					results[resolveTarget.index]["isDirectory"] = true
				}
			}(target)
		}
		wg.Wait()
	}
	sort.Slice(results, func(i, j int) bool {
		iDir := results[i]["isDirectory"].(bool)
		jDir := results[j]["isDirectory"].(bool)
		if iDir != jDir {
			return iDir
		}
		return results[i]["name"].(string) < results[j]["name"].(string)
	})
	return results, nil
}

type OwnershipCandidateEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type OwnershipCandidates struct {
	Users  []OwnershipCandidateEntry `json:"users"`
	Groups []OwnershipCandidateEntry `json:"groups"`
}

type PathOwnershipInfo struct {
	UID        string `json:"uid"`
	GID        string `json:"gid"`
	Mode       string `json:"mode"`
	Permission string `json:"permission"`
}

func normalizeOwnershipCandidateEntries(entries []OwnershipCandidateEntry) []OwnershipCandidateEntry {
	seen := make(map[string]struct{}, len(entries))
	result := make([]OwnershipCandidateEntry, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		id := strings.TrimSpace(entry.ID)
		if name == "" || id == "" || id == "-" {
			continue
		}
		key := id + "\x00" + name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, OwnershipCandidateEntry{
			ID:   id,
			Name: name,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		leftID, leftErr := strconv.Atoi(result[i].ID)
		rightID, rightErr := strconv.Atoi(result[j].ID)
		if leftErr == nil && rightErr == nil && leftID != rightID {
			return leftID < rightID
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (m *SSHManager) ListOwnershipCandidates(sessionId string) (OwnershipCandidates, error) {
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return OwnershipCandidates{}, err
	}
	out, err := m.executeCmdWithClient(client, `printf '__LUMETERM_USERS__\n'; (getent passwd 2>/dev/null || cat /etc/passwd 2>/dev/null || true); printf '__LUMETERM_GROUPS__\n'; (getent group 2>/dev/null || cat /etc/group 2>/dev/null || true)`)
	if err != nil {
		return OwnershipCandidates{}, err
	}
	result := OwnershipCandidates{}
	currentSection := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "__LUMETERM_USERS__":
			currentSection = "users"
			continue
		case "__LUMETERM_GROUPS__":
			currentSection = "groups"
			continue
		}
		if trimmed == "" {
			continue
		}
		parts := strings.Split(trimmed, ":")
		if len(parts) < 3 {
			continue
		}
		entry := OwnershipCandidateEntry{
			ID:   strings.TrimSpace(parts[2]),
			Name: strings.TrimSpace(parts[0]),
		}
		switch currentSection {
		case "users":
			result.Users = append(result.Users, entry)
		case "groups":
			result.Groups = append(result.Groups, entry)
		}
	}
	result.Users = normalizeOwnershipCandidateEntries(result.Users)
	result.Groups = normalizeOwnershipCandidateEntries(result.Groups)
	return result, nil
}

func buildChownSpec(owner string, group string) string {
	trimmedOwner := strings.TrimSpace(owner)
	trimmedGroup := strings.TrimSpace(group)
	if trimmedOwner == "" && trimmedGroup == "" {
		return ""
	}
	if trimmedOwner == "" {
		return ":" + trimmedGroup
	}
	if trimmedGroup == "" {
		return trimmedOwner
	}
	return trimmedOwner + ":" + trimmedGroup
}

func hasPathOwnershipInfo(info PathOwnershipInfo) bool {
	return strings.TrimSpace(info.Permission) != "" || strings.TrimSpace(info.Mode) != "" || strings.TrimSpace(info.UID) != "" && strings.TrimSpace(info.UID) != "-" || strings.TrimSpace(info.GID) != "" && strings.TrimSpace(info.GID) != "-"
}

func mergePathOwnershipInfo(base PathOwnershipInfo, candidate PathOwnershipInfo) PathOwnershipInfo {
	if strings.TrimSpace(base.UID) == "" || strings.TrimSpace(base.UID) == "-" {
		base.UID = strings.TrimSpace(candidate.UID)
	}
	if strings.TrimSpace(base.GID) == "" || strings.TrimSpace(base.GID) == "-" {
		base.GID = strings.TrimSpace(candidate.GID)
	}
	if strings.TrimSpace(base.Mode) == "" {
		base.Mode = strings.TrimSpace(candidate.Mode)
	}
	if strings.TrimSpace(base.Permission) == "" {
		base.Permission = strings.TrimSpace(candidate.Permission)
	}
	return base
}

func (m *SSHManager) GetPathOwnership(sessionId string, path string) (PathOwnershipInfo, error) {
	info := PathOwnershipInfo{
		UID: "-",
		GID: "-",
	}
	sftpClient, sftpErr := m.GetSFTPClient(sessionId)
	if sftpErr == nil && sftpClient != nil {
		if fileInfo, statErr := sftpClient.Stat(path); statErr == nil && fileInfo != nil {
			info.Permission = fileInfo.Mode().String()
			info.Mode = fmt.Sprintf("%o", fileInfo.Mode().Perm())
			if stat, ok := fileInfo.Sys().(interface{ GetUID() uint32 }); ok {
				info.UID = fmt.Sprintf("%d", stat.GetUID())
			}
			if stat, ok := fileInfo.Sys().(interface{ GetGID() uint32 }); ok {
				info.GID = fmt.Sprintf("%d", stat.GetGID())
			}
			if info.Permission != "" && info.Mode != "" && info.UID != "-" && info.GID != "-" {
				return info, nil
			}
		}
	}

	client, _, clientErr := m.GetClientEntry(sessionId)
	if clientErr != nil {
		if hasPathOwnershipInfo(info) {
			return info, nil
		}
		if sftpErr != nil {
			return info, sftpErr
		}
		return info, clientErr
	}

	out, err := m.executeCmdWithClient(client, "stat -Lc '%u\t%g\t%a\t%A' -- "+shellQuotePath(path)+" 2>/dev/null || stat -f '%u\t%g\t%Lp\t%Sp' -- "+shellQuotePath(path)+" 2>/dev/null")
	if err != nil {
		if hasPathOwnershipInfo(info) {
			return info, nil
		}
		return info, err
	}

	fields := strings.SplitN(strings.TrimSpace(out), "\t", 4)
	if len(fields) == 4 {
		info = mergePathOwnershipInfo(info, PathOwnershipInfo{
			UID:        fields[0],
			GID:        fields[1],
			Mode:       fields[2],
			Permission: fields[3],
		})
	}
	return info, nil
}

func (m *SSHManager) ChownFile(sessionId string, path string, owner string, group string, recursive bool) error {
	spec := buildChownSpec(owner, group)
	if spec == "" {
		return nil
	}
	prefix := ""
	if recursive {
		prefix = "-R "
	}
	return m.execRemoteCmdLong(context.Background(), sessionId, fmt.Sprintf("chown %s-- %s %s", prefix, shellQuotePath(spec), shellQuotePath(path)))
}

func (m *SSHManager) ChmodFile(sessionId string, path string, modeStr string, recursive bool) error {
	modeValue := strings.TrimSpace(modeStr)
	modeInt, err := strconv.ParseInt(modeValue, 8, 32)
	if err != nil {
		return fmt.Errorf("invalid mode: %w", err)
	}
	if !recursive {
		sftpClient, err := m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
		return sftpClient.Chmod(path, os.FileMode(modeInt))
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	_, err = m.executeCmdWithClient(client, "chmod -R "+modeValue+" -- "+shellQuotePath(path))
	return err
}

func (m *SSHManager) ReadFile(sessionId string, path string) (string, error) {
	return m.ReadFileContext(context.Background(), sessionId, path)
}

func (m *SSHManager) ReadFileContext(ctx context.Context, sessionId string, path string) (string, error) {
	if err := ensureContextActive(ctx); err != nil {
		return "", err
	}
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return "", err
	}

	f, err := sftpClient.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	const maxFileSize = 50 * 1024 * 1024
	if stat.Size() > maxFileSize {
		return "", fmt.Errorf("文件过大 (%.1f MB)，请使用终端命令查看", float64(stat.Size())/(1024*1024))
	}

	var b bytes.Buffer
	b.Grow(int(stat.Size()))
	buf := make([]byte, 32768)
	for {
		if err := ensureContextActive(ctx); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return b.String(), nil
}

// ReadFileBytes reads a file's raw bytes via SFTP without any string/encoding
// conversion. Use this when the caller needs the original bytes (e.g. writing
// to a local temp file for an external editor, so the editor can do its own
// encoding detection instead of getting UTF-8-mangled bytes from b.String()).
func (m *SSHManager) ReadFileBytes(sessionId string, path string) ([]byte, error) {
	return m.ReadFileBytesContext(context.Background(), sessionId, path)
}

func (m *SSHManager) ReadFileBytesContext(ctx context.Context, sessionId string, path string) ([]byte, error) {
	if err := ensureContextActive(ctx); err != nil {
		return nil, err
	}
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return nil, err
	}

	f, err := sftpClient.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	const maxFileSize = 50 * 1024 * 1024
	if stat.Size() > maxFileSize {
		return nil, fmt.Errorf("文件过大 (%.1f MB)，请使用终端命令查看", float64(stat.Size())/(1024*1024))
	}

	var b bytes.Buffer
	b.Grow(int(stat.Size()))
	buf := make([]byte, 32768)
	for {
		if err := ensureContextActive(ctx); err != nil {
			return nil, err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return b.Bytes(), nil
}

func (m *SSHManager) WriteFile(sessionId string, path string, content string) error {
	return m.WriteFileContext(context.Background(), sessionId, path, content)
}

func (m *SSHManager) WriteFileContext(ctx context.Context, sessionId string, path string, content string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}
	var originalMode os.FileMode
	hasOriginalMode := false
	if info, statErr := sftpClient.Stat(path); statErr == nil {
		originalMode = info.Mode().Perm()
		hasOriginalMode = true
	}
	token := newCommandExecutionToken()
	tempPath := path + ".lumeterm_tmp_" + token
	f, err := sftpClient.Create(tempPath)
	if err != nil {
		return err
	}
	if writeErr := writeStringChunksWithContext(ctx, f, content); writeErr != nil {
		f.Close()
		_ = sftpClient.Remove(tempPath)
		return writeErr
	}
	if err := f.Close(); err != nil {
		_ = sftpClient.Remove(tempPath)
		return err
	}
	if err := ensureContextActive(ctx); err != nil {
		_ = sftpClient.Remove(tempPath)
		return err
	}
	if hasOriginalMode {
		if chmodErr := sftpClient.Chmod(tempPath, originalMode); chmodErr != nil {
			_ = sftpClient.Remove(tempPath)
			return chmodErr
		}
	}
	if err := sftpClient.PosixRename(tempPath, path); err != nil {
		_ = sftpClient.Remove(tempPath)
		return fmt.Errorf("replace failed: %w", err)
	}
	if hasOriginalMode {
		_ = sftpClient.Chmod(path, originalMode)
	}
	return nil
}

// isDangerousPath 检查是否为危险路径（根目录、家目录等），防止误删
func isDangerousPath(path string) bool {
	return path == "" || path == "/" || path == "/*" || path == "~" || path == "~/*"
}

// shellQuotePath 用单引号包裹路径并转义内部单引号，用于安全构造 shell 命令
func shellQuotePath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// rmRfCmd 构造 rm -rf 删除命令
func rmRfCmd(path string) string {
	return "rm -rf " + shellQuotePath(path)
}

// remoteCmdLongTimeout 是文件复制/移动这类可能很耗时操作（大文件 cp/mv）的超时上限。
// executeCmdWithClientContext 固定 30 秒，对大文件 cp 会过早超时，故这里使用更长上限。
const remoteCmdLongTimeout = 30 * time.Minute

const (
	smartUncompressConflictStrategyOverwrite  = "overwrite"
	smartUncompressConflictStrategyAutoRename = "auto_rename"
	smartUncompressConflictStrategyPrompt     = "prompt"
	smartUncompressModeDirect                 = "direct"
	smartUncompressModeFolder                 = "folder"
)

type smartUncompressPlan struct {
	Mode         string
	Reason       string
	TargetName   string
	TargetPath   string
	TargetKind   string
	TargetExists bool
}

func normalizeSmartUncompressConflictStrategy(value string) string {
	switch strings.TrimSpace(value) {
	case smartUncompressConflictStrategyOverwrite:
		return smartUncompressConflictStrategyOverwrite
	case smartUncompressConflictStrategyPrompt:
		return smartUncompressConflictStrategyPrompt
	default:
		return smartUncompressConflictStrategyAutoRename
	}
}

func smartUncompressTargetBaseName(base string) string {
	lowerBase := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lowerBase, ".tar.gz"):
		return base[:len(base)-len(".tar.gz")]
	case strings.HasSuffix(lowerBase, ".tar.bz2"):
		return base[:len(base)-len(".tar.bz2")]
	case strings.HasSuffix(lowerBase, ".tgz"):
		return base[:len(base)-len(".tgz")]
	case strings.HasSuffix(lowerBase, ".tbz2"):
		return base[:len(base)-len(".tbz2")]
	case strings.HasSuffix(lowerBase, ".zip"):
		return base[:len(base)-len(".zip")]
	case strings.HasSuffix(lowerBase, ".tar"):
		return base[:len(base)-len(".tar")]
	case strings.HasSuffix(lowerBase, ".gz"):
		return base[:len(base)-len(".gz")]
	default:
		return base
	}
}

func buildSmartUncompressListCommand(dir string, base string) (string, error) {
	safeDir := shellQuotePath(dir)
	safeBase := shellQuotePath(base)
	lowerBase := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lowerBase, ".zip"):
		return fmt.Sprintf("cd %s && unzip -Z1 %s", safeDir, safeBase), nil
	case strings.HasSuffix(lowerBase, ".tar.gz") || strings.HasSuffix(lowerBase, ".tgz"):
		return fmt.Sprintf("cd %s && tar -tzf %s", safeDir, safeBase), nil
	case strings.HasSuffix(lowerBase, ".tar"):
		return fmt.Sprintf("cd %s && tar -tf %s", safeDir, safeBase), nil
	case strings.HasSuffix(lowerBase, ".tar.bz2") || strings.HasSuffix(lowerBase, ".tbz2"):
		return fmt.Sprintf("cd %s && tar -tjf %s", safeDir, safeBase), nil
	case strings.HasSuffix(lowerBase, ".gz"):
		return "", nil
	default:
		return "", fmt.Errorf("unsupported archive format")
	}
}

func parseSmartUncompressArchiveMembers(output string) []string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		member := strings.TrimSpace(strings.ReplaceAll(line, "\\", "/"))
		for strings.HasPrefix(member, "./") {
			member = strings.TrimPrefix(member, "./")
		}
		member = strings.TrimLeft(member, "/")
		if member == "" {
			continue
		}
		if _, ok := seen[member]; ok {
			continue
		}
		seen[member] = struct{}{}
		result = append(result, member)
	}
	return result
}

func buildSmartUncompressPlan(remoteDir string, base string, members []string, sftpClient *sftp.Client) smartUncompressPlan {
	normalizedMembers := members
	if len(normalizedMembers) == 0 {
		normalizedMembers = []string{smartUncompressTargetBaseName(base)}
	}
	if len(normalizedMembers) == 1 && !strings.HasSuffix(normalizedMembers[0], "/") {
		return smartUncompressPlan{
			Mode:   smartUncompressModeDirect,
			Reason: "single_file",
		}
	}
	topLevelName := ""
	allInSingleTopLevelDir := true
	sawEntry := false
	for _, member := range normalizedMembers {
		normalizedMember := strings.TrimSpace(member)
		if normalizedMember == "" {
			continue
		}
		sawEntry = true
		normalizedMember = strings.TrimSuffix(normalizedMember, "/")
		if normalizedMember == "" {
			continue
		}
		topLevelPart := strings.SplitN(normalizedMember, "/", 2)[0]
		if topLevelPart == "" {
			allInSingleTopLevelDir = false
			break
		}
		if topLevelName == "" {
			topLevelName = topLevelPart
			continue
		}
		if topLevelName != topLevelPart {
			allInSingleTopLevelDir = false
			break
		}
	}
	if sawEntry && allInSingleTopLevelDir && topLevelName != "" {
		return smartUncompressPlan{
			Mode:   smartUncompressModeDirect,
			Reason: "single_root_dir",
		}
	}
	targetName := strings.TrimSpace(smartUncompressTargetBaseName(base))
	if targetName == "" {
		targetName = strings.TrimSpace(base)
	}
	targetPath := pathpkg.Join(remoteDir, targetName)
	plan := smartUncompressPlan{
		Mode:       smartUncompressModeFolder,
		Reason:     "archive_name_folder",
		TargetName: targetName,
		TargetPath: targetPath,
		TargetKind: "directory",
	}
	if sftpClient != nil {
		if info, err := sftpClient.Stat(targetPath); err == nil && info != nil {
			plan.TargetExists = true
			if !info.IsDir() {
				plan.TargetKind = "file"
			}
		}
	}
	return plan
}

func resolveSmartUncompressUniqueTargetPath(sftpClient *sftp.Client, remoteDir string, targetName string) (string, string, error) {
	if sftpClient == nil {
		return "", "", fmt.Errorf("SFTP not available")
	}
	if strings.TrimSpace(targetName) == "" {
		return "", "", fmt.Errorf("missing target name")
	}
	for index := 2; index < 10000; index++ {
		candidateName := fmt.Sprintf("%s (%d)", targetName, index)
		candidatePath := pathpkg.Join(remoteDir, candidateName)
		if _, err := sftpClient.Stat(candidatePath); err != nil {
			if isRemotePathNotFound(err) {
				return candidateName, candidatePath, nil
			}
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("unable to find available smart extract target")
}

// execRemoteCmdLong 在 sessionId 对应服务器上执行命令，使用长超时，
// 适用于 cp/mv 等可能耗时较久的文件操作。返回命令的退出错误。
func (m *SSHManager) execRemoteCmdLong(ctx context.Context, sessionId string, cmd string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	_, err = runCommandWithSessionContext(ctx, session, cmd, remoteCmdLongTimeout)
	return err
}

func (m *SSHManager) DeleteItem(sessionId string, path string, isDir bool) error {
	return m.DeleteItemContext(context.Background(), sessionId, path, isDir)
}

func (m *SSHManager) DeleteItemContext(ctx context.Context, sessionId string, path string, isDir bool) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	if isDangerousPath(path) {
		return fmt.Errorf("refusing to delete dangerous path: %q", path)
	}
	client, sftpClient, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	if isDir {
		if sftpClient != nil {
			if err := sftpClient.RemoveAll(path); err == nil {
				return ensureContextActive(ctx)
			}
		}
		_, err := m.ExecuteCmdWithClientContext(ctx, client, rmRfCmd(path))
		return err
	}
	if sftpClient == nil {
		sftpClient, err = m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
	}
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	return sftpClient.Remove(path)
}

// DeleteItemShell 用 rm -rf 删除
func (m *SSHManager) DeleteItemShell(sessionId string, path string) error {
	return m.DeleteItemShellContext(context.Background(), sessionId, path)
}

func (m *SSHManager) DeleteItemShellContext(ctx context.Context, sessionId string, path string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	if isDangerousPath(path) {
		return fmt.Errorf("refusing to delete dangerous path: %q", path)
	}
	// Local sessions (WSL/PowerShell) have an embedded SFTP-only server with no
	// shell channel, so the rm -rf command below would fail. Delete via SFTP
	// (RemoveAll handles both files and directories) instead.
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		sftpClient, err := m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
		return sftpClient.RemoveAll(path)
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	_, err = m.ExecuteCmdWithClientContext(ctx, client, rmRfCmd(path))
	return err
}

const (
	batchDeleteTargetBytes = 64 * 1024
	batchDeleteMaxItems = 500
)

func batchRmRfCmd(paths []string) string {
	parts := make([]string, 0, len(paths)+2)
	parts = append(parts, "rm", "-rf")
	for _, p := range paths {
		parts = append(parts, shellQuotePath(p))
	}
	return strings.Join(parts, " ")
}

func splitBatchDeletePaths(paths []string) [][]string {
	batches := make([][]string, 0, (len(paths)+batchDeleteMaxItems-1)/batchDeleteMaxItems)
	current := make([]string, 0, batchDeleteMaxItems)
	for _, path := range paths {
		candidate := append(append([]string{}, current...), path)
		if len(current) > 0 && (len(candidate) > batchDeleteMaxItems || len(batchRmRfCmd(candidate)) > batchDeleteTargetBytes) {
			batches = append(batches, current)
			current = []string{path}
			continue
		}
		current = candidate
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

func (m *SSHManager) BatchDeleteItemShell(sessionId string, paths []string) error {
	return m.BatchDeleteItemShellContext(context.Background(), sessionId, paths)
}

func (m *SSHManager) BatchDeleteItemShellContext(ctx context.Context, sessionId string, paths []string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	safePaths := make([]string, 0, len(paths))
	for _, p := range paths {
		if !isDangerousPath(p) {
			safePaths = append(safePaths, p)
		}
	}
	if len(safePaths) == 0 {
		return nil
	}
	// Local sessions (WSL/PowerShell) have an embedded SFTP-only server with no
	// shell channel; delete each path via SFTP RemoveAll instead of rm -rf.
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		sftpClient, err := m.GetSFTPClient(sessionId)
		if err != nil {
			return err
		}
		var firstErr error
		for _, p := range safePaths {
			if err := sftpClient.RemoveAll(p); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	for _, batch := range splitBatchDeletePaths(safePaths) {
		if err := ensureContextActive(ctx); err != nil {
			return err
		}
		if _, err := m.ExecuteCmdWithClientContext(ctx, client, batchRmRfCmd(batch)); err != nil {
			return err
		}
	}
	return nil
}

func (m *SSHManager) Mkdir(sessionId string, path string) error {
	return m.MkdirContext(context.Background(), sessionId, path)
}

func (m *SSHManager) MkdirContext(ctx context.Context, sessionId string, path string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}
	return sftpClient.MkdirAll(path)
}

func (m *SSHManager) RenameItem(sessionId string, oldPath string, newPath string) error {
	return m.RenameItemContext(context.Background(), sessionId, oldPath, newPath)
}

func (m *SSHManager) RenameItemContext(ctx context.Context, sessionId string, oldPath string, newPath string) error {
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	sftpClient, err := m.GetSFTPClient(sessionId)
	if err != nil {
		return err
	}
	return sftpClient.Rename(oldPath, newPath)
}
