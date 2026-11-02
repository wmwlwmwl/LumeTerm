package sshmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newUnresponsiveChannelSSHClient 启动一个只完成 SSH 握手、对 session channel open
// 既不 Accept 也不 Reject 的假服务端：client.NewSession() 会一直阻塞。
// 用于回归「NewSession 不受 ctx/超时约束」的问题。
func newUnresponsiveChannelSSHClient(t *testing.T) *ssh.Client {
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
				// 关键：读取 channel open 但不 Accept/Reject，客户端会一直等确认。
				for range channels {
				}
			}(conn)
		}
	}()

	rawConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	clientConn, channels, requests, err := ssh.NewClientConn(rawConn, listener.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	})
	if err != nil {
		_ = rawConn.Close()
		_ = listener.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(clientConn, channels, requests)
	t.Cleanup(func() {
		_ = client.Close()
		_ = rawConn.Close()
		_ = listener.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("假 SSH 服务端未退出")
		}
	})
	return client
}

func TestExecuteCommandTimeoutAppliesToSessionOpen(t *testing.T) {
	client := newUnresponsiveChannelSSHClient(t)
	manager := NewSSHManager()

	started := time.Now()
	_, err := manager.ExecuteCmdWithClientContextTimeout(context.Background(), client, "echo hi", 150*time.Millisecond)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("session open 卡住时应超时返回")
	}
	if !strings.Contains(err.Error(), "timed out after 150ms") {
		t.Fatalf("超时错误信息异常: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("超时未生效，等待了 %v", elapsed)
	}
}

func TestLongCommandContextCancelAppliesToSessionOpen(t *testing.T) {
	client := newUnresponsiveChannelSSHClient(t)
	manager := NewSSHManager()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := manager.ExecuteLongCmdWithClientContext(ctx, client, "echo hi")
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx 到期应返回 DeadlineExceeded，实际: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("取消未生效，等待了 %v", elapsed)
	}
}
