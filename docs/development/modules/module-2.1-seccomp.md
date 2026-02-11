# 模块2.1：Seccomp-BPF 系统调用过滤

**阶段**：2 | **周数**：1周 | **难度**：★★★★ | **关键度**：关键 | **状态**：已完成

---

## 模块概述

Seccomp（Secure Computing Mode）是 Linux 内核提供的系统调用过滤机制。通过 BPF（Berkeley Packet Filter）字节码在内核态拦截危险的系统调用，防止 Agent 执行特权操作（如 mount、ptrace、加载内核模块等）。本模块采用**黑名单模式**：仅禁止已知危险 syscall 和不安全的 socket 协议族，允许其他所有 syscall。

**设计理念**：Namespace + OverlayFS 已限制了 Agent 的可见域（进程树、文件系统、网络栈），Seccomp 在此基础上禁止内核级危险操作和无关协议族，而非白名单限制所有 syscall。这样 Agent 在可见域内可以正常执行大部分系统调用。

**模块目标**：
- 禁止 30+ 个危险 syscall（调试、挂载、内核模块、namespace 操作等）
- 禁止 7 个不安全的 socket 协议族（AF_NETLINK、AF_PACKET 等）
- 违规进程被 `SECCOMP_RET_KILL_PROCESS` 杀死（或仅记录日志）
- 不影响正常 Agent 操作（echo、ls、网络请求等）

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **Syscall 黑名单** | 30+ 个危险 syscall 被拦截 | 调用 mount 时进程被杀死 |
| **Socket 协议族过滤** | 7 个危险协议族被禁止 | socket(AF_NETLINK, ...) 失败 |
| **允许正常操作** | echo/ls/网络等不受影响 | 常规命令正常执行 |
| **架构检查** | 仅允许 x86_64 架构 | 非 x86_64 进程被杀死 |
| **日志模式** | 调试时仅记录不杀死 | LogDenied=true 时进程存活 |
| **退出码传递** | 正常退出码不受影响 | `exit 42` 返回 42 |
| **并发安全** | 多个实例互不干扰 | 5 并发测试通过 |

---

## 技术实现

### BPF 程序结构

```
Section A: 架构检查
  [0] load seccomp_data.arch
  [1] jeq AUDIT_ARCH_X86_64 → skip, else → kill
  [2] ret KILL_PROCESS               ← 非法架构直接杀死
  [3] load seccomp_data.nr

Section B: socket() 重定向（仅当有禁止的协议族时）
  [4] jeq SYS_SOCKET → 跳到 Section E

Section C: 禁止的 syscall 检查
  [...] jeq blocked[i] → 跳到 Section F (kill)

Section D: 默认允许
  [...] ret ALLOW                    ← 未匹配黑名单的 syscall 通过

Section E: socket 协议族过滤（检查 args[0]）
  [...] load seccomp_data.args[0]
  [...] jeq AF_xxx → 跳到 Section F (kill)
  [...] ret ALLOW                    ← 不在黑名单中的协议族允许

Section F: kill/log 动作
  [...] ret KILL_PROCESS (或 LOG)
```

### 默认黑名单 Syscall（30 个）

| 分类 | Syscall |
|------|---------|
| **进程调试** | ptrace |
| **文件系统挂载** | mount, umount2 |
| **Root 切换** | pivot_root, chroot |
| **系统控制** | reboot, swapon, swapoff, acct |
| **内核模块** | init_module, finit_module, delete_module, create_module |
| **内核加载** | kexec_load, kexec_file_load |
| **Namespace 操作** | setns, unshare |
| **内核密钥** | keyctl, request_key, add_key |
| **BPF** | bpf |
| **可利用接口** | userfaultfd, perf_event_open, lookup_dcookie |
| **FS Handle** | open_by_handle_at, name_to_handle_at |
| **时间修改** | clock_settime, settimeofday, adjtimex, clock_adjtime |
| **I/O 端口** | ioperm, iopl |
| **监控** | fanotify_init |
| **终端** | vhangup |
| **NFS** | nfsservctl |

### 默认黑名单 Socket 协议族（7 个）

| 协议族 | 编号 | 风险 |
|--------|------|------|
| AF_NETLINK | 16 | 内核通信（网络配置、事件监控） |
| AF_PACKET | 17 | 原始数据包捕获/注入（嗅探） |
| AF_BLUETOOTH | 31 | 蓝牙硬件访问 |
| AF_KEY | 15 | IPsec 密钥管理 |
| AF_ALG | 38 | 内核加密 API |
| AF_VSOCK | 40 | 虚拟机/宿主机通信 |
| AF_XDP | 44 | XDP 原始数据包访问 |

**不受影响的协议族**：AF_UNIX(1)、AF_INET(2)、AF_INET6(10) - Agent 正常使用的 UNIX socket、IPv4、IPv6 不受限制。

### 核心类型

**SeccompConfig**（`seccomp.go:18`）：
```go
type SeccompConfig struct {
    Enabled               bool     // 是否启用
    BlockedSyscalls       []string // 要禁止的 syscall 名称（空则使用默认）
    BlockedSocketFamilies []int    // 要禁止的 socket 协议族（空则使用默认）
    LogDenied             bool     // 仅记录而不杀死（调试用）
}
```

**seccompInitConfig**（`seccomp.go:38`）：父进程将 syscall 名称解析为号码后传递给子进程。

### 关键函数

| 函数 | 说明 |
|------|------|
| `DefaultSeccompConfig()` | 返回默认配置（启用、默认黑名单、杀死） |
| `resolveBlocklist(names)` | 将 syscall 名称转换为号码（去重、排序） |
| `buildBPFProgram(nrs, families, log)` | 构建 BPF 字节码 |
| `applySeccomp(cfg)` | 加载 seccomp 过滤器（PR_SET_NO_NEW_PRIVS + SECCOMP_SET_MODE_FILTER） |
| `seccompAvailable()` | 检测内核是否支持 seccomp |

### 加载时序

Seccomp 过滤器在子进程 init 的**最后一步**加载（exec 前）：

```
mountOverlay → doPivotRoot → mountProc → sethostname
→ setupLoopback → applySeccomp → exec
```

**原因**：seccomp 加载后当前进程也受限制。mount、pivot_root、sethostname 等特权操作必须在 seccomp 之前完成。

### BPF 常量

| 常量 | 值 | 说明 |
|------|----|------|
| `SECCOMP_RET_KILL_PROCESS` | 0x80000000 | 杀死违规进程 |
| `SECCOMP_RET_LOG` | 0x7ffc0000 | 仅记录（调试模式） |
| `SECCOMP_RET_ALLOW` | 0x7fff0000 | 允许 |
| `AUDIT_ARCH_X86_64` | 0xC000003E | x86_64 架构标识 |

---

## 验收标准

### 功能验收

- [x] 默认黑名单 30 个 syscall 全部在 syscallMap 中有映射
- [x] resolveBlocklist 正确将名称转为号码，支持去重
- [x] resolveBlocklist 对未知 syscall 名称返回错误
- [x] BPF 程序结构正确（架构检查、syscall 过滤、socket 过滤）
- [x] 黑名单 syscall 触发 KILL_PROCESS（exit code 非零）
- [x] 正常 syscall（echo、ls、hostname）不受影响
- [x] LogDenied=true 时使用 RET_LOG 而非 RET_KILL
- [x] 退出码正确传递（exit 42 → 42）
- [x] 与 Namespace + PivotRoot 双层防护正常工作
- [x] 5 并发实例正常工作

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **加载时间** | < 1ms | BPF 程序构建 + 加载 |
| **运行时开销** | < 2% | 每次 syscall 的额外检查 |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultSeccompConfig` | 默认配置验证 |
| `TestDefaultBlockedSyscallsNotEmpty` | 关键 syscall 在默认黑名单中 |
| `TestDefaultBlockedSocketFamiliesNotEmpty` | 关键协议族在默认列表中 |
| `TestResolveBlocklist` | 名称→号码转换 + 排序 |
| `TestResolveBlocklistDedup` | 去重验证 |
| `TestResolveBlocklistInvalid` | 未知 syscall 报错 |
| `TestResolveBlocklistEmpty` | 空列表返回空结果 |
| `TestResolveBlocklistAllDefaults` | 所有默认 syscall 可解析 |
| `TestSyscallMapComplete` | defaultBlockedSyscalls 全部在 syscallMap 中 |
| `TestBuildBPFProgramBlockedOnly` | 仅 syscall 过滤的 BPF 结构 |
| `TestBuildBPFProgramWithSocketFilter` | syscall + socket 过滤的 BPF 结构 |
| `TestBuildBPFProgramSocketOnly` | 仅 socket 过滤的 BPF 结构 |
| `TestBuildBPFProgramEmpty` | 空过滤的 BPF 结构 |
| `TestBuildBPFProgramLogDenied` | 日志模式使用 RET_LOG |
| `TestSeccompAvailable` | 可用性检测 |
| `TestApplySeccompNil` | nil 配置不报错 |
| `TestApplySeccompEmpty` | 空配置不报错 |

### 集成测试（需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestSeccompBlockedSyscall` | mount 被拦截，进程被杀死 |
| `TestSeccompAllowedSyscall` | echo/ls 等正常执行 |
| `TestSeccompWithNamespace` | 在完整 Namespace + seccomp 下多操作验证 |
| `TestSeccompLogMode` | LogDenied=true 时进程不被杀死 |
| `TestSeccompExitCode` | 退出码正确传递 |
| `TestConcurrentSeccomp` | 5 并发实例 |

### 运行测试

```bash
# 单元测试（无需 root）
go test -v -run "TestDefault.*Seccomp|TestDefault.*Blocked|TestResolve|TestSyscallMap|TestBuildBPF|TestSeccompAvail|TestApplySeccomp" ./pkg/sandbox/

# 集成测试（需要 root）
sudo go test -v -run "TestSeccomp" ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/seccomp.go` | Seccomp-BPF 实现（374 行） |
| `pkg/sandbox/seccomp_test.go` | 测试用例（466 行） |

---

## 常见陷阱和解决方案

### 1. SECCOMP_RET_KILL_PROCESS 只杀调用者进程
**问题**：sh 通过 fork 执行外部命令时，seccomp 杀死子进程（如 /bin/mount），但 sh 本身继续执行
**原因**：`SECCOMP_RET_KILL_PROCESS` 杀死的是调用违规 syscall 的进程，不是其父进程
**解决**：测试中使用 `exec mount` 使 mount 替换 sh 进程，被杀死时整个命令链终止

### 2. seccomp 必须在特权操作之后加载
**问题**：加载 seccomp 后 mount/pivot_root/sethostname 都无法执行
**原因**：seccomp 一旦加载，当前进程也受限制
**解决**：init 流程中 seccomp 是最后一步（exec 前），所有特权操作已完成

### 3. PR_SET_NO_NEW_PRIVS 是必要前提
**问题**：非 root 进程加载 seccomp 失败
**原因**：加载 seccomp 过滤器需要先设置 `PR_SET_NO_NEW_PRIVS`
**解决**：`applySeccomp()` 第一步调用 `prctl(PR_SET_NO_NEW_PRIVS, 1)`

### 4. 架构检查不可省略
**问题**：在非 x86_64 系统上 syscall 号码不同，黑名单失效
**解决**：BPF 程序第一段检查 `AUDIT_ARCH_X86_64`，不匹配直接 KILL

---

## 与其他模块的关系

- **依赖**：模块1.1（Namespace）通过 `SetSeccomp()` 绑定
- **协作**：与模块2.2（PivotRoot）形成双层防护（目录禁锢 + syscall 过滤）
- **加载时序**：在 nsInit() 中所有特权操作完成后、exec 前加载
- **名称解析**：父进程负责将 syscall 名称解析为号码，通过管道传递给子进程

---

**更新日期**：2025-02-11
