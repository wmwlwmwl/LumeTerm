package sshmanager

import (
	"fmt"
	"log"
	"net"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"lumeterm/internal/localsysinfo"

	"golang.org/x/crypto/ssh"
)

// deployProbeScript writes probe.sh to ~/.lumeterm and /tmp/.lumeterm on the remote
// server via an exec-channel heredoc. 不依赖 SFTP：OpenWrt/Dropbear 未装
// openssh-sftp-server 时系统监控也必须可用。
// ponytail: 远程命令可能慢,用 select+timer 兜底 probeDeployTimeout,
// 避免服务器慢时永久阻塞 getSystemInfo 致前端定时器链断裂(数据不刷新)。
// 超时后 goroutine 仍在后台等待 IO,随 keepalive 关连时退出(可接受临时泄漏)。
func (m *SSHManager) deployProbeScript(client *ssh.Client, connKey string) error {
	if client == nil {
		return fmt.Errorf("PROBE_CLIENT_UNAVAILABLE")
	}
	m.mu.RLock()
	already := m.probeDeployed[connKey]
	failCount := m.probeFailed[connKey]
	m.mu.RUnlock()
	if already {
		return nil
	}
	if failCount >= 3 {
		return fmt.Errorf("PROBE_DEPLOY_GIVEUP|%d", failCount)
	}

	done := make(chan error, 1)
	go func() { done <- m.deployProbeScriptIO(client) }()

	timer := time.NewTimer(probeDeployTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			m.mu.Lock()
			m.probeFailed[connKey]++
			m.mu.Unlock()
			return err
		}
		m.mu.Lock()
		m.probeDeployed[connKey] = true
		delete(m.probeFailed, connKey) // 成功后重置失败计数，避免历史累计误判永久禁用
		m.mu.Unlock()
		return nil
	case <-timer.C:
		// ponytail: 超时多因服务器慢而非部署逻辑错误,不在此自增 probeFailed:
		// 自增会快速触达 ≥3 永久放弃,反而失去恢复机会。下次重试仍走部署。
		return fmt.Errorf("PROBE_DEPLOY_TIMEOUT|%v", probeDeployTimeout)
	}
}

// probeDeployCmd 构造把探针脚本写入 ~/.lumeterm 与 /tmp/.lumeterm 的 heredoc 命令。
// 引号定界符（<<'LUMETERM_EOF'）确保脚本内容中的 $、反引号等不被远端 shell 展开；
// tee 双写两个位置,任一写入成功即可（运行端 buildProbeScriptRunCommand 有双路径回退）。
// 末尾 [ -f ... ] 作为部署成功的最终校验,避免 tee 半成功时误判。
func probeDeployCmd() string {
	return fmt.Sprintf(`mkdir -p ~/.lumeterm /tmp/.lumeterm 2>/dev/null
tee ~/.lumeterm/probe.sh /tmp/.lumeterm/probe.sh >/dev/null <<'LUMETERM_EOF'
%s
LUMETERM_EOF
chmod 755 ~/.lumeterm/probe.sh /tmp/.lumeterm/probe.sh 2>/dev/null
[ -f ~/.lumeterm/probe.sh ] || [ -f /tmp/.lumeterm/probe.sh ]`, dynamicProbeScript)
}

// deployProbeScriptIO 通过 exec 通道写入 probe.sh,无超时(由调用方 deployProbeScript 兜底)。
// 命令包在 sh -c 中执行:远端登录 shell 可能是 fish/csh 等不支持 heredoc 的
// shell,强制走 POSIX sh 保证部署命令语义一致(与运行端 buildProbeScriptRunCommand 同理)。
func (m *SSHManager) deployProbeScriptIO(client *ssh.Client) error {
	_, err := m.executeCmdWithClient(client, wrapShCmd(probeDeployCmd()))
	if err != nil {
		return fmt.Errorf("PROBE_DEPLOY_IO|%v", err)
	}
	return nil
}

// wrapShCmd 把命令包进 POSIX sh 执行。命令中的单引号用 '\” 转义:该序列在
// bash/sh/fish/csh 下都会被原样透传给内层 sh(外层 shell 只把单引号当字面
// 量处理),内层 sh 再把它还原为引号语法,因此命令内容可含任意单引号。
func wrapShCmd(cmd string) string {
	return "sh -c '" + strings.ReplaceAll(cmd, "'", `'\''`) + "'"
}

// extractSection 从 lines 中提取 startMarker（不含）到 endMarker（不含）之间的内容。
// startMarker 为空时从开头开始收集；endMarker 为空时收集到末尾。
// GetSystemInfo 与 GetServerStaticInfo 共用此实现，避免重复定义。
func buildProbeScriptRunCommand(probeArg string) string {
	// if/else 而非 &&/||:tee 双写后 home 与 /tmp 两份都常在,&&/|| 会在
	// home 份非零退出时再跑一遍 /tmp 份——探针双跑、输出拼接、延迟翻倍。
	return fmt.Sprintf(`sh -c 'f=~/.lumeterm/probe.sh; if [ -f "$f" ]; then sh "$f"%s; else sh /tmp/.lumeterm/probe.sh%s; fi'`, probeArg, probeArg)
}

func (m *SSHManager) diagnoseProbeScriptFailure(client *ssh.Client, probeArg string) string {
	diagCmd := fmt.Sprintf(`sh -c 'f=~/.lumeterm/probe.sh; alt=/tmp/.lumeterm/probe.sh; if [ -f "$f" ]; then target="$f"; elif [ -f "$alt" ]; then target="$alt"; else echo "probe script not found"; echo "home candidate:$f"; echo "tmp candidate:$alt"; exit 0; fi; echo "target:$target"; ls -ld "$(dirname "$target")" 2>&1; ls -l "$target" 2>&1; command -v sh 2>&1; sh "$target"%s 2>&1 | head -n 20'`, probeArg)
	out, err := m.executeCmdWithClient(client, diagCmd)
	parts := make([]string, 0, 2)
	if trimmedOut := strings.TrimSpace(out); trimmedOut != "" {
		parts = append(parts, trimmedOut)
	}
	if err != nil {
		errText := strings.TrimSpace(err.Error())
		if errText != "" {
			parts = append(parts, errText)
		}
	}
	return strings.Join(parts, " | ")
}

func extractSection(lines []string, startMarker, endMarker string) []string {
	var out []string
	// BUG FIX: if startMarker is empty, strings.Contains(l,"") is always true
	// causing every line to be skipped via `continue`. Fix: start collecting immediately.
	inside := (startMarker == "")
	for _, l := range lines {
		if startMarker != "" && strings.Contains(l, startMarker) {
			inside = true
			continue
		}
		if endMarker != "" && strings.Contains(l, endMarker) {
			break
		}
		if inside {
			out = append(out, l)
		}
	}
	return out
}

// extractSectionExact 与 extractSection 相同,但 marker 必须整行精确匹配。
// 完整进程列表的记录行携带任意 cmdline,cmdline 中可能出现 "---PROCS2---"
// 等 marker 子串(典型:运行脚本的 sh,其 argv 就是整段脚本),子串匹配会把
// section 提前截断。要求脚本端保证记录单行(fullProcListScript 已把 cmdline
// 的换行转为空格)。
func extractSectionExact(lines []string, startMarker, endMarker string) []string {
	var out []string
	inside := startMarker == ""
	for _, l := range lines {
		if startMarker != "" && l == startMarker {
			inside = true
			continue
		}
		if endMarker != "" && l == endMarker {
			break
		}
		if inside {
			out = append(out, l)
		}
	}
	return out
}

func (m *SSHManager) GetSystemInfo(sessionId string) (map[string]interface{}, error) {
	return m.getSystemInfo(sessionId, false)
}

// GetSystemInfoLite retrieves only the metrics used by the big-screen view.
func (m *SSHManager) GetSystemInfoLite(sessionId string) (map[string]interface{}, error) {
	return m.getSystemInfoLite(sessionId)
}

func (m *SSHManager) GetNetworkInfo(sessionId string) (map[string]interface{}, error) {
	return m.getSystemInfo(sessionId, true)
}

func (m *SSHManager) getSystemInfo(sessionId string, includeNetworkConnections bool) (result map[string]interface{}, err error) {
	return m.getSystemInfoWithMode(sessionId, includeNetworkConnections, false)
}

func (m *SSHManager) getSystemInfoLite(sessionId string) (result map[string]interface{}, err error) {
	return m.getSystemInfoWithMode(sessionId, false, true)
}

func (m *SSHManager) getSystemInfoWithMode(sessionId string, includeNetworkConnections bool, lite bool) (result map[string]interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in GetSystemInfo: %v", r)
			log.Printf("[GetSystemInfo] panic: %v\n%s", r, debug.Stack())
			result = nil
		}
	}()
	// Local sessions (WSL/PowerShell/native terminal) run the probe script directly.
	m.mu.RLock()
	localSd, localOk := m.sessions[sessionId]
	m.mu.RUnlock()
	if localOk && localSd.IsLocal {
		if lite {
			return localsysinfo.SystemInfoLite(localSysinfoSession(localSd), localSysinfoDependencies())
		}
		return localsysinfo.SystemInfo(localSysinfoSession(localSd), includeNetworkConnections, localSysinfoDependencies())
	}
	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	s, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("session not found")
	}
	connKey := s.ConnKey
	m.mu.RUnlock()

	if err := m.deployProbeScript(client, connKey); err != nil {
		return nil, err
	}

	probeArg := ""
	if lite {
		probeArg = " lite"
	} else if includeNetworkConnections {
		probeArg = " network"
	} else {
		// GetSystemInfo 需要 top 进程；GetNetworkInfo(NetworkPage) 不需要进程列表
		probeArg = " procs"
	}
	out, err := m.executeCmdWithClient(client, buildProbeScriptRunCommand(probeArg))
	if err != nil || len(strings.TrimSpace(out)) == 0 {
		// ponytail: 偶发失败(服务器慢/30s 超时)不立即删 probeDeployed 重走部署,
		// 避免每次重试都重新 heredoc 传输探针脚本。连续失败 3 次才怀疑脚本损坏,强制重新部署。
		m.mu.Lock()
		m.probeRunFailed[connKey]++
		if m.probeRunFailed[connKey] >= 3 {
			delete(m.probeDeployed, connKey)
			delete(m.probeRunFailed, connKey)
		}
		m.mu.Unlock()
		detailParts := make([]string, 0, 3)
		if err != nil {
			detailParts = append(detailParts, err.Error())
		}
		if trimmedOut := strings.TrimSpace(out); trimmedOut != "" {
			detailParts = append(detailParts, "stdout: "+trimmedOut)
		}
		if diagnostic := m.diagnoseProbeScriptFailure(client, probeArg); diagnostic != "" {
			detailParts = append(detailParts, "diagnostics: "+diagnostic)
		}
		if len(detailParts) == 0 {
			return nil, fmt.Errorf("PROBE_EXEC_FAILED")
		}
		return nil, fmt.Errorf("PROBE_EXEC_FAILED|%s", strings.Join(detailParts, " | "))
	}

	// 成功:重置执行失败计数
	m.mu.Lock()
	delete(m.probeRunFailed, connKey)
	m.mu.Unlock()
	return parseProbeOutput(out, includeNetworkConnections)
}

// parseProbeOutput parses the stdout of dynamicProbeScript and returns the
// structured data map used by the frontend panels. Shared by SSH and local sessions.
func parseProbeOutput(out string, includeNetworkConnections bool) (map[string]interface{}, error) {
	// ── Split on ---CPU2--- to get two halves ──────────────────────────
	halves := strings.SplitN(out, "---CPU2---", 2)
	if len(halves) < 2 {
		return nil, fmt.Errorf("unexpected output format")
	}
	part1 := halves[0]
	part2 := halves[1] // everything after ---CPU2---

	lines1 := strings.Split(part1, "\n")
	lines2 := strings.Split(part2, "\n")

	// ── Parse uptime ──────────────────────────────────────────────────
	uptimeSeconds := 0.0
	uptimeDays := 0
	uptimeHours := 0
	uptimeMins := 0
	if len(lines1) > 0 {
		fmt.Sscanf(strings.TrimSpace(lines1[0]), "%f", &uptimeSeconds)
		uptimeDays = int(uptimeSeconds / 86400)
		uptimeHours = int((uptimeSeconds - float64(uptimeDays*86400)) / 3600)
		uptimeMins = int((uptimeSeconds - float64(uptimeDays*86400) - float64(uptimeHours*3600)) / 60)
	}

	// ── Parse load average ───────────────────────────────────────────
	loadLines := extractSection(lines1, "---LOAD---", "---MEM---")
	var load1, load5, load15 float64
	if len(loadLines) > 0 {
		fmt.Sscanf(strings.TrimSpace(loadLines[0]), "%f %f %f", &load1, &load5, &load15)
	}

	// ── Parse memory ──────────────────────────────────────────────────
	var memTotal, memFree, memAvailable, memBuffers, memCached, memSReclaimable uint64
	var swapTotal, swapFree uint64
	for _, l := range lines1 {
		switch {
		case strings.HasPrefix(l, "MemTotal:"):
			fmt.Sscanf(l, "MemTotal: %d", &memTotal)
		case strings.HasPrefix(l, "MemFree:"):
			fmt.Sscanf(l, "MemFree: %d", &memFree)
		case strings.HasPrefix(l, "MemAvailable:"):
			fmt.Sscanf(l, "MemAvailable: %d", &memAvailable)
		case strings.HasPrefix(l, "Buffers:"):
			fmt.Sscanf(l, "Buffers: %d", &memBuffers)
		case strings.HasPrefix(l, "Cached:"):
			fmt.Sscanf(l, "Cached: %d", &memCached)
		case strings.HasPrefix(l, "SReclaimable:"):
			fmt.Sscanf(l, "SReclaimable: %d", &memSReclaimable)
		case strings.HasPrefix(l, "SwapTotal:"):
			fmt.Sscanf(l, "SwapTotal: %d", &swapTotal)
		case strings.HasPrefix(l, "SwapFree:"):
			fmt.Sscanf(l, "SwapFree: %d", &swapFree)
		}
	}
	memTotalMB := float64(memTotal) / 1024.0
	memFreeMB := float64(memFree) / 1024.0
	memCacheMB := float64(memBuffers+memCached+memSReclaimable) / 1024.0
	// 用 MemAvailable 计算已用（与 free 命令一致）
	var memUsedMB float64
	if memAvailable > 0 {
		memUsedMB = float64(memTotal-memAvailable) / 1024.0
	} else {
		memUsedMB = float64(memTotal-memFree-memBuffers-memCached-memSReclaimable) / 1024.0
	}
	if memUsedMB < 0 {
		memUsedMB = 0
	}
	swapTotalMB := float64(swapTotal) / 1024.0
	swapFreeMB := float64(swapFree) / 1024.0
	swapUsedMB := swapTotalMB - swapFreeMB
	if swapUsedMB < 0 {
		swapUsedMB = 0
	}

	// ── Parse df (all partitions) ─────────────────────────────────────
	dfLines := extractSection(lines1, "---DF---", "---CPU1---")
	var diskTotalKB, diskUsedKB uint64
	var diskPercent float64
	diskDevice := "disk"
	type partition struct {
		Mount   string
		Size    string
		Avail   string
		UsedPct int
	}
	var partitions []partition
	for _, l := range dfLines {
		fields := strings.Fields(l)
		if len(fields) < 6 {
			continue
		}
		totalKB, errTotal := strconv.ParseUint(fields[1], 10, 64)
		usedKB, errUsed := strconv.ParseUint(fields[2], 10, 64)
		availKB, errAvail := strconv.ParseUint(fields[3], 10, 64)
		if !strings.HasSuffix(fields[4], "%") {
			continue
		}
		pctStr := strings.TrimSuffix(fields[4], "%")
		pct, errPct := strconv.Atoi(pctStr)
		if errTotal != nil || errUsed != nil || errAvail != nil || errPct != nil || pct < 0 || pct > 100 {
			continue
		}
		mount := strings.Join(fields[5:], " ")
		if mount == "" {
			continue
		}
		if mount == "/" {
			diskDevice = filepath.Base(fields[0])
			diskTotalKB = totalKB
			diskUsedKB = usedKB
			if totalKB > 0 {
				diskPercent = float64(usedKB) / float64(totalKB) * 100.0
			}
		}
		formatGB := func(kb uint64) string {
			gb := float64(kb) / (1024.0 * 1024.0)
			if gb < 1 {
				return fmt.Sprintf("%.0fM", float64(kb)/1024.0)
			}
			return fmt.Sprintf("%.1fG", gb)
		}
		partitions = append(partitions, partition{
			Mount:   mount,
			Size:    formatGB(totalKB),
			Avail:   formatGB(availKB),
			UsedPct: pct,
		})
	}
	diskTotalGB := float64(diskTotalKB) / (1024.0 * 1024.0)
	diskUsedGB := float64(diskUsedKB) / (1024.0 * 1024.0)

	// ── Parse CPU (/proc/stat delta, XTerminal method) ────────────────
	cpuLines1 := extractSection(lines1, "---CPU1---", "---NET1---")
	cpuLines2 := extractSection(lines2, "", "---NET2---") // empty startMarker = collect from beginning

	parseStat := func(lines []string) map[string][]uint64 {
		res := make(map[string][]uint64)
		for _, l := range lines {
			if !strings.HasPrefix(l, "cpu") {
				continue
			}
			parts := strings.Fields(l)
			if len(parts) < 5 {
				continue
			}
			// /proc/stat fields: user nice system idle iowait irq softirq steal ...
			getU := func(i int) uint64 {
				if i+1 < len(parts) {
					v, _ := strconv.ParseUint(parts[i+1], 10, 64)
					return v
				}
				return 0
			}
			userN := getU(0) + getU(1)                    // user + nice
			sysN := getU(2) + getU(5) + getU(6) + getU(7) // system + irq + softirq + steal
			idleN := getU(3) + getU(4)                    // idle + iowait
			total := userN + sysN + idleN
			res[parts[0]] = []uint64{userN, sysN, idleN, total}
		}
		return res
	}

	cpus1 := parseStat(cpuLines1)
	cpus2 := parseStat(cpuLines2)

	computeUsage := func(name string) float64 {
		v1, ok1 := cpus1[name]
		v2, ok2 := cpus2[name]
		if !ok1 || !ok2 || len(v1) < 4 || len(v2) < 4 {
			return 0
		}
		// v = [user+nice, system+irq+softirq+steal, idle+iowait, total]
		dTotal := float64(v2[3]) - float64(v1[3])
		dIdle := float64(v2[2]) - float64(v1[2])
		if dTotal <= 0 {
			return 0
		}
		usage := 100.0 * (1.0 - dIdle/dTotal)
		if usage < 0 {
			return 0
		}
		if usage > 100 {
			return 100
		}
		return usage
	}

	cpuTotalUsage := computeUsage("cpu")

	// Collect core names, sort them (cpu0, cpu1, cpu2...)
	var coreNames []string
	for name := range cpus2 {
		if name != "cpu" && strings.HasPrefix(name, "cpu") {
			coreNames = append(coreNames, name)
		}
	}
	sort.Strings(coreNames)

	var cpuCoreUsages []float64
	for _, name := range coreNames {
		cpuCoreUsages = append(cpuCoreUsages, computeUsage(name))
	}

	// ── Parse Network ─────────────────────────────────────────────────
	shouldIgnoreNetIf := func(name string) bool {
		name = strings.TrimSpace(name)
		return name == "" || name == "lo" || strings.HasPrefix(name, "lo:") ||
			strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") ||
			strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "virbr") ||
			strings.HasPrefix(name, "vmnet") || strings.HasPrefix(name, "tun") ||
			strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "wg")
	}

	parseNetworkStats := func(lines []string) map[string][]uint64 {
		res := make(map[string][]uint64)
		for i := 0; i < len(lines); i++ {
			l := strings.TrimSpace(lines[i])
			if l == "" {
				continue
			}

			// /proc/net/dev: eth0: rx ... tx
			if strings.Contains(l, ":") {
				parts := strings.SplitN(l, ":", 2)
				name := strings.TrimSpace(parts[0])
				fields := strings.Fields(parts[1])
				if !shouldIgnoreNetIf(name) && len(fields) >= 16 {
					rx, _ := strconv.ParseUint(fields[0], 10, 64)
					tx, _ := strconv.ParseUint(fields[8], 10, 64)
					res[name] = []uint64{rx, tx}
					continue
				}
			}

			// ifconfig: eth0 ... / RX bytes ... TX bytes ...
			if fields := strings.Fields(l); len(fields) > 0 && !strings.HasPrefix(fields[0], "RX") && !strings.HasPrefix(fields[0], "TX") {
				name := strings.TrimSuffix(fields[0], ":")
				if _, err := strconv.Atoi(name); err == nil {
					name = ""
				}
				if shouldIgnoreNetIf(name) {
					continue
				}
				var rx, tx uint64
				for j := i + 1; j < len(lines) && j < i+10; j++ {
					ll := strings.TrimSpace(lines[j])
					parts := strings.Fields(ll)
					for k, token := range parts {
						var v uint64
						var ok bool
						if strings.HasPrefix(token, "bytes:") {
							v, _ = strconv.ParseUint(strings.TrimPrefix(token, "bytes:"), 10, 64)
							ok = true
						} else if token == "bytes" && k+1 < len(parts) {
							v, _ = strconv.ParseUint(parts[k+1], 10, 64)
							ok = true
						}
						if ok && strings.HasPrefix(ll, "RX") {
							rx = v
						} else if ok && strings.HasPrefix(ll, "TX") {
							tx = v
						}
					}
				}
				if rx > 0 || tx > 0 {
					res[name] = []uint64{rx, tx}
				}
			}

			// ip -s link: iface line followed by RX/TX blocks.
			if strings.Contains(l, ": ") {
				parts := strings.SplitN(l, ": ", 3)
				if len(parts) >= 2 {
					name := strings.TrimSpace(strings.Split(parts[1], "@")[0])
					if shouldIgnoreNetIf(name) || i+5 >= len(lines) {
						continue
					}
					rxFields := strings.Fields(lines[i+3])
					txFields := strings.Fields(lines[i+5])
					if len(rxFields) > 0 && len(txFields) > 0 {
						rx, _ := strconv.ParseUint(rxFields[0], 10, 64)
						tx, _ := strconv.ParseUint(txFields[0], 10, 64)
						if rx > 0 || tx > 0 {
							res[name] = []uint64{rx, tx}
						}
					}
				}
			}
		}
		return res
	}

	netLines1 := extractSection(lines1, "---NET1---", "---NETCONN1---")
	netLines2 := extractSection(lines2, "---NET2---", "---NETCONN2---")
	nets1 := parseNetworkStats(netLines1)
	nets2 := parseNetworkStats(netLines2)

	var netUpSpeed, netDownSpeed, netUpTotal, netDownTotal float64
	var networkInterfaces []map[string]interface{}
	for ifName, v2 := range nets2 {
		v1, ok := nets1[ifName]
		if !ok {
			continue
		}
		netDownTotal += float64(v2[0]) / (1024.0 * 1024.0)
		netUpTotal += float64(v2[1]) / (1024.0 * 1024.0)
		// 防止 v2 < v1 时 uint64 减法下溢（计数器回绕/重置）
		var rxSpeed, txSpeed float64
		if v2[0] >= v1[0] {
			rxSpeed = float64(v2[0]-v1[0]) / 1024.0 // KB/s over 1s
		}
		if v2[1] >= v1[1] {
			txSpeed = float64(v2[1]-v1[1]) / 1024.0
		}
		netDownSpeed += rxSpeed
		netUpSpeed += txSpeed
		networkInterfaces = append(networkInterfaces, map[string]interface{}{
			"name":          ifName,
			"uploadSpeed":   txSpeed,
			"downloadSpeed": rxSpeed,
			"uploadTotal":   float64(v2[1]) / (1024.0 * 1024.0),
			"downloadTotal": float64(v2[0]) / (1024.0 * 1024.0),
		})
	}
	sort.Slice(networkInterfaces, func(i, j int) bool {
		return networkInterfaces[i]["name"].(string) < networkInterfaces[j]["name"].(string)
	})

	// ── Parse Disk IO ─────────────────────────────────────────────────
	parseDiskIO := func(lines []string) map[string][]uint64 {
		res := make(map[string][]uint64)
		for _, l := range lines {
			fields := strings.Fields(l)
			if len(fields) < 10 {
				continue
			}
			name := fields[2]
			if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
				continue
			}
			r, _ := strconv.ParseUint(fields[5], 10, 64)
			w, _ := strconv.ParseUint(fields[9], 10, 64)
			res[name] = []uint64{r, w}
		}
		return res
	}

	diskIO1 := parseDiskIO(extractSection(lines1, "---DISKIO1---", "---PROC1---"))
	diskIO2 := parseDiskIO(extractSection(lines2, "---DISKIO2---", "---PROC2---"))

	var diskReadSpeed, diskWriteSpeed float64
	for dName, v2 := range diskIO2 {
		v1, ok := diskIO1[dName]
		if !ok {
			continue
		}
		// 防止 v2 < v1 时 uint64 减法下溢（计数器回绕/重置）
		var rKB, wKB float64
		if v2[0] >= v1[0] {
			rKB = float64(v2[0]-v1[0]) * 0.5 // 512-byte sectors → KB over 1s
		}
		if v2[1] >= v1[1] {
			wKB = float64(v2[1]-v1[1]) * 0.5
		}
		if rKB > diskReadSpeed {
			diskReadSpeed = rKB
		}
		if wKB > diskWriteSpeed {
			diskWriteSpeed = wKB
		}
	}

	// Convert partitions to []map for JSON
	var partMaps []map[string]interface{}
	for _, p := range partitions {
		partMaps = append(partMaps, map[string]interface{}{
			"mount":   p.Mount,
			"size":    p.Size,
			"avail":   p.Avail,
			"usedPct": p.UsedPct,
		})
	}

	// ── Parse Network Connections ─────────────────────────────────────
	connLines1 := extractSection(lines1, "---NETCONN1---", "---DISKIO1---")
	connLines := extractSection(lines2, "---NETCONN2---", "---DISKIO2---")
	type netConnAgg struct {
		PID        string
		Name       string
		ListenIP   string
		Port       string
		IPs        map[string]struct{}
		ConnCount  int
		UploadMB   float64
		DownloadMB float64
		Peers      []map[string]interface{}
	}
	connAgg := make(map[string]*netConnAgg)
	extractPIDName := func(line string) (string, string) {
		pid := "-"
		name := "-"
		if idx := strings.Index(line, "pid="); idx >= 0 {
			rest := line[idx+4:]
			end := strings.IndexAny(rest, ",) ")
			if end < 0 {
				end = len(rest)
			}
			pid = strings.Trim(rest[:end], "\"")
		}
		if idx := strings.Index(line, "users:((\""); idx >= 0 {
			rest := line[idx+9:]
			if end := strings.Index(rest, "\""); end >= 0 {
				name = rest[:end]
			}
		} else if idx := strings.LastIndex(line, "/"); idx >= 0 {
			rest := strings.TrimSpace(line[idx+1:])
			if rest != "" && !strings.Contains(rest, ":") {
				name = strings.Fields(rest)[0]
			}
		}
		return pid, name
	}
	splitHostPort := func(addr string) (string, string) {
		addr = strings.Trim(addr, "[]")
		if addr == "" || addr == "*" {
			return "*", "-"
		}
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			return addr, "-"
		}
		host := strings.Trim(addr[:idx], "[]")
		port := addr[idx+1:]
		if host == "" {
			host = "*"
		}
		return host, port
	}
	addrFamily := func(host string) string {
		if strings.Contains(host, ":") || host == "::" {
			return "6"
		}
		return "4"
	}
	peerLocation := func(host string) string {
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil {
			return "-"
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return "reserved"
		}
		return "-"
	}
	parseSSBytesMB := func(line string) (float64, float64) {
		var sent, received uint64
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, "bytes_sent:") {
				sent, _ = strconv.ParseUint(strings.TrimPrefix(token, "bytes_sent:"), 10, 64)
			} else if strings.HasPrefix(token, "bytes_received:") {
				received, _ = strconv.ParseUint(strings.TrimPrefix(token, "bytes_received:"), 10, 64)
			}
		}
		return float64(sent) / (1024.0 * 1024.0), float64(received) / (1024.0 * 1024.0)
	}
	connByteKey := func(pid, name, local, peer string) string {
		return pid + "|" + name + "|" + local + "|" + peer
	}
	isConnHeader := func(fields []string) bool {
		if len(fields) < 5 {
			return false
		}
		proto := strings.ToLower(fields[0])
		if strings.HasPrefix(proto, "tcp") {
			return true
		}
		_, err1 := strconv.Atoi(fields[1])
		_, err2 := strconv.Atoi(fields[2])
		return err1 == nil && err2 == nil
	}
	parseConnBytes := func(lines []string) map[string][2]float64 {
		res := make(map[string][2]float64)
		for i, l := range lines {
			fields := strings.Fields(l)
			if len(fields) < 5 {
				continue
			}
			if !isConnHeader(fields) {
				continue
			}
			localIdx := 3
			if len(fields) >= 6 {
				if _, err := strconv.Atoi(fields[1]); err != nil {
					localIdx = 4
				}
			}
			peerIdx := localIdx + 1
			if len(fields) <= peerIdx || i+1 >= len(lines) {
				continue
			}
			nextFields := strings.Fields(lines[i+1])
			if isConnHeader(nextFields) {
				continue
			}
			sent, received := parseSSBytesMB(lines[i+1])
			pid, name := extractPIDName(l)
			res[connByteKey(pid, name, fields[localIdx], fields[peerIdx])] = [2]float64{sent, received}
		}
		return res
	}
	connBytes1 := parseConnBytes(connLines1)
	for i, l := range connLines {
		fields := strings.Fields(l)
		if len(fields) < 5 {
			continue
		}
		if isConnHeader(fields) {
			localIdx := 3
			if len(fields) >= 6 {
				if _, err := strconv.Atoi(fields[1]); err != nil {
					localIdx = 4
				}
			}
			peerIdx := localIdx + 1
			if len(fields) <= peerIdx {
				continue
			}
			local := fields[localIdx]
			peer := fields[peerIdx]
			listenIP, port := splitHostPort(local)
			peerIP, peerPort := splitHostPort(peer)
			pid, name := extractPIDName(l)
			uploadMB, downloadMB := 0.0, 0.0
			if i+1 < len(connLines) {
				nextFields := strings.Fields(connLines[i+1])
				if !isConnHeader(nextFields) {
					uploadNow, downloadNow := parseSSBytesMB(connLines[i+1])
					if prev, ok := connBytes1[connByteKey(pid, name, local, peer)]; ok {
						if uploadNow >= prev[0] {
							uploadMB = uploadNow - prev[0]
						}
						if downloadNow >= prev[1] {
							downloadMB = downloadNow - prev[1]
						}
					}
				}
			}
			key := pid + "|" + name + "|" + listenIP + "|" + port
			item := connAgg[key]
			if item == nil {
				item = &netConnAgg{PID: pid, Name: name, ListenIP: listenIP, Port: port, IPs: map[string]struct{}{}}
				connAgg[key] = item
			}
			isRealPeer := peerIP != "" && peerIP != "*" && peerIP != "0.0.0.0" && peerIP != "::"
			if isRealPeer {
				item.IPs[peerIP] = struct{}{}
				item.ConnCount++
				item.Peers = append(item.Peers, map[string]interface{}{
					"location": peerLocation(peerIP),
					"ip":       peerIP,
					"port":     peerPort,
					"upload":   uploadMB,
					"download": downloadMB,
				})
			}
			item.UploadMB += uploadMB
			item.DownloadMB += downloadMB
		}
	}
	listenerByPortFamily := make(map[string]*netConnAgg)
	for _, item := range connAgg {
		if item.Port == "-" {
			continue
		}
		if item.ListenIP == "0.0.0.0" || item.ListenIP == "::" || item.ListenIP == "*" {
			listenerByPortFamily[item.Port+"|"+addrFamily(item.ListenIP)] = item
		}
	}
	for key, item := range connAgg {
		if target := listenerByPortFamily[item.Port+"|"+addrFamily(item.ListenIP)]; target != nil && target != item {
			for ip := range item.IPs {
				target.IPs[ip] = struct{}{}
			}
			target.ConnCount += item.ConnCount
			target.UploadMB += item.UploadMB
			target.DownloadMB += item.DownloadMB
			target.Peers = append(target.Peers, item.Peers...)
			delete(connAgg, key)
		}
	}

	var networkConnections []map[string]interface{}
	for _, item := range connAgg {
		networkConnections = append(networkConnections, map[string]interface{}{
			"pid":       item.PID,
			"name":      item.Name,
			"listenIP":  item.ListenIP,
			"port":      item.Port,
			"ipCount":   len(item.IPs),
			"connCount": item.ConnCount,
			"upload":    item.UploadMB,
			"download":  item.DownloadMB,
			"peers":     item.Peers,
		})
	}
	sort.Slice(networkConnections, func(i, j int) bool {
		ci := networkConnections[i]["connCount"].(int)
		cj := networkConnections[j]["connCount"].(int)
		if ci == cj {
			return fmt.Sprint(networkConnections[i]["port"]) < fmt.Sprint(networkConnections[j]["port"])
		}
		return ci > cj
	})
	if len(networkConnections) > 200 {
		networkConnections = networkConnections[:200]
	}

	// ── Parse Processes (PROC1/PROC2 双采样, 远端 join 选 top6, /proc 直读) ──
	// PROC1/PROC2 均在 ---CPU2--- 之后(lines2):pass1 落盘、pass2 选 top6 后
	// 两段背靠背输出。PROC1 终点为 ---PROC2---(不再是 ---CPU2---)。
	proc1Lines := extractSection(lines2, "---PROC1---", "---PROC2---")
	proc2Lines := extractSection(lines2, "---PROC2---", "---DONE---")
	processes, _ := parseProbeProcSections(proc1Lines, proc2Lines)

	return map[string]interface{}{
		"uptime": map[string]int{"days": uptimeDays, "hours": uptimeHours, "mins": uptimeMins},
		"load":   map[string]float64{"load1": load1, "load5": load5, "load15": load15},
		"cpu": map[string]interface{}{
			"usage": cpuTotalUsage,
			"cores": cpuCoreUsages,
		},
		"memory": map[string]interface{}{
			"total":     memTotalMB,
			"used":      memUsedMB,
			"cache":     memCacheMB,
			"free":      memFreeMB,
			"swapTotal": swapTotalMB,
			"swapUsed":  swapUsedMB,
		},
		"disk": map[string]interface{}{
			"device":     diskDevice,
			"type":       "",
			"total":      diskTotalGB,
			"used":       diskUsedGB,
			"usage":      diskPercent,
			"readSpeed":  diskReadSpeed,
			"writeSpeed": diskWriteSpeed,
			"partitions": partMaps,
		},
		"network": map[string]interface{}{
			"uploadSpeed":   netUpSpeed,
			"downloadSpeed": netDownSpeed,
			"uploadTotal":   netUpTotal,
			"downloadTotal": netDownTotal,
			"interfaces":    networkInterfaces,
			"connections":   networkConnections,
		},
		"processes": processes,
	}, nil
}

// GetFullProcessList 获取服务器上所有进程列表（无 head 限制）
func (m *SSHManager) GetFullProcessList(sessionId string) ([]map[string]interface{}, error) {
	// For local sessions run ps directly.
	m.mu.RLock()
	sd, sdOk := m.sessions[sessionId]
	m.mu.RUnlock()
	if sdOk && sd.IsLocal {
		return getLocalFullProcessList(sd)
	}

	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return nil, err
	}

	// OpenWrt/BusyBox 的 ps 不支持 -eo/--sort/nlwp 等 procps 语法,走 /proc 直读;
	// 常规 Linux 保持 ps 路径不变。GetClientEntry 返回后会话可能已被断开,
	// 与 getSystemInfo 同样做 ok 检查,避免对 nil 条目取 ConnKey。
	m.mu.RLock()
	s, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("session not found")
	}
	connKey := s.ConnKey
	m.mu.RUnlock()
	isBusybox := m.remoteFeatureIs(client, connKey, featureBusybox)
	out, err := m.executeCmdWithClient(client, fullProcListCmdFor(isBusybox))
	if err != nil {
		return nil, err
	}
	if isBusybox {
		return parseFullProcListOutput(out)
	}
	return parseFullProcessListOutput(out)
}

// fullProcListCmdFor 按远端能力选择完整进程列表命令。BusyBox 脚本含 POSIX
// 函数与参数展开语法,必须经 wrapShCmd 强制 sh 执行——远端登录 shell 可能
// 是 fish/csh,裸发脚本会语法报错、进程列表整页失败。独立成纯函数便于
// 分支路由单测。
// fullProcListCmdFor 按远端能力选择完整进程列表命令。BusyBox 脚本含 POSIX
// 函数与参数展开语法,必须经 wrapShCmd 强制 sh 执行——远端登录 shell 可能
// 是 fish/csh,裸发脚本会语法报错、进程列表整页失败。独立成纯函数便于
// 分支路由单测。
func fullProcListCmdFor(isBusybox bool) string {
	if isBusybox {
		return wrapShCmd(fullProcListScript)
	}
	return `ps -eo pid,pcpu,rss,user,comm,stat,nlwp,etime,args --sort=-pcpu 2>/dev/null`
}

// parseFullProcessListOutput parses ps output into structured process maps.
func parseFullProcessListOutput(out string) ([]map[string]interface{}, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var processes []map[string]interface{}
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) < 9 {
			continue
		}
		if fields[0] == "PID" {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[1], 64)
		rss, _ := strconv.ParseUint(fields[2], 10, 64)
		nlwp, _ := strconv.ParseUint(fields[6], 10, 64)

		name := fields[4]
		stat := fields[5]
		etime := fields[7]
		args := strings.Join(fields[8:], " ")

		// "位置" 取 args 的第一个词（可执行路径）
		var loc string
		if idx := strings.Index(args, " "); idx > 0 {
			loc = args[:idx]
		} else {
			loc = args
		}

		processes = append(processes, map[string]interface{}{
			"pid":   fields[0],
			"cpu":   cpu,
			"mem":   float64(rss) / 1024.0,
			"user":  fields[3],
			"name":  name,
			"cmd":   args,
			"loc":   loc,
			"stat":  stat,
			"nlwp":  nlwp,
			"etime": etime,
		})
	}
	return processes, nil
}

// KillProcess 终止指定 PID 的进程
func (m *SSHManager) KillProcess(sessionId string, pid string) error {
	if _, err := strconv.Atoi(pid); err != nil {
		return fmt.Errorf("invalid pid: %s", pid)
	}
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		m.mu.RLock()
		wslDistro := sd.WSLDistro
		m.mu.RUnlock()
		return localsysinfo.KillProcess(localsysinfo.Session{WSLDistro: wslDistro}, pid)
	}

	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return err
	}
	_, err = m.executeCmdWithClient(client, "kill -9 "+pid+" 2>/dev/null")
	return err
}

// GetProcessEnv 获取指定进程的环境变量列表
func (m *SSHManager) GetProcessEnv(sessionId string, pid string) ([]string, error) {
	if _, err := strconv.Atoi(pid); err != nil {
		return nil, fmt.Errorf("invalid pid: %s", pid)
	}
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		m.mu.RLock()
		wslDistro := sd.WSLDistro
		m.mu.RUnlock()
		return localsysinfo.ProcessEnvironment(localsysinfo.Session{WSLDistro: wslDistro}, pid)
	}

	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return nil, err
	}
	out, err := m.executeCmdWithClient(client, "cat /proc/"+pid+"/environ 2>/dev/null | tr '\\0' '\\n'")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 过滤掉空行
	var result []string
	for _, l := range lines {
		if l != "" {
			result = append(result, l)
		}
	}
	return result, nil
}

// GetServerStaticInfo 获取服务器静态信息（OS/时区/主机名/CPU 型号），只在连接时调用一次
func (m *SSHManager) GetServerStaticInfo(sessionId string) (result map[string]interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in GetServerStaticInfo: %v", r)
			log.Printf("[GetServerStaticInfo] panic: %v\n%s", r, debug.Stack())
			result = nil
		}
	}()
	m.mu.RLock()
	sd, hasSd := m.sessions[sessionId]
	m.mu.RUnlock()
	if hasSd && sd.IsLocal {
		m.mu.RLock()
		wslDistro := sd.WSLDistro
		m.mu.RUnlock()
		return localsysinfo.StaticInfo(localsysinfo.Session{WSLDistro: wslDistro}, localSysinfoDependencies())
	}

	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return nil, err
	}

	out, err := m.executeCmdWithClient(client, `echo ---OS---
grep PRETTY_NAME /etc/os-release 2>/dev/null || cat /etc/redhat-release 2>/dev/null || cat /etc/issue 2>/dev/null | head -1 || uname -s -r
echo ---TZ---
timedatectl show -p Timezone --value 2>/dev/null || readlink -f /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || cat /etc/timezone 2>/dev/null || date +'%z'
echo ---CPUINFO---
grep 'model name' /proc/cpuinfo | head -1
echo ---IP---
ip route get 1.1.1.1 2>/dev/null | grep -oE 'src [0-9.]+' | awk '{print $2}' || hostname -I 2>/dev/null | awk '{print $1}'`)
	if err != nil {
		return nil, err
	}

	return parseServerStaticInfoOutput(out)
}

// parseServerStaticInfoOutput parses static query outputs into OS details map.
func parseServerStaticInfoOutput(out string) (map[string]interface{}, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")

	osName := "Linux"
	for _, l := range extractSection(lines, "---OS---", "---TZ---") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "PRETTY_NAME=") {
			osName = strings.Trim(strings.TrimPrefix(t, "PRETTY_NAME="), "\"")
			break
		}
		osName = t
		break
	}
	tzStr := "UTC"
	for _, l := range extractSection(lines, "---TZ---", "---CPUINFO---") {
		t := strings.TrimSpace(l)
		if t != "" {
			tzStr = t
			break
		}
	}
	cpuModel := ""
	for _, l := range extractSection(lines, "---CPUINFO---", "---IP---") {
		t := strings.TrimSpace(l)
		if t != "" {
			if idx := strings.Index(t, ":"); idx >= 0 {
				cpuModel = strings.TrimSpace(t[idx+1:])
			}
			break
		}
	}
	ipAddr := ""
	for _, l := range extractSection(lines, "---IP---", "") {
		t := strings.TrimSpace(l)
		if t != "" {
			ipAddr = t
			break
		}
	}

	return map[string]interface{}{
		"os":       osName,
		"timezone": tzStr,
		"ip":       ipAddr,
		"cpu": map[string]interface{}{
			"model": cpuModel,
		},
	}, nil
}

// SFTP Methods
