# 模块2.2：Pivot Root 目录禁锢

**阶段**：2 | **周数**：0.5周 | **难度**：★★ | **关键度**：关键 | **状态**：已完成

---

## 模块概述

`pivot_root` 是 Linux 系统调用，用于将进程的根文件系统切换到新的目录，同时将旧的根文件系统挂载到指定位置。与 `chroot` 相比，`pivot_root` 更安全：它不仅改变根目录路径，还彻底卸载旧的根文件系统，使进程无法通过任何方式访问宿主机文件。

本模块结合 OverlayFS 使用：将 overlay 的 merged 目录作为新 root，执行 pivot_root 后卸载旧 root，Agent 被完全限制在 overlay 的合并视图中。同时创建最小的 `/dev` 目录，提供基本设备节点。

**模块目标**：
- 将 Agent 的根文件系统切换到 overlay merged 目录
- 卸载并删除旧根文件系统（/.pivot_old），防止逃逸
- 创建最小 /dev 目录（null、zero、urandom + fd 符号链接）
- 与 seccomp 配合形成双层防护

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **Root 切换** | pivot_root 后 / 是 overlay merged | 正常执行 ls / |
| **旧 root 不可访问** | /.pivot_old 被卸载和删除 | 检查 /.pivot_old 不存在 |
| **路径逃逸防护** | ../../ 无法超出新 root | realpath /../../etc 不泄露宿主机文件 |
| **/dev 设备可用** | /dev/null、/dev/zero、/dev/urandom 可用 | 设备读写正常 |
| **符号链接** | /dev/fd、/dev/stdin、/dev/stdout、/dev/stderr | 符号链接指向 /proc/self/fd |
| **/proc 可用** | pivot_root 后 /proc 正确挂载 | /proc/self/status 可读 |
| **写入隔离** | pivot_root + overlay 下写入不影响宿主机 | 沙箱退出后宿主机无残留文件 |

---

## 技术实现

### pivot_root 流程

```
doPivotRoot(newRoot) 执行步骤：

1. bind mount newRoot → newRoot
   （pivot_root 要求 newRoot 是挂载点）

2. mkdir newRoot/.pivot_old
   （旧 root 的临时挂载点）

3. pivot_root(newRoot, newRoot/.pivot_old)
   （切换根文件系统）

4. chdir("/")
   （切换到新的根目录）

5. umount("/.pivot_old", MNT_DETACH)
   （卸载旧根文件系统）

6. rmdir("/.pivot_old")
   （删除临时目录）
```

### 最小 /dev 创建

`setupMinimalDev(rootDir)` 在 pivot_root 之前在新 root 中创建：

**Bind mount 设备**：

| 设备 | 说明 |
|------|------|
| `/dev/null` | 丢弃写入的数据（`echo x > /dev/null`） |
| `/dev/zero` | 读取返回零字节（`head -c 4 /dev/zero`） |
| `/dev/urandom` | 伪随机数生成器 |

**符号链接**：

| 链接 | 目标 | 说明 |
|------|------|------|
| `/dev/fd` | `/proc/self/fd` | 文件描述符目录 |
| `/dev/stdin` | `/proc/self/fd/0` | 标准输入 |
| `/dev/stdout` | `/proc/self/fd/1` | 标准输出 |
| `/dev/stderr` | `/proc/self/fd/2` | 标准错误 |

### 执行时序

在 nsInit() 中的位置：

```
mount propagation private
    ↓
mountOverlay()          ← OverlayFS 挂载
    ↓
setupMinimalDev()       ← 在 newRoot 中创建 /dev（pivot 前）
    ↓
doPivotRoot()           ← pivot_root 切换根文件系统
    ↓
mountProc()             ← /proc 重新挂载（pivot 后）
    ↓
sethostname / setupLoopback / applySeccomp / exec
```

### 核心类型

**PivotRootConfig**（`pivotroot.go:13`）：
```go
type PivotRootConfig struct {
    Enabled bool   // 是否启用
    RootDir string // 新根目录路径（未启用 overlay 时使用）
}
```

**pivotRootConfig**（`pivotroot.go:27`）：通过管道传递给子进程的配置。

### 新 Root 选择逻辑

子进程 init 中的逻辑：

```go
newRoot := cfg.PivotRoot.RootDir
if cfg.Overlay != nil {
    newRoot = cfg.Overlay.MergeDir  // overlay 的 merged 目录优先
}
```

- **有 OverlayFS**：使用 overlay 的 merged 目录作为新 root（推荐）
- **无 OverlayFS**：使用 `PivotRootConfig.RootDir` 指定的目录

### 关键函数

| 函数 | 说明 |
|------|------|
| `DefaultPivotRootConfig()` | 返回默认配置（启用，RootDir 为空） |
| `doPivotRoot(newRoot)` | 执行完整的 pivot_root 流程（6 步） |
| `setupMinimalDev(rootDir)` | 创建最小 /dev（bind mount + symlinks） |

---

## 验收标准

### 功能验收

- [x] pivot_root 后 ls / 正常工作
- [x] /.pivot_old 不存在（已卸载和删除）
- [x] 路径遍历 ../../ 无法逃逸
- [x] /proc 正确挂载，/proc/self/status 可读
- [x] /dev/null、/dev/zero、/dev/urandom 可用
- [x] pivot_root + overlay 下写入不影响宿主机
- [x] 与 seccomp 形成双层防护，正常命令不受影响
- [x] 5 并发实例正常工作

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **pivot_root 时间** | < 5ms | bind mount + pivot + unmount |
| **setupMinimalDev 时间** | < 5ms | 3 个 bind mount + 4 个 symlink |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultPivotRootConfig` | 默认配置验证 |

### 集成测试（需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestPivotRootWithOverlay` | pivot_root 后正常执行命令 |
| `TestPivotRootEscape` | /.pivot_old 不存在 + 路径遍历防护 |
| `TestPivotRootProcVisible` | /proc 正确挂载 |
| `TestPivotRootDevAvailable` | /dev/null、/dev/zero、/dev/urandom 可用 |
| `TestPivotRootWriteIsolation` | 写入不影响宿主机 |
| `TestPivotRootWithSeccomp` | 双层防护（pivot_root + seccomp） |
| `TestConcurrentPivotRoot` | 5 并发实例 |

### 运行测试

```bash
# 单元测试（无需 root）
go test -v -run "TestDefaultPivotRoot" ./pkg/sandbox/

# 集成测试（需要 root）
sudo go test -v -run "TestPivotRoot|TestConcurrentPivotRoot" ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/pivotroot.go` | Pivot Root 实现（142 行） |
| `pkg/sandbox/pivotroot_test.go` | 测试用例（358 行） |

---

## 常见陷阱和解决方案

### 1. pivot_root 要求 newRoot 是挂载点
**问题**：`pivot_root()` 返回 EINVAL
**原因**：newRoot 不是一个挂载点
**解决**：先执行 `bind mount newRoot → newRoot` 使其成为挂载点

### 2. /.pivot_old unmount 失败
**问题**：旧根文件系统卸载时 "device is busy"
**解决**：使用 `MNT_DETACH` 延迟卸载，即使有文件被占用也能卸载

### 3. /proc 符号链接在 pivot 前不工作
**问题**：/dev/fd → /proc/self/fd 创建后无法使用
**原因**：/proc 在 pivot_root 之后才重新挂载
**解决**：符号链接在 setupMinimalDev 中创建，在 mountProc 之后才能正常工作。这是预期行为。

### 4. pivot_root vs chroot
**问题**：为什么不用更简单的 chroot？
**原因**：chroot 只改变路径解析的起点，进程仍可通过 `chdir("..") + chroot(".")` 逃逸。pivot_root 彻底替换根文件系统并卸载旧 root，安全性更高。

---

## 与其他模块的关系

- **依赖**：模块1.2（OverlayFS）提供 merged 目录作为新 root
- **依赖**：模块1.1（Namespace）提供 Mount Namespace
- **协作**：与模块2.1（Seccomp）形成双层防护
- **时序**：在 OverlayFS 挂载之后、/proc 挂载之前执行

### 双层防护架构

```
层1：Pivot Root（目录禁锢）
  └─ 进程只能看到 overlay merged 中的文件
  └─ 旧根文件系统被彻底卸载

层2：Seccomp-BPF（syscall 过滤）
  └─ 禁止 mount/pivot_root/chroot 等 syscall
  └─ 即使发现漏洞也无法执行挂载操作
```

---

**更新日期**：2025-02-11
