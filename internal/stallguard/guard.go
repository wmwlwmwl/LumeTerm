// Package stallguard 提供「进度看门狗」：调用方在每次有进展时 Touch，
// 超过 idleLimit 没有任何进展就触发一次 onStall 回调（通常用于关闭卡死的传输通道）。
//
// 用途：SFTP 的 Read/ReadDir 没有超时参数，服务端活着但传输卡住时会永久阻塞；
// 关闭本次传输专用的 SFTP 通道能让阻塞的 Read 立刻返回错误，而不是永久挂起。
package stallguard

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultIdleLimit 是默认的「无进展」阈值：大文件慢链路仍应有字节级进展，
	// 120s 完全没有进展基本可判定为卡死。
	DefaultIdleLimit = 120 * time.Second
	// DefaultInterval 是看门狗检查间隔。
	DefaultInterval = 5 * time.Second
)

// StallError 表示因长时间无进展而主动中断。
type StallError struct {
	Idle time.Duration
}

func (e *StallError) Error() string {
	return fmt.Sprintf("remote SFTP made no progress for %s", e.Idle)
}

// Guard 是一个单次触发的进度看门狗。
type Guard struct {
	idleLimit time.Duration
	interval  time.Duration
	onStall   func()
	last      atomic.Int64
	stalled   atomic.Bool
	stop      chan struct{}
	stopOnce  sync.Once
}

// New 创建并启动看门狗；onStall 只会被调用一次（不会与 Stop 并发保证顺序）。
func New(idleLimit, interval time.Duration, onStall func()) *Guard {
	if idleLimit <= 0 {
		idleLimit = DefaultIdleLimit
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	if interval > idleLimit {
		interval = idleLimit
	}
	g := &Guard{
		idleLimit: idleLimit,
		interval:  interval,
		onStall:   onStall,
		stop:      make(chan struct{}),
	}
	g.last.Store(time.Now().UnixNano())
	go g.run()
	return g
}

// Touch 记录一次进展（收到数据/完成一个文件/遍历到一个目录项时调用）。
func (g *Guard) Touch() {
	g.last.Store(time.Now().UnixNano())
}

// Stalled 返回是否已因无进展触发中断。
func (g *Guard) Stalled() bool {
	return g.stalled.Load()
}

// Err 返回 StallError（未触发时返回 nil），供调用方包装原始 IO 错误。
func (g *Guard) Err() error {
	if !g.Stalled() {
		return nil
	}
	return &StallError{Idle: g.idleLimit}
}

// Stop 停止看门狗（可重复调用）。
func (g *Guard) Stop() {
	g.stopOnce.Do(func() { close(g.stop) })
}

func (g *Guard) run() {
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
			idle := time.Since(time.Unix(0, g.last.Load()))
			if idle < g.idleLimit {
				continue
			}
			g.stalled.Store(true)
			if g.onStall != nil {
				g.onStall()
			}
			return
		}
	}
}
