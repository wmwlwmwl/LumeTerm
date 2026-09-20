package mcp

import (
	"context"
	"fmt"

	"lumeterm/internal/mcpserver"
)

type TransferProvider struct {
	host Host
}

func NewTransferProvider(host Host) TransferProvider {
	return TransferProvider{host: host}
}

func (p TransferProvider) TransferFile(sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	return p.TransferFileContext(context.Background(), sessionID, request)
}

func (p TransferProvider) TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	if p.host == nil {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("ssh manager unavailable")
	}
	return p.host.TransferFileContext(ctx, sessionID, request)
}

func (p TransferProvider) ListTransfers(sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	return p.ListTransfersContext(context.Background(), sessionID)
}

func (p TransferProvider) ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	if p.host == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return p.host.ListTransfersContext(ctx, sessionID)
}