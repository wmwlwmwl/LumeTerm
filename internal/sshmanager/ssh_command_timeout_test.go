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

// 本文件回归「短命令 30s 兜底超时被误用到长耗时远端操作」的问题。
//
// 背景：用户反馈下载一个 2GB 的文件夹，远端 `tar -czf` 压缩超过 30s 就失败。
// 根因是 ExecuteCmdWithClientContext 对所有远端命令硬编码 30s，而压缩/解压/
// 递归删除/git 等合法长耗时操作也复用了它。修复引入
// ExecuteCmdWithClientContextTimeout / ExecuteLongCmdWithClientContext，
// 长耗时调用点改走 remoteCmdLongTimeout，同时保留 ctx 取消能力。
//
// 这里不依赖真实 sshd：用假 SSH 服务端接收 exec 请求后延迟 delay 再回 exit-status，
// 以此验证超时选择与取消行为。

// newSlowExecSSHClient 启动一个只会「收 exec 请求 → 等 delay → 回 exit-status 0」的假 SSH 服务端。
func newSlowExecSSHClient(t *testing.T, delay time.Duration) *ssh.Client {
	t.Helper()
	client, _ := newSlowExecSSHClientWithSignalLog(t, delay)
	return client
}

// newSlowExecSSHClientWithSignalLog 同上，但额外记录 exec 之后收到的 signal 请求，
// 用来验证「取消时会向远端进程组发 SIGTERM」，而不是只关 channel 把命令丢在远端继续跑。
func newSlowExecSSHClientWithSignalLog(t *testing.T, delay time.Duration) (*ssh.Client, <-chan string) {
	t.Helper()
	signalLog := make(chan string, 4)

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
							switch request.Type {
							case "exec":
								if request.WantReply {
									_ = request.Reply(true, nil)
								}
								// 命令开始执行：模拟一个会跑 delay 的远端任务。
								go func() {
									time.Sleep(delay)
									_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
									_ = channel.Close()
								}()
							case "signal":
								// RFC 4254 §6.9：取消时客户端会先发 signal（wantReply=false）。
								var payload struct{ Signal string }
								_ = ssh.Unmarshal(request.Payload, &payload)
								select {
								case signalLog <- payload.Signal:
								default:
								}
							default:
								if request.WantReply {
									_ = request.Reply(false, nil)
								}
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
		listener.Close()
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
		listener.Close()
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
	return client, signalLog
}

// TestRemoteCommandTimeoutPolicy 锁住「短命令 30s、长命令 30min」这条策略，
// 防止后续又有人把长耗时操作塞回短命令通道。
func TestRemoteCommandTimeoutPolicy(t *testing.T) {
	if remoteCmdShortTimeout != 30*time.Second {
		t.Fatalf("短命令兜底应保持 30s，实际 %v", remoteCmdShortTimeout)
	}
	if remoteCmdLongTimeout < 10*time.Minute {
		t.Fatalf("长耗时操作超时上限过小：%v，GB 级 tar 压缩/解压会被打断", remoteCmdLongTimeout)
	}
}

// TestExecuteCmdWithClientContextTimeoutHonorsExplicitTimeout 验证显式超时会如期打断慢命令。
func TestExecuteCmdWithClientContextTimeoutHonorsExplicitTimeout(t *testing.T) {
	const serverDelay = 2 * time.Second
	client := newSlowExecSSHClient(t, serverDelay)
	manager := NewSSHManager()

	started := time.Now()
	_, err := manager.ExecuteCmdWithClientContextTimeout(context.Background(), client, "slow-command", 150*time.Millisecond)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("显式 150ms 超时应打断耗时 2s 的命令，实际成功返回")
	}
	if !strings.Contains(err.Error(), "timed out after 150ms") {
		t.Fatalf("超时错误信息异常: %v", err)
	}
	if elapsed >= serverDelay {
		t.Fatalf("超时未生效，等待了 %v", elapsed)
	}
}

// TestExecuteLongCmdWithClientContextAllowsSlowCommand 验证长超时通道不会在 30s 内打断慢命令
// （这里用 2s 慢命令代表 GB 级 tar 压缩：远超短命令阈值，长通道应正常跑完）。
func TestExecuteLongCmdWithClientContextAllowsSlowCommand(t *testing.T) {
	const serverDelay = 2 * time.Second
	if remoteCmdLongTimeout <= serverDelay {
		t.Fatalf("测试前提不成立：长超时 %v 不大于模拟耗时 %v", remoteCmdLongTimeout, serverDelay)
	}
	client := newSlowExecSSHClient(t, serverDelay)
	manager := NewSSHManager()

	started := time.Now()
	if _, err := manager.ExecuteLongCmdWithClientContext(context.Background(), client, "slow-command"); err != nil {
		t.Fatalf("长超时通道不应打断 %v 的命令: %v", serverDelay, err)
	}
	if elapsed := time.Since(started); elapsed < serverDelay {
		t.Fatalf("命令未跑完就返回，耗时 %v", elapsed)
	}
}

// TestExecuteLongCmdWithClientContextStillCancellable 验证长超时通道仍响应 ctx 取消，
// 用户在下载/压缩中途点取消不会被 30min 长超时拖住。
func TestExecuteLongCmdWithClientContextStillCancellable(t *testing.T) {
	client := newSlowExecSSHClient(t, 5*time.Second)
	manager := NewSSHManager()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := manager.ExecuteLongCmdWithClientContext(ctx, client, "slow-command")
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx 到期应返回 DeadlineExceeded，实际: %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("ctx 取消未生效，等待了 %v", elapsed)
	}
}

// TestCancelSendsSIGTERMToRemoteCommand 回归「取消只关 channel，远端 tar/rm 继续跑」：
// 取消/超时必须先向远端发 signal（SIGTERM），OpenSSH 对非受限会话是 killpg，
// 能把 tar/unzip/rm 这些孙进程一起停住；只关 channel 不杀进程。
func TestCancelSendsSIGTERMToRemoteCommand(t *testing.T) {
	client, signals := newSlowExecSSHClientWithSignalLog(t, 5*time.Second)
	manager := NewSSHManager()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := manager.ExecuteLongCmdWithClientContext(ctx, client, "slow-command")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx 到期应返回 DeadlineExceeded，实际: %v", err)
	}
	select {
	case sig := <-signals:
		if sig != "TERM" {
			t.Fatalf("取消时应发送 SIGTERM，实际发送 %q", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消时未向远端发送 signal 请求，远端进程会继续执行到结束")
	}
}
