package ping

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"lumeterm/internal/config"
	"lumeterm/internal/sshmanager"
)

// 智能模式下，疑似 TUN/代理路径的 Banner 可达性确认最短间隔。
// 高频 tick 仍用 TCP 刷新路径信息；真正半开 SSH 握手按此间隔节流。
const autoBannerVerifyInterval = 30 * time.Second

// isLoopbackHost 仅判断本机回环（localhost / 127.x / ::1）。
// 这类目标一定走本机、不经过 TUN，所以 TUN 判定时要把它们排除掉。
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// normalizePingMode 归一化延迟检测模式。合法值：auto / banner / tcp；非法值统一回退 auto（保持兼容）。
//
//	auto   智能检测：直连只做 TCP；疑似代理/TUN 时 TCP 高频 + Banner 低频确认可达性。
//	banner 强制读 SSH Banner：最准确，能识别 TUN/代理下的不可达服务器，但不可达时要等超时。
//	tcp    强制只看 TCP 端口连通：最快，但在 TUN/代理下可能把不可达服务器误判为在线。
func normalizePingMode(mode string) string {
	switch mode {
	case "auto", "banner", "tcp":
		return mode
	default:
		return "auto"
	}
}

// parseIPFromAddr 从 net.Addr / host:port / 纯 IP 中解析 IP。
func parseIPFromAddr(addr net.Addr) net.IP {
	if addr == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	host = strings.Trim(host, "[]")
	return net.ParseIP(host)
}

// resolveProbeIP 将探测目标解析为 IP。纯 IP 直接用；主机名做一次 Lookup（失败则无法做同子网判断）。
func resolveProbeIP(host string) net.IP {
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" {
		return net.ParseIP("127.0.0.1")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	// 去掉可能的 zone / 方括号。
	host = strings.Trim(host, "[]")
	if i := strings.LastIndex(host, "%"); i >= 0 {
		host = host[:i]
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return nil
	}
	// 优先 IPv4，与 SSH 常见拨号一致。
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4
		}
	}
	return ips[0]
}

// isFakeIP 判断 IP 是否落在 Mihomo/Clash fake-ip 池：198.18.0.0/15。
// 该段是 IANA 保留的基准测试网段，现实网络几乎不会用作真实主机，是 TUN/fake-ip 的强信号。
// 因此同时用于"本机出站地址"和"拨号对端地址"两种判断。
func isFakeIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)
}

// isTUNInterfaceIP 判断 IP 是否像 TUN/代理虚拟网卡的地址。
// 仅用于判断"本机出站网卡地址"：除 fake-ip 段外，Clash Meta 等默认网关 172.19/20 也算。
// 注意：172.19/20 同时是合法 RFC1918 私有地址，故此函数不能用来判断"对端目标"——
// 真实内网服务器完全可能用 172.19/20，那种情况应由 isDirectLocalPath(同子网) 放行。
func isTUNInterfaceIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if isFakeIP(ip) {
		return true
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// Clash Meta 等默认网关段：172.19.0.0/16、172.20.0.0/16（仅作本机网卡信号）。
	return ip4[0] == 172 && (ip4[1] == 19 || ip4[1] == 20)
}

// isTUNLocalAddr 判断"出站拨号所用的本地地址"是否落在常见 TUN/代理虚拟网卡网段。
// 这是识别 TUN 模式最可靠的信号：TUN 网卡会把出站流量劫持到自己的网段。
// 故意不使用 dialMs 作为 TUN 判据：局域网与低延迟公网（如同区域云主机）的 dialMs 也常很小。
func isTUNLocalAddr(local net.Addr) bool {
	return isTUNInterfaceIP(parseIPFromAddr(local))
}

// interfaceAddrsCache 缓存 net.InterfaceAddrs() 的结果。
// 网卡配置极少变动，避免在 auto 模式下每 2 秒、每台主机都做一次系统调用。
var (
	interfaceAddrsMu      sync.Mutex
	interfaceAddrsCache   []net.Addr
	interfaceAddrsCacheAt time.Time
)

// interfaceAddrsTTL 缓存有效期。网卡变更（插网线、连 WiFi、起 TUN）通常远慢于此。
const interfaceAddrsTTL = 30 * time.Second

// cachedInterfaceAddrs 返回带 TTL 缓存的非回环网卡地址列表。
func cachedInterfaceAddrs() []net.Addr {
	interfaceAddrsMu.Lock()
	defer interfaceAddrsMu.Unlock()
	now := time.Now()
	if interfaceAddrsCache != nil && now.Sub(interfaceAddrsCacheAt) < interfaceAddrsTTL {
		return interfaceAddrsCache
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil || len(addrs) == 0 {
		// 失败时若已有旧缓存，沿用旧值（网卡配置大概率没变）。
		if interfaceAddrsCache != nil {
			return interfaceAddrsCache
		}
		return nil
	}
	interfaceAddrsCache = addrs
	interfaceAddrsCacheAt = now
	return addrs
}

// isDirectLocalPath 判断本次拨号是否像「真实网卡直连到同子网目标」。
// 优先用拨号实际对端 IP（remoteIP）判断，避免对主机名做 DNS 查询；
// 仅当 local 是回环但 remoteIP 缺失时，才回退用 targetHost 解析。
// 要求：出站本地 IP 与目标 IP 落在同一非 TUN 网卡子网。用于避免对纯局域网误做 Banner。
func isDirectLocalPath(local net.Addr, remoteIP net.IP, targetHost string) bool {
	localIP := parseIPFromAddr(local)
	if localIP == nil {
		return false
	}
	targetIP := remoteIP
	if targetIP == nil {
		// 仅在拿不到对端 IP 时才回退解析主机名（会触发 DNS）。
		targetIP = resolveProbeIP(targetHost)
	}
	if targetIP == nil {
		return false
	}
	if isTUNInterfaceIP(localIP) {
		return false
	}
	if localIP.IsLoopback() || targetIP.IsLoopback() {
		return true
	}

	for _, addr := range cachedInterfaceAddrs() {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP == nil {
			continue
		}
		if ipnet.IP.IsLoopback() {
			continue
		}
		// 排除 TUN/代理虚拟网卡（含 172.19/20 网关段），避免把 TUN 当成直连网卡。
		if isTUNInterfaceIP(ipnet.IP) {
			continue
		}
		// 忽略 /32 之类过窄掩码，避免把单主机地址误当“同网段直连”。
		ones, bits := ipnet.Mask.Size()
		if bits == 0 || ones >= bits {
			continue
		}
		if ipnet.Contains(localIP) && ipnet.Contains(targetIP) {
			return true
		}
	}
	return false
}

// needsSSHBannerProbe 判断 auto 模式下是否需要走 Banner 可达性确认（可低频节流）。
//
// local/remote 为拨号后 conn.LocalAddr()/RemoteAddr()；remoteIP 为已解析的对端 IP。
// 规则（尽量少误伤）：
//  1. 显式代理 → 需要
//  2. 本机回环目标 → 不需要
//  3. 出站与对端同属真实网卡子网 → 不需要（纯局域网直连）
//  4. 出站本地地址落在已知 TUN 网段（198.18/15 fake-ip 或 172.19/20 网关）→ 需要
//  5. 对端地址落在 fake-ip 段（198.18/15）→ 需要；对端 172.19/20 不算（合法私有网段）
//  6. 其余直连（含低延迟公网）→ 不需要
//
// 注意：172.19/20 既可能是 Clash Meta 的 TUN 网关，也可能是真实内网。为避免误伤，
// 只有"本机出站网卡地址"落该段才判 TUN；"对端目标"落该段不判（由 isDirectLocalPath 同子网放行）。
//
// 局限：仅识别 Mihomo/Clash fake-ip(198.18/15) 与 Clash Meta 网关段(172.19/16、172.20/16)。
// 其他 TUN（Tailscale 100.64/10、WireGuard 10.x、ZeroTier 10.147、OpenVPN 10.8 等）不在列，
// 这类环境下不可达主机可能仍显示 0ms 在线，建议显式选择「SSH Banner RTT」模式。
func needsSSHBannerProbe(usesProxy bool, local, remote net.Addr, remoteIP net.IP, targetHost string) bool {
	if usesProxy {
		return true
	}
	if isLoopbackHost(targetHost) {
		return false
	}
	if isDirectLocalPath(local, remoteIP, targetHost) {
		return false
	}
	if isTUNLocalAddr(local) {
		return true
	}
	// 对端落在 fake-ip 段（198.18/15）：典型 TUN/代理劫持特征，真实拨号对端不会是 fake-ip。
	// 注意：这里只用 isFakeIP，不用 isTUNInterfaceIP——172.19/20 是合法私有网段，
	// 真实内网服务器完全可能用它，若按对端 172.19/20 判 TUN 会误伤内网资产。
	if isFakeIP(remoteIP) {
		return true
	}
	_ = remote // remote 仅用于未来扩展，remoteIP 已是解析结果。
	return false
}

// closePingConn 在延迟探测结束后规范关闭连接：
// 先清掉 deadline，再尽量半关闭写端（FIN），最后 Close，避免半开握手后直接硬断。
func closePingConn(conn net.Conn) {
	if conn == nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
	_ = conn.Close()
}

// pingHostState 记录智能模式下每台主机的 Banner 确认状态，用于双频节流。
type pingHostState struct {
	lastBannerAt     time.Time
	lastBannerOnline bool
	lastBannerMs     int64
	hasBannerResult  bool
}

var (
	pingStateMu sync.Mutex
	pingStates  = map[string]*pingHostState{}
)

func pingHostKey(conn config.Connection) string {
	id := strings.TrimSpace(conn.ID)
	if id == "" {
		id = fmt.Sprintf("%s@%s", conn.Username, sshmanager.DialAddr(conn.Host, conn.Port))
	}
	return id
}

// snapshotPingHostState 在锁内拷贝一份状态快照返回，供调用方无锁读取，
// 避免「持指针出锁、与 updatePingHostBannerState 并发读写 time.Time 字段」造成数据竞争。
func snapshotPingHostState(key string) pingHostState {
	pingStateMu.Lock()
	defer pingStateMu.Unlock()
	if st, ok := pingStates[key]; ok {
		return *st
	}
	return pingHostState{}
}

func updatePingHostBannerState(key string, online bool, ms int64) {
	pingStateMu.Lock()
	defer pingStateMu.Unlock()
	st, ok := pingStates[key]
	if !ok {
		st = &pingHostState{}
		pingStates[key] = st
	}
	st.lastBannerAt = time.Now()
	st.lastBannerOnline = online
	st.lastBannerMs = ms
	st.hasBannerResult = true
}

// ClearPingHostState 在删除连接时清理其 Banner 状态，避免 pingStates 无限增长。
func ClearPingHostState(key string) {
	pingStateMu.Lock()
	defer pingStateMu.Unlock()
	delete(pingStates, key)
}

// shouldRunBannerVerify 是否应对该主机执行一次 Banner 确认。
func shouldRunBannerVerify(st *pingHostState, now time.Time) bool {
	if st == nil || !st.hasBannerResult {
		return true
	}
	return now.Sub(st.lastBannerAt) >= autoBannerVerifyInterval
}

// readSSHBanner 在已建立的 TCP 连接上做轻量 Banner 交换，确认真实 SSH 可达性。
// 不做鉴权；结束后由调用方 closePingConn。
func readSSHBanner(conn net.Conn, connectedAt time.Time) (int64, bool) {
	// 服务端通常先发 Banner；先短读，读到 SSH- 即可确认，减少多余交互。
	_ = conn.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err == nil && n > 0 && bytes.HasPrefix(buf[:n], []byte("SSH-")) {
		return time.Since(connectedAt).Milliseconds(), true
	}

	// 未读到服务端 Banner 时，写客户端标识再读一次（部分代理链路需要客户端先说话）。
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, writeErr := conn.Write([]byte("SSH-2.0-LumeTermPing\r\n")); writeErr != nil {
		return 0, false
	}
	n, err = conn.Read(buf)
	if err != nil || n == 0 || !bytes.HasPrefix(buf[:n], []byte("SSH-")) {
		return 0, false
	}
	return time.Since(connectedAt).Milliseconds(), true
}

// dialForPing 拨号并返回连接与 dial 耗时。
func dialForPing(connConfig config.Connection) (net.Conn, int64, time.Time, error) {
	target := sshmanager.DialAddr(connConfig.Host, connConfig.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := config.DialConnectionTargetContext(ctx, connConfig, target, 4*time.Second)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	connectedAt := time.Now()
	return conn, connectedAt.Sub(start).Milliseconds(), connectedAt, nil
}

// measureLatencyTCP 仅 TCP 端口连通探测。
func measureLatencyTCP(connConfig config.Connection) (int64, bool) {
	conn, dialMs, _, err := dialForPing(connConfig)
	if err != nil {
		return 0, false
	}
	closePingConn(conn)
	return dialMs, true
}

// measureLatencyBanner 强制 Banner 可达性探测。
func measureLatencyBanner(connConfig config.Connection) (int64, bool) {
	conn, _, connectedAt, err := dialForPing(connConfig)
	if err != nil {
		return 0, false
	}
	defer closePingConn(conn)
	return readSSHBanner(conn, connectedAt)
}

// measureLatencyAuto 智能模式：
//   - 直连路径：TCP 测延迟/在线
//   - 疑似代理/TUN：TCP 每轮拨号观察路径；Banner 按 autoBannerVerifyInterval 低频确认；
//     未通过 Banner 确认前，不把「TCP 0ms」当成在线（避免 TUN 黑洞假在线）。
func measureLatencyAuto(connConfig config.Connection) (int64, bool) {
	conn, dialMs, connectedAt, err := dialForPing(connConfig)
	if err != nil {
		return 0, false
	}

	usesProxy := config.ConnectionUsesProxy(connConfig)
	local := conn.LocalAddr()
	remote := conn.RemoteAddr()
	remoteIP := parseIPFromAddr(remote)
	suspicious := needsSSHBannerProbe(usesProxy, local, remote, remoteIP, connConfig.Host)

	if !suspicious {
		closePingConn(conn)
		return dialMs, true
	}

	// 疑似被代理/TUN 接管：在线状态以 Banner 确认为准。
	key := pingHostKey(connConfig)
	// 取锁内快照，避免与 updatePingHostBannerState 并发读写 time.Time 字段。
	st := snapshotPingHostState(key)
	now := time.Now()

	if !shouldRunBannerVerify(&st, now) {
		closePingConn(conn)
		if st.lastBannerOnline {
			// 信任窗口内：展示最近一次真实 Banner RTT，避免 TUN 下 TCP 假 0ms。
			return st.lastBannerMs, true
		}
		return 0, false
	}

	ms, online := readSSHBanner(conn, connectedAt)
	closePingConn(conn)
	updatePingHostBannerState(key, online, ms)
	if !online {
		return 0, false
	}
	return ms, true
}

// measureLatency measures the one-way RTT to the SSH server.
//
// mode（见 normalizePingMode）：
//   - auto：路径感知 + 双频（直连 TCP；TUN/代理低频 Banner）
//   - tcp：强制只看 TCP 端口连通
//   - banner：强制所有连接都读真实 SSH Banner
func measureLatency(connConfig config.Connection, mode string) (int64, bool) {
	mode = normalizePingMode(mode)
	switch mode {
	case "tcp":
		return measureLatencyTCP(connConfig)
	case "banner":
		return measureLatencyBanner(connConfig)
	default:
		return measureLatencyAuto(connConfig)
	}
}

// PingServer returns the latency to the SSH port. mode 见 normalizePingMode。
// 每轮只采样 1 次，降低对 SSH 端口的连接频率。
func PingServer(connConfig config.Connection, mode string) map[string]interface{} {
	rtt, online := measureLatency(connConfig, mode)
	if !online {
		return map[string]interface{}{
			"online":  false,
			"latency": 0,
		}
	}
	return map[string]interface{}{
		"online":  true,
		"latency": rtt,
	}
}
