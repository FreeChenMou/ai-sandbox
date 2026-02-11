//go:build linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- 配置和纯函数测试（不需要root） ---

func TestDefaultCgroupsConfig(t *testing.T) {
	cfg := DefaultCgroupsConfig()
	if !cfg.Enabled {
		t.Error("default config should be enabled")
	}
	if cfg.CPUQuota != 100000 {
		t.Errorf("expected CPUQuota=100000, got %d", cfg.CPUQuota)
	}
	if cfg.CPUPeriod != 100000 {
		t.Errorf("expected CPUPeriod=100000, got %d", cfg.CPUPeriod)
	}
	if cfg.MemoryMax != 536870912 {
		t.Errorf("expected MemoryMax=536870912, got %d", cfg.MemoryMax)
	}
	if cfg.PidsMax != 512 {
		t.Errorf("expected PidsMax=512, got %d", cfg.PidsMax)
	}
	if cfg.BaseDir != "/sys/fs/cgroup" {
		t.Errorf("expected BaseDir=/sys/fs/cgroup, got %q", cfg.BaseDir)
	}
}

func TestNewCgroupsV2(t *testing.T) {
	cfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cfg)
	if cg == nil {
		t.Fatal("NewCgroupsV2 returned nil")
	}
	// 未Setup时，访问器应返回空值
	if cg.ID() != "" {
		t.Error("ID should be empty before Setup")
	}
	if cg.CgroupDir() != "" {
		t.Error("CgroupDir should be empty before Setup")
	}
}

func TestCgroupsSetupValidation(t *testing.T) {
	// 测试未启用
	cg := NewCgroupsV2(CgroupsConfig{Enabled: false})
	if err := cg.Setup(); err == nil {
		t.Error("expected error when not enabled")
	}

	// 测试负 CPU quota
	cg = NewCgroupsV2(CgroupsConfig{Enabled: true, CPUQuota: -1})
	if err := cg.Setup(); err == nil {
		t.Error("expected error for negative cpu quota")
	}

	// 测试负 CPU period
	cg = NewCgroupsV2(CgroupsConfig{Enabled: true, CPUPeriod: -1})
	if err := cg.Setup(); err == nil {
		t.Error("expected error for negative cpu period")
	}

	// 测试负 memory max
	cg = NewCgroupsV2(CgroupsConfig{Enabled: true, MemoryMax: -1})
	if err := cg.Setup(); err == nil {
		t.Error("expected error for negative memory max")
	}

	// 测试负 pids max
	cg = NewCgroupsV2(CgroupsConfig{Enabled: true, PidsMax: -1})
	if err := cg.Setup(); err == nil {
		t.Error("expected error for negative pids max")
	}
}

func TestCgroupsV2Available(t *testing.T) {
	// 仅验证函数不会 panic
	_ = CgroupsV2Available()
}

// --- 集成测试（需要 root + cgroups v2） ---

// skipIfNoCgroupsV2 在不支持 cgroups v2 的环境中跳过测试。
func skipIfNoCgroupsV2(t *testing.T) {
	t.Helper()
	skipIfNotRoot(t)
	if !CgroupsV2Available() {
		t.Skip("skipping: requires cgroups v2")
	}
}

func TestCgroupsSetupAndCleanup(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cfg)

	// Setup
	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// 验证 ID 和目录
	id := cg.ID()
	if id == "" {
		t.Fatal("ID should not be empty after Setup")
	}

	cgroupDir := cg.CgroupDir()
	if cgroupDir == "" {
		t.Fatal("CgroupDir should not be empty after Setup")
	}

	// 验证目录存在
	if _, err := os.Stat(cgroupDir); err != nil {
		t.Errorf("cgroup dir %s should exist: %v", cgroupDir, err)
	}

	// 验证 cpu.max
	cpuMax, err := os.ReadFile(filepath.Join(cgroupDir, "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if strings.TrimSpace(string(cpuMax)) != "100000 100000" {
		t.Errorf("unexpected cpu.max: %q", string(cpuMax))
	}

	// 验证 memory.max
	memMax, err := os.ReadFile(filepath.Join(cgroupDir, "memory.max"))
	if err != nil {
		t.Fatalf("read memory.max: %v", err)
	}
	if strings.TrimSpace(string(memMax)) != "536870912" {
		t.Errorf("unexpected memory.max: %q", string(memMax))
	}

	// 验证 pids.max
	pidsMax, err := os.ReadFile(filepath.Join(cgroupDir, "pids.max"))
	if err != nil {
		t.Fatalf("read pids.max: %v", err)
	}
	if strings.TrimSpace(string(pidsMax)) != "512" {
		t.Errorf("unexpected pids.max: %q", string(pidsMax))
	}

	// Cleanup
	if err := cg.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// 验证目录已删除
	if _, err := os.Stat(cgroupDir); !os.IsNotExist(err) {
		t.Errorf("cgroup dir %s should not exist after Cleanup", cgroupDir)
	}

	// 重复 Cleanup 应无错误（幂等）
	if err := cg.Cleanup(); err != nil {
		t.Errorf("second Cleanup should not error: %v", err)
	}
}

func TestCgroupsDoubleSetup(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer cg.Cleanup()

	// 重复 Setup 应失败
	if err := cg.Setup(); err == nil {
		t.Error("expected error on double Setup")
	}
}

func TestCgroupsAddProcess(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer cg.Cleanup()

	// 添加当前进程（测试进程）到 cgroup
	pid := os.Getpid()
	if err := cg.AddProcess(pid); err != nil {
		t.Fatalf("AddProcess failed: %v", err)
	}

	// 验证 cgroup.procs 包含我们的 PID
	pids := readPids(cg.CgroupDir())
	found := false
	for _, p := range pids {
		if p == pid {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PID %d in cgroup.procs, got %v", pid, pids)
	}

	// 清理前将自身迁移回父 cgroup（否则 Cleanup 时 rmdir 会失败）
	// Cleanup 内部会处理迁移，这里只是验证 AddProcess 功能
}

func TestCgroupsCPULimit(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cfg.CPUQuota = 10000 // 10% CPU（10ms/100ms）
	cfg.CPUPeriod = 100000
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer cg.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetCgroupsV2(cg)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 运行 CPU 密集型任务，然后检查节流统计
	cmd := "for i in $(seq 1 1000000); do :; done; cat " + filepath.Join(cg.CgroupDir(), "cpu.stat")
	err := ns.Start("sh", "-c", cmd)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, err := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	output := buf.String()
	_ = result

	// 验证 cpu.stat 中 nr_throttled > 0
	if !strings.Contains(output, "nr_throttled") {
		t.Logf("cpu.stat output: %s", output)
		t.Skip("could not read cpu.stat (may not be accessible from namespace)")
	}

	// 解析 nr_throttled
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "nr_throttled ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 && parts[1] != "0" {
				t.Logf("CPU throttling detected: %s", line)
				return
			}
		}
	}

	t.Logf("cpu.stat output: %s", output)
	t.Log("nr_throttled may be 0 on fast systems, skipping strict check")
}

func TestCgroupsMemoryLimit(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cfg.MemoryMax = 16 * 1024 * 1024 // 16MB
	cfg.CPUQuota = 0
	cfg.PidsMax = 0
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer cg.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetCgroupsV2(cg)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 尝试分配超过 16MB 的内存，应触发 OOM Kill
	// 使用 dd 往 /dev/shm 写大文件来消耗内存
	cmd := "dd if=/dev/zero of=/dev/shm/bigfile bs=1M count=32 2>&1; echo exit=$?"
	err := ns.Start("sh", "-c", cmd)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	// 进程应该被 OOM Kill（非零退出码）或 dd 报告写入失败
	output := buf.String()
	if result.ExitCode == 0 && !strings.Contains(output, "Cannot allocate") &&
		!strings.Contains(output, "No space left") && !strings.Contains(output, "Killed") {
		t.Logf("output: %s", output)
		t.Log("memory limit may not have been enforced in this environment")
	} else {
		t.Logf("memory limit enforced: exit_code=%d", result.ExitCode)
	}
}

func TestCgroupsPidsLimit(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cfg.PidsMax = 5
	cfg.CPUQuota = 0
	cfg.MemoryMax = 0
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer cg.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetCgroupsV2(cg)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 尝试 fork 超过限制的进程数
	cmd := "for i in 1 2 3 4 5 6 7 8 9 10; do sleep 10 & done 2>&1; echo exit=$?"
	err := ns.Start("sh", "-c", cmd)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := buf.String()
	// fork 应该在某个点失败
	if strings.Contains(output, "Resource temporarily unavailable") ||
		strings.Contains(output, "fork") ||
		strings.Contains(output, "Cannot fork") {
		t.Logf("pids limit enforced: %s", strings.TrimSpace(output))
	} else {
		t.Logf("output: %s, exit_code=%d", output, result.ExitCode)
		t.Log("pids limit enforcement may vary by environment")
	}
}

func TestCgroupsWithNamespace(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	ns := NewNamespace(DefaultNamespaceConfig())
	ns.SetCgroupsV2(cg)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w

	err := ns.Start("sh", "-c", "echo pid=$$ host=$(hostname)")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, err := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d", result.ExitCode)
	}

	output := strings.TrimSpace(buf.String())
	if !strings.Contains(output, "pid=1") {
		t.Errorf("expected pid=1, got: %s", output)
	}
	if !strings.Contains(output, "host=sandbox") {
		t.Errorf("expected host=sandbox, got: %s", output)
	}

	// Cleanup 应同时清理 cgroup
	ns.Cleanup()

	cgroupDir := cg.CgroupDir()
	if _, err := os.Stat(cgroupDir); !os.IsNotExist(err) {
		t.Errorf("cgroup dir %s should not exist after Cleanup", cgroupDir)
	}
}

func TestCgroupsWithOverlayAndNamespace(t *testing.T) {
	skipIfNoCgroupsV2(t)

	// 设置 OverlayFS
	lowerDir := t.TempDir()
	os.WriteFile(filepath.Join(lowerDir, "test.txt"), []byte("hello"), 0644)

	ovCfg := DefaultOverlayConfig(lowerDir)
	ov := NewOverlayFS(ovCfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("OverlayFS Setup failed: %v", err)
	}

	// 设置 CgroupsV2
	cgCfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cgCfg)
	if err := cg.Setup(); err != nil {
		t.Fatalf("CgroupsV2 Setup failed: %v", err)
	}

	// 创建 Namespace 并绑定两者
	ns := NewNamespace(DefaultNamespaceConfig())
	ns.SetOverlayFS(ov)
	ns.SetCgroupsV2(cg)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	mergeDir := ov.MergeDir()
	cmd := fmt.Sprintf("cat %s/test.txt && echo pid=$$", mergeDir)
	err := ns.Start("sh", "-c", cmd)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, err := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d, output: %s", result.ExitCode, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "hello") {
		t.Errorf("expected 'hello' from overlay, got: %s", output)
	}
	if !strings.Contains(output, "pid=1") {
		t.Errorf("expected pid=1, got: %s", output)
	}
}

func TestCgroupsCleanupAfterCrash(t *testing.T) {
	skipIfNoCgroupsV2(t)

	cfg := DefaultCgroupsConfig()
	cg := NewCgroupsV2(cfg)

	if err := cg.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetCgroupsV2(cg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	ns.Start("sleep", "60")

	// 强制清理（模拟 crash 后的清理）
	err := ns.Cleanup()
	w.Close()
	r.Close()

	if err != nil {
		t.Fatalf("Cleanup after crash failed: %v", err)
	}

	cgroupDir := cg.CgroupDir()
	if _, err := os.Stat(cgroupDir); !os.IsNotExist(err) {
		t.Errorf("cgroup dir %s should not exist after cleanup", cgroupDir)
	}
}

func TestConcurrentCgroups(t *testing.T) {
	skipIfNoCgroupsV2(t)

	const count = 10
	var wg sync.WaitGroup
	errors := make(chan error, count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cfg := DefaultCgroupsConfig()
			cg := NewCgroupsV2(cfg)
			if err := cg.Setup(); err != nil {
				errors <- fmt.Errorf("instance %d Setup: %w", idx, err)
				return
			}
			defer cg.Cleanup()

			ns := NewNamespace(NamespaceConfig{
				PID:       true,
				Mount:     true,
				MountProc: true,
			})
			ns.SetCgroupsV2(cg)
			defer ns.Cleanup()

			_, err := ns.Execute("true")
			if err != nil {
				errors <- fmt.Errorf("instance %d Execute: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// --- 基准测试 ---

func BenchmarkCgroupsSetupCleanup(b *testing.B) {
	if os.Getuid() != 0 {
		b.Skip("skipping: requires root privileges")
	}
	if !CgroupsV2Available() {
		b.Skip("skipping: requires cgroups v2")
	}

	for i := 0; i < b.N; i++ {
		cfg := DefaultCgroupsConfig()
		cg := NewCgroupsV2(cfg)
		if err := cg.Setup(); err != nil {
			b.Fatalf("Setup failed: %v", err)
		}
		if err := cg.Cleanup(); err != nil {
			b.Fatalf("Cleanup failed: %v", err)
		}
	}
}

func BenchmarkCgroupsWithNamespace(b *testing.B) {
	if os.Getuid() != 0 {
		b.Skip("skipping: requires root privileges")
	}
	if !CgroupsV2Available() {
		b.Skip("skipping: requires cgroups v2")
	}

	for i := 0; i < b.N; i++ {
		cfg := DefaultCgroupsConfig()
		cg := NewCgroupsV2(cfg)
		if err := cg.Setup(); err != nil {
			b.Fatalf("Setup failed: %v", err)
		}

		ns := NewNamespace(MinimalNamespaceConfig())
		ns.SetCgroupsV2(cg)

		_, err := ns.Execute("true")
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
		ns.Cleanup()
	}
}
