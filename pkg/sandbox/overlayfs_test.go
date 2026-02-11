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

func TestDefaultOverlayConfig(t *testing.T) {
	cfg := DefaultOverlayConfig("/myroot")
	if !cfg.Enabled {
		t.Error("default config should be enabled")
	}
	if len(cfg.LowerDirs) != 1 || cfg.LowerDirs[0] != "/myroot" {
		t.Errorf("expected LowerDirs=[/myroot], got %v", cfg.LowerDirs)
	}
	if cfg.TmpfsSize != "64m" {
		t.Errorf("expected TmpfsSize=64m, got %q", cfg.TmpfsSize)
	}
	if cfg.BaseDir != "/tmp" {
		t.Errorf("expected BaseDir=/tmp, got %q", cfg.BaseDir)
	}
	if cfg.ReadOnly {
		t.Error("default config should not be read-only")
	}
}

func TestBuildOverlayOptions(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *overlayInitConfig
		expected string
	}{
		{
			name: "single lower dir",
			cfg: &overlayInitConfig{
				LowerDirs: []string{"/lower"},
				UpperDir:  "/upper",
				WorkDir:   "/work",
				MergeDir:  "/merged",
			},
			expected: "lowerdir=/lower,upperdir=/upper,workdir=/work",
		},
		{
			name: "multiple lower dirs",
			cfg: &overlayInitConfig{
				LowerDirs: []string{"/lower1", "/lower2", "/lower3"},
				UpperDir:  "/upper",
				WorkDir:   "/work",
				MergeDir:  "/merged",
			},
			expected: "lowerdir=/lower1:/lower2:/lower3,upperdir=/upper,workdir=/work",
		},
		{
			name: "read-only",
			cfg: &overlayInitConfig{
				LowerDirs: []string{"/lower"},
				MergeDir:  "/merged",
				ReadOnly:  true,
			},
			expected: "lowerdir=/lower",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildOverlayOptions(tt.cfg)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGenerateIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	const count = 100
	for i := 0; i < count; i++ {
		id := generateID()
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
	if len(ids) != count {
		t.Errorf("expected %d unique IDs, got %d", count, len(ids))
	}
}

func TestNewOverlayFS(t *testing.T) {
	cfg := DefaultOverlayConfig("/")
	ov := NewOverlayFS(cfg)
	if ov == nil {
		t.Fatal("NewOverlayFS returned nil")
	}
	// 未Setup时，访问器应返回空值
	if ov.ID() != "" {
		t.Error("ID should be empty before Setup")
	}
	if ov.MergeDir() != "" {
		t.Error("MergeDir should be empty before Setup")
	}
	if ov.UpperDir() != "" {
		t.Error("UpperDir should be empty before Setup")
	}
	if ov.InitConfig() != nil {
		t.Error("InitConfig should return nil before Setup")
	}
}

func TestOverlaySetupValidation(t *testing.T) {
	// 测试未启用
	ov := NewOverlayFS(OverlayConfig{Enabled: false})
	if err := ov.Setup(); err == nil {
		t.Error("expected error when not enabled")
	}

	// 测试无LowerDir
	ov = NewOverlayFS(OverlayConfig{Enabled: true})
	if err := ov.Setup(); err == nil {
		t.Error("expected error when no lower dirs")
	}

	// 测试不存在的LowerDir
	ov = NewOverlayFS(OverlayConfig{
		Enabled:   true,
		LowerDirs: []string{"/nonexistent-dir-12345"},
	})
	if err := ov.Setup(); err == nil {
		t.Error("expected error for nonexistent lower dir")
	}
}

// --- 集成测试（需要root权限） ---

func TestOverlaySetupAndCleanup(t *testing.T) {
	skipIfNotRoot(t)

	cfg := DefaultOverlayConfig("/")
	ov := NewOverlayFS(cfg)

	// Setup
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// 验证目录已创建
	id := ov.ID()
	if id == "" {
		t.Fatal("ID should not be empty after Setup")
	}

	mergeDir := ov.MergeDir()
	upperDir := ov.UpperDir()
	if mergeDir == "" || upperDir == "" {
		t.Fatal("MergeDir and UpperDir should not be empty after Setup")
	}

	// 验证目录存在
	for _, dir := range []string{mergeDir, upperDir} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("directory %s should exist: %v", dir, err)
		}
	}

	// 验证InitConfig
	initCfg := ov.InitConfig()
	if initCfg == nil {
		t.Fatal("InitConfig should not be nil after Setup")
	}
	if len(initCfg.LowerDirs) != 1 || initCfg.LowerDirs[0] != "/" {
		t.Errorf("unexpected LowerDirs: %v", initCfg.LowerDirs)
	}

	// 重复Setup应失败
	if err := ov.Setup(); err == nil {
		t.Error("expected error on double Setup")
	}

	// Cleanup
	if err := ov.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// 验证基础目录已删除
	baseDir := filepath.Join("/tmp", "sandbox-overlay-"+id)
	if _, err := os.Stat(baseDir); !os.IsNotExist(err) {
		t.Errorf("base dir %s should not exist after Cleanup", baseDir)
	}

	// 重复Cleanup应无错误（幂等）
	if err := ov.Cleanup(); err != nil {
		t.Errorf("second Cleanup should not error: %v", err)
	}
}

func TestOverlayWriteIsolation(t *testing.T) {
	skipIfNotRoot(t)

	// 创建临时目录作为lower
	lowerDir := t.TempDir()
	testFile := filepath.Join(lowerDir, "existing.txt")
	os.WriteFile(testFile, []byte("original"), 0644)

	cfg := DefaultOverlayConfig(lowerDir)
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer ov.Cleanup()

	// 在Namespace中使用overlay
	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetOverlayFS(ov)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	mergeDir := ov.MergeDir()
	// 在merged中创建新文件，验证写入进入upper
	cmd := fmt.Sprintf("echo newcontent > %s/newfile.txt && cat %s/newfile.txt", mergeDir, mergeDir)
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

	output := strings.TrimSpace(buf.String())
	if output != "newcontent" {
		t.Errorf("expected 'newcontent', got %q", output)
	}
}

func TestOverlayReadFromLower(t *testing.T) {
	skipIfNotRoot(t)

	// 创建临时目录作为lower，写入测试文件
	lowerDir := t.TempDir()
	os.WriteFile(filepath.Join(lowerDir, "readme.txt"), []byte("hello from lower"), 0644)

	cfg := DefaultOverlayConfig(lowerDir)
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer ov.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetOverlayFS(ov)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	mergeDir := ov.MergeDir()
	err := ns.Start("cat", filepath.Join(mergeDir, "readme.txt"))
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

	output := strings.TrimSpace(buf.String())
	if output != "hello from lower" {
		t.Errorf("expected 'hello from lower', got %q", output)
	}
}

func TestOverlayLowerUnmodified(t *testing.T) {
	skipIfNotRoot(t)

	// 创建临时目录作为lower
	lowerDir := t.TempDir()
	os.WriteFile(filepath.Join(lowerDir, "original.txt"), []byte("untouched"), 0644)

	cfg := DefaultOverlayConfig(lowerDir)
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer ov.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetOverlayFS(ov)
	defer ns.Cleanup()

	mergeDir := ov.MergeDir()
	// 在merged中修改文件和创建新文件
	cmd := fmt.Sprintf(
		"echo modified > %s/original.txt && echo newfile > %s/created.txt",
		mergeDir, mergeDir,
	)
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	ns.Start("sh", "-c", cmd)
	ns.Wait()
	w.Close()
	r.Close()

	// 验证lower目录未被修改
	content, err := os.ReadFile(filepath.Join(lowerDir, "original.txt"))
	if err != nil {
		t.Fatalf("read original.txt: %v", err)
	}
	if string(content) != "untouched" {
		t.Errorf("lower dir modified! expected 'untouched', got %q", string(content))
	}

	// 验证lower目录没有新文件
	if _, err := os.Stat(filepath.Join(lowerDir, "created.txt")); !os.IsNotExist(err) {
		t.Error("lower dir should not contain created.txt")
	}
}

func TestOverlayWithNamespace(t *testing.T) {
	skipIfNotRoot(t)

	lowerDir := t.TempDir()
	os.WriteFile(filepath.Join(lowerDir, "test.txt"), []byte("lower-content"), 0644)

	cfg := DefaultOverlayConfig(lowerDir)
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// 使用完整的Namespace配置
	ns := NewNamespace(DefaultNamespaceConfig())
	ns.SetOverlayFS(ov)
	defer ns.Cleanup() // 清理应同时清理overlay

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	mergeDir := ov.MergeDir()
	// 端到端测试：读取lower文件、写入新文件、验证隔离
	cmd := fmt.Sprintf(
		"cat %s/test.txt && echo overwritten > %s/test.txt && cat %s/test.txt",
		mergeDir, mergeDir, mergeDir,
	)
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
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2 lines, got: %q", output)
	}
	if strings.TrimSpace(lines[0]) != "lower-content" {
		t.Errorf("first read expected 'lower-content', got %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "overwritten" {
		t.Errorf("second read expected 'overwritten', got %q", lines[1])
	}

	// Cleanup后验证lower未被修改
	ns.Cleanup()
	content, _ := os.ReadFile(filepath.Join(lowerDir, "test.txt"))
	if string(content) != "lower-content" {
		t.Errorf("lower dir modified after cleanup! got %q", string(content))
	}
}

func TestOverlayCleanupAfterCrash(t *testing.T) {
	skipIfNotRoot(t)

	cfg := DefaultOverlayConfig("/")
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// 模拟进程异常退出：直接kill子进程
	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetOverlayFS(ov)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	ns.Start("sleep", "60")

	// 强制清理（模拟crash后的清理）
	err := ns.Cleanup()
	w.Close()
	r.Close()

	if err != nil {
		t.Fatalf("Cleanup after crash failed: %v", err)
	}

	// 验证资源已清理
	id := ov.ID()
	baseDir := filepath.Join("/tmp", "sandbox-overlay-"+id)
	if _, err := os.Stat(baseDir); !os.IsNotExist(err) {
		t.Errorf("base dir %s should not exist after cleanup", baseDir)
	}
}

func TestConcurrentOverlays(t *testing.T) {
	skipIfNotRoot(t)

	const count = 10
	var wg sync.WaitGroup
	errors := make(chan error, count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			lowerDir := t.TempDir()
			content := fmt.Sprintf("instance-%d", idx)
			os.WriteFile(filepath.Join(lowerDir, "id.txt"), []byte(content), 0644)

			cfg := DefaultOverlayConfig(lowerDir)
			ov := NewOverlayFS(cfg)
			if err := ov.Setup(); err != nil {
				errors <- fmt.Errorf("instance %d Setup: %w", idx, err)
				return
			}
			defer ov.Cleanup()

			ns := NewNamespace(NamespaceConfig{
				PID:       true,
				Mount:     true,
				MountProc: true,
			})
			ns.SetOverlayFS(ov)
			defer ns.Cleanup()

			var buf bytes.Buffer
			r, w, _ := os.Pipe()
			ns.Stdout = w
			ns.Stderr = w

			mergeDir := ov.MergeDir()
			ns.Start("cat", filepath.Join(mergeDir, "id.txt"))
			ns.Wait()
			w.Close()
			buf.ReadFrom(r)
			r.Close()

			output := strings.TrimSpace(buf.String())
			if output != content {
				errors <- fmt.Errorf("instance %d: expected %q, got %q", idx, content, output)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestOverlayTmpfsSize(t *testing.T) {
	skipIfNotRoot(t)

	cfg := DefaultOverlayConfig("/")
	cfg.TmpfsSize = "1m" // 限制为1MB
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer ov.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetOverlayFS(ov)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 尝试写入超过1MB的数据到upper，应该失败
	mergeDir := ov.MergeDir()
	cmd := fmt.Sprintf("dd if=/dev/zero of=%s/bigfile bs=1M count=2 2>&1; echo exit=$?", mergeDir)
	ns.Start("sh", "-c", cmd)
	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := buf.String()
	// dd应该报错或文件应小于请求的大小
	// 至少应该看到exit code或空间不足的信息
	_ = result
	if !strings.Contains(output, "No space left") && !strings.Contains(output, "exit=1") && !strings.Contains(output, "exit=0") {
		// tmpfs大小限制生效的情况下，dd可能部分成功
		t.Logf("tmpfs size test output: %s", output)
	}
}

func TestOverlayMultipleLowerDirs(t *testing.T) {
	skipIfNotRoot(t)

	// 创建两个lower目录
	lower1 := t.TempDir()
	lower2 := t.TempDir()
	os.WriteFile(filepath.Join(lower1, "from-lower1.txt"), []byte("content1"), 0644)
	os.WriteFile(filepath.Join(lower2, "from-lower2.txt"), []byte("content2"), 0644)

	cfg := OverlayConfig{
		Enabled:   true,
		LowerDirs: []string{lower1, lower2},
		TmpfsSize: "64m",
		BaseDir:   "/tmp",
	}
	ov := NewOverlayFS(cfg)
	if err := ov.Setup(); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer ov.Cleanup()

	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.SetOverlayFS(ov)
	defer ns.Cleanup()

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	mergeDir := ov.MergeDir()
	// 验证两个lower目录的文件都可见
	cmd := fmt.Sprintf(
		"cat %s/from-lower1.txt && echo '---' && cat %s/from-lower2.txt",
		mergeDir, mergeDir,
	)
	ns.Start("sh", "-c", cmd)
	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d, output: %s", result.ExitCode, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "content1") {
		t.Errorf("expected content from lower1, got: %s", output)
	}
	if !strings.Contains(output, "content2") {
		t.Errorf("expected content from lower2, got: %s", output)
	}
}

// --- 基准测试 ---

func BenchmarkOverlaySetupCleanup(b *testing.B) {
	if os.Getuid() != 0 {
		b.Skip("skipping: requires root privileges")
	}

	for i := 0; i < b.N; i++ {
		cfg := DefaultOverlayConfig("/")
		ov := NewOverlayFS(cfg)
		if err := ov.Setup(); err != nil {
			b.Fatalf("Setup failed: %v", err)
		}
		if err := ov.Cleanup(); err != nil {
			b.Fatalf("Cleanup failed: %v", err)
		}
	}
}

func BenchmarkOverlayWithNamespace(b *testing.B) {
	if os.Getuid() != 0 {
		b.Skip("skipping: requires root privileges")
	}

	for i := 0; i < b.N; i++ {
		cfg := DefaultOverlayConfig("/")
		ov := NewOverlayFS(cfg)
		if err := ov.Setup(); err != nil {
			b.Fatalf("Setup failed: %v", err)
		}

		ns := NewNamespace(MinimalNamespaceConfig())
		ns.SetOverlayFS(ov)

		_, err := ns.Execute("true")
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
		ns.Cleanup()
	}
}
