# 模块1.3：Cgroups v2 资源限制

**阶段**：1 | **周数**：1周 | **难度**：★★★★ | **关键度**：关键 | **状态**：已完成

---

## 模块概述

Cgroups v2 是 Linux 内核的资源控制机制，可限制进程组的 CPU、内存、进程数等资源使用。本模块为沙箱提供资源限制能力，防止 Agent 消耗过多系统资源（CPU 密集型循环、内存泄漏、fork 炸弹等）。

**模块目标**：
- 限制 Agent 的 CPU 使用量（配额/周期模型）
- 限制 Agent 的最大内存用量（超限触发 OOM Kill）
- 限制 Agent 可创建的最大进程数（防 fork 炸弹）
- 自动清理 cgroup 资源（迁移残留进程、删除 cgroup 目录）

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **CPU 限制** | 配额/周期模型，限制 CPU 使用百分比 | `cpu.stat` 中 nr_throttled > 0 |
| **内存限制** | 字节级上限，超限触发 OOM Kill | 分配超限内存后进程被杀死 |
| **进程数限制** | 最大进程数限制 | fork 超限后返回 EAGAIN |
| **进程加入** | 将沙箱进程加入 cgroup | `cgroup.procs` 包含目标 PID |
| **控制器启用** | 自动在 subtree_control 中启用需要的控制器 | cpu/memory/pids 控制器生效 |
| **防御性清理** | 残留进程迁移到父 cgroup，带重试删除 | cgroup 目录被删除 |
| **并发安全** | 多个 cgroup 实例互不干扰 | 10 并发测试通过 |

---

## 技术实现

### Cgroups v2 文件系统

```
/sys/fs/cgroup/                           ← cgroups v2 挂载点
  ├─ cgroup.controllers                   ← 系统可用控制器
  ├─ cgroup.subtree_control               ← 启用的控制器
  ├─ cgroup.procs                         ← 根 cgroup 进程列表
  │
  └─ sandbox-<id>/                        ← 沙箱专属 cgroup
      ├─ cpu.max                          ← "quota period"（如 "100000 100000"）
      ├─ cpu.stat                         ← CPU 使用统计
      ├─ memory.max                       ← 内存上限（字节）
      ├─ pids.max                         ← 最大进程数
      └─ cgroup.procs                     ← 属于此 cgroup 的进程
```

### 生命周期

```
NewCgroupsV2(config)
    │
    ├─ Setup()
    │   ├─ 验证配置（非负值）
    │   ├─ 检测 cgroups v2 可用性
    │   ├─ enableControllers() → 写入 cgroup.subtree_control
    │   ├─ 生成 ID，创建 cgroup 目录
    │   └─ writeLimits() → 写入 cpu.max / memory.max / pids.max
    │
    ├─ AddProcess(pid)
    │   └─ 写入 cgroup.procs
    │
    └─ Cleanup()
        ├─ readPids() → 读取残留进程
        ├─ 迁移残留进程到父 cgroup
        └─ os.Remove() 删除目录（带 10ms 重试）
```

### 核心类型

**CgroupsConfig**（`cgroups.go:18`）：
```go
type CgroupsConfig struct {
    Enabled   bool   // 是否启用
    CPUQuota  int    // CPU 配额（微秒/周期），0=不限制。100000=1核
    CPUPeriod int    // CPU 周期（微秒），默认 100000（100ms）
    MemoryMax int64  // 内存上限（字节），0=不限制。536870912=512MB
    PidsMax   int    // 最大进程数，0=不限制
    BaseDir   string // cgroup2 挂载点，默认 "/sys/fs/cgroup"
}
```

**CgroupsV2**（`cgroups.go:54`）：管理器实例，持有 ID、cgroupDir、互斥锁。

### 默认配置

`DefaultCgroupsConfig()` 返回：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| CPUQuota | 100000 | 1 核 CPU |
| CPUPeriod | 100000 | 100ms 周期 |
| MemoryMax | 536870912 | 512MB |
| PidsMax | 512 | 最多 512 个进程 |
| BaseDir | /sys/fs/cgroup | cgroups v2 挂载点 |

### 关键方法

| 方法 | 说明 |
|------|------|
| `DefaultCgroupsConfig()` | 返回默认配置 |
| `CgroupsV2Available()` | 检测系统是否支持 cgroups v2 |
| `NewCgroupsV2(config)` | 创建管理器 |
| `SetLogger(l)` | 设置日志记录器 |
| `Setup()` | 创建 cgroup 目录并写入资源限制 |
| `AddProcess(pid)` | 将进程加入此 cgroup |
| `Cleanup()` | 迁移残留进程 + 删除 cgroup 目录 |
| `ID()` | 返回唯一标识符 |
| `CgroupDir()` | 返回 cgroup 目录路径 |

### CPU 配额模型

CPU 限制通过 `cpu.max` 文件控制，格式为 `"quota period"`：

| 设置 | cpu.max | 含义 |
|------|---------|------|
| 1 核 | "100000 100000" | 每 100ms 可用 100ms CPU |
| 0.5 核 | "50000 100000" | 每 100ms 可用 50ms CPU |
| 2 核 | "200000 100000" | 每 100ms 可用 200ms CPU |
| 10% | "10000 100000" | 每 100ms 可用 10ms CPU |

### 安全考量

`AddProcess()` 失败被视为致命错误。如果进程未能加入 cgroup，资源限制不会生效，安全边界被突破。父进程在发送 initConfig 之前先调用 `AddProcess()`，确保子进程从一开始就受限。

---

## 验收标准

### 功能验收

- [x] cgroup 目录正确创建，cpu.max/memory.max/pids.max 值正确
- [x] CPU 限制生效（cpu.stat 中 nr_throttled > 0）
- [x] 内存限制生效（超限进程被 OOM Kill）
- [x] 进程数限制生效（fork 超限返回 EAGAIN）
- [x] AddProcess 后 cgroup.procs 包含目标 PID
- [x] Cleanup 后 cgroup 目录被删除
- [x] 重复 Setup 报错，重复 Cleanup 不报错（幂等）
- [x] 崩溃后 Cleanup 正确清理
- [x] 与 Namespace + OverlayFS 三模块集成正常工作
- [x] 10 并发实例互不干扰

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **Setup 时间** | < 10ms | 创建目录 + 写入控制文件 |
| **Cleanup 时间** | < 20ms | 含可能的 10ms 重试 |
| **性能开销** | < 2% | cgroups 本身的额外开销 |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultCgroupsConfig` | 默认配置值验证 |
| `TestNewCgroupsV2` | Setup 前访问器返回空值 |
| `TestCgroupsSetupValidation` | 未启用/负值参数验证 |
| `TestCgroupsV2Available` | 可用性检测不 panic |

### 集成测试（需要 root + cgroups v2）

| 测试函数 | 说明 |
|----------|------|
| `TestCgroupsSetupAndCleanup` | 完整生命周期 + 验证控制文件内容 |
| `TestCgroupsDoubleSetup` | 重复 Setup 报错 |
| `TestCgroupsAddProcess` | 加入进程并验证 cgroup.procs |
| `TestCgroupsCPULimit` | CPU 节流验证 |
| `TestCgroupsMemoryLimit` | 内存超限 OOM 验证 |
| `TestCgroupsPidsLimit` | fork 超限验证 |
| `TestCgroupsWithNamespace` | 与 Namespace 集成 |
| `TestCgroupsWithOverlayAndNamespace` | 三模块集成 |
| `TestCgroupsCleanupAfterCrash` | 崩溃后清理 |
| `TestConcurrentCgroups` | 10 并发实例 |

### 基准测试

| 测试函数 | 说明 |
|----------|------|
| `BenchmarkCgroupsSetupCleanup` | Setup + Cleanup 性能 |
| `BenchmarkCgroupsWithNamespace` | 含 Namespace 的完整性能 |

### 运行测试

```bash
# 单元测试（无需 root）
go test -v -run "TestDefaultCgroups|TestNewCgroups|TestCgroupsSetupValid|TestCgroupsV2Avail" ./pkg/sandbox/

# 集成测试（需要 root + cgroups v2）
sudo go test -v -run "TestCgroups" ./pkg/sandbox/

# 基准测试
sudo go test -bench "BenchmarkCgroups" ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/cgroups.go` | Cgroups v2 管理器（327 行） |
| `pkg/sandbox/cgroups_test.go` | 测试用例（630 行） |

---

## 常见陷阱和解决方案

### 1. cgroup 目录删除失败
**问题**：`os.Remove()` 返回 "device or resource busy"
**原因**：cgroup 中仍有活跃进程
**解决**：先将残留进程迁移到父 cgroup（写入父级 cgroup.procs），再删除目录。实现中包含 10ms 的单次重试。

### 2. 控制器未启用
**问题**：写入 cpu.max 时报错 "no such file or directory"
**原因**：cpu 控制器未在 `cgroup.subtree_control` 中启用
**解决**：Setup 时先调用 `enableControllers()` 启用所需控制器。

### 3. cgroup 目录不能用 RemoveAll
**问题**：`os.RemoveAll()` 删除 cgroup 目录失败
**原因**：cgroup 目录是内核虚拟文件系统，只能用 `os.Remove()` 删除空目录
**解决**：先迁移所有进程，再用 `os.Remove()` 删除。

### 4. 系统不支持 cgroups v2
**问题**：在旧内核或 cgroups v1 系统上 Setup 失败
**解决**：`CgroupsV2Available()` 检测可用性，测试中使用 `skipIfNoCgroupsV2` 跳过。

---

## 与其他模块的关系

- **依赖**：模块1.1（Namespace）通过 `SetCgroupsV2()` 绑定
- **集成时序**：父进程在子进程 fork 后、发送 initConfig 前，调用 `AddProcess(pid)` 将子进程加入 cgroup
- **清理链**：自动注册到 Namespace 清理链，在 OverlayFS 之后清理

---

**更新日期**：2025-02-11
