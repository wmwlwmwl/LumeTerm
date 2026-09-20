package mcp

import (
	"context"
	"os"
	"time"

	"lumeterm/internal/mcpserver"
)

type Host interface {
	RegistryKey() any
	ListSessionDescriptors() ([]mcpserver.SessionDescriptor, error)
	ExecuteCommandInTerminalControlled(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration) (mcpserver.CommandExecutionResult, error)
	ListDirectoryContext(ctx context.Context, sessionID string, remotePath string) ([]map[string]interface{}, error)
	ReadTextFileContext(ctx context.Context, sessionID string, remotePath string) (string, error)
	WriteTextFileContext(ctx context.Context, sessionID string, remotePath string, content string) error
	DeleteItemContext(ctx context.Context, sessionID string, remotePath string, isDir bool) error
	MkdirContext(ctx context.Context, sessionID string, remotePath string) error
	TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error)
	ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error)
	RunCommandContext(ctx context.Context, sessionID string, command string) (string, error)
	UploadTempTextContext(ctx context.Context, sessionID string, suffix string, content string, mode os.FileMode) (string, error)
	RemoveFile(sessionID string, remotePath string)
}

// CommandCallbackExecutor is an optional Host capability. Hosts that implement
// this interface allow the command provider to receive fine-grained lifecycle
// callbacks (queued / started / output) that are forwarded to the activity
// reporter for UI visibility.
type CommandCallbackExecutor interface {
	ExecuteCommandInTerminalControlledCallbacks(
		sessionID string, command string, purpose string, isMutating bool,
		cwd string, shellType string, timeout time.Duration,
		onQueued func(), onStarted func(), onOutput func(string),
	) (mcpserver.CommandExecutionResult, error)
}

// ActivityReporterCarrier is an optional capability interface. Hosts that can
// supply a reporter implement this; StartServer type-asserts to inject it into
// the per-call context so tools can emit activity events.
type ActivityReporterCarrier interface {
	MCPActivityReporter() mcpserver.ActivityReporter
}

// ReconnectCarrier is an optional capability interface for the SSH disconnect
// recovery flow: hosts implementing it expose the dead-session lookup (used to
// turn tool calls on disconnected sessions into actionable errors) and the
// reconnect provider (which adds the reconnect_server tool to the catalog).
type ReconnectCarrier interface {
	MCPDisconnectTracker() mcpserver.DisconnectTracker
	MCPReconnectProvider() mcpserver.ReconnectProvider
}