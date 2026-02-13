# 模块3.1：CRIU 快照集成

**阶段**：3 | **周数**：1.5周 | **难度**：★★★★★ | **关键度**：关键 | **状态**：规划中

---

## 模块概述

CRIU（Checkpoint/Restore In Userspace）是 Linux 用户态的进程快照与恢复工具，能将运行中的进程完整状态（内存映射、寄存器、文件描述符、信号处理等）序列化到磁盘，并在之后从磁盘恢复为运行态进程。本模块将 CRIU 集成到沙箱系统中，为 Agent 提供快照保存与恢复能力。

**本模块解决的核心问题**：

1. **启动延迟**：新建沙盒初始化耗时高（Namespace + OverlayFS + Cgroups + Seccomp），通过快照恢复跳过初始化流程，实现亚秒级响应
2. **内存占用**：大量并发沙盒的完整快照消耗巨大存储空间，通过增量快照（脏页跟踪）和链式快照优化存储效率
3. **服务可用性**：沙盒故障导致 Agent 状态丢失，通过完整状态快照支持从任意保存点恢复，保证服务连续性

**模块目标**：
- 对运行中的沙盒进程执行完整 checkpoint，保存所有状态到磁盘
- 从快照恢复进程，恢复后状态与快照时刻完全一致
- 支持增量快照，仅保存修改过的内存页，减少存储和时间开销
- 支持链式快照（base → delta1 → delta2），按需合并恢复
- 保证快照操作的原子性和一致性

---

## 核心需求

| 需求 | 说明 | 验证方法 |
|------|------|---------|
| **完整快照** | 保存进程的内存、寄存器、fd、信号等全部状态 | 恢复后进程继续执行，输出与快照前一致 |
| **快照恢复** | 从磁盘快照文件恢复为运行态进程 | restore 后进程 PID、fd、内存内容正确 |
| **增量快照** | 仅保存自上次快照以来修改的内存页 | 增量快照大小远小于完整快照 |
| **链式快照** | 增量快照引用父快照，恢复时按链合并 | 从 delta2 恢复时自动合并 base + delta1 + delta2 |
| **原子性保证** | checkpoint 前 freeze 进程，完成后 resume/kill | 快照过程中进程不会产生不一致状态 |
| **快照校验** | 校验快照文件完整性 | 损坏的快照文件被检测并拒绝恢复 |
| **Namespace 兼容** | 与现有 PID/Mount/Network Namespace 协同 | 在完整 Namespace 隔离下 checkpoint/restore 正常 |
| **OverlayFS 状态** | 快照包含 OverlayFS upper 层变更 | 恢复后文件系统变更与快照时一致 |
| **Cgroups 状态** | 恢复后进程归入正确的 cgroup | 恢复后资源限制继续生效 |

---

## 技术实现

### 架构设计

```
快照流程（Checkpoint）                     恢复流程（Restore）
  │                                         │
  ├─ freeze 沙盒进程                         ├─ 验证快照完整性
  ├─ 保存 OverlayFS upper 层                 ├─ 准备 Namespace 环境
  ├─ criu dump                               ├─ 恢复 OverlayFS upper 层
  │   ├─ /proc/pid 读取进程状态              ├─ criu restore
  │   ├─ 序列化内存页 → pages-*.img          │   ├─ 读取 pages-*.img → 恢复内存
  │   ├─ 序列化寄存器 → core-*.img           │   ├─ 读取 core-*.img → 恢复寄存器
  │   ├─ 序列化 fd → fdinfo-*.img            │   ├─ 读取 fdinfo-*.img → 恢复 fd
  │   └─ 序列化其他 → *.img                  │   └─ 恢复进程继续执行
  ├─ 记录快照元数据                           ├─ 加入 cgroup
  └─ resume / kill 原进程                     └─ 验证恢复状态

增量快照流程
  │
  ├─ 首次：完整快照（base）
  │   └─ --track-mem 启用脏页跟踪
  ├─ 后续：增量快照（delta）
  │   ├─ --prev-images-dir 指向上一次快照
  │   └─ 仅保存 soft-dirty bit 标记的脏页
  └─ 恢复：按链合并
      └─ base + delta1 + delta2 → 完整状态
```

### CRIU 工作原理

CRIU 通过 `/proc/pid` 读取进程的完整状态：

| 状态类型 | 来源 | 快照文件 |
|----------|------|---------|
| 内存映射 | `/proc/pid/maps` + `/proc/pid/mem` | `pages-*.img` |
| 寄存器 | `ptrace(PTRACE_GETREGS)` | `core-*.img` |
| 文件描述符 | `/proc/pid/fd` + `/proc/pid/fdinfo` | `fdinfo-*.img` |
| 信号处理 | `/proc/pid/status` | `sigacts-*.img` |
| 网络连接 | `/proc/pid/net` | `netns-*.img` |
| 进程关系 | `/proc/pid/stat` | `pstree.img` |

快照目录结构：
```
snapshots/<snapshot-id>/
├── meta.json              # 快照元数据（时间、类型、父快照ID等）
├── images/                # CRIU 镜像文件
│   ├── pages-1.img        # 内存页
│   ├── core-1.img         # CPU 寄存器状态
│   ├── fdinfo-2.img       # 文件描述符
│   ├── sigacts-1.img      # 信号处理
│   ├── pstree.img         # 进程树
│   └── ...
└── overlay/               # OverlayFS upper 层快照
    └── upper.tar.gz       # upper 层压缩备份
```

### 核心类型

**SnapshotConfig**（`snapshot.go`）：
```go
type SnapshotConfig struct {
    // 快照存储根目录
    StorageDir     string

    // 增量快照配置
    Incremental    bool   // 是否启用增量快照
    TrackMemory    bool   // 启用脏页跟踪（--track-mem）

    // 链式快照配置
    MaxChainLength int    // 最大链长度（超过后强制完整快照）

    // CRIU 配置
    CriuBinary     string // criu 二进制路径（默认 /usr/sbin/criu）
    LeaveRunning   bool   // checkpoint 后保持原进程运行（--leave-running）
    ShellJob       bool   // 进程关联终端（--shell-job）
    TCPEstablished bool   // 保存 TCP 连接（--tcp-established）

    // 校验配置
    EnableChecksum bool   // 启用快照校验
}
```

**Snapshot**（`snapshot.go`）：
```go
type Snapshot struct {
    ID          string            // 唯一标识（UUID）
    SandboxID   string            // 所属沙盒 ID
    Type        SnapshotType      // Full / Incremental
    ParentID    string            // 父快照 ID（增量快照时非空）
    CreatedAt   time.Time         // 创建时间
    Size        int64             // 快照大小（字节）
    ImageDir    string            // CRIU 镜像目录路径
    OverlayDir  string            // OverlayFS 备份路径
    Checksum    string            // SHA256 校验和
    Metadata    map[string]string // 自定义元数据
}
```

**SnapshotType**：
```go
type SnapshotType int

const (
    SnapshotTypeFull        SnapshotType = iota // 完整快照
    SnapshotTypeIncremental                      // 增量快照
)
```

**SnapshotManager**（`snapshot.go`）：
```go
type SnapshotManager struct {
    config     SnapshotConfig
    snapshots  map[string]*Snapshot  // ID → Snapshot
    chains     map[string][]string   // sandboxID → [快照链 ID 列表]
    logger     *zap.Logger
    mu         sync.RWMutex
}
```

### 关键方法

| 方法 | 说明 |
|------|------|
| `NewSnapshotManager(config)` | 创建快照管理器 |
| `Checkpoint(sandboxID, pid) (*Snapshot, error)` | 对运行中的沙盒做完整快照 |
| `IncrementalCheckpoint(sandboxID, pid, parentID) (*Snapshot, error)` | 增量快照，基于父快照仅保存变化 |
| `Restore(snapshotID) (pid int, error)` | 从快照恢复进程 |
| `RestoreChain(snapshotID) (pid int, error)` | 从链式快照恢复（自动合并增量链） |
| `Delete(snapshotID) error` | 删除快照（检查是否有子快照依赖） |
| `Validate(snapshotID) error` | 校验快照完整性 |
| `List(sandboxID) ([]*Snapshot, error)` | 列出沙盒的所有快照 |
| `GetChain(snapshotID) ([]*Snapshot, error)` | 获取快照链（从 base 到当前） |
| `Compact(sandboxID) error` | 压缩快照链（合并增量为新的完整快照） |

### Checkpoint 流程

```
Checkpoint(sandboxID, pid)
  │
  ├─ 1. 获取沙盒状态（验证 pid 存在且属于目标沙盒）
  │
  ├─ 2. Freeze 进程
  │     └─ cgroup freezer 或 SIGSTOP
  │
  ├─ 3. 保存 OverlayFS upper 层
  │     └─ tar czf overlay/upper.tar.gz <upper-dir>
  │
  ├─ 4. 执行 CRIU dump
  │     ├─ criu dump --tree <pid> --images-dir <dir>
  │     ├─ [增量] --track-mem --prev-images-dir <parent-dir>
  │     └─ [保持运行] --leave-running
  │
  ├─ 5. 生成元数据
  │     ├─ 计算 checksum
  │     ├─ 记录快照大小
  │     └─ 写入 meta.json
  │
  └─ 6. Resume / Kill 原进程
        └─ cgroup thaw 或 SIGCONT
```

### Restore 流程

```
Restore(snapshotID)
  │
  ├─ 1. 加载快照元数据
  │
  ├─ 2. 校验完整性
  │     └─ 验证 checksum
  │
  ├─ 3. [链式快照] 解析快照链
  │     └─ 收集 base + delta1 + ... + deltaN
  │
  ├─ 4. 准备环境
  │     ├─ 创建 Namespace
  │     ├─ 恢复 OverlayFS upper 层
  │     └─ 准备 cgroup
  │
  ├─ 5. 执行 CRIU restore
  │     ├─ criu restore --images-dir <dir>
  │     ├─ [链式] 按序指定 --inherit-fd
  │     └─ --restore-detached
  │
  ├─ 6. 后处理
  │     ├─ 将恢复的进程加入 cgroup
  │     └─ 验证进程状态
  │
  └─ 7. 返回恢复后的 PID
```

### 增量快照：脏页跟踪

```
  时间轴: ─────────────────────────────────────────────→

  T0: 完整快照（base）
      ┌──────────────────────────┐
      │ pages: [A][B][C][D][E]   │   全部内存页
      │ --track-mem 启用跟踪     │
      └──────────────────────────┘
                  │
  T1: 进程修改了页 B 和 D
      内核通过 soft-dirty bit 标记:
      [A][ B'][C][ D'][E]
           ↑       ↑
           dirty   dirty
                  │
  T2: 增量快照（delta1）
      ┌──────────────────────────┐
      │ pages: [B'][D']          │   仅脏页
      │ --prev-images-dir base/  │
      └──────────────────────────┘
                  │
  T3: 进程修改了页 A
      ┌──────────────────────────┐
      │ pages: [A']              │   仅脏页
      │ --prev-images-dir delta1/│
      └──────────────────────────┘

  恢复时合并:
      base[A][B][C][D][E] + delta1[B'][D'] + delta2[A']
    = [A'][B'][C][D'][E]
```

### 链式快照存储结构

```
snapshots/
├── snap-001/              # base（完整快照）
│   ├── meta.json          # { type: "full", parent: null }
│   └── images/
│       ├── pages-1.img    # 全量内存页（~150MB）
│       └── ...
├── snap-002/              # delta1（增量快照）
│   ├── meta.json          # { type: "incremental", parent: "snap-001" }
│   └── images/
│       ├── pages-1.img    # 仅修改页（~20MB）
│       └── ...
└── snap-003/              # delta2（增量快照）
    ├── meta.json          # { type: "incremental", parent: "snap-002" }
    └── images/
        ├── pages-1.img    # 仅修改页（~10MB）
        └── ...

总存储: 150 + 20 + 10 = 180MB
对比3次完整快照: 150 × 3 = 450MB → 节省 60%
```

---

## 验收标准

### 功能验收

- [ ] 对运行中进程执行完整 checkpoint，生成快照目录
- [ ] 从完整快照 restore 进程，恢复后继续执行
- [ ] 恢复后进程的内存内容、fd、环境变量与快照时一致
- [ ] 增量快照仅保存脏页，大小远小于完整快照
- [ ] 链式快照恢复：从 delta 恢复时自动合并父链
- [ ] MaxChainLength 超限时自动生成完整快照
- [ ] 快照校验能检测损坏的镜像文件
- [ ] 损坏快照恢复时返回明确错误
- [ ] 与 Namespace + OverlayFS + Cgroups 环境兼容
- [ ] OverlayFS upper 层正确保存和恢复
- [ ] 恢复后进程归入正确的 cgroup，资源限制生效
- [ ] Compact 操作合并增量链为新完整快照
- [ ] Delete 操作检查子快照依赖，拒绝删除被引用的快照
- [ ] 并发快照操作互不干扰

### 性能验收

| 指标 | 目标 | 说明 |
|------|------|------|
| **完整快照时间** | < 2s | 512MB 内存沙盒 |
| **增量快照时间** | < 500ms | 少量脏页场景 |
| **快照恢复时间** | < 500ms | 完整快照恢复 |
| **完整快照大小** | < 200MB | 512MB 内存沙盒 |
| **增量快照大小** | < 50MB | 典型修改量 |
| **链式恢复开销** | < 100ms/层 | 每层增量合并 |

---

## 测试覆盖

### 单元测试（不需要 root）

| 测试函数 | 说明 |
|----------|------|
| `TestDefaultSnapshotConfig` | 默认配置验证 |
| `TestSnapshotMetadata` | 元数据序列化/反序列化 |
| `TestSnapshotChainResolution` | 快照链解析（base → delta1 → delta2） |
| `TestSnapshotValidation` | 校验和验证 |
| `TestSnapshotIDGeneration` | ID 唯一性 |
| `TestMaxChainLengthEnforcement` | 链长度超限检测 |
| `TestDeleteDependencyCheck` | 删除时检查子快照依赖 |

### 集成测试（需要 root + CRIU）

| 测试函数 | 说明 |
|----------|------|
| `TestFullCheckpoint` | 完整快照生成 |
| `TestFullRestore` | 完整快照恢复，验证进程状态 |
| `TestIncrementalCheckpoint` | 增量快照仅含脏页 |
| `TestChainRestore` | 链式快照恢复（3 层链） |
| `TestCheckpointWithNamespace` | 在完整 Namespace 下快照 |
| `TestCheckpointWithOverlayFS` | OverlayFS upper 层保存/恢复 |
| `TestCheckpointWithCgroups` | 恢复后 cgroup 限制生效 |
| `TestCorruptedSnapshot` | 损坏快照检测 |
| `TestCompact` | 链压缩为完整快照 |
| `TestConcurrentCheckpoint` | 并发快照操作 |
| `TestLeaveRunning` | checkpoint 后原进程继续运行 |

### 运行测试

```bash
# 前置条件：安装 CRIU
sudo apt-get install criu
criu check  # 验证内核兼容性

# 单元测试（无需 root）
go test -v -run "TestDefaultSnapshot|TestSnapshotMeta|TestSnapshotChain|TestSnapshotValid|TestSnapshotID|TestMaxChain|TestDeleteDep" ./pkg/sandbox/

# 集成测试（需要 root + CRIU）
sudo go test -v -run "TestFull|TestIncremental|TestChainRestore|TestCheckpointWith|TestCorrupted|TestCompact|TestConcurrentCheckpoint|TestLeaveRunning" ./pkg/sandbox/

# 性能基准测试
sudo go test -bench "BenchmarkCheckpoint|BenchmarkRestore|BenchmarkIncremental" -benchtime 5x ./pkg/sandbox/
```

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/sandbox/snapshot.go` | 快照管理器、Checkpoint/Restore 核心逻辑 |
| `pkg/sandbox/snapshot_criu.go` | CRIU 命令行封装（dump/restore/check） |
| `pkg/sandbox/snapshot_chain.go` | 链式快照管理（链解析、合并、压缩） |
| `pkg/sandbox/snapshot_test.go` | 单元测试 + 集成测试 |

---

## 常见陷阱和解决方案

### 1. CRIU 内核版本兼容性
**问题**：CRIU 依赖特定内核特性（如 `CONFIG_CHECKPOINT_RESTORE`），某些发行版内核未启用
**解决**：在 `NewSnapshotManager()` 中调用 `criu check` 验证兼容性，不兼容时返回明确错误信息，列出缺失的内核特性

### 2. PID 命名空间恢复冲突
**问题**：恢复时原 PID 可能已被占用，导致 restore 失败
**解决**：CRIU 的 `--restore-detached` 模式在新 PID Namespace 中恢复，PID 由新 Namespace 分配，不会与宿主机冲突

### 3. OverlayFS 挂载点与快照不一致
**问题**：checkpoint 时 OverlayFS 有未刷新的缓存，导致快照中文件状态不一致
**解决**：checkpoint 前先 `sync` 文件系统，freeze 进程后再备份 upper 层，确保所有写入已落盘

### 4. 增量快照链过长导致恢复缓慢
**问题**：链式增量快照层数过多时，恢复需要逐层合并，延迟线性增长
**解决**：配置 `MaxChainLength`（建议 5-10），超过后自动生成完整快照截断链；提供 `Compact()` 方法主动合并

### 5. 快照目录非原子写入
**问题**：checkpoint 过程中进程崩溃或断电，导致快照目录不完整
**解决**：先写入临时目录（`.tmp-<id>`），完成后通过 `fsync` + `rename` 原子替换为最终目录。恢复时检测到 `.tmp-` 前缀的目录自动清理

### 6. TCP 连接恢复
**问题**：checkpoint 时的 TCP 连接在 restore 后失效（对端已关闭）
**解决**：使用 `--tcp-established` 保存 TCP 状态。对于不可恢复的连接，应用层需要重连逻辑。建议在 Agent 设计中使用短连接或支持重连

---

## 与其他模块的关系

```
CRIU 快照集成（本模块）
  │
  ├─ 依赖 模块1.1（Namespace）
  │     └─ 快照/恢复在 Namespace 环境中进行
  │
  ├─ 依赖 模块1.2（OverlayFS）
  │     └─ 需要保存/恢复 OverlayFS upper 层
  │
  ├─ 依赖 模块1.3（Cgroups）
  │     ├─ 使用 cgroup freezer 冻结进程
  │     └─ 恢复后进程需加入 cgroup
  │
  ├─ 协作 模块2.1（Seccomp）
  │     └─ CRIU 保存/恢复 seccomp 过滤器状态
  │
  ├─ 协作 模块2.2（PivotRoot）
  │     └─ 恢复时需要重建 pivot_root 环境
  │
  └─ 被依赖 模块3.2（预热与快速恢复）
        └─ 预热池使用 CRIU restore 预创建沙盒
```

**CRIU 依赖和内核要求**：
- CRIU 版本：>= 3.16
- 内核版本：>= 4.19（推荐 5.4+）
- 内核配置：`CONFIG_CHECKPOINT_RESTORE=y`、`CONFIG_NAMESPACES=y`、`CONFIG_MEM_SOFT_DIRTY=y`（增量快照）
- 权限要求：root 或 `CAP_SYS_PTRACE` + `CAP_SYS_ADMIN`

---

**更新日期**：2025-02-12
