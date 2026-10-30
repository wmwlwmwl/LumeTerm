package sshmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"lumeterm/internal/localsftp"
)

// 本文件回归「远端删除目录优先走 shell rm -rf」的策略。
//
// 背景：DeleteItemContext 原先先调 SFTP RemoveAll（逐目录项 RPC），十万级文件的
// 目录要跑很久，且整条路径没有任何超时/取消手段，卡住就是永久挂起。
// 远端会话现在优先用一条 rm -rf（走长超时通道、可被 ctx 取消），
// 仅在 shell 不可用时回退 SFTP RemoveAll。

// recordedExec 记录假 SSH 服务端收到的 exec 命令。
type recordedExec struct {
	mu       sync.Mutex
	commands []string
}

func (r *recordedExec) add(cmd string) {
	r.mu.Lock()
	r.commands = append(r.commands, cmd)
	r.mu.Unlock()
}

func (r *recordedExec) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.commands))
	copy(out, r.commands)
	return out
}

// newRecordingSSHClient 启动一个会记录 exec 命令、按固定 exit status 回包的假 SSH 服务端。
func newRecordingSSHClient(t *testing.T, exitStatus uint32) (*ssh.Client, *recordedExec) {
	t.Helper()
	return newRecordingSSHClientWithStderr(t, exitStatus, "")
}

// newRecordingSSHClientWithStderr 同上，但会在回 exit-status 前先写一段 stderr。
// 用来覆盖「错误被 fmt.Errorf("%w: %s", err, stderr) 包装后按文本判定」的路径
// （Windows cmd/PowerShell 上 rm 缺失时退出码是 1，不是 126/127）。
func newRecordingSSHClientWithStderr(t *testing.T, exitStatus uint32, stderr string) (*ssh.Client, *recordedExec) {
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

	recorder := &recordedExec{}
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
						for request := range channelRequests {
							if request.Type != "exec" {
								if request.WantReply {
									_ = request.Reply(false, nil)
								}
								continue
							}
							var payload struct{ Command string }
							_ = ssh.Unmarshal(request.Payload, &payload)
							recorder.add(payload.Command)
							if request.WantReply {
								_ = request.Reply(true, nil)
							}
							if stderr != "" {
								_, _ = channel.Stderr().Write([]byte(stderr + "\n"))
							}
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exitStatus}))
							_ = channel.Close()
							return
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
	return client, recorder
}

func newDeleteStrategyManager(t *testing.T, client *ssh.Client) *SSHManager {
	t.Helper()
	manager := NewSSHManager()
	manager.clients["conn-1"] = &sshClientEntry{Client: client}
	manager.sessions["session-1"] = &SessionData{ConnKey: "conn-1"}
	return manager
}

func TestDeleteItemContextPrefersShellForRemoteDirectory(t *testing.T) {
	client, recorder := newRecordingSSHClient(t, 0)
	manager := newDeleteStrategyManager(t, client)

	if err := manager.DeleteItemContext(context.Background(), "session-1", "/tmp/big-dir", true); err != nil {
		t.Fatalf("远端目录删除应成功: %v", err)
	}
	commands := recorder.all()
	if len(commands) != 1 {
		t.Fatalf("应只执行一条 shell 删除命令，实际 %d 条: %v", len(commands), commands)
	}
	if !strings.Contains(commands[0], "rm -rf") || !strings.Contains(commands[0], "/tmp/big-dir") {
		t.Fatalf("删除命令不符合预期: %q", commands[0])
	}
}

func TestDeleteItemContextFallsBackWhenRmMissing(t *testing.T) {
	// 127 = command not found，属于「服务器没有 rm」的合法回退场景。
	client, recorder := newRecordingSSHClient(t, 127)
	manager := newDeleteStrategyManager(t, client)

	err := manager.DeleteItemContext(context.Background(), "session-1", "/tmp/big-dir", true)
	if err == nil {
		t.Fatal("shell 删除失败且无 SFTP 可用时应返回错误")
	}
	if !strings.Contains(err.Error(), "rm -rf failed") || !strings.Contains(err.Error(), "sftp unavailable") {
		t.Fatalf("rm 不存在时应尝试 SFTP 回退: %v", err)
	}
	if len(recorder.all()) != 1 {
		t.Fatalf("应先尝试一次 shell 删除，实际命令: %v", recorder.all())
	}
}

func TestDeleteItemContextDoesNotFallbackOnGenericShellFailure(t *testing.T) {
	// exit 1 是普通失败（权限/磁盘/路径错误），不能再用不可中断的 SFTP RemoveAll
	// 继续删；否则「取消/超时后继续删除」会重新出现。
	client, recorder := newRecordingSSHClient(t, 1)
	manager := newDeleteStrategyManager(t, client)

	err := manager.DeleteItemContext(context.Background(), "session-1", "/tmp/big-dir", true)
	if err == nil {
		t.Fatal("shell 删除失败应返回错误")
	}
	if strings.Contains(err.Error(), "sftp fallback") {
		t.Fatalf("普通 shell 失败不应回退 SFTP，实际: %v", err)
	}
	if len(recorder.all()) != 1 {
		t.Fatalf("只应尝试一次 shell 删除，实际命令: %v", recorder.all())
	}
}

// TestDeleteItemContextFallsBackOnWindowsRmMissing 回归 Windows：cmd.exe 里没有 rm，
// 退出码是 1（不是 POSIX 的 127），stderr 带 "is not recognized..."。
// 判定必须先看错误文本，否则这条回退分支永远走不到，Windows 服务器上目录删除
// 会直接失败而不是退回 SFTP。
func TestDeleteItemContextFallsBackOnWindowsRmMissing(t *testing.T) {
	client, recorder := newRecordingSSHClientWithStderr(t, 1,
		"'rm' is not recognized as an internal or external command,\r\noperable program or batch file.")
	manager := newDeleteStrategyManager(t, client)

	err := manager.DeleteItemContext(context.Background(), "session-1", "/tmp/big-dir", true)
	if err == nil {
		t.Fatal("shell 删除失败且无 SFTP 可用时应返回错误")
	}
	if !strings.Contains(err.Error(), "sftp unavailable") {
		t.Fatalf("Windows cmd 上 rm 缺失（exit 1）应触发 SFTP 回退: %v", err)
	}
	if len(recorder.all()) != 1 {
		t.Fatalf("应先尝试一次 shell 删除，实际命令: %v", recorder.all())
	}
}

func TestDeleteItemContextCanceledDoesNotFallBack(t *testing.T) {
	// 命令卡住时用户取消：必须立刻返回 ctx 错误，不能落到无超时的 SFTP RemoveAll。
	client := newSlowExecSSHClient(t, 3*time.Second)
	manager := NewSSHManager()
	manager.clients["conn-1"] = &sshClientEntry{Client: client}
	manager.sessions["session-1"] = &SessionData{ConnKey: "conn-1"}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := manager.DeleteItemContext(ctx, "session-1", "/tmp/big-dir", true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("取消后应返回 DeadlineExceeded，实际: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("取消后未及时返回，耗时 %v", elapsed)
	}
}

func TestRmUnavailableForFallbackClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"restricted-shell", errors.New("ssh: exec request failed"), true},
		{"windows-cmd", errors.New("'rm' is not recognized as an internal or external command"), true},
		{"powershell", errors.New("The term 'rm' is not recognized as a name. CommandNotFoundException"), true},
		{"powershell-param-zh", errors.New("Remove-Item : 找不到与参数名称“rf”匹配的参数。"), true},
		{"permission-denied", errors.New("rm: cannot remove '/x': Permission denied"), false},
		{"timeout", errors.New("command timed out after 30m0s"), false},
		{"canceled", context.Canceled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rmUnavailableForFallback(tc.err); got != tc.want {
				t.Fatalf("rmUnavailableForFallback(%v)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// localPathToSFTPPath 把本机路径转成 localsftp 测试服务认的 SFTP 路径，
// 映射规则与 localsftp.WindowsPathMapper 保持一致。
func localPathToSFTPPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	slash := filepath.ToSlash(p)
	if len(slash) >= 2 && slash[1] == ':' {
		return "/" + strings.ToLower(slash[:1]) + slash[2:]
	}
	return "/" + strings.TrimPrefix(slash, "/")
}

// newLocalSFTPForTest 起一个 localsftp 测试服务，返回直接操作真实磁盘的 SFTP 客户端，
// 用来验证「shell 删除没生效后回退 SFTP」这条路径真的能把东西删掉。
func newLocalSFTPForTest(t *testing.T) *sftp.Client {
	t.Helper()
	server, sshClient, sftpClient, err := localsftp.Start(localsftp.WindowsPathMapper(), localsftp.ListRoot)
	if err != nil {
		t.Fatalf("启动本地 SFTP 测试服务失败: %v", err)
	}
	t.Cleanup(func() {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		_ = server.Close()
	})
	return sftpClient
}

// newSilentRmManager 构造一个 manager：exec 永远成功返回（模拟 Windows cmd 下
// `rm -rf 'C:\dir'` 的静默成功），SFTP 指向真实的本地磁盘。
func newSilentRmManager(t *testing.T) (*SSHManager, *recordedExec) {
	t.Helper()
	sshClient, recorder := newRecordingSSHClient(t, 0)
	manager := NewSSHManager()
	manager.clients["conn-1"] = &sshClientEntry{Client: sshClient, SFTP: newLocalSFTPForTest(t)}
	manager.sessions["session-1"] = &SessionData{ConnKey: "conn-1"}
	return manager, recorder
}

func prepareDeleteTarget(t *testing.T) (localPath string, remotePath string) {
	t.Helper()
	localPath = filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(filepath.Join(localPath, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localPath, "nested", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return localPath, localPathToSFTPPath(localPath)
}

// TestDeleteItemContextFallsBackWhenRmSilentlySucceeds 回归 Windows coreutils 场景：
// cmd.exe 不解析 POSIX 单引号，`rm -rf 'C:\dir'` 拿到的是带引号的字面量路径，
// -f 让它「静默成功」（退出码 0、什么都没删）。只靠退出码/错误文本发现不了，
// 必须用 SFTP 复核目标是否仍在，再回退 RemoveAll。
func TestDeleteItemContextFallsBackWhenRmSilentlySucceeds(t *testing.T) {
	manager, recorder := newSilentRmManager(t)
	localPath, remotePath := prepareDeleteTarget(t)

	if err := manager.DeleteItemContext(context.Background(), "session-1", remotePath, true); err != nil {
		t.Fatalf("rm 静默成功时应回退 SFTP 删除: %v", err)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("目标目录应已被删除，stat=%v", err)
	}
	if len(recorder.all()) != 1 {
		t.Fatalf("应先尝试一次 rm -rf，实际: %v", recorder.all())
	}
}

// TestDeleteItemShellContextFallsBackWhenRmSilentlySucceeds 验证文件管理器实际走的
// 删除入口（DeleteItemShell）也有同样的复核/回退，而不是只修了 DeleteItemContext。
func TestDeleteItemShellContextFallsBackWhenRmSilentlySucceeds(t *testing.T) {
	manager, recorder := newSilentRmManager(t)
	localPath, remotePath := prepareDeleteTarget(t)

	if err := manager.DeleteItemShellContext(context.Background(), "session-1", remotePath); err != nil {
		t.Fatalf("rm 静默成功时应回退 SFTP 删除: %v", err)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("目标目录应已被删除，stat=%v", err)
	}
	if len(recorder.all()) != 1 {
		t.Fatalf("应先尝试一次 rm -rf，实际: %v", recorder.all())
	}
}

// TestRemoveRemotePathsSkipsShellOnWindowsRemote 验证 banner 判定为 Windows 时
// 完全不执行 POSIX shell 命令：cmd 下单引号会失效，路径里的 & | 还会被当成
// 命令分隔符（注入风险），所以直接走 SFTP。
func TestRemoveRemotePathsSkipsShellOnWindowsRemote(t *testing.T) {
	manager, recorder := newSilentRmManager(t)
	localPath, remotePath := prepareDeleteTarget(t)
	client, sftpClient, err := manager.GetClientEntry("session-1")
	if err != nil {
		t.Fatal(err)
	}

	err = manager.removeRemotePaths(context.Background(), "session-1", client, sftpClient, rmRfCmd(remotePath), []string{remotePath}, true)
	if err != nil {
		t.Fatalf("Windows 远端应直接走 SFTP 删除: %v", err)
	}
	if _, statErr := os.Stat(localPath); !os.IsNotExist(statErr) {
		t.Fatalf("目标目录应已被删除，stat=%v", statErr)
	}
	if commands := recorder.all(); len(commands) != 0 {
		t.Fatalf("Windows 远端不应执行 shell 命令，实际: %v", commands)
	}
}

// TestIsWindowsServerVersion 覆盖 Windows OpenSSH 的 banner 判定。
func TestIsWindowsServerVersion(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"SSH-2.0-OpenSSH_for_Windows_9.5", true},
		{"SSH-2.0-OpenSSH_for_Windows_8.6", true},
		{"SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13", false},
		{"SSH-2.0-dropbear_2022.83", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isWindowsServerVersion([]byte(tc.version)); got != tc.want {
			t.Fatalf("isWindowsServerVersion(%q)=%v, want %v", tc.version, got, tc.want)
		}
	}
}

// TestRemotePathRemovedBoundedTimesOut 回归：共享 SFTP client 没有看门狗，复核用的
// Lstat 卡住时删除调用不能被拖住；超时按「无法复核」处理（保留相信 rm 的旧行为）。
func TestRemotePathRemovedBoundedTimesOut(t *testing.T) {
	started := time.Now()
	removed, verifiable := remotePathRemovedBounded(context.Background(), func() (bool, bool) {
		time.Sleep(3 * time.Second)
		return false, true
	}, 100*time.Millisecond)
	if removed || verifiable {
		t.Fatalf("复核超时应返回无法复核，实际 removed=%v verifiable=%v", removed, verifiable)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("复核超时未及时返回，耗时 %v", elapsed)
	}
}

// TestRemotePathRemovedBoundedCancel 验证复核阶段响应取消，不会被卡住的 Lstat 拖住。
func TestRemotePathRemovedBoundedCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	removed, verifiable := remotePathRemovedBounded(ctx, func() (bool, bool) {
		time.Sleep(3 * time.Second)
		return false, true
	}, 5*time.Second)
	if removed || verifiable {
		t.Fatalf("取消后应返回无法复核，实际 removed=%v verifiable=%v", removed, verifiable)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("取消后复核未及时返回，耗时 %v", elapsed)
	}
}
