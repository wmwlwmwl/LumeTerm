package wailsapp

import (
	"fmt"
	"sync"
	"time"

	ai "lumeterm/internal/ai"
	"lumeterm/internal/mcpserver"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const mcpApprovalTimeout = 5 * time.Minute

// mcpActivityReporter implements mcpserver.ActivityReporter by emitting Wails
// events to the frontend. It lives in wailsapp because it needs the Wails ctx.
type mcpActivityReporter struct {
	app *App

	pendingMu  sync.Mutex
	pending    map[string]chan bool // requestID → approval result
}

func newMCPActivityReporter(app *App) *mcpActivityReporter {
	return &mcpActivityReporter{
		app:     app,
		pending: make(map[string]chan bool),
	}
}

func (r *mcpActivityReporter) ReportActivity(event mcpserver.ActivityEvent) {
	if r == nil || r.app == nil || r.app.ctx == nil {
		return
	}
	runtime.EventsEmit(r.app.ctx, "mcp-activity", event)
}

func (r *mcpActivityReporter) RequestApproval(event mcpserver.ActivityEvent) (bool, error) {
	if r == nil || r.app == nil {
		return true, nil
	}

	configDir := ""
	if r.app.configManager != nil {
		configDir = r.app.configManager.GetConfigDir()
	}
	settings := ai.LoadAIGlobalSettings(configDir)
	if !settings.MCPRequireApproval {
		return true, nil
	}

	ch := make(chan bool, 1)
	r.pendingMu.Lock()
	r.pending[event.RequestID] = ch
	r.pendingMu.Unlock()

	defer func() {
		r.pendingMu.Lock()
		delete(r.pending, event.RequestID)
		r.pendingMu.Unlock()
	}()

	event.Status = mcpserver.ActivityStatusApprovalRequired
	event.Timestamp = mcpserver.Now()
	r.ReportActivity(event)

	timer := time.NewTimer(mcpApprovalTimeout)
	defer timer.Stop()

	select {
	case approved := <-ch:
		if approved {
			event.Status = mcpserver.ActivityStatusApproved
		} else {
			event.Status = mcpserver.ActivityStatusRejected
		}
		event.Timestamp = mcpserver.Now()
		r.ReportActivity(event)
		return approved, nil
	case <-timer.C:
		event.Status = mcpserver.ActivityStatusTimedOut
		event.Timestamp = mcpserver.Now()
		r.ReportActivity(event)
		return false, fmt.Errorf("approval timed out")
	}
}

// ResolveMCPApproval is a Wails binding called by the frontend when the user
// approves or rejects an external MCP tool invocation.
func (a *App) ResolveMCPApproval(requestID string, approved bool) error {
	if a == nil || a.mcpReporter == nil {
		return fmt.Errorf("MCP activity reporter not initialized")
	}
	a.mcpReporter.pendingMu.Lock()
	ch, ok := a.mcpReporter.pending[requestID]
	a.mcpReporter.pendingMu.Unlock()
	if !ok {
		return fmt.Errorf("no pending approval for request: %s", requestID)
	}
	select {
	case ch <- approved:
	default:
	}
	return nil
}
