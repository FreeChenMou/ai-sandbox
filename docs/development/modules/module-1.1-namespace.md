# 模块1.1：Namespace隔离

**阶段**：1 | **周数**：1周 | **难度**：★★★ | **关键度**：🔴 关键

---

## 模块概述

Namespace 是 Linux 提供的进程隔离机制，允许创建独立的进程树、文件系统挂载、网络栈、IPC等资源的"虚拟视图"。本模块实现对Namespace的创建、管理和清理。

**模块目标**：为Agent提供独立的进程/IPC/文件系统挂载/网络命名空间，确保进程树、消息队列、网络等资源被隔离。

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **PID Namespace** | Agent进程看到的PID为1，无法看到宿主机进程 | 在隔离环境中`echo $$` |
| **IPC Namespace** | Agent进程间通信资源隔离，无法跨Namespace通信 | `ipcs` 命令验证 |
| **Mount Namespace** | Agent有独立的文件系统挂载点视图 | 后续与OverlayFS配合验证 |
| **Network Namespace** | 网络资源隔离（基础支持，详细配置在第2阶段） | `ip netns exec` 验证 |
| **UTS Namespace** | 独立的hostname（可选，第2阶段实现） | `hostname` 命令验证 |
| **清理机制** | 隔离环境销毁时，所有Namespace资源被释放 | 检查`/proc/[pid]/ns`目录 |

---

## 技术实现范围

### 1. PID Namespace隔离

```
宿主机视图：                隔离环境视图：
├─ systemd (PID 1)         ├─ Agent (PID 1)
├─ sshd (PID 1234)         └─ Child (PID 2)
├─ Agent (PID 5678)           （看不到宿主机进程）
└─ ...
```

**实现要点**：
- 使用 `unshare(CLONE_NEWPID)` 创建新PID Namespace
- Agent进程在新Namespace中PID为1
- 宿主机可以看到Agent的真实PID，但Agent看不到宿主机的PID

**关键代码片段** (伪代码示意)：
```
func CreatePIDNamespace() {
    // 在新PID Namespace中fork进程
    cmd := exec.Command("bash")
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Cloneflags: syscall.CLONE_NEWPID,
    }
    cmd.Run()
    // Agent进程将看到PID=1
}
```

### 2. IPC Namespace隔离

```
宿主机IPC资源：              隔离环境IPC资源：
├─ SharedMemory (ID: 100)   ├─ SharedMemory (ID: 100)
├─ MessageQueue (ID: 200)   │  （独立，与宿主机无关）
└─ Semaphore (ID: 300)      └─ Semaphore (ID: 200)
```

**实现要点**：
- 使用 `unshare(CLONE_NEWIPC)` 创建新IPC Namespace
- Agent的`shmget/msgget/semget`只能在新Namespace中操作
- 防止跨Namespace的IPC攻击

**关键实现**：
```
CLONE_NEWIPC
  ├─ 独立的System V IPC命名空间
  ├─ 独立的POSIX消息队列（mq_open等）
  └─ 不影响宿主机IPC资源
```

### 3. Mount Namespace隔离

**实现要点**：
- 使用 `unshare(CLONE_NEWNS)` 创建新Mount Namespace
- Agent可以挂载/卸载文件系统，不影响宿主机
- 为后续的OverlayFS挂载做准备

**关键考虑**：
- 初始继承宿主机的挂载点
- 配置为"私有"挂载传播（private mount propagation）
- 后续使用OverlayFS替换根文件系统

### 4. Network Namespace隔离

**实现要点** (基础，详细在第2阶段)：
- 使用 `unshare(CLONE_NEWNET)` 创建新Network Namespace
- Agent有独立的网络栈（lo, eth等网卡）
- 后续通过veth pair与宿主机通信

**初始状态**：
- 新Network Namespace仅有lo（loopback）
- 其他网络接口通过veth pair添加（第2阶段）

---

## 验收标准

### 功能验收

**PID Namespace**：
- ✅ 创建新PID Namespace后，`getpid()` 返回1
- ✅ 子进程PPid为1（无法看到真实父进程）
- ✅ `ps aux` 仅显示隔离环境的进程
- ✅ 销毁Namespace时进程被正确清理

**IPC Namespace**：
- ✅ `ipcs` 在隔离环境中为空（无继承的IPC）
- ✅ Agent创建的IPC资源不在`ipcs`全局结果中
- ✅ 宿主机和Agent的IPC资源完全隔离

**Mount Namespace**：
- ✅ 创建后可独立挂载文件系统
- ✅ `mount` 操作仅影响隔离环境，不影响宿主机
- ✅ 卸载时资源正确释放

**Network Namespace**：
- ✅ 创建后有独立的network stack
- ✅ `ip link show` 仅显示隔离环境的网卡
- ✅ 初始仅有lo网卡

**清理机制**：
- ✅ Namespace销毁后，`/proc/[pid]/ns/` 文件消失
- ✅ 所有隔离资源被释放
- ✅ 无资源泄漏

### 性能验收

| 指标 | 目标 | 实际 |
|------|------|------|
| **创建时间** | < 50ms | 待测 |
| **内存开销** | < 1MB | 待测 |
| **清理时间** | < 10ms | 待测 |

---

## 实现步骤

### 第1-2天：基础PID + IPC Namespace

**任务**：
1. 学习Namespace基础
   - 读Linux man手册：`man namespaces`
   - 研究`unshare`和`clone`系统调用
   - 理解Namespace的生命周期

2. 实现PID Namespace创建和验证
   ```go
   // 伪代码示意
   type Namespace struct {
       PID   int
       Path  string
   }

   func (n *Namespace) CreatePIDNamespace() error {
       // 使用syscall.UnshareUnsafe或cmd.SysProcAttr
       // 创建新PID Namespace
   }
   ```

3. 实现IPC Namespace创建和验证
   - 类似PID Namespace的方式

**交付物**：
- `pkg/sandbox/namespace.go` 基础框架
- 单元测试：`test/namespace_test.go`

### 第3-4天：Mount + Network Namespace

**任务**：
1. 实现Mount Namespace
   - 创建并验证挂载隔离

2. 实现Network Namespace
   - 创建基础网络命名空间
   - 验证lo网卡存在

3. 统一的Namespace管理接口
   ```go
   type SandboxNamespace struct {
       PID     *os.Process
       NsPath  string
       Cleanup func() error
   }
   ```

**交付物**：
- Mount和Network Namespace支持
- 集成测试用例

### 第5天：清理和测试优化

**任务**：
1. 实现完整的Namespace生命周期管理
   - 创建 → 运行 → 清理

2. 处理边界情况
   - 进程异常退出
   - 清理失败重试
   - 资源泄漏检查

3. 性能测试和优化
   - 测量创建/清理时间
   - 进行压力测试（多并发隔离环境）

**交付物**：
- 完整的生产级代码
- 性能基准测试报告

---

## 关键技术点

### Namespace 创建方式

**方式1：unshare()** （推荐）
```
优点：简单，不需要fork
缺点：进程已在新Namespace中，看不到之前的资源
用途：创建隔离环境
```

**方式2：clone()** （备选）
```
优点：可精细控制进程行为
缺点：需要fork，相对复杂
用途：可与fork合并使用
```

### Namespace 文件系统接口

```
/proc/[pid]/ns/
├─ pid      # PID Namespace inode
├─ ipc      # IPC Namespace inode
├─ mnt      # Mount Namespace inode
├─ net      # Network Namespace inode
├─ uts      # UTS Namespace inode
└─ user     # User Namespace inode (可选)
```

**用途**：
- 检查Namespace是否存在
- 加入存在的Namespace：`nsenter -t $PID -n bash`

### 进程生命周期

```
创建Namespace
    ↓
exec Agent进程
    ↓
Agent运行
    ↓
Agent退出
    ↓
Namespace销毁（当所有进程都退出）
```

---

## 测试策略

### 单元测试

```bash
# 测试PID Namespace
./test/pid_namespace_test.go
  ✓ TestCreatePIDNamespace
  ✓ TestProcessInNamespace
  ✓ TestNamespacePIDIs1
  ✓ TestCleanupPIDNamespace

# 测试IPC Namespace
./test/ipc_namespace_test.go
  ✓ TestCreateIPCNamespace
  ✓ TestIPCIsolation
  ✓ TestCleanupIPCNamespace

# 测试Mount Namespace
./test/mount_namespace_test.go
  ✓ TestCreateMountNamespace
  ✓ TestMountIsolation
  ✓ TestCleanupMountNamespace

# 测试Network Namespace
./test/network_namespace_test.go
  ✓ TestCreateNetworkNamespace
  ✓ TestNetworkIsolation
  ✓ TestLoopbackOnly
```

### 集成测试

```bash
# 所有Namespace配合工作
./test/integration_namespace_test.go
  ✓ TestAllNamespacesTogether
  ✓ TestMultipleSandboxes
  ✓ TestCleanupAllResources
```

### 性能测试

```bash
# 性能基准
./test/benchmark_namespace_test.go
  BenchmarkCreateNamespace
  BenchmarkCleanupNamespace
  BenchmarkConcurrentNamespaces(100)
```

---

## 依赖和前置条件

### 系统要求
- Linux Kernel >= 4.10 (最新Namespace特性)
- 足够的权限（通常需要root或CAP_SYS_ADMIN）

### Go库依赖
- `syscall` (标准库)
- `os/exec` (标准库)

### 可选工具
- `nsenter` - 进入Namespace进行验证
- `unshare` - 命令行创建Namespace
- `lsns` - 列出所有Namespace

---

## 常见陷阱和解决方案

### 1. "Permission denied" 错误
**问题**：创建Namespace失败
**解决**：需要root权限或CAP_SYS_ADMIN

### 2. PID不是1
**问题**：创建PID Namespace后，getpid()仍不返回1
**解决**：确保在新Namespace中exec进程，而不是fork

### 3. 资源泄漏
**问题**：Namespace创建很多但清理不完整
**解决**：确保所有进程都正确终止，使用`pgrep`验证

### 4. 网络Namespace创建失败
**问题**：CLONE_NEWNET返回EBUSY
**解决**：可能是系统限制，确保内核支持

---

## 下一步

- 完成后，与模块1.2（OverlayFS）集成
- 为Seccomp规则准备（需要Namespace环境）
- 支持网络配置（veth pair，在第2阶段）

---

**模块时间表**：
- 开发：1周
- 测试：与开发并行
- 集成：与1.2/1.3并行

**关键成功指标**：
- ✅ 所有测试通过
- ✅ 性能指标达标
- ✅ 无资源泄漏
- ✅ 代码文档完整

---

**更新日期**：2024-02-10
**责任人**：系统组
