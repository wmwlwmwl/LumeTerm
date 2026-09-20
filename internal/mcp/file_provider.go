package mcp

import (
	"context"
	"fmt"
	pathpkg "path"

	"lumeterm/internal/mcpserver"
)

type FileProvider struct {
	host Host
}

func NewFileProvider(host Host) FileProvider {
	return FileProvider{host: host}
}

func (p FileProvider) ListDirectory(sessionID string, remotePath string) ([]mcpserver.DirectoryEntry, error) {
	return p.ListDirectoryContext(context.Background(), sessionID, remotePath)
}

func (p FileProvider) ListDirectoryContext(ctx context.Context, sessionID string, remotePath string) ([]mcpserver.DirectoryEntry, error) {
	if p.host == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	items, err := p.host.ListDirectoryContext(ctx, sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	result := make([]mcpserver.DirectoryEntry, 0, len(items))
	for _, item := range items {
		result = append(result, mcpserver.DirectoryEntry{
			Name:        readString(item, "name"),
			IsDirectory: readBool(item, "isDirectory"),
			Size:        readInt64(item, "size"),
			ModifyTime:  readString(item, "modifyTime"),
			Permission:  readString(item, "permission"),
			Mode:        readString(item, "mode"),
			UID:         readString(item, "uid"),
			GID:         readString(item, "gid"),
		})
	}
	return result, nil
}

func (p FileProvider) ReadTextFile(sessionID string, remotePath string) (string, error) {
	return p.ReadTextFileContext(context.Background(), sessionID, remotePath)
}

func (p FileProvider) ReadTextFileContext(ctx context.Context, sessionID string, remotePath string) (string, error) {
	if p.host == nil {
		return "", fmt.Errorf("ssh manager unavailable")
	}
	return p.host.ReadTextFileContext(ctx, sessionID, remotePath)
}

func (p FileProvider) WriteTextFile(sessionID string, remotePath string, content string) error {
	return p.WriteTextFileContext(context.Background(), sessionID, remotePath, content)
}

func (p FileProvider) WriteTextFileContext(ctx context.Context, sessionID string, remotePath string, content string) error {
	if p.host == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	parentDir := pathpkg.Dir(remotePath)
	if parentDir != "" && parentDir != "." && parentDir != "/" {
		if err := p.host.MkdirContext(ctx, sessionID, parentDir); err != nil {
			return err
		}
	}
	return p.host.WriteTextFileContext(ctx, sessionID, remotePath, content)
}

func (p FileProvider) DeleteFile(sessionID string, remotePath string) error {
	return p.DeleteFileContext(context.Background(), sessionID, remotePath)
}

func (p FileProvider) DeleteFileContext(ctx context.Context, sessionID string, remotePath string) error {
	if p.host == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return p.host.DeleteItemContext(ctx, sessionID, remotePath, false)
}

func readString(item map[string]interface{}, key string) string {
	value, ok := item[key]
	if !ok || value == nil {
		return ""
	}
	str, ok := value.(string)
	if ok {
		return str
	}
	return ""
}

func readBool(item map[string]interface{}, key string) bool {
	value, ok := item[key]
	if !ok || value == nil {
		return false
	}
	flag, ok := value.(bool)
	if ok {
		return flag
	}
	return false
}

func readInt64(item map[string]interface{}, key string) int64 {
	value, ok := item[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}