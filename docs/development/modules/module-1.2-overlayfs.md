# 模块1.2：OverlayFS文件系统隔离

**阶段**：1 | **周数**：1周 | **难度**：★★★ | **关键度**：关键 | **状态**：已完成

---

## 模块概述

OverlayFS 是 Linux 内核提供的联合文件系统，通过将只读底层（LowerDir）和可写上层（UpperDir）叠加，实现写时复制（Copy-on-Write）语义。本模块利用 OverlayFS + tmpfs 为沙箱提供文件系统隔离：Agent 的所有写入操作都落入内存中的 tmpfs，不会修改宿主机文件系统，沙箱退出后所有修改自动消失。

**模块目标**：
- Agent 可以正常读取宿主机文件系统（只读）
- Agent 的所有写入/修改/删除操作都进入 tmpfs 上层，不影响宿主机
- tmpfs 大小可配置，防止 Agent 通过写入大量数据耗尽宿主机内存
- 沙箱清理后所有临时数据自动销毁

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **CoW 写入隔离** | 写入操作落入 UpperDir，LowerDir 不被修改 | 写入后检查 LowerDir 原文件不变 |
| **读取穿透** | 未修改的文件从 LowerDir 读取 | 在 merged 中能读到 lower 的文件 |
| **tmpfs 大小限制** | 可写层大小受限，防止内存耗尽 | 写入超过限制时失败 |
| **多层 LowerDir** | 支持多个只读底层叠加 | 两个 lower 目录的文件都可见 |
| **只读模式** | 支持完全只读的 overlay（无 UpperDir） | 写入操作失败 |
| **防御性清理** | 忽略已卸载的挂载点，幂等清理 | 重复 Cleanup 不报错 |
| **并发安全** | 多个 OverlayFS 实例互不干扰 | 10 并发测试通过 |

---

## 技术实现

### 两阶段设计

OverlayFS 的挂载分为父进程和子进程两个阶段：

```
父进程（Setup）                    子进程（nsInit → mountOverlay）
  │                                  │
  ├─ 生成唯一 ID                     │
  ├─ 创建 baseDir                    │
  ├─ 挂载 tmpfs（限制大小）           │
  ├─ 创建 upper/work/merged 目录     │
  │                                  │
  │  --- 通过管道传递配置 ---         │
  │                                  │
  │                                  ├─ 读取 overlayInitConfig
  │                                  ├─ 确保 mergeDir 存在
  │                                  └─ mount -t overlay（真正挂载）
  │
  └─ Cleanup()
      ├─ unmount overlay（MNT_DETACH）
      ├─ unmount tmpfs（MNT_DETACH）
      └─ rmdir baseDir
```

**为什么分两阶段**：OverlayFS 必须在新 Mount Namespace 中挂载才能实现隔离。父进程只能准备 tmpfs 和目录结构，实际的 overlay mount 在子进程的 Namespace 中完成。

### 目录结构

```
/tmp/sandbox-overlay-<id>/        ← baseDir（tmpfs 挂载点）
  ├─ upper/                       ← UpperDir（可写层，CoW 目标）
  ├─ work/                        ← WorkDir（overlay 内部使用）
  └─ merged/                      ← MergeDir（合并挂载点）
```

### 核心类型

**OverlayConfig**（`overlayfs.go:20`）：
```go
type OverlayConfig struct {
    Enabled   bool     // 是否启用
    LowerDirs []string // 只读底层目录（支持多层，高优先级在前）
    MergeDir  string   // 合并挂载点（默认自动生成）
    TmpfsSize string   // tmpfs 大小限制，如 "64m"（默认 "64m"）
    BaseDir   string   // 临时目录父路径（默认 "/tmp"）
    ReadOnly  bool     // true 时无 UpperDir，完全只读
}
```

**overlayInitConfig**（`overlayfs.go:42`）：通过管道传递给子进程的配置。

**OverlayFS**（`overlayfs.go:58`）：管理器实例，持有 ID、目录路径、互斥锁。

### 关键方法

| 方法 | 说明 |
|------|------|
| `DefaultOverlayConfig(lowerDir)` | 创建默认配置（enabled, tmpfs 64m） |
| `NewOverlayFS(config)` | 创建管理器实例 |
| `SetLogger(l)` | 设置日志记录器 |
| `Setup()` | 父进程侧：创建 tmpfs + 目录结构 |
| `InitConfig()` | 返回传递给子进程的配置（Setup 后可用） |
| `Cleanup()` | 卸载 overlay + tmpfs + 删除目录 |
| `MergeDir()` | 返回合并挂载点路径 |
| `UpperDir()` | 返回上层可写目录路径 |
| `ID()` | 返回唯一标识符 |

### 内部函数

| 函数 | 说明 |
|------|------|
| `generateID()` | 纳秒时间戳 + 4 字节随机数 |
| `buildOverlayOptions(cfg)` | 构建 mount 选项字符串 |
| `mountOverlay(cfg)` | 子进程中执行实际的 overlay mount |

### Mount 选项格式

```
# 读写模式
lowerdir=/lower1:/lower2,upperdir=/upper,workdir=/work

# 只读模式
lowerdir=/lower1:/lower2
```

---

## 验收标准

### 功能验收

- [x] 挂载 OverlayFS 后，merged 目录可读写
- [x] LowerDir 文件可通过 merged 目录正常读取
- [x] 写入操作进入 UpperDir，LowerDir 不被修改
- [x] 修改已有文件触发 CoW，原文件不变
- [x] tmpfs 大小限制生效，超限写入失败
- [x] 多层 LowerDir 的文件都可见
- [x] Cleanup 后 baseDir 不存在
- [x] 重复 Cleanup 不报错（幂等）
- [x] 崩溃后 Cleanup 正确清理
- [x] 10 并发实例互不干扰

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **Setup 时间** | < 20ms | tmpfs 挂载 + 创建目录 |
| **Cleanup 时间** | < 10ms | 卸载 + 删除 |
| **内存开销** | 由 TmpfsSize 决定 | 默认 64MB 上限 |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultOverlayConfig` | 默认配置验证 |
| `TestBuildOverlayOptions` | mount 选项字符串构建（单层、多层、只读） |
| `TestGenerateIDUniqueness` | 100 个 ID 无重复 |
| `TestNewOverlayFS` | Setup 前访问器返回空值 |
| `TestOverlaySetupValidation` | 未启用/无 LowerDir/不存在的目录验证 |

### 集成测试（需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestOverlaySetupAndCleanup` | 完整生命周期：Setup → 验证 → Cleanup |
| `TestOverlayWriteIsolation` | 写入进入 upper，merged 可读取 |
| `TestOverlayReadFromLower` | 从 lower 读取文件 |
| `TestOverlayLowerUnmodified` | 修改/创建文件后 lower 不变 |
| `TestOverlayWithNamespace` | 与 Namespace 集成端到端测试 |
| `TestOverlayCleanupAfterCrash` | 模拟崩溃后清理 |
| `TestConcurrentOverlays` | 10 并发实例 |
| `TestOverlayTmpfsSize` | tmpfs 大小限制生效 |
| `TestOverlayMultipleLowerDirs` | 两个 LowerDir 的文件都可见 |

### 基准测试

| 测试函数 | 说明 |
|----------|------|
| `BenchmarkOverlaySetupCleanup` | Setup + Cleanup 性能 |
| `BenchmarkOverlayWithNamespace` | 含 Namespace 的完整性能 |

### 运行测试

```bash
# 单元测试（无需 root）
go test -v -run "TestDefaultOverlay|TestBuildOverlay|TestGenerateID|TestNewOverlay|TestOverlaySetupValid" ./pkg/sandbox/

# 集成测试（需要 root）
sudo go test -v -run "TestOverlay" ./pkg/sandbox/

# 基准测试
sudo go test -bench "BenchmarkOverlay" ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/overlayfs.go` | OverlayFS 管理器（289 行） |
| `pkg/sandbox/overlayfs_test.go` | 测试用例（670 行） |

---

## 常见陷阱和解决方案

### 1. Overlay mount 在父进程中执行
**问题**：在父进程中挂载 overlay 后，子进程的写入会影响宿主机
**解决**：overlay mount 必须在子进程的新 Mount Namespace 中执行。父进程仅准备 tmpfs 和目录。

### 2. Cleanup 时 "device is busy"
**问题**：子进程仍在使用 overlay 挂载点，无法卸载
**解决**：使用 `MNT_DETACH` 延迟卸载，即使有文件被占用也能卸载。

### 3. WorkDir 和 UpperDir 必须在同一文件系统
**问题**：overlay mount 要求 workdir 和 upperdir 在同一文件系统
**解决**：两者都创建在同一个 tmpfs 挂载点下。

### 4. tmpfs 大小为 0 导致无法写入
**问题**：未配置 tmpfs 大小
**解决**：默认值为 "64m"，空字符串也会回退到 "64m"。

---

## 与其他模块的关系

- **依赖**：模块1.1（Namespace）提供 Mount Namespace
- **被依赖**：模块2.2（PivotRoot）使用 overlay 的 MergeDir 作为新 root
- **集成方式**：`ns.SetOverlayFS(ov)`，cleanup 自动注册到 Namespace 清理链

---

**更新日期**：2025-02-11
