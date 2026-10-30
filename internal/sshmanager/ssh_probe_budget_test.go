package sshmanager

import (
	"context"
	"testing"
	"time"
)

// 本文件回归探针采集的「总预算」策略：后端一次采集（部署 + 运行 + 诊断）
// 必须明显小于前端等待上限，否则会出现前端先超时、后端还在空转的假超时。
// 同时验证 ctx 取消能立刻中断部署/诊断，而不是留下后台命令。

func TestProbeTimeoutBudgetRelation(t *testing.T) {
	// 前端 ProbePanel / BigScreen 的等待上限为 90s（probeTypes.ts / bigScreenTypes.ts）。
	if probeTotalBudget >= 90*time.Second {
		t.Fatalf("probeTotalBudget=%v 必须小于前端等待上限 90s", probeTotalBudget)
	}
	if probeRunTimeout >= probeTotalBudget {
		t.Fatalf("probeRunTimeout=%v 必须小于总预算 %v", probeRunTimeout, probeTotalBudget)
	}
	if probeDiagnoseTimeout >= probeRunTimeout {
		t.Fatalf("诊断预算 %v 不应超过运行预算 %v", probeDiagnoseTimeout, probeRunTimeout)
	}
	if probeDeployTimeout >= probeTotalBudget {
		t.Fatalf("部署预算 %v 必须小于总预算 %v", probeDeployTimeout, probeTotalBudget)
	}
}

func TestDeployProbeScriptReturnsWhenBudgetCanceled(t *testing.T) {
	client, _ := newRecordingSSHClient(t, 0)
	manager := NewSSHManager()
	manager.clients["conn-1"] = &sshClientEntry{Client: client}
	manager.sessions["session-1"] = &SessionData{ConnKey: "conn-1"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	err := manager.deployProbeScript(ctx, client, "conn-1")
	if err == nil {
		t.Fatal("ctx 已取消时部署应返回错误")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("ctx 已取消时部署应立即返回，实际耗时 %v", elapsed)
	}
}

func TestDiagnoseProbeScriptFailureReturnsWhenBudgetCanceled(t *testing.T) {
	client, _ := newRecordingSSHClient(t, 0)
	manager := NewSSHManager()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	_ = manager.diagnoseProbeScriptFailure(ctx, client, " lite")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("ctx 已取消时诊断应立即返回，实际耗时 %v", elapsed)
	}
}
