//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// skipIfNotRoot 在非root环境中跳过测试。
// Namespace操作需要CAP_SYS_ADMIN权限。
func skipIfNotRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("skipping: requires root privileges")
	}
}

// TestMain 确保reexec机制在测试二进制中也能工作。
func TestMain(m *testing.M) {
	MustReexecInit()
	os.Exit(m.Run())
}

// --- 配置测试 ---

func TestDefaultNamespaceConfig(t *testing.T) {
	cfg := DefaultNamespaceConfig()
	if !cfg.PID || !cfg.IPC || !cfg.Mount || !cfg.Network || !cfg.UTS {
		t.Error("default config should enable all namespaces")
	}
	if cfg.Hostname != "sandbox" {
		t.Errorf("expected hostname 'sandbox', got %q", cfg.Hostname)
	}
}

func TestMinimalNamespaceConfig(t *testing.T) {
	cfg := MinimalNamespaceConfig()
	if !cfg.PID || !cfg.Mount {
		t.Error("minimal config should enable PID and Mount")
	}
	if cfg.IPC || cfg.Network || cfg.UTS {
		t.Error("minimal config should not enable IPC/Network/UTS")
	}
}

func TestCloneFlags(t *testing.T) {
	ns := NewNamespace(DefaultNamespaceConfig())
	flags := ns.cloneFlags()
	if flags == 0 {
		t.Error("clone flags should not be zero for default config")
	}

	ns2 := NewNamespace(NamespaceConfig{})
	flags2 := ns2.cloneFlags()
	if flags2 != 0 {
		t.Error("clone flags should be zero when all namespaces disabled")
	}
}

// --- Namespace创建和运行测试 ---

func TestPIDNamespace(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	})
	ns.Stdout = nil // 后面通过管道捕获

	// 创建管道捕获输出
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ns.Stdout = w
	ns.Stderr = w

	err = ns.Start("sh", "-c", "echo $$")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, err := ns.Wait()
	w.Close()

	// 读取输出
	buf.ReadFrom(r)
	r.Close()

	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d", result.ExitCode)
	}

	// 在PID Namespace中，init进程exec后的shell应得到PID 1
	output := strings.TrimSpace(buf.String())
	if output != "1" {
		t.Errorf("expected PID 1 in namespace, got %q", output)
	}
}

func TestIPCNamespace(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(NamespaceConfig{
		IPC:   true,
		Mount: true,
	})

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// ipcs在新IPC Namespace中应该为空
	err := ns.Start("ipcs", "-q")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d, output: %s", result.ExitCode, buf.String())
	}
}

func TestNetworkNamespace(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(NamespaceConfig{
		Network:       true,
		Mount:         true,
		SetupLoopback: true,
	})

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 在新Network Namespace中应只有lo
	err := ns.Start("ip", "link", "show")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d, output: %s", result.ExitCode, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "lo") {
		t.Errorf("expected lo interface, got: %s", output)
	}
	// 不应该包含宿主机的eth0等网卡
	if strings.Contains(output, "eth0") {
		t.Errorf("host eth0 should not be visible in network namespace")
	}
}

func TestUTSNamespace(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(NamespaceConfig{
		UTS:      true,
		Mount:    true,
		Hostname: "test-sandbox",
	})

	r, w, _ := os.Pipe()
	ns.Stdout = w

	err := ns.Start("hostname")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	if result.ExitCode != 0 {
		t.Fatalf("exit code: %d", result.ExitCode)
	}

	output := strings.TrimSpace(buf.String())
	if output != "test-sandbox" {
		t.Errorf("expected hostname 'test-sandbox', got %q", output)
	}
}

func TestAllNamespaces(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	r, w, _ := os.Pipe()
	ns.Stdout = w

	// 验证PID=1且hostname=sandbox
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
}

// --- 生命周期测试 ---

func TestCleanup(t *testing.T) {
	skipIfNotRoot(t)

	ns := NewNamespace(MinimalNamespaceConfig())

	err := ns.Start("sleep", "60")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if !ns.Running() {
		t.Error("should be running after Start")
	}
	if ns.PID() == 0 {
		t.Error("PID should not be 0 after Start")
	}

	// Cleanup应该终止进程
	err = ns.Cleanup()
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	if ns.Running() {
		t.Error("should not be running after Cleanup")
	}
}

func TestCleanupHooks(t *testing.T) {
	skipIfNotRoot(t)

	ns := NewNamespace(MinimalNamespaceConfig())

	var order []int
	ns.AddCleanup(func() error { order = append(order, 1); return nil })
	ns.AddCleanup(func() error { order = append(order, 2); return nil })
	ns.AddCleanup(func() error { order = append(order, 3); return nil })

	ns.Start("true")
	ns.Wait()
	ns.Cleanup()

	// 清理函数应按逆序执行
	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Errorf("expected cleanup order [3,2,1], got %v", order)
	}
}

func TestDoubleStart(t *testing.T) {
	skipIfNotRoot(t)

	ns := NewNamespace(MinimalNamespaceConfig())
	defer ns.Cleanup()

	err := ns.Start("sleep", "60")
	if err != nil {
		t.Fatalf("first start failed: %v", err)
	}

	// 第二次Start应返回错误
	err = ns.Start("echo", "hello")
	if err == nil {
		t.Error("expected error on double start")
	}
}

func TestNsPath(t *testing.T) {
	skipIfNotRoot(t)

	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	// 启动前path应为空
	if ns.NsPath(NsPID) != "" {
		t.Error("NsPath should be empty before start")
	}

	ns.Start("sleep", "5")

	pidPath := ns.NsPath(NsPID)
	if pidPath == "" {
		t.Error("NsPath should not be empty after start")
	}
	if !strings.Contains(pidPath, "/ns/pid") {
		t.Errorf("unexpected NsPath: %s", pidPath)
	}

	// 验证procfs路径存在
	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("namespace path %s does not exist: %v", pidPath, err)
	}
}

func TestExitCode(t *testing.T) {
	skipIfNotRoot(t)

	ns := NewNamespace(MinimalNamespaceConfig())
	defer ns.Cleanup()

	result, err := ns.Execute("sh", "-c", "exit 42")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestEnvPassing(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(MinimalNamespaceConfig())
	defer ns.Cleanup()
	ns.Env = []string{"PATH=/usr/bin:/bin", "MY_VAR=hello_sandbox"}

	r, w, _ := os.Pipe()
	ns.Stdout = w

	ns.Start("sh", "-c", "echo $MY_VAR")
	ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := strings.TrimSpace(buf.String())
	if output != "hello_sandbox" {
		t.Errorf("expected 'hello_sandbox', got %q", output)
	}
}

// --- 并发测试 ---

func TestConcurrentNamespaces(t *testing.T) {
	skipIfNotRoot(t)

	const count = 10
	results := make(chan error, count)

	for i := 0; i < count; i++ {
		go func() {
			ns := NewNamespace(MinimalNamespaceConfig())
			defer ns.Cleanup()
			_, err := ns.Execute("true")
			results <- err
		}()
	}

	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent namespace %d failed: %v", i, err)
		}
	}
}
