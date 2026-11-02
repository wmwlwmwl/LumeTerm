package sshmanager

import (
	"context"
	"strings"
	"testing"
)

// 本文件回归「取消副作用」相关行为：
// - 压缩先写临时文件、成功后才原子 mv 到目标，取消不会破坏已存在的同名归档；
// - 解压计划为 direct 模式时记录解压产生的顶层名字（用于取消后清理新输出）。

func TestCompressItemContextUsesTempThenAtomicRename(t *testing.T) {
	client, recorder := newRecordingSSHClient(t, 0)
	manager := newDeleteStrategyManager(t, client)

	if err := manager.CompressItemContext(context.Background(), "session-1", "/tmp/data-dir"); err != nil {
		t.Fatalf("压缩应成功: %v", err)
	}
	commands := recorder.all()
	if len(commands) != 1 {
		t.Fatalf("应只执行一条压缩命令，实际 %d 条: %v", len(commands), commands)
	}
	cmd := commands[0]
	if !strings.Contains(cmd, "tar -czf") {
		t.Fatalf("命令应调用 tar: %q", cmd)
	}
	if !strings.Contains(cmd, ".lumeterm_compress_") {
		t.Fatalf("命令应写入临时归档: %q", cmd)
	}
	if !strings.Contains(cmd, "&& mv -f") {
		t.Fatalf("命令应在 tar 成功后原子 mv 到目标: %q", cmd)
	}
	if !strings.Contains(cmd, "data-dir.tar.gz") {
		t.Fatalf("命令应包含最终归档名: %q", cmd)
	}
}

func TestBuildSmartUncompressPlanRecordsDirectRootName(t *testing.T) {
	singleFile := buildSmartUncompressPlan("/remote", "pkg.tar.gz", []string{"bin/tool"}, nil)
	if singleFile.Mode != smartUncompressModeDirect || singleFile.RootName != "bin" {
		t.Fatalf("单文件归档应记录顶层 RootName=bin，实际 mode=%q root=%q", singleFile.Mode, singleFile.RootName)
	}

	singleRootDir := buildSmartUncompressPlan("/remote", "pkg.tar.gz", []string{"app/", "app/main.go", "app/go.mod"}, nil)
	if singleRootDir.Mode != smartUncompressModeDirect || singleRootDir.RootName != "app" {
		t.Fatalf("单根目录归档应记录 RootName=app，实际 mode=%q root=%q", singleRootDir.Mode, singleRootDir.RootName)
	}

	multiRoot := buildSmartUncompressPlan("/remote", "pkg.tar.gz", []string{"a.txt", "b.txt"}, nil)
	if multiRoot.Mode != smartUncompressModeFolder {
		t.Fatalf("多顶层归档应为 folder 模式，实际 %q", multiRoot.Mode)
	}
	if multiRoot.RootName != "" {
		t.Fatalf("folder 模式不应有 RootName，实际 %q", multiRoot.RootName)
	}
}
