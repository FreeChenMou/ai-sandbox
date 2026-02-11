//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// ===================================================================
// Seccomp 单元测试（不需要 root）
// ===================================================================

func TestDefaultSeccompConfig(t *testing.T) {
	cfg := DefaultSeccompConfig()
	if !cfg.Enabled {
		t.Error("default config should be enabled")
	}
	if cfg.BlockedSyscalls != nil {
		t.Error("default BlockedSyscalls should be nil (use defaultBlockedSyscalls)")
	}
	if cfg.BlockedSocketFamilies != nil {
		t.Error("default BlockedSocketFamilies should be nil (use defaultBlockedSocketFamilies)")
	}
	if cfg.LogDenied {
		t.Error("default LogDenied should be false")
	}
}

func TestDefaultBlockedSyscallsNotEmpty(t *testing.T) {
	if len(defaultBlockedSyscalls) == 0 {
		t.Error("defaultBlockedSyscalls should not be empty")
	}
	// 验证关键危险 syscall 在默认黑名单中
	critical := []string{"ptrace", "mount", "pivot_root", "chroot", "reboot",
		"kexec_load", "setns", "unshare", "bpf"}
	for _, name := range critical {
		found := false
		for _, blocked := range defaultBlockedSyscalls {
			if blocked == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("critical syscall %q should be in defaultBlockedSyscalls", name)
		}
	}
}

func TestDefaultBlockedSocketFamiliesNotEmpty(t *testing.T) {
	if len(defaultBlockedSocketFamilies) == 0 {
		t.Error("defaultBlockedSocketFamilies should not be empty")
	}
	// 验证 Netlink、Packet、Bluetooth 在默认列表中
	expected := map[int]string{
		unix.AF_NETLINK:   "AF_NETLINK",
		unix.AF_PACKET:    "AF_PACKET",
		unix.AF_BLUETOOTH: "AF_BLUETOOTH",
	}
	for af, name := range expected {
		found := false
		for _, blocked := range defaultBlockedSocketFamilies {
			if blocked == af {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s (%d) should be in defaultBlockedSocketFamilies", name, af)
		}
	}
}

func TestResolveBlocklist(t *testing.T) {
	nrs, err := resolveBlocklist([]string{"ptrace", "mount", "reboot"})
	if err != nil {
		t.Fatalf("resolveBlocklist failed: %v", err)
	}
	if len(nrs) != 3 {
		t.Fatalf("expected 3 syscall numbers, got %d", len(nrs))
	}
	// 验证结果已排序
	for i := 1; i < len(nrs); i++ {
		if nrs[i] <= nrs[i-1] {
			t.Errorf("result should be sorted: nrs[%d]=%d <= nrs[%d]=%d", i, nrs[i], i-1, nrs[i-1])
		}
	}
	// 验证实际值
	expectedMap := map[int]bool{
		unix.SYS_PTRACE: true,
		unix.SYS_MOUNT:  true,
		unix.SYS_REBOOT: true,
	}
	for _, nr := range nrs {
		if !expectedMap[nr] {
			t.Errorf("unexpected syscall number %d in result", nr)
		}
	}
}

func TestResolveBlocklistDedup(t *testing.T) {
	nrs, err := resolveBlocklist([]string{"ptrace", "ptrace", "mount"})
	if err != nil {
		t.Fatalf("resolveBlocklist failed: %v", err)
	}
	if len(nrs) != 2 {
		t.Errorf("expected 2 unique syscall numbers, got %d", len(nrs))
	}
}

func TestResolveBlocklistInvalid(t *testing.T) {
	_, err := resolveBlocklist([]string{"ptrace", "nonexistent_syscall"})
	if err == nil {
		t.Error("expected error for unknown syscall name")
	}
	if !strings.Contains(err.Error(), "nonexistent_syscall") {
		t.Errorf("error should mention unknown syscall name, got: %v", err)
	}
}

func TestResolveBlocklistEmpty(t *testing.T) {
	nrs, err := resolveBlocklist([]string{})
	if err != nil {
		t.Fatalf("resolveBlocklist failed: %v", err)
	}
	if len(nrs) != 0 {
		t.Errorf("expected empty result, got %d", len(nrs))
	}
}

func TestResolveBlocklistAllDefaults(t *testing.T) {
	// 所有默认黑名单 syscall 都应该能正确解析
	nrs, err := resolveBlocklist(defaultBlockedSyscalls)
	if err != nil {
		t.Fatalf("resolveBlocklist(defaultBlockedSyscalls) failed: %v", err)
	}
	if len(nrs) == 0 {
		t.Error("resolved default blocklist should not be empty")
	}
}

func TestSyscallMapComplete(t *testing.T) {
	// 验证 defaultBlockedSyscalls 中的每个名称都在 syscallMap 中
	for _, name := range defaultBlockedSyscalls {
		if _, ok := syscallMap[name]; !ok {
			t.Errorf("syscall %q is in defaultBlockedSyscalls but not in syscallMap", name)
		}
	}
}

func TestBuildBPFProgramBlockedOnly(t *testing.T) {
	// 只有 blocked syscalls，没有 socket 过滤
	blocked := []int{unix.SYS_PTRACE, unix.SYS_MOUNT}
	program := buildBPFProgram(blocked, nil, false)

	// 期望结构：4(arch check) + 0(no socket) + 2(blocked) + 1(allow) + 0(no socket filter) + 1(kill) = 8
	expectedLen := 4 + 2 + 1 + 1
	if len(program) != expectedLen {
		t.Errorf("expected program length %d, got %d", expectedLen, len(program))
	}

	// 第一条：load arch
	if program[0].Code != bpfLD|bpfW|bpfABS || program[0].K != seccompDataArchOffset {
		t.Error("program[0] should load arch")
	}
	// 第二条：jeq arch
	if program[1].K != auditArchX86_64 {
		t.Error("program[1] should check x86_64 arch")
	}
	// 第三条：ret kill (wrong arch)
	if program[2].Code != bpfRET || program[2].K != seccompRetKillProcess {
		t.Error("program[2] should ret kill for wrong arch")
	}
	// 倒数第二条：ret allow (默认)
	if program[len(program)-2].Code != bpfRET || program[len(program)-2].K != seccompRetAllow {
		t.Error("second to last should be ret ALLOW")
	}
	// 最后一条：ret kill (匹配黑名单)
	if program[len(program)-1].Code != bpfRET || program[len(program)-1].K != seccompRetKillProcess {
		t.Error("last instruction should be ret KILL")
	}
}

func TestBuildBPFProgramWithSocketFilter(t *testing.T) {
	blocked := []int{unix.SYS_PTRACE}
	families := []int{unix.AF_NETLINK, unix.AF_PACKET}
	program := buildBPFProgram(blocked, families, false)

	// 期望结构：4(arch) + 1(socket redirect) + 1(blocked) + 1(allow)
	//          + 1(load args0) + 2(family checks) + 1(socket allow) + 1(kill) = 12
	expectedLen := 4 + 1 + 1 + 1 + 1 + 2 + 1 + 1
	if len(program) != expectedLen {
		t.Errorf("expected program length %d, got %d", expectedLen, len(program))
	}

	// 验证 socket redirect 指令存在（第5条，索引4）
	socketRedirect := program[4]
	if socketRedirect.K != uint32(unix.SYS_SOCKET) {
		t.Errorf("program[4] should check SYS_SOCKET, got K=%d", socketRedirect.K)
	}
}

func TestBuildBPFProgramSocketOnly(t *testing.T) {
	// 没有 blocked syscalls，只有 socket 过滤
	families := []int{unix.AF_NETLINK}
	program := buildBPFProgram(nil, families, false)

	// 4(arch) + 1(socket redirect) + 0(blocked) + 1(allow) + 1(load args0) + 1(family) + 1(socket allow) + 1(kill) = 10
	expectedLen := 4 + 1 + 0 + 1 + 1 + 1 + 1 + 1
	if len(program) != expectedLen {
		t.Errorf("expected program length %d, got %d", expectedLen, len(program))
	}
}

func TestBuildBPFProgramEmpty(t *testing.T) {
	program := buildBPFProgram(nil, nil, false)
	// 4(arch) + 0 + 0 + 1(allow) + 0 + 1(kill) = 6
	expectedLen := 6
	if len(program) != expectedLen {
		t.Errorf("expected program length %d, got %d", expectedLen, len(program))
	}
}

func TestBuildBPFProgramLogDenied(t *testing.T) {
	blocked := []int{unix.SYS_PTRACE}
	program := buildBPFProgram(blocked, nil, true)

	// 最后一条应该是 RET LOG 而非 RET KILL
	last := program[len(program)-1]
	if last.K != seccompRetLog {
		t.Errorf("last instruction should be RET LOG when logDenied=true, got K=0x%x", last.K)
	}
	// 但架构检查的 kill 仍然是 KILL_PROCESS（不受 logDenied 影响）
	if program[2].K != seccompRetKillProcess {
		t.Error("arch check kill should always be KILL_PROCESS regardless of logDenied")
	}
}

func TestSeccompAvailable(t *testing.T) {
	// 这个测试在大多数 Linux 内核上应该通过
	result := seccompAvailable()
	// 只记录结果，不强制要求
	t.Logf("seccomp available: %v", result)
}

func TestApplySeccompNil(t *testing.T) {
	// nil 配置不应该报错
	err := applySeccomp(nil)
	if err != nil {
		t.Errorf("applySeccomp(nil) should not error, got: %v", err)
	}
}

func TestApplySeccompEmpty(t *testing.T) {
	// 空黑名单不应该报错
	cfg := &seccompInitConfig{}
	err := applySeccomp(cfg)
	if err != nil {
		t.Errorf("applySeccomp(empty) should not error, got: %v", err)
	}
}

// ===================================================================
// Seccomp 集成测试（需要 root）
// ===================================================================

func TestSeccompBlockedSyscall(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	scfg := DefaultSeccompConfig()
	ns.SetSeccomp(&scfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// mount 在默认黑名单中，exec 使 mount 替换 sh 成为 PID 1，
	// 被 seccomp KILL_PROCESS 杀死时整个进程终止
	err := ns.Start("sh", "-c", "exec mount -t tmpfs none /tmp")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := strings.TrimSpace(buf.String())
	t.Logf("output: %q, exit: %d", output, result.ExitCode)

	// 退出码应非0（进程被 seccomp 信号杀死）
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code when blocked syscall is attempted")
	}
}

func TestSeccompAllowedSyscall(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	scfg := DefaultSeccompConfig()
	ns.SetSeccomp(&scfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// echo、ls 等常规命令不在黑名单中，应正常执行
	err := ns.Start("sh", "-c", "echo seccomp-ok && ls / > /dev/null && echo done")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := strings.TrimSpace(buf.String())
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d, output: %s", result.ExitCode, output)
	}
	if !strings.Contains(output, "seccomp-ok") {
		t.Errorf("expected 'seccomp-ok' in output, got: %s", output)
	}
	if !strings.Contains(output, "done") {
		t.Errorf("expected 'done' in output, got: %s", output)
	}
}

func TestSeccompWithNamespace(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	scfg := DefaultSeccompConfig()
	ns.SetSeccomp(&scfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// 在完整 namespace + seccomp 下执行多个常规操作
	err := ns.Start("sh", "-c", `
		echo "pid=$$"
		hostname
		ls /proc/self/status > /dev/null
		echo "all-ok"
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
	if !strings.Contains(output, "all-ok") {
		t.Errorf("expected 'all-ok', got: %s", output)
	}
}

func TestSeccompLogMode(t *testing.T) {
	skipIfNotRoot(t)

	var buf bytes.Buffer
	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	scfg := DefaultSeccompConfig()
	scfg.LogDenied = true // 仅记录，不杀死
	ns.SetSeccomp(&scfg)

	r, w, _ := os.Pipe()
	ns.Stdout = w
	ns.Stderr = w

	// log 模式下进程不应被杀死
	err := ns.Start("sh", "-c", "echo log-mode-ok")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	result, _ := ns.Wait()
	w.Close()
	buf.ReadFrom(r)
	r.Close()

	output := strings.TrimSpace(buf.String())
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0 in log mode, got %d", result.ExitCode)
	}
	if !strings.Contains(output, "log-mode-ok") {
		t.Errorf("expected 'log-mode-ok', got: %s", output)
	}
}

func TestSeccompExitCode(t *testing.T) {
	skipIfNotRoot(t)

	ns := NewNamespace(DefaultNamespaceConfig())
	defer ns.Cleanup()

	scfg := DefaultSeccompConfig()
	ns.SetSeccomp(&scfg)

	// 正常退出码应正确传递
	result, err := ns.Execute("sh", "-c", "exit 42")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestConcurrentSeccomp(t *testing.T) {
	skipIfNotRoot(t)

	const count = 5
	results := make(chan error, count)

	for i := 0; i < count; i++ {
		go func() {
			ns := NewNamespace(DefaultNamespaceConfig())
			defer ns.Cleanup()
			scfg := DefaultSeccompConfig()
			ns.SetSeccomp(&scfg)
			_, err := ns.Execute("true")
			results <- err
		}()
	}

	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent seccomp %d failed: %v", i, err)
		}
	}
}
