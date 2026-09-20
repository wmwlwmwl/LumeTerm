package transfer

import (
	"testing"

	"lumeterm/internal/mcpserver"
)

func TestResolveMCPTransferPathsRejectEscape(t *testing.T) {
	localParent := t.TempDir()
	if _, err := resolveMCPTransferLocalPath(localParent, "../escape.txt"); err == nil {
		t.Fatal("local path escape was accepted")
	}
	if _, err := resolveMCPTransferLocalPath(localParent, "nested/file.txt"); err != nil {
		t.Fatalf("valid local path rejected: %v", err)
	}
	if _, err := resolveMCPTransferRemotePath("/srv/data", "../escape.txt"); err == nil {
		t.Fatal("remote path escape was accepted")
	}
	if got, err := resolveMCPTransferRemotePath("/srv/data", "nested/file.txt"); err != nil || got != "/srv/data/nested/file.txt" {
		t.Fatalf("remote path = %q, error = %v", got, err)
	}
}

func TestMCPTransferStoreHistoryLimitAndIsolation(t *testing.T) {
	first := newMCPTransferStore()
	second := newMCPTransferStore()
	for index := 0; index < mcpTransferHistoryLimit+5; index++ {
		id := newMCPTransferID()
		first.start(mcpserver.TransferTaskSnapshot{TransferID: id, SessionID: "session", Active: true})
		first.finish(id, func(snapshot *mcpserver.TransferTaskSnapshot) {
			snapshot.Status = mcpTransferStatusCompleted
		})
	}
	if got := len(first.list("session")); got != mcpTransferHistoryLimit {
		t.Fatalf("history size = %d, want %d", got, mcpTransferHistoryLimit)
	}
	if got := len(second.list("session")); got != 0 {
		t.Fatalf("second store inherited %d records", got)
	}
}

func TestMCPTransferStoreCancelAllIsIdempotent(t *testing.T) {
	store := newMCPTransferStore()
	store.start(mcpserver.TransferTaskSnapshot{TransferID: "task", SessionID: "session", Active: true})
	store.cancelAll()
	store.cancelAll()
	items := store.list("session")
	if len(items) != 1 {
		t.Fatalf("history size = %d, want 1", len(items))
	}
	if items[0].Active || items[0].Status != mcpTransferStatusFailed || items[0].Phase != "cancelled" {
		t.Fatalf("unexpected cancelled snapshot: %+v", items[0])
	}
}
