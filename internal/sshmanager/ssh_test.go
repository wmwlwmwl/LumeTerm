package sshmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestBuildShellLaunchCommandSkipsMarkersInMultiplexers(t *testing.T) {
	command, historyActive := buildShellLaunchCommand("/bin/bash", "")
	if !historyActive {
		t.Fatal("bash 会话应启用远端历史钩子")
	}
	guard := `case "${TERM:-}" in screen*|tmux*)`
	guardIndex := strings.Index(command, guard)
	markerIndex := strings.Index(command, "LUMETERM_CMD")
	if guardIndex < 0 || markerIndex < 0 || guardIndex > markerIndex {
		t.Fatalf("screen/tmux 保护应包住内部标记: %q", command)
	}
	if !strings.Contains(command, `LUMETERM_OLD_PROMPT_COMMAND`) {
		t.Fatal("跳过内部标记时仍应保留原 PROMPT_COMMAND")
	}
}

func TestParseProbeOutputSkipsLocalizedDFHeader(t *testing.T) {
	out := strings.Join([]string{
		"1 0 0",
		"---LOAD---",
		"0 0 0",
		"---MEM---",
		"MemTotal: 1024 kB",
		"MemFree: 512 kB",
		"---DF---",
		"文件系统 1K-块 已用 可用 已用% 挂载点",
		"伪设备 0 0 0 0 挂载点",
		"/dev/sda1 1048576 524288 524288 50% /",
		"---CPU1---",
		"cpu 1 0 1 8 0",
		"---NET1---",
		"---NETCONN1---",
		"---DISKIO1---",
		"---PROC1---",
		"1000",
		"---CPU2---",
		"cpu 2 0 1 9 0",
		"---NET2---",
		"---NETCONN2---",
		"---DISKIO2---",
		"---PROC2---",
		"1001",
		"---DONE---",
	}, "\n")

	result, err := parseProbeOutput(out, false)
	if err != nil {
		t.Fatalf("解析磁盘数据失败: %v", err)
	}
	disk := result["disk"].(map[string]interface{})
	partitions := disk["partitions"].([]map[string]interface{})
	if len(partitions) != 1 || partitions[0]["mount"] != "/" {
		t.Fatalf("本地化 df 表头不应生成伪分区: %#v", partitions)
	}
}

func TestHostKeyAlgorithmsForConnection(t *testing.T) {
	defaultAlgorithms := hostKeyAlgorithmsForConnection(Connection{})
	if slices.Contains(defaultAlgorithms, ssh.KeyAlgoRSA) {
		t.Fatal("默认连接不应启用旧版 ssh-rsa 主机密钥算法")
	}

	legacyAlgorithms := hostKeyAlgorithmsForConnection(Connection{AllowLegacySSHRSA: true})
	if legacyAlgorithms[len(legacyAlgorithms)-1] != ssh.KeyAlgoRSA {
		t.Fatalf("启用兼容选项后应在现代算法之后追加 ssh-rsa，实际为 %#v", legacyAlgorithms)
	}
	if slices.Contains(sshHostKeyAlgorithms, ssh.KeyAlgoRSA) {
		t.Fatal("单服务器兼容选项不应修改全局默认算法")
	}
}

func newTestSSHClient(t *testing.T, reply *bool) (*ssh.Client, net.Conn) {
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
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		server, channels, requests, serverErr := ssh.NewServerConn(conn, serverConfig)
		if serverErr != nil {
			conn.Close()
			return
		}
		defer server.Close()
		go func() {
			for newChannel := range channels {
				if newChannel.ChannelType() != "session" {
					newChannel.Reject(ssh.UnknownChannelType, "测试服务不支持通道")
					continue
				}
				channel, channelRequests, acceptErr := newChannel.Accept()
				if acceptErr != nil {
					continue
				}
				go func() {
					defer channel.Close()
					for request := range channelRequests {
						_ = request.Reply(true, nil)
						if request.Type == "shell" || request.Type == "exec" {
							return
						}
					}
				}()
			}
		}()
		for request := range requests {
			if reply != nil {
				_ = request.Reply(*reply, nil)
			}
		}
	}()
	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	config := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second,
	}
	sshConn, channels, requests, err := ssh.NewClientConn(clientConn, listener.Addr().String(), config)
	if err != nil {
		clientConn.Close()
		listener.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(sshConn, channels, requests)
	t.Cleanup(func() {
		client.Close()
		clientConn.Close()
		listener.Close()
		select {
		case <-serverDone:
		case <-time.After(time.Second):
			t.Error("测试 SSH 服务未退出")
		}
	})
	return client, clientConn
}

func TestClientKeepaliveTimeoutDoesNotCleanOnSingleFailure(t *testing.T) {
	client, netConn := newTestSSHClient(t, nil)
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{Client: client, NetConn: netConn}
	manager.connTerminals["server"] = []string{"terminal-1", "terminal-2"}
	manager.sessions["terminal-1"] = &SessionData{ConnKey: "server"}
	manager.sessions["terminal-2"] = &SessionData{ConnKey: "server"}

	started := time.Now()
	tracked, probeOK := manager.checkClientKeepalive("server", client, 50*time.Millisecond)
	if time.Since(started) > time.Second {
		t.Fatal("保活超时未在限定时间内结束")
	}
	if !tracked || probeOK {
		t.Fatalf("无响应超时应 tracked=true probeOK=false，实际 tracked=%v probeOK=%v", tracked, probeOK)
	}
	fails, stop := manager.handleKeepaliveProbeResult("server", client, 0, tracked, probeOK)
	if stop || fails != 1 {
		t.Fatalf("单次失败不应拆线: fails=%d stop=%v", fails, stop)
	}
	if len(manager.clients) != 1 || len(manager.sessions) != 2 {
		t.Fatalf("单次超时不应清理连接: clients=%d sessions=%d", len(manager.clients), len(manager.sessions))
	}
}

func TestClientKeepaliveConsecutiveFailuresCleanSharedConnection(t *testing.T) {
	client, netConn := newTestSSHClient(t, nil)
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{Client: client, NetConn: netConn}
	manager.connTerminals["server"] = []string{"terminal-1", "terminal-2"}
	manager.sessions["terminal-1"] = &SessionData{ConnKey: "server"}
	manager.sessions["terminal-2"] = &SessionData{ConnKey: "server"}

	fails := 0
	for i := 0; i < sshKeepaliveFailMax; i++ {
		tracked, probeOK := manager.checkClientKeepalive("server", client, 50*time.Millisecond)
		if !tracked || probeOK {
			t.Fatalf("第 %d 次超时期望 tracked=true probeOK=false", i+1)
		}
		var stop bool
		fails, stop = manager.handleKeepaliveProbeResult("server", client, fails, tracked, probeOK)
		if i < sshKeepaliveFailMax-1 {
			if stop {
				t.Fatalf("第 %d 次失败不应结束 watch", i+1)
			}
			continue
		}
		if !stop || fails < sshKeepaliveFailMax {
			t.Fatalf("达到失败阈值应拆线: fails=%d stop=%v", fails, stop)
		}
	}
	if len(manager.clients) != 0 || len(manager.connTerminals) != 0 || len(manager.sessions) != 0 {
		t.Fatalf("连接清理不完整: clients=%d terminals=%d sessions=%d", len(manager.clients), len(manager.connTerminals), len(manager.sessions))
	}
}

func TestHandleKeepaliveProbeResultResetsOnSuccess(t *testing.T) {
	manager := NewSSHManager()
	fails, stop := manager.handleKeepaliveProbeResult("server", nil, 2, true, true)
	if stop || fails != 0 {
		t.Fatalf("探活成功应清零失败计数: fails=%d stop=%v", fails, stop)
	}
	fails, stop = manager.handleKeepaliveProbeResult("server", nil, 1, false, false)
	if !stop {
		t.Fatal("未跟踪的 client 应结束 watch")
	}
	_ = fails
}

func TestClientKeepaliveRejectionKeepsConnection(t *testing.T) {
	reply := false
	client, netConn := newTestSSHClient(t, &reply)
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{Client: client, NetConn: netConn}
	manager.connTerminals["server"] = []string{"terminal"}
	manager.sessions["terminal"] = &SessionData{ConnKey: "server"}

	tracked, probeOK := manager.checkClientKeepalive("server", client, time.Second)
	if !tracked || !probeOK {
		t.Fatalf("服务端拒绝未知保活请求仍应证明 SSH 传输存活: tracked=%v probeOK=%v", tracked, probeOK)
	}
	fails, stop := manager.handleKeepaliveProbeResult("server", client, 2, tracked, probeOK)
	if stop || fails != 0 {
		t.Fatalf("成功探活后不应拆线: fails=%d stop=%v", fails, stop)
	}
	if manager.clients["server"].Client != client || manager.sessions["terminal"] == nil {
		t.Fatal("成功收到保活响应后不应清理连接")
	}
}

func TestSessionWaitDisconnectsSession(t *testing.T) {
	reply := false
	client, netConn := newTestSSHClient(t, &reply)
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{Client: client, NetConn: netConn}
	manager.connTerminals["server"] = []string{}
	if err := manager.setupSession(context.Background(), client, "server", "terminal", "", "", false, "", "", ""); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.RLock()
		_, exists := manager.sessions["terminal"]
		manager.mu.RUnlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("远端 session 结束后未及时清理终端")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStaleSessionWaitKeepsReplacement(t *testing.T) {
	manager := NewSSHManager()
	oldSession := &ssh.Session{}
	newData := &SessionData{ConnKey: "server", Session: &ssh.Session{}}
	manager.sessions["terminal"] = newData
	manager.connTerminals["server"] = []string{"terminal"}

	manager.disconnectAndNotify("terminal", oldSession, "session_end")

	if manager.sessions["terminal"] != newData {
		t.Fatal("旧 session.Wait 的迟到清理不应删除快速重连后的新 session")
	}
}

func TestStaleClientCleanupKeepsReplacement(t *testing.T) {
	manager := NewSSHManager()
	oldClient := &ssh.Client{}
	newClient := &ssh.Client{}
	manager.clients["server"] = &sshClientEntry{Client: newClient}
	manager.connTerminals["server"] = []string{"terminal"}
	manager.sessions["terminal"] = &SessionData{ConnKey: "server"}

	manager.cleanupClientTransport("server", oldClient, "transport")
	if manager.clients["server"].Client != newClient || manager.sessions["terminal"] == nil {
		t.Fatal("旧连接的迟到清理不应删除快速重连后的新连接")
	}
}

func TestDisconnectConnectionClosesAllTerminals(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{NetConn: clientConn}
	manager.sessions["root"] = &SessionData{ConnKey: "server"}
	manager.sessions["child"] = &SessionData{ConnKey: "server", GroupSessionId: "root"}
	manager.connTerminals["server"] = []string{"root", "child"}

	manager.DisconnectConnection("root", []string{"root", "child"})

	if len(manager.sessions) != 0 || len(manager.clients) != 0 || len(manager.connTerminals) != 0 {
		t.Fatalf("关闭连接后仍有资源: sessions=%d clients=%d connTerminals=%d", len(manager.sessions), len(manager.clients), len(manager.connTerminals))
	}
	if _, err := serverConn.Read(make([]byte, 1)); err == nil {
		t.Fatal("关闭连接后底层 SSH transport 仍可读取")
	}
}

// TestDisconnectConnectionCleansOrphanedTerminals 复现「根终端已关闭、子终端成为孤儿」的泄漏：
// 用户在根终端标签上点 X（或根 shell 自然退出）时后端只删了根 session，
// connTerminals 里残留子终端；随后会话级关闭只携带根 id，后端因根 session 已不在
// map 中而整体 no-op，子终端与共享 client 永久泄漏，导致每次重新进入同一服务器
// 通道占用恒多 1 个。
func TestDisconnectConnectionCleansOrphanedTerminals(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{NetConn: clientConn}
	// 根终端已被关闭（m.sessions 无 root），但子终端与共享连接仍存活
	manager.sessions["child"] = &SessionData{ConnKey: "server", GroupSessionId: "root"}
	manager.connTerminals["server"] = []string{"child"}

	// 前端仍持有该会话，关闭时传入全部终端 id（含已不存在的根 id）
	manager.DisconnectConnection("root", []string{"root", "child"})

	if len(manager.sessions) != 0 || len(manager.clients) != 0 || len(manager.connTerminals) != 0 {
		t.Fatalf("关闭会话后仍有资源: sessions=%d clients=%d connTerminals=%d", len(manager.sessions), len(manager.clients), len(manager.connTerminals))
	}
	if _, err := serverConn.Read(make([]byte, 1)); err == nil {
		t.Fatal("关闭会话后底层 SSH transport 仍可读取")
	}
}

// TestDisconnectConnectionCancelsInFlightConnect 复现「连接中取消」泄漏：
// session 尚未登记进 m.sessions（Connect 正处在 dial/握手/认证，如密码弹窗），
// 前端点取消走 DisconnectConnection —— targets 为空会整体 no-op，在途 Connect
// 完成后永久登记。兜底必须取消 pendingCancels 里的 cancel，并清理孤儿终端。
func TestDisconnectConnectionCancelsInFlightConnect(t *testing.T) {
	manager := NewSSHManager()
	cancelCtx, cancel := context.WithCancel(context.Background())
	manager.pendingMu.Lock()
	manager.pendingCancels["connecting"] = cancel
	manager.pendingMu.Unlock()
	// 子终端已登记而根终端未登记（孤儿场景）也应被 terminalIds 兜底清理
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	manager.clients["server"] = &sshClientEntry{NetConn: clientConn}
	manager.sessions["orphan-child"] = &SessionData{ConnKey: "server", GroupSessionId: "connecting"}
	manager.connTerminals["server"] = []string{"orphan-child"}

	manager.DisconnectConnection("connecting", []string{"connecting", "orphan-child"})

	select {
	case <-cancelCtx.Done():
	default:
		t.Fatal("取消连接中会话应触发 pendingCancels 取消")
	}
	if len(manager.sessions) != 0 || len(manager.clients) != 0 || len(manager.connTerminals) != 0 {
		t.Fatalf("取消后仍有资源: sessions=%d clients=%d connTerminals=%d", len(manager.sessions), len(manager.clients), len(manager.connTerminals))
	}
}

func TestDisconnectPreservesSharedClient(t *testing.T) {
	manager := NewSSHManager()
	client := &ssh.Client{}
	manager.clients["server"] = &sshClientEntry{Client: client}
	manager.connTerminals["server"] = []string{"terminal-1", "terminal-2"}
	manager.sessions["terminal-1"] = &SessionData{ConnKey: "server"}
	manager.sessions["terminal-2"] = &SessionData{ConnKey: "server"}

	if !manager.Disconnect("terminal-1") {
		t.Fatal("首次断开已登记终端应成功")
	}
	if manager.Disconnect("terminal-1") {
		t.Fatal("重复断开同一终端应保持幂等")
	}
	if manager.clients["server"].Client != client || manager.sessions["terminal-2"] == nil {
		t.Fatal("断开一个终端不应关闭仍被其他终端复用的 client")
	}
}

func TestOutputTapConcurrentClose(t *testing.T) {
	manager := NewSSHManager()
	_, channel, cancel := manager.registerSessionOutputTap("terminal")
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		for i := 0; i < 1000; i++ {
			manager.emitSessionOutput("terminal", []byte("output"))
		}
	}()
	go func() {
		defer wait.Done()
		manager.closeSessionOutputTaps("terminal")
		cancel()
	}()
	wait.Wait()
	for range channel {
	}
}

// 复现「只接受本次 → 密码错误 → 填对密码 → 主机密钥确认二次弹出」。
// Disconnect 会清掉临时密钥授权，但换密码重连仍是同一会话，须跨重连保留。
func TestTempAcceptedKeySurvivesPasswordReconnect(t *testing.T) {
	manager := NewSSHManager()
	const sessionId = "terminal"
	const fingerprint = "SHA256:test-fingerprint"

	// 用户选了「只接受本次」
	manager.RestoreTempAcceptedKey(sessionId, fingerprint)

	// 登记会话让 Disconnect 走到真正的清理分支。留一个兄弟终端占用同一
	// client，避免走到关闭共享连接的分支（空 ssh.Client 无法 Close）。
	manager.clients["server"] = &sshClientEntry{Client: &ssh.Client{}}
	manager.connTerminals["server"] = []string{sessionId, "sibling"}
	manager.sessions[sessionId] = &SessionData{ConnKey: "server"}
	manager.sessions["sibling"] = &SessionData{ConnKey: "server"}

	// ReconnectWithPassword 的做法：Disconnect 前后保存并恢复
	saved, had := manager.TempAcceptedKey(sessionId)
	if !had || saved != fingerprint {
		t.Fatalf("应能读回临时密钥，实际 had=%v fp=%q", had, saved)
	}
	manager.Disconnect(sessionId)

	if _, stillThere := manager.TempAcceptedKey(sessionId); stillThere {
		t.Fatal("Disconnect 应清掉临时密钥（这正是需要跨重连恢复的原因）")
	}
	manager.RestoreTempAcceptedKey(sessionId, saved)

	// 重连后主机密钥校验应仍被临时授权放行，不再二次要求确认
	got, ok := manager.TempAcceptedKey(sessionId)
	if !ok || got != fingerprint {
		t.Fatalf("换密码重连后临时密钥授权应保留，实际 ok=%v fp=%q", ok, got)
	}

	// 重连最终失败（非认证失败）时须清除，避免残留静默绕过校验
	manager.ClearTempAcceptedKey(sessionId)
	if _, leaked := manager.TempAcceptedKey(sessionId); leaked {
		t.Fatal("清除后不应残留临时密钥")
	}
}

// 握手失败的会话从未进入 m.sessions，Disconnect 会在 ok 判断处提前返回。
// 临时密钥与待确认条目必须先于该返回清理，否则「只接受本次 → 密码错误 →
// 取消」这条路径会永久残留条目。
func TestDisconnectClearsTempKeyForNeverEstablishedSession(t *testing.T) {
	manager := NewSSHManager()
	const sessionId = "never-established"

	manager.tempAcceptedKeys[sessionId] = "SHA256:test-fingerprint"
	manager.pendingHostKeys[sessionId] = &PendingHostKey{Hostname: "example.com"}

	// 没有 m.sessions 条目，Disconnect 返回 false（幂等语义不变）
	if manager.Disconnect(sessionId) {
		t.Fatal("未建立的会话断开应返回 false")
	}

	manager.mu.RLock()
	_, tempLeaked := manager.tempAcceptedKeys[sessionId]
	_, pendingLeaked := manager.pendingHostKeys[sessionId]
	manager.mu.RUnlock()

	if tempLeaked {
		t.Fatal("未建立的会话断开后临时密钥泄漏")
	}
	if pendingLeaked {
		t.Fatal("未建立的会话断开后待确认条目泄漏")
	}
}

// 认证失败必须是可判定的 sentinel，否则「只接受本次」会在密码输错时
// 连带丢弃临时授权，导致主机密钥确认二次弹出。
func TestAuthFailedIsIdentifiableSentinel(t *testing.T) {
	if !errors.Is(ErrAuthFailed, ErrAuthFailed) {
		t.Fatal("ErrAuthFailed 应可被 errors.Is 判定")
	}
	// 前端按文案 includes('认证失败') 分流，文案不能改
	if ErrAuthFailed.Error() != "认证失败" {
		t.Fatalf("文案变更会破坏前端认证失败分流，实际 %q", ErrAuthFailed.Error())
	}
	if errors.Is(ErrHostKeyChanged, ErrAuthFailed) {
		t.Fatal("主机密钥变更不应被判定为认证失败")
	}
}

// ─── 端到端连接生命周期测试 ─────────────────────────────────────

// newCycleTestServer 起一个接受多次连接的测试 SSH 服务器（NoClientAuth）。
// 每次连接的 session 通道：exec 请求回复 true 后关闭通道（命令立即结束）；
// shell 请求回复 true 并保持通道打开（模拟 shell 常驻）；sftp 子系统直接拒绝，
// 让 initSFTPClient 快速失败（测试只关心终端通道计数）。
func newCycleTestServer(t *testing.T) (host string, port int, hostKeyLine string, cleanup func()) {
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
	addr := listener.Addr().(*net.TCPAddr)
	host, port = addr.IP.String(), addr.Port
	hostKeyLine = knownhosts.Line([]string{dialAddr(host, port)}, signer.PublicKey())

	go func() {
		// 不限连接数：Connect 对瞬态错误会重试重拨，预算耗尽会误报被测代码泄漏
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				server, channels, requests, serverErr := ssh.NewServerConn(conn, serverConfig)
				if serverErr != nil {
					conn.Close()
					return
				}
				defer server.Close()
				go func() {
					for newChannel := range channels {
						if newChannel.ChannelType() != "session" {
							newChannel.Reject(ssh.UnknownChannelType, "测试服务不支持通道")
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
									// 命令立即结束：回复 true，补发 exit-status 再关闭通道。
									// 缺 exit-status 时客户端 session.Wait 返回
									// "remote command exited without exit status or exit signal"，
									// 会让 waitForCommandChannel 误判命令通道不可用。
									_ = request.Reply(true, nil)
									_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
									return
								case "shell":
									// shell 常驻：回复 true，保持通道打开
									_ = request.Reply(true, nil)
								default:
									_ = request.Reply(true, nil)
								}
							}
						}()
					}
				}()
				// 回复全局请求（含 keepalive@openssh.com），避免探活超时误拆连接
				for request := range requests {
					if request.WantReply {
						_ = request.Reply(true, nil)
					}
				}
			}()
		}
	}()
	cleanup = func() {
		listener.Close()
	}
	return host, port, hostKeyLine, cleanup
}

// setupCycleTestManager 准备可驱动完整 Connect 的 SSHManager：
// known_hosts 预置测试服务器公钥（Connect 走 initKnownHostsCallback）。
func setupCycleTestManager(t *testing.T, host string, port int, hostKeyLine string) *SSHManager {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp) // Windows
	t.Setenv("HOME", tmp)        // Unix
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".ssh", "known_hosts"), []byte(hostKeyLine+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return NewSSHManager()
}

// TestConnectDisconnectCycleTerminalCount 模拟用户反复「进入 → 退出」同一服务器：
// 每次进入 Connect 应恰好登记 1 个终端，每次退出（会话级关闭）应清零，
// 通道占用不能出现 1↔2 循环。
func TestConnectDisconnectCycleTerminalCount(t *testing.T) {
	host, port, hostKeyLine, cleanup := newCycleTestServer(t)
	defer cleanup()
	manager := setupCycleTestManager(t, host, port, hostKeyLine)
	conn := Connection{ID: "server-1", Username: "test", Host: host, Port: port, AuthMethod: ""}

	sid := "session_1"
	for i := 0; i < 4; i++ {
		if err := manager.Connect(sid, conn); err != nil {
			t.Fatalf("第 %d 次进入失败: %v", i+1, err)
		}
		if got := len(manager.connTerminals[conn.ID]); got != 1 {
			t.Fatalf("第 %d 次进入后终端数 = %d，期望 1", i+1, got)
		}
		// 退出：与前端 forceCloseSession 一致，会话级关闭
		manager.DisconnectConnection(sid, []string{sid})
		if got := len(manager.connTerminals[conn.ID]); got != 0 {
			t.Fatalf("第 %d 次退出后终端数 = %d，期望 0", i+1, got)
		}
		if len(manager.clients) != 0 || len(manager.sessions) != 0 {
			t.Fatalf("第 %d 次退出后仍有资源: clients=%d sessions=%d", i+1, len(manager.clients), len(manager.sessions))
		}
	}
}

// TestConnectDisconnectCycleViaTerminalClose 模拟用户通过「终端标签 X」关闭：
// 单终端会话走 DisconnectSSH（与 forceCloseSession 不同路径），同样必须清零。
func TestConnectDisconnectCycleViaTerminalClose(t *testing.T) {
	host, port, hostKeyLine, cleanup := newCycleTestServer(t)
	defer cleanup()
	manager := setupCycleTestManager(t, host, port, hostKeyLine)
	conn := Connection{ID: "server-2", Username: "test", Host: host, Port: port, AuthMethod: ""}

	sid := "session_2"
	for i := 0; i < 4; i++ {
		if err := manager.Connect(sid, conn); err != nil {
			t.Fatalf("第 %d 次进入失败: %v", i+1, err)
		}
		if got := len(manager.connTerminals[conn.ID]); got != 1 {
			t.Fatalf("第 %d 次进入后终端数 = %d，期望 1", i+1, got)
		}
		// 退出：前端 closeTerminal 对单终端会话调用 DisconnectSSH
		manager.Disconnect(sid)
		if got := len(manager.connTerminals[conn.ID]); got != 0 {
			t.Fatalf("第 %d 次退出后终端数 = %d，期望 0", i+1, got)
		}
	}
}

// TestConnectDisconnectCycleConcurrent 模拟用户快速「开关」：Connect 与
// DisconnectConnection 并发交错执行。真实应用里 Wails 绑定在主 goroutine 串行，
// 并发仅作压力测试——竞争下 Connect 可能瞬时失败（另一个会话关闭正在关闭共享
// transport），这是可接受的瞬时错误；但结束后不得残留终端/客户端（泄漏）。
func TestConnectDisconnectCycleConcurrent(t *testing.T) {
	host, port, hostKeyLine, cleanup := newCycleTestServer(t)
	defer cleanup()
	manager := setupCycleTestManager(t, host, port, hostKeyLine)
	conn := Connection{ID: "server-3", Username: "test", Host: host, Port: port, AuthMethod: ""}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		sid := fmt.Sprintf("session_%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.Connect(sid, conn); err != nil {
				return // 竞争下的瞬时错误可接受，不视为失败
			}
			manager.DisconnectConnection(sid, []string{sid})
		}()
	}
	wg.Wait()

	// 兜底清理可能残留的会话（失败的 Connect 不应留任何状态）
	manager.DisconnectAll()

	manager.mu.RLock()
	clients, sessions, terminals := len(manager.clients), len(manager.sessions), len(manager.connTerminals[conn.ID])
	manager.mu.RUnlock()
	if clients != 0 || sessions != 0 || terminals != 0 {
		t.Fatalf("并发开关后仍有资源: clients=%d sessions=%d terminals=%d", clients, sessions, terminals)
	}
}

// TestSetupSessionIdempotentRegistration 工作区恢复与手动进入并发时，同一
// sessionId 可能被 setupSession 重复登记：幂等化后 connTerminals 不得出现
// 重复 id，旧通道被释放。
func TestSetupSessionIdempotentRegistration(t *testing.T) {
	host, port, hostKeyLine, cleanup := newCycleTestServer(t)
	defer cleanup()
	manager := setupCycleTestManager(t, host, port, hostKeyLine)
	conn := Connection{ID: "server-4", Username: "test", Host: host, Port: port, AuthMethod: ""}

	// 第一次进入
	if err := manager.Connect("term", conn); err != nil {
		t.Fatal(err)
	}
	// 模拟恢复与手动进入竞态：同 id 不先断开再次 Connect
	if err := manager.Connect("term", conn); err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	terminals := manager.connTerminals[conn.ID]
	manager.mu.RUnlock()
	if len(terminals) != 1 || terminals[0] != "term" {
		t.Fatalf("同 id 重复登记后 connTerminals 应为 [term]，实际 %v", terminals)
	}
	// 断开后必须清零
	manager.DisconnectConnection("term", []string{"term"})
	manager.mu.RLock()
	clients, sessions, terms := len(manager.clients), len(manager.sessions), len(manager.connTerminals[conn.ID])
	manager.mu.RUnlock()
	if clients != 0 || sessions != 0 || terms != 0 {
		t.Fatalf("断开后仍有资源: clients=%d sessions=%d terminals=%d", clients, sessions, terms)
	}
}

// TestDisconnectRemovesAllDuplicateIds 历史竞态遗留的重复 id（[X, X]）在
// 断开时必须全部移除，不能残留半个。
func TestDisconnectRemovesAllDuplicateIds(t *testing.T) {
	manager := NewSSHManager()
	manager.clients["server"] = &sshClientEntry{}
	manager.sessions["term"] = &SessionData{ConnKey: "server"}
	manager.connTerminals["server"] = []string{"term", "term"}

	if !manager.Disconnect("term") {
		t.Fatal("断开已登记终端应成功")
	}
	manager.mu.RLock()
	terms := len(manager.connTerminals["server"])
	manager.mu.RUnlock()
	if terms != 0 {
		t.Fatalf("断开后 connTerminals 应为空，实际 %d 个残留", terms)
	}
}
