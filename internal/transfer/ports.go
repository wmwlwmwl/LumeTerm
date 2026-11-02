package transfer

import (
	"context"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SessionBackend provides the SSH resources needed by transfer operations.
type SessionBackend interface {
	ClientEntry(sessionID string) (*ssh.Client, *sftp.Client, error)
	SFTPClient(sessionID string) (*sftp.Client, error)
	ExecuteCommand(ctx context.Context, client *ssh.Client, command string) (string, error)
	// ExecuteLongCommand 用于压缩/解压等可能远超 30s 的远端命令；仍响应 ctx 取消。
	ExecuteLongCommand(ctx context.Context, client *ssh.Client, command string) (string, error)
	DeleteRemote(ctx context.Context, sessionID string, remotePath string, isDir bool) error
	MkdirRemote(ctx context.Context, sessionID string, remotePath string) error
	RenameRemote(ctx context.Context, sessionID string, oldPath string, newPath string) error
	UpdateUploadChannels(sessionID string, delta int)
}

// ProgressSink keeps UI delivery outside the transfer domain.
type ProgressSink interface {
	Emit(event string, payload any)
}

type nopProgressSink struct{}

func (nopProgressSink) Emit(string, any) {}
