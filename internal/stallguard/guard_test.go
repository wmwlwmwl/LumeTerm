package stallguard

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuardTriggersOnStallOnce(t *testing.T) {
	var calls atomic.Int64
	guard := New(60*time.Millisecond, 10*time.Millisecond, func() { calls.Add(1) })
	defer guard.Stop()

	deadline := time.Now().Add(time.Second)
	for !guard.Stalled() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !guard.Stalled() {
		t.Fatal("无进展时看门狗应触发")
	}
	var stallErr *StallError
	if !errors.As(guard.Err(), &stallErr) {
		t.Fatalf("Err 应返回 StallError，实际 %v", guard.Err())
	}
	guard.Touch() // 触发后 Touch 不应重新激活
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("onStall 只应调用一次，实际 %d", calls.Load())
	}
}

func TestGuardTouchKeepsAlive(t *testing.T) {
	guard := New(80*time.Millisecond, 10*time.Millisecond, nil)
	defer guard.Stop()

	for i := 0; i < 12; i++ {
		time.Sleep(20 * time.Millisecond)
		guard.Touch()
	}
	if guard.Stalled() {
		t.Fatal("持续 Touch 时不应触发看门狗")
	}
}

func TestGuardStopPreventsTrigger(t *testing.T) {
	var calls atomic.Int64
	guard := New(50*time.Millisecond, 10*time.Millisecond, func() { calls.Add(1) })
	guard.Stop()
	time.Sleep(120 * time.Millisecond)
	if guard.Stalled() || calls.Load() != 0 {
		t.Fatal("Stop 之后不应再触发")
	}
	guard.Stop() // 幂等
}
