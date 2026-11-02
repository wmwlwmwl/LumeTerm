package transfer

import (
	"testing"
	"time"
)

// TestUploadPoolCloseClientDoesNotBlock 回归：上传分片超时/断线时关闭池 client
// 不能阻塞调用方（否则 Wails binding 会永久挂起）。
func TestUploadPoolCloseClientDoesNotBlock(t *testing.T) {
	sshClient := newHungSFTPTestClient(t)
	pool := newSFTPUploadPool(sshClient, 1, DefaultTuning())
	client, err := pool.Acquire()
	if err != nil {
		t.Fatalf("获取池 client 失败: %v", err)
	}

	closeDone := make(chan struct{})
	go func() {
		pool.closeClient(client)
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("closeClient 阻塞调用方")
	}

	discardDone := make(chan struct{})
	go func() {
		pool.Discard(client)
		close(discardDone)
	}()
	select {
	case <-discardDone:
	case <-time.After(time.Second):
		t.Fatal("Discard 已关闭的 client 阻塞")
	}
	pool.Close()
}
