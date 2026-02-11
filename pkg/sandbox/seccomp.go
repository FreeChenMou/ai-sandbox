//go:build linux

package sandbox

import (
	"fmt"
	"sort"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SeccompConfig 定义 Seccomp-BPF 系统调用过滤的配置（父进程侧）。
//
// 设计思路：Namespace + OverlayFS 已限制了 Agent 的可见域（进程树、文件系统、网络栈），
// 在此基础上 Seccomp 只需黑名单禁止内核级危险操作和无关的 socket 协议族，
// 而非白名单限制所有 syscall。这样 Agent 在可见域内可以正常执行大部分系统调用。
type SeccompConfig struct {
	Enabled               bool     // 是否启用 Seccomp 过滤
	BlockedSyscalls       []string // 要禁止的 syscall 名称列表（空则使用默认黑名单）
	BlockedSocketFamilies []int    // 要禁止的 socket 协议族（AF_*，空则使用默认列表）
	LogDenied             bool     // 是否仅记录被拒绝的 syscall 而不杀死进程（调试用）
}

// DefaultSeccompConfig 返回默认的 Seccomp 配置：
// 启用、使用默认黑名单 + 默认 socket 协议族黑名单、违规杀死进程。
func DefaultSeccompConfig() SeccompConfig {
	return SeccompConfig{
		Enabled:               true,
		BlockedSyscalls:       nil, // nil 表示使用 defaultBlockedSyscalls
		BlockedSocketFamilies: nil, // nil 表示使用 defaultBlockedSocketFamilies
		LogDenied:             false,
	}
}

// seccompInitConfig 通过管道传递给子进程的 seccomp 配置。
// 父进程负责将 syscall 名称解析为号码，子进程直接使用号码构建 BPF 程序。
type seccompInitConfig struct {
	BlockedSyscalls       []int `json:"blocked_syscalls"`                  // 禁止的 syscall 号码列表
	BlockedSocketFamilies []int `json:"blocked_socket_families,omitempty"` // 禁止的 socket 协议族
	LogDenied             bool  `json:"log_denied,omitempty"`
}

// defaultBlockedSyscalls 定义默认禁止的危险系统调用。
// 这些 syscall 可用于逃逸隔离边界、加载内核代码或窃取信息。
// init 阶段（mount、pivot_root、sethostname 等）在 seccomp 加载前已完成，
// exec 后不再需要这些特权操作。
var defaultBlockedSyscalls = []string{
	// 进程调试/注入
	"ptrace",

	// 文件系统挂载（init 阶段已完成）
	"mount", "umount2",

	// root 切换（init 阶段已完成）
	"pivot_root", "chroot",

	// 系统控制
	"reboot",
	"swapon", "swapoff",
	"acct",

	// 内核模块
	"init_module", "finit_module", "delete_module",
	"create_module",

	// 内核加载
	"kexec_load", "kexec_file_load",

	// Namespace 操作（防止逃逸）
	"setns", "unshare",

	// 内核密钥管理
	"keyctl", "request_key", "add_key",

	// BPF 程序加载
	"bpf",

	// 可被利用的内核接口
	"userfaultfd",
	"perf_event_open",
	"lookup_dcookie",

	// 文件系统 handle（可绕过 DAC 权限检查）
	"open_by_handle_at", "name_to_handle_at",

	// 系统时间修改
	"clock_settime", "settimeofday", "adjtimex", "clock_adjtime",

	// I/O 端口直接访问
	"ioperm", "iopl",

	// 文件系统监控（可监控宿主机活动）
	"fanotify_init",

	// 虚拟终端
	"vhangup",

	// NFS 管理
	"nfsservctl",
}

// defaultBlockedSocketFamilies 定义默认禁止的 socket 协议族。
// 这些协议族用于内核通信、抓包、硬件控制等，与 Agent 正常业务无关。
// Agent 正常使用的 AF_UNIX(1)、AF_INET(2)、AF_INET6(10) 不受影响。
var defaultBlockedSocketFamilies = []int{
	unix.AF_NETLINK,   // 16 - 内核通信（监控内核事件、网络配置）
	unix.AF_PACKET,    // 17 - 原始数据包捕获/注入（抓包嗅探）
	unix.AF_BLUETOOTH, // 31 - 蓝牙（硬件访问）
	unix.AF_KEY,       // 15 - IPsec 密钥管理（内核安全）
	unix.AF_ALG,       // 38 - 内核加密 API
	unix.AF_VSOCK,     // 40 - 虚拟机/宿主机通信
	unix.AF_XDP,       // 44 - XDP 原始数据包访问
}

// syscallMap 存储 syscall 名称到号码的映射（amd64）。
var syscallMap = map[string]int{
	"ptrace":            unix.SYS_PTRACE,
	"mount":             unix.SYS_MOUNT,
	"umount2":           unix.SYS_UMOUNT2,
	"pivot_root":        unix.SYS_PIVOT_ROOT,
	"chroot":            unix.SYS_CHROOT,
	"reboot":            unix.SYS_REBOOT,
	"swapon":            unix.SYS_SWAPON,
	"swapoff":           unix.SYS_SWAPOFF,
	"acct":              unix.SYS_ACCT,
	"init_module":       unix.SYS_INIT_MODULE,
	"finit_module":      unix.SYS_FINIT_MODULE,
	"delete_module":     unix.SYS_DELETE_MODULE,
	"create_module":     unix.SYS_CREATE_MODULE,
	"kexec_load":        unix.SYS_KEXEC_LOAD,
	"kexec_file_load":   unix.SYS_KEXEC_FILE_LOAD,
	"setns":             unix.SYS_SETNS,
	"unshare":           unix.SYS_UNSHARE,
	"keyctl":            unix.SYS_KEYCTL,
	"request_key":       unix.SYS_REQUEST_KEY,
	"add_key":           unix.SYS_ADD_KEY,
	"bpf":               unix.SYS_BPF,
	"userfaultfd":       unix.SYS_USERFAULTFD,
	"perf_event_open":   unix.SYS_PERF_EVENT_OPEN,
	"lookup_dcookie":    unix.SYS_LOOKUP_DCOOKIE,
	"open_by_handle_at": unix.SYS_OPEN_BY_HANDLE_AT,
	"name_to_handle_at": unix.SYS_NAME_TO_HANDLE_AT,
	"clock_settime":     unix.SYS_CLOCK_SETTIME,
	"settimeofday":      unix.SYS_SETTIMEOFDAY,
	"adjtimex":          unix.SYS_ADJTIMEX,
	"clock_adjtime":     unix.SYS_CLOCK_ADJTIME,
	"ioperm":            unix.SYS_IOPERM,
	"iopl":              unix.SYS_IOPL,
	"fanotify_init":     unix.SYS_FANOTIFY_INIT,
	"vhangup":           unix.SYS_VHANGUP,
	"nfsservctl":        unix.SYS_NFSSERVCTL,
}

// resolveBlocklist 将 syscall 名称列表转换为 syscall 号码列表。
// 如果遇到未知的 syscall 名称，返回错误。
func resolveBlocklist(names []string) ([]int, error) {
	seen := make(map[int]bool)
	var nrs []int

	for _, name := range names {
		nr, ok := syscallMap[name]
		if !ok {
			return nil, fmt.Errorf("unknown syscall: %q", name)
		}
		if !seen[nr] {
			seen[nr] = true
			nrs = append(nrs, nr)
		}
	}

	sort.Ints(nrs)
	return nrs, nil
}

// seccomp_data 结构体中各字段的偏移量（用于 BPF 加载指令）。
//
//	struct seccomp_data {
//	    int   nr;                    // offset 0
//	    __u32 arch;                  // offset 4
//	    __u64 instruction_pointer;   // offset 8
//	    __u64 args[6];               // offset 16, 24, 32, ...
//	};
const (
	seccompDataNROffset    = 0
	seccompDataArchOffset  = 4
	seccompDataArgs0Offset = 16 // args[0] 低 32 位（little-endian x86_64）
)

// buildBPFProgram 根据被禁止的 syscall 号码和 socket 协议族构建 BPF 字节码。
//
// BPF 程序结构（黑名单 + socket 参数过滤）：
//
//	Section A: 架构检查
//	  [0] load arch
//	  [1] jeq AUDIT_ARCH_X86_64 → skip, else → kill
//	  [2] ret KILL
//	  [3] load syscall nr
//
//	Section B: socket() 重定向（仅当有禁止的协议族时）
//	  [4] jeq SYS_SOCKET → 跳到 Section E
//
//	Section C: 禁止的 syscall 检查
//	  [...] jeq blocked[i] → 跳到 Section F (kill)
//
//	Section D: 默认允许
//	  [...] ret ALLOW
//
//	Section E: socket 协议族过滤（检查 args[0]）
//	  [...] load args[0]
//	  [...] jeq AF_xxx → 跳到 Section F (kill)
//	  [...] ret ALLOW
//
//	Section F: kill/log 动作
//	  [...] ret KILL (或 LOG)
func buildBPFProgram(blockedNRs []int, blockedFamilies []int, logDenied bool) []unix.SockFilter {
	denyAction := uint32(seccompRetKillProcess)
	if logDenied {
		denyAction = uint32(seccompRetLog)
	}

	hasSocketFilter := len(blockedFamilies) > 0
	numBlocked := len(blockedNRs)
	numFamilies := len(blockedFamilies)

	// 计算各 Section 大小
	sizeB := 0 // socket 重定向指令数
	if hasSocketFilter {
		sizeB = 1
	}
	sizeE := 0 // socket 协议族过滤指令数
	if hasSocketFilter {
		sizeE = 1 + numFamilies + 1 // load + N 条 jeq + ret ALLOW
	}

	// Section F (kill) 的指令索引
	killIdx := 4 + sizeB + numBlocked + 1 + sizeE

	var program []unix.SockFilter

	// --- Section A: 架构检查 ---
	// [0] 加载 seccomp_data.arch
	program = append(program, bpfStmt(bpfLD|bpfW|bpfABS, seccompDataArchOffset))
	// [1] 检查 arch == AUDIT_ARCH_X86_64，匹配跳过 1 条，不匹配执行 kill
	program = append(program, bpfJump(bpfJMP|bpfJEQ|bpfK, auditArchX86_64, 1, 0))
	// [2] 非法架构 → 直接 kill（不受 logDenied 影响）
	program = append(program, bpfStmt(bpfRET, seccompRetKillProcess))
	// [3] 加载 seccomp_data.nr
	program = append(program, bpfStmt(bpfLD|bpfW|bpfABS, seccompDataNROffset))

	// --- Section B: socket() 重定向 ---
	if hasSocketFilter {
		// [4] jeq SYS_SOCKET → 跳到 Section E 起始（load args[0]）
		// Section E 起始索引 = 4 + 1 + numBlocked + 1
		// jt = (4 + 1 + numBlocked + 1) - 4 - 1 = numBlocked + 1
		jt := uint8(numBlocked + 1)
		program = append(program, bpfJump(bpfJMP|bpfJEQ|bpfK, uint32(unix.SYS_SOCKET), jt, 0))
	}

	// --- Section C: 禁止的 syscall 检查 ---
	for i := range blockedNRs {
		// 当前指令索引 = 4 + sizeB + i
		// jt 到 Section F (kill) = killIdx - currentIdx - 1
		currentIdx := 4 + sizeB + i
		jt := uint8(killIdx - currentIdx - 1)
		program = append(program, bpfJump(bpfJMP|bpfJEQ|bpfK, uint32(blockedNRs[i]), jt, 0))
	}

	// --- Section D: 默认允许 ---
	program = append(program, bpfStmt(bpfRET, seccompRetAllow))

	// --- Section E: socket 协议族过滤 ---
	if hasSocketFilter {
		// 加载 args[0]（socket domain 参数）
		program = append(program, bpfStmt(bpfLD|bpfW|bpfABS, seccompDataArgs0Offset))

		for j := range blockedFamilies {
			// jt 到 Section F (kill) = numFamilies - j
			jt := uint8(numFamilies - j)
			program = append(program, bpfJump(bpfJMP|bpfJEQ|bpfK, uint32(blockedFamilies[j]), jt, 0))
		}

		// socket 协议族不在黑名单中 → 允许
		program = append(program, bpfStmt(bpfRET, seccompRetAllow))
	}

	// --- Section F: kill/log 动作 ---
	program = append(program, bpfStmt(bpfRET, denyAction))

	return program
}

// applySeccomp 在当前进程上加载 Seccomp-BPF 过滤器。
// 必须在 exec 前最后调用，因为加载后当前进程也受 syscall 过滤限制。
//
// 步骤：
//  1. PR_SET_NO_NEW_PRIVS（防止通过 exec setuid 提权，也是非 root 加载 seccomp 的前提）
//  2. SECCOMP_SET_MODE_FILTER（加载 BPF 过滤器）
func applySeccomp(cfg *seccompInitConfig) error {
	if cfg == nil || (len(cfg.BlockedSyscalls) == 0 && len(cfg.BlockedSocketFamilies) == 0) {
		return nil
	}

	// 1. 设置 PR_SET_NO_NEW_PRIVS
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl PR_SET_NO_NEW_PRIVS: %w", err)
	}

	// 2. 构建 BPF 程序
	program := buildBPFProgram(cfg.BlockedSyscalls, cfg.BlockedSocketFamilies, cfg.LogDenied)

	// 3. 构造 sock_fprog 结构体
	sockFprog := &unix.SockFprog{
		Len:    uint16(len(program)),
		Filter: &program[0],
	}

	// 4. 加载 seccomp 过滤器
	if _, _, errno := unix.Syscall(
		unix.SYS_SECCOMP,
		seccompSetModeFilter,
		0,
		uintptr(unsafe.Pointer(sockFprog)),
	); errno != 0 {
		return fmt.Errorf("seccomp SECCOMP_SET_MODE_FILTER: %w", errno)
	}

	return nil
}

// seccompAvailable 检测内核是否支持 seccomp。
func seccompAvailable() bool {
	_, err := unix.PrctlRetInt(unix.PR_GET_SECCOMP, 0, 0, 0, 0)
	return err == nil
}

// --- BPF 指令构建辅助函数 ---

// BPF 指令类型常量
const (
	bpfLD  = 0x00
	bpfW   = 0x00
	bpfABS = 0x20
	bpfJMP = 0x05
	bpfJEQ = 0x10
	bpfK   = 0x00
	bpfRET = 0x06
)

// Seccomp 返回值常量
const (
	seccompRetKillProcess = 0x80000000 // SECCOMP_RET_KILL_PROCESS
	seccompRetLog         = 0x7ffc0000 // SECCOMP_RET_LOG
	seccompRetAllow       = 0x7fff0000 // SECCOMP_RET_ALLOW
)

// Seccomp 操作常量
const (
	seccompSetModeFilter = 1 // SECCOMP_SET_MODE_FILTER
)

// AUDIT_ARCH_X86_64 = EM_X86_64 | __AUDIT_ARCH_64BIT | __AUDIT_ARCH_LE
const auditArchX86_64 = 0xC000003E

// bpfStmt 创建一条 BPF 语句指令。
func bpfStmt(code uint16, k uint32) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: 0, Jf: 0, K: k}
}

// bpfJump 创建一条 BPF 跳转指令。
func bpfJump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}
