# 模块3.2：预热与快速恢复

**阶段**：3 | **周数**：0.5周 | **难度**：★★★ | **关键度**：优化 | **状态**：规划中

---

## 模块概述

预热与快速恢复模块在 CRIU 快照能力之上构建高层调度机制，通过预热池（预创建暂停态沙盒）和故障恢复策略，将沙盒交付延迟从秒级降至毫秒级，并在沙盒故障时自动从最近快照恢复，保证服务连续性。

**本模块解决的核心问题**：

1. **启动延迟**：即使从快照恢复也需要数百毫秒，预热池预先创建并暂停沙盒，激活仅需 thaw 操作，实现 < 50ms 交付
2. **资源利用**：预热池需要平衡"就绪沙盒数量"与"内存占用"，通过自动扩缩容和水位线控制，避免资源浪费
3. **服务可用性**：沙盒进程意外退出时，自动检测故障并从最近有效快照恢复，实现零停机运维

**模块目标**：
- 维护预热沙盒池，按需激活已就绪的沙盒
- 后台 goroutine 自动维持池水位，低于阈值时补充
- 提供故障恢复策略，从最近快照自动恢复
- 支持滚动替换和优雅关闭

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **预热池管理** | 预创建暂停态沙盒，维持最小/最大数量 | 池中沙盒数量在 min-max 范围内 |
| **快速激活** | 从池中取出暂停态沙盒，thaw 后交付 | Acquire() 耗时 < 50ms |
| **自动补充** | 池中沙盒数量低于 min 时后台补充 | 取出后自动触发补充 |
| **自动缩容** | 池中空闲沙盒超过 max 时释放多余 | 空闲数量不超过 max |
| **故障检测** | 检测沙盒进程意外退出 | 进程退出后 < 1s 触发恢复 |
| **快照恢复** | 从最近有效快照恢复故障沙盒 | 恢复后进程状态与快照一致 |
| **恢复校验** | 恢复后验证进程和环境一致性 | 校验失败时重试或报错 |
| **优雅关闭** | 关闭池时清理所有预热沙盒 | Close() 后无残留进程和目录 |
| **并发安全** | 多 goroutine 同时 Acquire/Release | 无竞态条件 |

---

## 技术实现

### 架构设计

```
                    ┌─────────────────────────────────────┐
                    │          WarmupPool                  │
                    │                                     │
   Acquire() ──→   │  ┌─────┐ ┌─────┐ ┌─────┐          │
                    │  │ SB1 │ │ SB2 │ │ SB3 │  frozen  │ ──→ 交付给调用者
   Release() ──→   │  │(frz)│ │(frz)│ │(frz)│          │     (thaw + 返回)
                    │  └─────┘ └─────┘ └─────┘          │
                    │        ↑                           │
                    │        │ 后台补充                    │
                    │  ┌─────┴──────┐                    │
                    │  │ refillLoop │ goroutine           │
                    │  │ 监控水位线  │                     │
                    │  └────────────┘                    │
                    └─────────────────────────────────────┘

                    ┌─────────────────────────────────────┐
                    │       RecoveryManager                │
                    │                                     │
   检测故障 ──→     │  1. 检测进程退出                      │
                    │  2. 查找最近有效快照                   │
                    │  3. CRIU restore                    │
                    │  4. 验证恢复状态                      │
                    │  5. 替换故障沙盒                      │
                    └─────────────────────────────────────┘
```

### 预热池生命周期

```
初始化                   运行中                      关闭
  │                       │                          │
  ├─ 创建 min 个沙盒      ├─ Acquire():              ├─ 停止 refillLoop
  │   ├─ CRIU restore    │   ├─ 从池取出 frozen SB   ├─ Thaw + Kill 所有池中 SB
  │   │   (从基础快照)    │   ├─ cgroup thaw          ├─ 清理 OverlayFS
  │   └─ cgroup freeze   │   └─ 返回 ready SB        └─ 释放所有资源
  │                       │
  ├─ 启动 refillLoop     ├─ Release():
  └─ 池就绪               │   ├─ 回收沙盒
                          │   ├─ freeze 并放回池
                          │   └─ (或销毁，视状态)
                          │
                          └─ refillLoop:
                              ├─ 检查: len(pool) < min?
                              ├─ 是: 创建新沙盒补充
                              └─ 检查: len(pool) > max?
                                  └─ 是: 销毁多余沙盒
```

### 核心类型

**PoolConfig**（`warmup.go`）：
```go
type PoolConfig struct {
    // 池大小
    MinSize         int           // 最小预热数量（低于此值触发补充）
    MaxSize         int           // 最大预热数量（超过此值释放多余）
    InitialSize     int           // 初始创建数量

    // 基础快照
    BaseSnapshotID  string        // 用于创建预热沙盒的基础快照 ID

    // 补充配置
    RefillInterval  time.Duration // 水位线检查间隔
    RefillBatchSize int           // 每批补充数量

    // 沙盒配置模板
    SandboxConfig   SandboxTemplate // 沙盒配置模板（Namespace、Cgroups 等）

    // 超时配置
    AcquireTimeout  time.Duration // Acquire 等待超时
}
```

**SandboxTemplate**（`warmup.go`）：
```go
type SandboxTemplate struct {
    NamespaceConfig  NamespaceConfig
    CgroupsConfig    CgroupsV2Config
    OverlayConfig    OverlayConfig
    SeccompConfig    *SeccompConfig
    PivotRootConfig  *PivotRootConfig
}
```

**WarmupPool**（`warmup.go`）：
```go
type WarmupPool struct {
    config    PoolConfig
    pool      []*PooledSandbox      // 池中暂停态沙盒
    active    map[string]*PooledSandbox // 已交付的活跃沙盒
    snapMgr   *SnapshotManager      // 快照管理器（模块3.1）
    logger    *zap.Logger
    mu        sync.Mutex
    stopCh    chan struct{}          // 停止信号
    wg        sync.WaitGroup        // 等待后台 goroutine
}
```

**PooledSandbox**（`warmup.go`）：
```go
type PooledSandbox struct {
    ID         string
    PID        int
    State      PooledState          // Frozen / Active / Failed
    Namespace  *Namespace
    CreatedAt  time.Time
    ActivatedAt time.Time           // 激活时间（Acquire 时设置）
}

type PooledState int

const (
    PooledStateFrozen  PooledState = iota // 暂停态（在池中）
    PooledStateActive                      // 活跃态（已交付）
    PooledStateFailed                      // 故障态
)
```

**RecoveryStrategy**（`recovery.go`）：
```go
type RecoveryStrategy struct {
    // 恢复配置
    MaxRetries       int           // 最大重试次数
    RetryInterval    time.Duration // 重试间隔
    SnapshotSelector string        // 快照选择策略: "latest" / "nearest"

    // 健康检查
    HealthCheckFunc  func(pid int) error // 恢复后健康检查函数
    HealthTimeout    time.Duration       // 健康检查超时
}
```

**RecoveryManager**（`recovery.go`）：
```go
type RecoveryManager struct {
    strategy  RecoveryStrategy
    snapMgr   *SnapshotManager
    pool      *WarmupPool           // 可选：恢复后放回池
    logger    *zap.Logger
    watchers  map[string]chan struct{} // sandboxID → 停止监控信号
    mu        sync.Mutex
}
```

### 关键方法

**WarmupPool 方法**：

| 方法 | 说明 |
|------|------|
| `NewWarmupPool(config, snapMgr) (*WarmupPool, error)` | 创建预热池并初始化 |
| `Start() error` | 启动池：创建初始沙盒 + 启动 refillLoop |
| `Acquire(ctx context.Context) (*PooledSandbox, error)` | 从池中获取就绪沙盒（thaw 后返回） |
| `Release(sandboxID string) error` | 归还沙盒到池（freeze 后放回）或销毁 |
| `Size() int` | 当前池中暂停态沙盒数量 |
| `ActiveCount() int` | 当前已交付的活跃沙盒数量 |
| `Close() error` | 关闭池，清理所有沙盒 |

**RecoveryManager 方法**：

| 方法 | 说明 |
|------|------|
| `NewRecoveryManager(strategy, snapMgr) *RecoveryManager` | 创建恢复管理器 |
| `Watch(sandboxID string, pid int) error` | 开始监控沙盒进程，异常退出时触发恢复 |
| `Unwatch(sandboxID string)` | 停止监控 |
| `RecoverFrom(sandboxID, snapshotID string) (int, error)` | 从指定快照恢复 |
| `RecoverLatest(sandboxID string) (int, error)` | 从最近有效快照恢复 |

### Acquire 流程

```
Acquire(ctx)
  │
  ├─ 1. 加锁
  │
  ├─ 2. 池中有就绪沙盒？
  │     │
  │     ├─ 有: 取出最早创建的（FIFO）
  │     │    ├─ cgroup thaw（解冻）
  │     │    ├─ 设置 State = Active
  │     │    ├─ 记录 ActivatedAt
  │     │    ├─ 移入 active map
  │     │    └─ 返回 PooledSandbox
  │     │
  │     └─ 无: 等待补充或超时
  │          ├─ refillLoop 被触发
  │          ├─ 等待 ctx.Done() 或新沙盒就绪
  │          └─ 超时返回 ErrPoolExhausted
  │
  └─ 3. 触发 refillLoop 检查水位线
```

### 故障恢复流程

```
Watch(sandboxID, pid)
  │
  ├─ 启动 goroutine 监控 /proc/<pid>
  │
  └─ 循环检测:
      │
      ├─ 进程存在 → 继续监控
      │
      └─ 进程不存在 → 触发恢复:
          │
          ├─ 1. 标记沙盒为 Failed
          │
          ├─ 2. 查找最近有效快照
          │     └─ snapMgr.List(sandboxID) → 按时间倒序
          │
          ├─ 3. CRIU restore
          │     └─ snapMgr.Restore(snapshotID)
          │
          ├─ 4. 健康检查
          │     ├─ 成功: 替换故障沙盒的 PID
          │     └─ 失败: 重试（最多 MaxRetries 次）
          │
          └─ 5. 继续监控新 PID
```

### 原子状态转换

预热池和恢复操作的状态转换通过以下机制保证原子性：

```
快照目录的原子写入:
  1. 写入临时目录: snapshots/.tmp-<id>/
  2. 写入完成后: fsync(临时目录)
  3. 原子替换: rename(.tmp-<id>/, <id>/)
  4. fsync(父目录)

池状态的原子转换:
  Frozen ──Acquire()──→ Active ──Release()──→ Frozen
    │                      │
    └──Close()──→ 销毁     └──故障──→ Failed ──Recovery──→ Active
```

---

## 验收标准

### 功能验收

- [ ] 预热池初始化创建 InitialSize 个暂停态沙盒
- [ ] Acquire() 从池中取出沙盒并激活
- [ ] 激活后沙盒进程正常运行
- [ ] Release() 回收沙盒，freeze 后放回池中
- [ ] 池中沙盒低于 MinSize 时后台自动补充
- [ ] 池中沙盒超过 MaxSize 时释放多余
- [ ] Acquire() 在池为空时等待或超时
- [ ] Close() 清理所有池中和活跃沙盒
- [ ] Watch() 检测进程异常退出
- [ ] 故障检测后自动从最近快照恢复
- [ ] 恢复后健康检查通过
- [ ] 恢复失败时重试（最多 MaxRetries 次）
- [ ] 多 goroutine 并发 Acquire/Release 无竞态
- [ ] 优雅关闭：停止补充 → 等待活跃沙盒完成 → 清理池

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **预热池激活** | < 50ms | Acquire() 从池取出到交付 |
| **故障恢复** | < 500ms | 检测故障 → 快照恢复 → 验证完成 |
| **池补充（后台）** | 不影响前台 | 补充操作不阻塞 Acquire |
| **故障检测延迟** | < 1s | 进程退出到检测到 |
| **并发 Acquire** | 线性扩展 | 10 并发 Acquire 无显著退化 |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultPoolConfig` | 默认配置验证 |
| `TestPoolConfigValidation` | 非法配置检测（min > max 等） |
| `TestRecoveryStrategyDefaults` | 恢复策略默认值 |
| `TestPooledStateTransitions` | 状态转换合法性 |
| `TestSandboxTemplateValidation` | 沙盒模板配置验证 |

### 集成测试（需要 root + CRIU）

| 测试函数 | 说明 |
|----------|------|
| `TestWarmupPoolInit` | 池初始化创建指定数量沙盒 |
| `TestAcquireRelease` | 获取和归还沙盒 |
| `TestAcquireActivation` | 激活后沙盒进程运行 |
| `TestPoolRefill` | 低于 min 时自动补充 |
| `TestPoolShrink` | 超过 max 时自动缩容 |
| `TestAcquireTimeout` | 池为空时超时返回错误 |
| `TestConcurrentAcquire` | 10 并发 Acquire |
| `TestPoolClose` | 关闭后无残留进程 |
| `TestFaultDetection` | 杀死进程后触发恢复 |
| `TestRecoverFromSnapshot` | 从快照恢复故障沙盒 |
| `TestRecoveryRetry` | 恢复失败重试 |
| `TestRecoveryHealthCheck` | 恢复后健康检查 |

### 运行测试

```bash
# 单元测试（无需 root）
go test -v -run "TestDefaultPool|TestPoolConfig|TestRecoveryStrategy|TestPooledState|TestSandboxTemplate" ./pkg/sandbox/

# 集成测试（需要 root + CRIU）
sudo go test -v -run "TestWarmupPool|TestAcquire|TestPoolRefill|TestPoolShrink|TestPoolClose|TestFault|TestRecover" ./pkg/sandbox/

# 性能基准测试
sudo go test -bench "BenchmarkAcquire|BenchmarkRecovery" -benchtime 10x ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/warmup.go` | 预热池管理器（WarmupPool） |
| `pkg/sandbox/recovery.go` | 故障恢复管理器（RecoveryManager） |
| `pkg/sandbox/warmup_test.go` | 预热池测试 |
| `pkg/sandbox/recovery_test.go` | 故障恢复测试 |

---

## 常见陷阱和解决方案

### 1. 预热沙盒 freeze 后内存未释放
**问题**：cgroup freeze 暂停了进程执行，但内存仍然被占用
**解决**：这是预期行为。通过 `MaxSize` 控制预热数量上限，权衡就绪性与内存开销。如需进一步优化，可结合 CRIU dump + kill（而非 freeze）预热沙盒，用磁盘存储替代内存

### 2. Acquire 与 refillLoop 的竞态
**问题**：Acquire 取出最后一个沙盒的同时 refillLoop 检测水位线，导致判断不一致
**解决**：Acquire 和 refillLoop 共享 `sync.Mutex`，所有池操作在锁保护下进行。使用 `sync.Cond` 通知等待中的 Acquire

### 3. 故障检测的假阳性
**问题**：进程正常退出（任务完成）被误判为故障
**解决**：Watch 机制区分正常退出（exit code 0）和异常退出（非零 exit code / 被信号杀死）。正常退出触发 Release 而非 Recovery

### 4. 恢复后的网络状态
**问题**：快照恢复后网络连接已失效（对端超时断开）
**解决**：恢复后的健康检查中包含网络连通性验证。Agent 应用层需要设计重连机制，不依赖长连接跨越快照恢复

### 5. 基础快照过期
**问题**：预热池使用的 BaseSnapshotID 对应的快照被删除
**解决**：预热池持有基础快照的引用计数，阻止删除被引用的快照。更换基础快照时，通过滚动替换逐步淘汰旧沙盒

---

## 与其他模块的关系

```
预热与快速恢复（本模块）
  │
  ├─ 强依赖 模块3.1（CRIU 快照）
  │     ├─ 预热池使用 Restore() 创建预热沙盒
  │     ├─ 故障恢复使用 Restore() 恢复进程
  │     └─ 使用 Checkpoint() 创建基础快照
  │
  ├─ 依赖 模块1.1（Namespace）
  │     └─ 预热沙盒在完整 Namespace 中运行
  │
  ├─ 依赖 模块1.2（OverlayFS）
  │     └─ 预热沙盒需要独立的文件系统层
  │
  ├─ 依赖 模块1.3（Cgroups）
  │     ├─ 使用 cgroup freezer 暂停/恢复沙盒
  │     └─ 预热沙盒受资源限制
  │
  └─ 被依赖 模块4.x（Workflow 编排）
        └─ Workflow 引擎通过预热池快速获取沙盒
```

---

**更新日期**：2025-02-12
