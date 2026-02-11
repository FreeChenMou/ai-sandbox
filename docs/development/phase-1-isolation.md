# 阶段1：基础隔离 (第1-2周)

**关键里程碑**：构建可工作的隔离沙箱基础

**核心目标**：
- ✅ 能创建独立的Namespace环境
- ✅ 能隔离文件系统（OverlayFS）
- ✅ 能限制资源使用（Cgroups v2）
- ✅ Agent能在隔离环境中正常执行和清理

**阶段验收标准**：
- 所有3个模块单独验证通过
- 3个模块集成验证通过
- 性能指标达标（参考各模块）
- 文档完整

---

## 模块列表

| 模块 | 周数 | 难度 | 关键度 | 完整规划 |
|------|------|------|--------|---------|
| [1.1 Namespace隔离](./modules/module-1.1-namespace.md) | 1周 | ★★★ | 🔴 关键 | [详情](./modules/module-1.1-namespace.md) |
| [1.2 OverlayFS文件系统](./modules/module-1.2-overlayfs.md) | 1周 | ★★★ | 🔴 关键 | [详情](./modules/module-1.2-overlayfs.md) |
| [1.3 Cgroups v2资源限制](./modules/module-1.3-cgroups.md) | 1周 | ★★★★ | 🔴 关键 | [详情](./modules/module-1.3-cgroups.md) |

---

## 周任务分解

### 第1周：Namespace + OverlayFS基础

#### 工作内容
- **第1-3天**：实现Namespace隔离
  - PID Namespace 创建和清理
  - IPC Namespace 隔离
  - Mount Namespace 基础操作
  - Network Namespace 创建（先不配置veth）

- **第4-5天**：实现OverlayFS文件系统
  - 只读底层(LowerDir)挂载
  - 内存中间层(UpperDir)管理
  - CoW验证
  - 卸载和清理

- **第5-7天**：集成验证
  - Namespace + OverlayFS集成测试
  - 进程在隔离环境中执行验证
  - 文件系统隔离验证

#### 交付物
- Namespace管理库（Go）
- OverlayFS管理库（Go）
- 单元测试用例
- 集成测试用例

---

### 第2周：Cgroups v2 + 综合测试

#### 工作内容
- **第1-3天**：实现Cgroups v2支持
  - Cgroup创建和清理
  - CPU限制配置
  - 内存限制配置
  - 资源监控（可选）

- **第4-5天**：性能和压力测试
  - 启动时间基准测试
  - 资源隔离验证
  - 内存占用测试
  - 并发隔离测试

- **第5-7天**：文档和优化
  - 完整文档编写
  - 性能优化
  - 代码review

#### 交付物
- Cgroups管理库（Go）
- 集成隔离库（Namespace+FS+Cgroups）
- 性能基准测试报告
- 完整技术文档

---

## 验收标准（必须全部通过）

### Namespace模块
- ✅ 能创建PID Namespace，进程树隔离
- ✅ 能创建IPC Namespace，消息队列隔离
- ✅ 能创建Mount Namespace，文件系统挂载隔离
- ✅ 能创建Network Namespace（基础）
- ✅ 创建后进程可正常执行
- ✅ 清理时Namespace被销毁

### OverlayFS模块
- ✅ 能挂载OverlayFS结构
- ✅ 上层(UpperDir)写入验证
- ✅ 下层(LowerDir)未被修改验证
- ✅ CoW机制正常工作
- ✅ 卸载后所有临时数据销毁
- ✅ 权限隔离生效（不能修改底层）

### Cgroups模块
- ✅ 能创建Cgroup v2层级
- ✅ CPU限制生效（通过压力测试验证）
- ✅ 内存限制生效（OOM触发）
- ✅ 嵌套Cgroup正确继承
- ✅ 资源回收（Cgroup删除后资源释放）

### 集成验证
- ✅ 三个模块配合：创建隔离环境 → 运行Agent → 清理
- ✅ Agent启动延迟 < 100ms（性能目标）
- ✅ Agent内存占用 5-20MB（不包括应用代码）
- ✅ 支持10+并发隔离环境同时运行
- ✅ 无资源泄漏（清理完整）

---

## 技术架构图

```
┌────────────────────────────────────┐
│      应用代码 (Agent)              │
│   Python/Node.js/Bash script       │
└────────────────────────────────────┘
                │ 运行在
┌────────────────────────────────────┐
│    隔离沙箱容器 (Sandbox)           │
│  ┌──────────────────────────────┐ │
│  │  Mount Namespace             │ │
│  │  ├─ OverlayFS (merged)       │ │
│  │  │  ├─ LowerDir (RO)         │ │
│  │  │  ├─ UpperDir (RW mem)     │ │
│  │  │  └─ WorkDir               │ │
│  │  └─ Chroot (第2阶段)         │ │
│  └──────────────────────────────┘ │
│  ┌──────────────────────────────┐ │
│  │  PID Namespace               │ │
│  │  └─ Agent PID 1 (在沙箱内)   │ │
│  └──────────────────────────────┘ │
│  ┌──────────────────────────────┐ │
│  │  IPC Namespace               │ │
│  │  └─ 进程间通信隔离           │ │
│  └──────────────────────────────┘ │
│  ┌──────────────────────────────┐ │
│  │  Network Namespace           │ │
│  │  └─ veth pair (第2阶段)      │ │
│  └──────────────────────────────┘ │
└────────────────────────────────────┘
                │ 受限于
┌────────────────────────────────────┐
│    Cgroups v2 资源限制             │
│  ├─ CPU: 1000m (1核)             │
│  ├─ Memory: 512MB                 │
│  └─ Events: 监控违限              │
└────────────────────────────────────┘
```

---

## 测试计划

### 单元测试（每个模块）

**Namespace模块**：
```
✓ unshare创建成功
✓ 子进程在新namespace中
✓ 资源隔离验证（getpid/getppid）
✓ namespace清理完整
```

**OverlayFS模块**：
```
✓ mount成功
✓ write操作到UpperDir
✓ read操作命中LowerDir
✓ LowerDir数据未修改
✓ umount清理完整
```

**Cgroups模块**：
```
✓ cgroup创建成功
✓ 进程加入cgroup
✓ CPU限制生效
✓ 内存限制生效
✓ cgroup删除资源释放
```

### 集成测试

```
场景1：运行简单Python脚本
- 创建隔离环境 → 运行script → 验证输出 → 清理
- 验证文件隔离、资源隔离

场景2：资源压力测试
- 多个Agent并发运行
- 验证资源限制和隔离

场景3：失败恢复测试
- Agent异常退出
- 验证环境正确清理
```

---

## 关键技术细节

### Namespace 创建流程
1. `unshare()` - 创建新namespace
2. `nsenter()` - 加入存在的namespace
3. `/proc/[pid]/ns/` - namespace文件描述符
4. 清理：namespace删除条件检查

### OverlayFS 挂载
1. 准备目录结构（lower/upper/work/merged）
2. 执行mount命令：`mount -t overlay overlay -o lowerdir=/lower,upperdir=/upper,workdir=/work /merged`
3. 验证挂载成功
4. 清理：`umount /merged`

### Cgroups 配置
1. 创建cgroupv2文件系统：`mount -t cgroup2 none /sys/fs/cgroup`
2. 创建层级目录
3. 配置资源限制：
   - CPU: `echo "1000000 1000000" > cpu.max`（1核，周期1s）
   - 内存：`echo "536870912" > memory.max`（512MB）
4. 进程加入：`echo $PID > cgroup.procs`

---

## 性能目标

| 指标 | 目标 | 测量方法 |
|------|------|---------|
| **单Agent启动延迟** | < 100ms | 时间测量 |
| **隔离环境创建时间** | < 50ms | 时间测量 |
| **内存占用/Agent** | 5-20MB | /proc/[pid]/status |
| **并发隔离数** | > 100 (单主机) | 同时创建并运行 |
| **资源隔离准确度** | ±5% | CPU/内存实际vs限制 |

---

## 依赖和环境

### 系统要求
- Linux Kernel >= 5.0 (Cgroups v2支持)
- Go >= 1.16
- 必要命令：mount, umount

### 外部依赖
- libcgroup-dev (可选，cgroup管理)
- criu (第3阶段需要)

---

## 文件结构

```
.
├── pkg/sandbox/
│   ├── namespace.go        # Namespace管理
│   ├── overlayfs.go        # OverlayFS管理
│   ├── cgroups.go          # Cgroups管理
│   └── sandbox.go          # 集成隔离环境
├── test/
│   ├── namespace_test.go
│   ├── overlayfs_test.go
│   ├── cgroups_test.go
│   └── integration_test.go
└── docs/
    └── phase-1-isolation.md
```

---

## 常见问题和解决方案

**Q: 如何验证Namespace隔离成功？**
A: 在隔离环境中运行`echo $$`，应该返回PID 1。

**Q: OverlayFS创建失败？**
A: 检查kernel支持，运行`cat /proc/filesystems | grep overlay`

**Q: Cgroups配置不生效？**
A: 确保是cgroupsv2（挂载点为cgroup2），检查权限。

---

**更新日期**：2024-02-10
**责任人**：系统组
