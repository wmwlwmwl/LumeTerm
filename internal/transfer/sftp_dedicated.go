package transfer

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// OpenDedicatedSFTPClient 在独立 SSH session 上打开私有 SFTP 通道。
//
// 为什么需要：共享 SFTP client 被下载/编辑读文件/文件管理共用，一旦某次 Read
// 卡住（服务端活着但传输卡死），没有超时参数可打断；关闭共享 client 又会误伤
// 终端与其它传输。专用通道的 closeFn 会先关闭 SSH session，让阻塞中的
// Read/ReadDir 立刻返回错误。
func OpenDedicatedSFTPClient(sshClient *ssh.Client, tuning Tuning) (*sftp.Client, func(), error) {
	return openDedicatedSFTPClientWithOptions(sshClient, SharedSFTPClientOptions(tuning))
}

// openDedicatedSFTPClientWithOptions 与 OpenDedicatedSFTPClient 相同，但由调用方
// 决定 sftp 选项：下载/读文件用 SharedSFTPClientOptions（32KB 读安全包长），
// 上传池用 buildTunedSFTPOptions（写方向可放大包长）。
//
// 打开过程有上限：NewSession()/RequestSubsystem() 都是同步调用，库不接受 ctx、
// 也没有超时，服务端卡住或 channel open 得不到确认时会永久阻塞，而 stallguard
// 只在通道打开成功后才创建，覆盖不到这个阶段。超时后返回错误，调用方会回退共享
// client（失去看门狗但保持可用）。
func openDedicatedSFTPClientWithOptions(sshClient *ssh.Client, options []sftp.ClientOption) (*sftp.Client, func(), error) {
	return openDedicatedSFTPClientWithTimeout(sshClient, options, dedicatedSFTPOpenTimeout)
}

// dedicatedSFTPOpenTimeout 是一次专用通道打开的上限。
const dedicatedSFTPOpenTimeout = 30 * time.Second

// openDedicatedSFTPClientWithTimeout 是带可配置超时的版本（测试用）。
func openDedicatedSFTPClientWithTimeout(sshClient *ssh.Client, options []sftp.ClientOption, timeout time.Duration) (*sftp.Client, func(), error) {
	if sshClient == nil {
		return nil, nil, fmt.Errorf("ssh client unavailable")
	}
	if timeout <= 0 {
		timeout = dedicatedSFTPOpenTimeout
	}
	type openResult struct {
		client  *sftp.Client
		closeFn func()
		err     error
	}
	// 与 sshmanager.ExecuteCmdWithClientContextTimeout 同一套路：无缓冲 channel +
	// abandon，保证「超时返回」与「后台稍后打开成功」不会同时发生；后者在
	// abandon 分支里自行关闭，避免 session/client 无人接收而泄漏。
	resultCh := make(chan openResult)
	abandon := make(chan struct{})
	// sessionCh 让后台在 NewSession 成功后立刻交出 session：超时路径只要拿到它
	// 就能 Close，从而唤醒卡在 RequestSubsystem 上的后台 goroutine。否则每次
	// 超时都会连带遗留一个 goroutine 和一个已打开的 session（连接还活着时不会自愈）。
	sessionCh := make(chan *ssh.Session, 1)
	go func() {
		client, closeFn, err := openDedicatedSFTPClientBlocking(sshClient, options, sessionCh, abandon)
		select {
		case resultCh <- openResult{client: client, closeFn: closeFn, err: err}:
		case <-abandon:
			if closeFn != nil {
				closeFn()
			} else if client != nil {
				_ = client.Close()
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-resultCh:
		return res.client, res.closeFn, res.err
	case <-timer.C:
		// 超时与完成可能同时就绪：先尝试收结果，收到就按成功返回。
		select {
		case res := <-resultCh:
			return res.client, res.closeFn, res.err
		default:
		}
		close(abandon)
		closeStalledSession(sessionCh)
		return nil, nil, fmt.Errorf("open dedicated sftp timed out after %v", timeout)
	}
}

// closeStalledSession 在超时路径里收走后台已创建的 session 并关闭。
// 只等很短时间：session 是在 NewSession 返回后立刻发布的，正常情况下要么已经在
// channel 里，要么后台还卡在 NewSession（此时没有 session 可关，只能等连接被拆）。
func closeStalledSession(sessionCh <-chan *ssh.Session) {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case session := <-sessionCh:
		if session != nil {
			_ = session.Close()
		}
	case <-timer.C:
	}
}

// openDedicatedSFTPClientBlocking 是原来的同步实现：建 session、开 sftp subsystem、
// 包出 closeFn。调用方必须保证它不会永久阻塞（见上面的超时包一层）。
//
// sessionCh/abandon 用于让超时路径能接管：session 一建出来就发布，之后每步都会
// 检查 abandon，避免超时后还继续占用 channel。
func openDedicatedSFTPClientBlocking(sshClient *ssh.Client, options []sftp.ClientOption, sessionCh chan<- *ssh.Session, abandon <-chan struct{}) (*sftp.Client, func(), error) {
	if sshClient == nil {
		return nil, nil, fmt.Errorf("ssh client unavailable")
	}
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("open dedicated sftp session: %w", err)
	}
	select {
	case sessionCh <- session:
	default:
	}
	select {
	case <-abandon:
		_ = session.Close()
		return nil, nil, fmt.Errorf("open dedicated sftp abandoned")
	default:
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("dedicated sftp stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("dedicated sftp stdout: %w", err)
	}
	if err := session.RequestSubsystem("sftp"); err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("request sftp subsystem: %w", err)
	}
	client, err := sftp.NewClientPipe(stdout, stdin, options...)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("open dedicated sftp client: %w", err)
	}

	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			// 顺序是关键：必须先发 SSH_MSG_CHANNEL_CLOSE（session.Close）。
			// 对端 sshd 的传输层会回 CLOSE，本地 mux 随即关闭 channel，唤醒
			// 阻塞在 Read/ReadDir 的 goroutine；反过来先 client.Close 的话，
			// pkg/sftp 会阻塞在等待 recv goroutine 的 wg.Wait 上（关闭 stdin
			// 对卡死的 sftp-server 无效），session.Close 永远执行不到，
			// 看门狗和调用方都会永久挂起。
			//
			// 全程异步：closeFn 会被看门狗 goroutine 与调用方 defer 并发调用，
			// 任何同步阻塞都可能卡死调用方，甚至让 sync.Once 永久占用。
			go func() {
				_ = session.Close()
				_ = client.Close()
			}()
		})
	}
	return client, closeFn, nil
}

// openDedicatedSFTP 为单次传输打开专用 SFTP 通道（使用当前调优参数）。
func (s *Service) openDedicatedSFTP(sessionID string) (*sftp.Client, func(), error) {
	sshClient, _, err := s.backend.ClientEntry(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return OpenDedicatedSFTPClient(sshClient, s.Tuning())
}

// openDownloadSFTP 优先使用专用通道；服务器 MaxSessions 紧张/拒绝新 channel 时
// 回退共享 client（保持旧行为，但失去看门狗保护）。dedicated=false 时调用方不启动看门狗。
func (s *Service) openDownloadSFTP(sessionID string) (*sftp.Client, func(), bool, error) {
	client, closeFn, err := s.openDedicatedSFTP(sessionID)
	if err == nil {
		return client, closeFn, true, nil
	}
	shared, sharedErr := s.backend.SFTPClient(sessionID)
	if sharedErr != nil {
		return nil, nil, false, fmt.Errorf("%v; shared sftp unavailable: %w", err, sharedErr)
	}
	log.Printf("[transfer] dedicated sftp unavailable, fallback to shared client: %v", err)
	return shared, func() {}, false, nil
}
