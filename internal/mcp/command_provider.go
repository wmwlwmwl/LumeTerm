package mcp

import (
	"context"
	"fmt"
	"time"

	"lumeterm/internal/mcpserver"
)

type CommandProvider struct {
	host Host
}

func NewCommandProvider(host Host) CommandProvider {
	return CommandProvider{host: host}
}

func (p CommandProvider) ExecuteCommand(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration) (mcpserver.CommandExecutionResult, error) {
	return p.ExecuteCommandContext(context.Background(), sessionID, command, purpose, isMutating, cwd, shellType, timeout)
}

func (p CommandProvider) ExecuteCommandContext(ctx context.Context, sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration) (mcpserver.CommandExecutionResult, error) {
	if p.host == nil {
		return mcpserver.CommandExecutionResult{}, fmt.Errorf("ssh manager unavailable")
	}

	reporter, clientName := mcpserver.ActivityFromContext(ctx)
	requestID := mcpserver.NewRequestID()
	serverName := resolveServerName(p.host, sessionID)

	base := mcpserver.ActivityEvent{
		RequestID:  requestID,
		Source:     mcpserver.ActivitySourceExternalMCP,
		ClientName: clientName,
		Tool:       "execute_command",
		SessionID:  sessionID,
		ServerName: serverName,
		Command:    command,
		Purpose:    purpose,
		IsMutating: isMutating,
		CWD:        cwd,
	}

	emit := func(status mcpserver.ActivityStatus, output string, exitCode *int) {
		event := base
		event.Status = status
		event.Output = output
		event.ExitCode = exitCode
		event.Timestamp = mcpserver.Now()
		reporter.ReportActivity(event)
	}

	emit(mcpserver.ActivityStatusStarted, "", nil)

	if isMutating {
		approvalEvent := base
		approvalEvent.Status = mcpserver.ActivityStatusApprovalRequired
		approvalEvent.Timestamp = mcpserver.Now()
		approved, err := reporter.RequestApproval(approvalEvent)
		if err != nil {
			emit(mcpserver.ActivityStatusError, "approval error: "+err.Error(), nil)
			return mcpserver.CommandExecutionResult{SessionID: sessionID, Command: command, Purpose: purpose, IsMutating: isMutating, CWD: cwd, ShellType: shellType}, err
		}
		if !approved {
			emit(mcpserver.ActivityStatusRejected, "rejected by user", nil)
			return mcpserver.CommandExecutionResult{SessionID: sessionID, Command: command, Purpose: purpose, IsMutating: isMutating, CWD: cwd, ShellType: shellType}, fmt.Errorf("command rejected by user")
		}
		emit(mcpserver.ActivityStatusApproved, "", nil)
	}

	if cbHost, ok := p.host.(CommandCallbackExecutor); ok {
		result, err := cbHost.ExecuteCommandInTerminalControlledCallbacks(
			sessionID, command, purpose, isMutating, cwd, shellType, timeout,
			func() { emit(mcpserver.ActivityStatusQueued, "", nil) },
			func() { emit(mcpserver.ActivityStatusRunning, "", nil) },
			func(snapshot string) { emit(mcpserver.ActivityStatusOutput, snapshot, nil) },
		)
		if err != nil {
			emit(mcpserver.ActivityStatusError, err.Error(), nil)
			return result, err
		}
		emitFinal(emit, result)
		return result, nil
	}

	result, err := p.host.ExecuteCommandInTerminalControlled(sessionID, command, purpose, isMutating, cwd, shellType, timeout)
	if err != nil {
		emit(mcpserver.ActivityStatusError, err.Error(), nil)
		return result, err
	}
	emitFinal(emit, result)
	return result, nil
}

func emitFinal(emit func(mcpserver.ActivityStatus, string, *int), result mcpserver.CommandExecutionResult) {
	exitCode := result.ExitCode
	status := mcpserver.ActivityStatusDone
	if result.TimedOut {
		status = mcpserver.ActivityStatusTimedOut
	} else if exitCode != nil && *exitCode != 0 {
		status = mcpserver.ActivityStatusError
	}
	emit(status, result.Output, exitCode)
}

func resolveServerName(host Host, sessionID string) string {
	if host == nil {
		return ""
	}
	descriptors, err := host.ListSessionDescriptors()
	if err != nil {
		return ""
	}
	for _, desc := range descriptors {
		if desc.SessionID == sessionID {
			if len(desc.Tags) > 0 {
				return desc.Tags[0]
			}
			return desc.ConnectionRef
		}
	}
	return ""
}
