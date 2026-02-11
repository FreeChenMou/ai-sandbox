//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ===================================================================
// PivotRoot 单元测试（不需要 root）
// ===================================================================

func TestDefaultPivotRootConfig(t *testing.T) {
	cfg := DefaultPivotRootConfig()
	if !cfg.Enabled {
		t.Error("default config should be enabled")
	}
	if cfg.RootDir != "" {
		t.Errorf("default RootDir should be empty, got %q", cfg.RootDir)
	}
}

// ===================================================================
// PivotRoot 集成测试（需要 root）
// ===================================================================

func TestPivotRootWithOverlay(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	// 配置 OverlayFS
	ovConfig := DefaultOverlayConfig("/")
	ov := NewOverlayFS(ovConfig)
	if err := ov.Setup(); err != nil {
		t.Fatalf("overlay setup: %v", err)
	}
	ns.SetOverlayFS(ov)

	// 启用 pivot_root
	pcfg := DefaultPivotRootConfig()
	ns.SetPivotRoot(&pcfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// pivot_root 后 / 应该是 overlay merged 目录
	err := ns.Start("sh", "-c", "echo pivot-ok && ls / > /dev/null && echo done")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := strings.TrimSpace(buf.String())
	if result.ExitCode != 0 {
		t.Errorf("exit code %d, output: %s", result.ExitCode, output)
	}
	if !strings.Contains(output, "pivot-ok") {
		t.Errorf("expected 'pivot-ok', got: %s", output)
	}
	if !strings.Contains(output, "done") {
		t.Errorf("expected 'done', got: %s", output)
	}
}

func TestPivotRootEscape(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	ovConfig := DefaultOverlayConfig("/")
	ov := NewOverlayFS(ovConfig)
	if err := ov.Setup(); err != nil {
		t.Fatalf("overlay setup: %v", err)
	}
	ns.SetOverlayFS(ov)

	pcfg := DefaultPivotRootConfig()
	ns.SetPivotRoot(&pcfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 尝试通过 ../../ 路径逃逸，应该无法超出新 root
	// /.pivot_old 应该已被 umount 和删除
	err := ns.Start("sh", "-c", `
		if [ -d "/.pivot_old" ]; then
			echo "ESCAPE: .pivot_old exists"
		else
			echo "SAFE: no .pivot_old"
		fi
		# 尝试通过路径遍历逃逸
		realpath /../../etc/hostname 2>/dev/null || echo "realpath-ok"
	`)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := buf.String()
	if result.ExitCode != 0 {
		t.Logf("exit code: %d, output: %s", result.ExitCode, output)
	}
	if strings.Contains(output, "ESCAPE") {
		t.Error(".pivot_old should not exist after pivot_root")
	}
	if !strings.Contains(output, "SAFE") {
		t.Errorf("expected 'SAFE' in output, got: %s", output)
	}
}

func TestPivotRootProcVisible(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	ovConfig := DefaultOverlayConfig("/")
	ov := NewOverlayFS(ovConfig)
	if err := ov.Setup(); err != nil {
		t.Fatalf("overlay setup: %v", err)
	}
	ns.SetOverlayFS(ov)

	pcfg := DefaultPivotRootConfig()
	ns.SetPivotRoot(&pcfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// pivot_root 后 /proc 应正确挂载，反映新 namespace 内的进程
	err := ns.Start("sh", "-c", `
		if [ -f /proc/self/status ]; then
			echo "proc-ok"
			cat /proc/self/status | head -1
		else
			echo "proc-missing"
		fi
	`)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := buf.String()
	if result.ExitCode != 0 {
		t.Errorf("exit code %d, output: %s", result.ExitCode, output)
	}
	if !strings.Contains(output, "proc-ok") {
		t.Errorf("expected 'proc-ok', got: %s", output)
	}
}

func TestPivotRootDevAvailable(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	ovConfig := DefaultOverlayConfig("/")
	ov := NewOverlayFS(ovConfig)
	if err := ov.Setup(); err != nil {
		t.Fatalf("overlay setup: %v", err)
	}
	ns.SetOverlayFS(ov)

	pcfg := DefaultPivotRootConfig()
	ns.SetPivotRoot(&pcfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 验证 /dev/null、/dev/zero、/dev/urandom 可用
	err := ns.Start("sh", "-c", `
		echo test > /dev/null && echo "null-ok"
		head -c 4 /dev/zero | wc -c | tr -d ' '
		head -c 4 /dev/urandom | wc -c | tr -d ' '
	`)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := buf.String()
	if result.ExitCode != 0 {
		t.Errorf("exit code %d, output: %s", result.ExitCode, output)
	}
	if !strings.Contains(output, "null-ok") {
		t.Errorf("expected 'null-ok', got: %s", output)
	}
}

func TestPivotRootWriteIsolation(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	ovConfig := DefaultOverlayConfig("/")
	ov := NewOverlayFS(ovConfig)
	if err := ov.Setup(); err != nil {
		t.Fatalf("overlay setup: %v", err)
	}
	ns.SetOverlayFS(ov)

	pcfg := DefaultPivotRootConfig()
	ns.SetPivotRoot(&pcfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// pivot_root + overlay 下写入应落到 tmpfs，不影响宿主机
	err := ns.Start("sh", "-c", `
		echo "sandbox-marker" > /tmp/pivot-test-file
		if [ -f /tmp/pivot-test-file ]; then
			cat /tmp/pivot-test-file
		fi
	`)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := strings.TrimSpace(buf.String())
	if result.ExitCode != 0 {
		t.Errorf("exit code %d, output: %s", result.ExitCode, output)
	}
	if !strings.Contains(output, "sandbox-marker") {
		t.Errorf("expected 'sandbox-marker', got: %s", output)
	}
	// 沙箱退出后宿主机上不应存在此文件
	if _, err := os.Stat("/tmp/pivot-test-file"); err == nil {
		os.Remove("/tmp/pivot-test-file")
		t.Error("file should not exist on host after sandbox exit")
	}
}

func TestPivotRootWithSeccomp(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	ovConfig := DefaultOverlayConfig("/")
	ov := NewOverlayFS(ovConfig)
	if err := ov.Setup(); err != nil {
		t.Fatalf("overlay setup: %v", err)
	}
	ns.SetOverlayFS(ov)

	pcfg := DefaultPivotRootConfig()
	ns.SetPivotRoot(&pcfg)

	scfg := DefaultSeccompConfig()
	ns.SetSeccomp(&scfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 双层防护：pivot_root + seccomp
	err := ns.Start("sh", "-c", `
		echo "pid=$$"
		hostname
		ls /proc/self/status > /dev/null
		echo "double-ok"
	`)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := buf.String()
	if result.ExitCode != 0 {
		t.Errorf("exit code %d, output: %s", result.ExitCode, output)
	}
	if !strings.Contains(output, "pid=1") {
		t.Errorf("expected pid=1, got: %s", output)
	}
	if !strings.Contains(output, "double-ok") {
		t.Errorf("expected 'double-ok', got: %s", output)
	}
}

func TestConcurrentPivotRoot(t *testing.T) {
	skipIfNotRoot(t)

	const count = 5
	results := make(chan error, count)

	for i := 0; i < count; i++ {
		go func() {
			ns := NewNamespace(DefaultNamespaceConfig())
			defer ns.Cleanup()

			ovConfig := DefaultOverlayConfig("/")
			ov := NewOverlayFS(ovConfig)
			if err := ov.Setup(); err != nil {
				results <- err
				return
			}
			ns.SetOverlayFS(ov)

			pcfg := DefaultPivotRootConfig()
			ns.SetPivotRoot(&pcfg)

			_, err := ns.Execute("true")
			results <- err
		}()
	}

	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent pivot_root %d failed: %v", i, err)
		}
	}
}
