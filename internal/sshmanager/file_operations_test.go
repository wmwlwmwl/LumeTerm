package sshmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// 本文件回归「文件操作取消注册表」：opID 精确取消、session 级统一回收、
// 以及压缩操作被取消时 ctx 取消能关闭远端 session、让 tar 立即终止。

func TestFileOperationRegistryAbortAndCleanup(t *testing.T) {
	registry := newFileOperationRegistry()

	ctx, done, err := registry.begin("session-1", "op-1")
	if err != nil {
		t.Fatalf("begin 失败: %v", err)
	}
	if err := registry.finishCheck("op-1"); err != nil { // helper below
		t.Fatalf("注册后应能查到 op: %v", err)
	}
	if err := registry.abort("op-1"); err != nil {
		t.Fatalf("abort 失败: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("abort 后 ctx 应被取消")
	}
	done()
	if registry.activeCount() != 0 {
		t.Fatalf("操作结束后注册表应为空，实际 %d", registry.activeCount())
	}
	if err := registry.abort("op-1"); err == nil {
		t.Fatal("重复 abort 不存在的操作应返回错误")
	}
}

func TestFileOperationRegistryRejectsDuplicateID(t *testing.T) {
	registry := newFileOperationRegistry()
	_, done, err := registry.begin("session-1", "op-dup")
	if err != nil {
		t.Fatal(err)
	}
	defer done()

	if _, _, err := registry.begin("session-2", "op-dup"); err == nil {
		t.Fatal("同一 opID 重复注册应被拒绝")
	}
}

func TestFileOperationRegistryAbortsSessionScope(t *testing.T) {
	registry := newFileOperationRegistry()
	ctx1, done1, err := registry.begin("session-1", "op-a")
	if err != nil {
		t.Fatal(err)
	}
	defer done1()
	ctx2, done2, err := registry.begin("session-1", "op-b")
	if err != nil {
		t.Fatal(err)
	}
	defer done2()
	ctx3, done3, err := registry.begin("session-2", "op-c")
	if err != nil {
		t.Fatal(err)
	}
	defer done3()

	if got := registry.abortSession("session-1"); got != 2 {
		t.Fatalf("应取消 session-1 的 2 个操作，实际 %d", got)
	}
	for name, ctx := range map[string]context.Context{"op-a": ctx1, "op-b": ctx2} {
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("%s 应被取消", name)
		}
	}
	select {
	case <-ctx3.Done():
		t.Fatal("其它 session 的操作不应被取消")
	default:
	}
}

// finishCheck 判断 opID 是否仍在注册表中。
func (r *fileOperationRegistry) finishCheck(opID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[opID]; !ok {
		return errors.New("not found")
	}
	return nil
}

// newBlockingExecSSHClient 启动一个「收 exec 请求后只回成功、不主动结束」的假 SSH 服务端，
// 用来模拟正在执行的长时间命令；客户端关闭 session 时服务端才会退出。
func newBlockingExecSSHClient(t *testing.T) *ssh.Client {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				server, channels, requests, handshakeErr := ssh.NewServerConn(conn, serverConfig)
				if handshakeErr != nil {
					_ = conn.Close()
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for newChannel := range channels {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "only session")
						continue
					}
					channel, channelRequests, acceptErr := newChannel.Accept()
					if acceptErr != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range channelRequests {
							if request.Type == "exec" && request.WantReply {
								_ = request.Reply(true, nil)
							}
						}
					}()
				}
			}(conn)
		}
	}()

	dialer := net.Dialer{Timeout: 2 * time.Second}
	clientConn, err := dialer.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	config := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	sshConn, channels, requests, err := ssh.NewClientConn(clientConn, listener.Addr().String(), config)
	if err != nil {
		_ = clientConn.Close()
		_ = listener.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(sshConn, channels, requests)
	t.Cleanup(func() {
		_ = client.Close()
		_ = clientConn.Close()
		_ = listener.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("假 SSH 服务端未退出")
		}
	})
	return client
}

func TestCompressItemContextAbortStopsRemoteCommand(t *testing.T) {
	client := newBlockingExecSSHClient(t)
	manager := NewSSHManager()
	manager.clients["conn-1"] = &sshClientEntry{Client: client}
	manager.sessions["session-1"] = &SessionData{ConnKey: "conn-1"}

	ctx, done, err := manager.BeginFileOperation("session-1", "op-compress")
	if err != nil {
		t.Fatal(err)
	}
	defer done()

	errCh := make(chan error, 1)
	go func() { errCh <- manager.CompressItemContext(ctx, "session-1", "/tmp/big-dir") }()

	// 等压缩命令真正跑起来再取消（否则可能在打开 session 前就被取消，测不到关闭路径）。
	time.Sleep(100 * time.Millisecond)
	if err := manager.AbortFileOperation("op-compress"); err != nil {
		t.Fatalf("abort 失败: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后应返回 context.Canceled，实际: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后压缩命令未及时返回（远端 session 可能未被关闭）")
	}
}
