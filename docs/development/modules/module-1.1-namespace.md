# 模块1.1：Namespace隔离

**阶段**：1 | **周数**：1周 | **难度**：★★★ | **关键度**：关键 | **状态**：已完成

---

## 模块概述

Namespace 是 Linux 提供的进程隔离机制，允许创建独立的进程树、文件系统挂载、网络栈、IPC等资源的"虚拟视图"。本模块是整个沙箱系统的核心骨架，负责 Namespace 的创建、管理和清理，并通过 reexec 模式在新 Namespace 中执行子进程初始化。

**模块目标**：为 Agent 提供独立的 PID/IPC/Mount/Network/UTS 命名空间，并作为其他模块（OverlayFS、Cgroups、Seccomp、PivotRoot）的集成点。

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **PID Namespace** | Agent 进程在新 Namespace 中 PID 为 1，无法看到宿主机进程 | `echo $$` 返回 1 |
| **IPC Namespace** | 进程间通信资源隔离，System V IPC / POSIX 消息队列独立 | `ipcs -q` 在隔离环境中为空 |
| **Mount Namespace** | 独立的文件系统挂载点视图，为 OverlayFS 提供基础 | 与 OverlayFS 配合验证 |
| **Network Namespace** | 网络栈隔离：独立网卡、路由表、iptables | `ip link show` 仅显示 lo |
| **UTS Namespace** | 独立的 hostname | `hostname` 命令返回设置值 |
| **Reexec 机制** | 通过 /proc/self/exe 重新执行自身，在新 Namespace 中运行 init | 子进程正确读取管道配置 |
| **模块集成** | 可绑定 OverlayFS、Cgroups、Seccomp、PivotRoot | 各模块 Set 方法正常工作 |
| **清理机制** | 清理函数按注册逆序执行，释放所有资源 | 清理后无残留进程和目录 |

---

## 技术实现

### 架构设计

```
父进程                              子进程（新 Namespace）
  │                                    │
  │ Start()                            │
  ├─ 创建 config pipe + log pipe       │
  ├─ fork /proc/self/exe               │
  │   __sandbox_init__                 │
  ├─ AddProcess(pid) → cgroup          │ ← 阻塞在管道读取
  ├─ 注入 initConfig (JSON via pipe)   │
  │                                    ├─ nsInit()
  │                                    ├─ 读取 initConfig
  │                                    ├─ mount propagation private
  │                                    ├─ mountOverlay()
  │                                    ├─ doPivotRoot()
  │                                    ├─ mountProc()
  │                                    ├─ sethostname()
  │                                    ├─ setupLoopback()
  │                                    ├─ applySeccomp()
  │                                    └─ syscall.Exec(用户命令)
  │
  ├─ Wait()
  └─ Cleanup() (逆序)
      ├─ Kill 进程
      ├─ CgroupsV2.Cleanup()
      └─ OverlayFS.Cleanup()
```

### 核心类型

**NamespaceConfig**（`namespace.go:30`）：
```go
type NamespaceConfig struct {
    PID           bool   // 进程树隔离
    IPC           bool   // 进程间通信隔离
    Mount         bool   // 文件系统挂载隔离
    Network       bool   // 网络栈隔离
    UTS           bool   // 主机名隔离
    Hostname      string // UTS Namespace 中的主机名
    MountProc     bool   // 重新挂载 /proc
    SetupLoopback bool   // 启动 lo 网卡
}
```

**Namespace**（`namespace.go:95`）：
```go
type Namespace struct {
    config          NamespaceConfig
    overlayFS       *OverlayFS
    cgroupsV2       *CgroupsV2
    seccompConfig   *SeccompConfig
    pivotRootConfig *PivotRootConfig
    logger          *zap.Logger
    cmd             *exec.Cmd
    pid             int
    running         bool
    done            chan struct{}
    mu              sync.Mutex
    Stdin, Stdout, Stderr *os.File
    Env             []string
    Dir             string
    cleanups        []func() error
}
```

**initConfig**（`namespace.go:70`）：通过管道传递给子进程的 JSON 配置，包含所有模块的初始化参数。

### 预设配置

| 配置 | 函数 | 启用的 Namespace |
|------|------|-----------------|
| 默认（推荐） | `DefaultNamespaceConfig()` | PID + IPC + Mount + Network + UTS，hostname="sandbox" |
| 最小 | `MinimalNamespaceConfig()` | PID + Mount |

### 关键方法

| 方法 | 说明 |
|------|------|
| `NewNamespace(config)` | 创建管理器 |
| `SetOverlayFS(ov)` | 绑定 OverlayFS（Start 前调用） |
| `SetCgroupsV2(cg)` | 绑定 Cgroups（Start 前调用） |
| `SetSeccomp(cfg)` | 绑定 Seccomp 过滤器（Start 前调用） |
| `SetPivotRoot(cfg)` | 绑定 PivotRoot 禁锢（Start 前调用） |
| `SetLogger(l)` | 设置日志记录器（Start 前调用） |
| `Execute(cmd, args...)` | 同步执行命令（= Start + Wait） |
| `Start(cmd, args...)` | 异步启动命令 |
| `Wait()` | 阻塞等待完成，返回 ExecResult |
| `Signal(sig)` | 向隔离进程发送信号 |
| `Cleanup()` | 终止进程 + 逆序执行清理钩子 |
| `PID()` | 返回宿主机侧 PID |
| `Running()` | 是否正在运行 |
| `Done()` | 完成通知 channel |
| `NsPath(nsType)` | 返回 `/proc/<pid>/ns/<type>` 路径 |
| `AddCleanup(fn)` | 注册清理函数 |

### Reexec 机制（init_linux.go）

子进程初始化使用 reexec 模式而非直接 fork + exec：

1. 父进程通过 `/proc/self/exe __sandbox_init__` 重新执行自身
2. `MustReexecInit()` 在 `main()` 第一行检测标记，进入 `nsInit()` 流程
3. 子进程从 fd 3 读取 `initConfig`（JSON），按顺序执行初始化
4. 最终通过 `syscall.Exec()` 替换为用户命令

初始化顺序：
```
mount propagation private → mountOverlay → doPivotRoot
→ mountProc → sethostname → setupLoopback → applySeccomp → exec
```

### Clone Flags 组合

`cloneFlags()` 根据配置动态组合：

| 配置项 | Clone Flag |
|--------|-----------|
| PID | `CLONE_NEWPID` |
| IPC | `CLONE_NEWIPC` |
| Mount | `CLONE_NEWNS` |
| Network | `CLONE_NEWNET` |
| UTS | `CLONE_NEWUTS` |

---

## 验收标准

### 功能验收

- [x] PID Namespace：`echo $$` 返回 1
- [x] IPC Namespace：`ipcs -q` 在隔离环境中为空
- [x] Mount Namespace：独立挂载不影响宿主机
- [x] Network Namespace：仅有 lo 网卡，宿主机 eth0 不可见
- [x] UTS Namespace：hostname 可自定义（默认 "sandbox"）
- [x] 清理机制：Cleanup 后进程终止、资源释放
- [x] 清理钩子按注册逆序执行
- [x] 退出码正确传递
- [x] 环境变量传递（过滤内部变量）
- [x] 防止重复 Start
- [x] 并发 Namespace 互不干扰

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **创建时间** | < 50ms | Namespace 创建 + fork |
| **内存开销** | < 1MB | 管理器本身 |
| **清理时间** | < 10ms | 终止进程 + 执行清理链 |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultNamespaceConfig` | 默认配置启用所有 Namespace |
| `TestMinimalNamespaceConfig` | 最小配置仅启用 PID + Mount |
| `TestCloneFlags` | Clone flags 正确组合 |

### 集成测试（需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestPIDNamespace` | PID=1 验证 |
| `TestIPCNamespace` | IPC 隔离验证 |
| `TestNetworkNamespace` | 仅 lo 网卡，无 eth0 |
| `TestUTSNamespace` | hostname 设置验证 |
| `TestAllNamespaces` | 全部 Namespace 同时工作 |
| `TestCleanup` | 清理后进程终止 |
| `TestCleanupHooks` | 清理钩子逆序执行 |
| `TestDoubleStart` | 禁止重复 Start |
| `TestNsPath` | procfs 路径正确且存在 |
| `TestExitCode` | 退出码正确传递 |
| `TestEnvPassing` | 环境变量传递 |
| `TestConcurrentNamespaces` | 10 并发 Namespace |

### 运行测试

```bash
# 单元测试（无需 root）
go test -v -run "TestDefault|TestMinimal|TestCloneFlags" ./pkg/sandbox/

# 集成测试（需要 root）
sudo go test -v -run "TestPID|TestIPC|TestNetwork|TestUTS|TestAll|TestCleanup|TestDouble|TestNsPath|TestExitCode|TestEnv|TestConcurrent" ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/namespace.go` | Namespace 管理器主体（505 行） |
| `pkg/sandbox/init_linux.go` | 子进程 init 逻辑（231 行） |
| `pkg/sandbox/namespace_test.go` | 测试用例（404 行） |

---

## 常见陷阱和解决方案

### 1. PID 不是 1
**问题**：创建 PID Namespace 后，`getpid()` 不返回 1
**解决**：必须在新 Namespace 中 exec 新进程。reexec 模式确保子进程是新 PID Namespace 的 init 进程。

### 2. "Permission denied" 错误
**问题**：创建 Namespace 失败
**解决**：需要 root 权限或 CAP_SYS_ADMIN。

### 3. Mount 事件泄漏到宿主机
**问题**：子进程的 mount 操作影响了宿主机
**解决**：`nsInit()` 第一步设置 `MS_PRIVATE|MS_REC` 传播属性。

### 4. 管道竞态
**问题**：子进程在配置写入前就开始读取
**解决**：子进程 fork 后阻塞在管道读取，父进程先将进程加入 cgroup，再写入配置，最后关闭管道。

---

## 与其他模块的关系

```
Namespace（核心骨架）
  ├─ SetOverlayFS()  → 模块1.2（文件系统隔离）
  ├─ SetCgroupsV2()  → 模块1.3（资源限制）
  ├─ SetSeccomp()    → 模块2.1（系统调用过滤）
  └─ SetPivotRoot()  → 模块2.2（目录禁锢）
```

所有模块通过 `Namespace` 的 Set 方法注入配置，在 `nsInit()` 中按顺序执行初始化，在 `Cleanup()` 中逆序清理。

---

**更新日期**：2025-02-11
