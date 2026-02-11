# 模块开发规范

本文档定义了ai-sandbox开发中使用的统一规范，确保所有模块的文档和代码风格一致。

---

## 文件结构规范

### 模块文档格式

每个模块应创建一个独立的markdown文件，位置：
```
docs/development/modules/module-[阶段].[序号]-[名称].md
```

**示例**：
```
docs/development/modules/module-1.1-namespace.md
docs/development/modules/module-2.1-seccomp.md
```

### 文件内容结构

```markdown
# 模块[X.Y]：[模块名称]

**阶段** | **周数** | **难度** | **关键度** | **优先级**

## 模块概述
...

## 核心需求
...

## 技术实现范围
...

## 验收标准
...

## 实现步骤
...

## 测试策略
...

## 常见陷阱
...
```

---

## 模块文档模板

### 必需的节点

1. **模块概述**
   - 一句话概括
   - 为什么要做
   - 关键成果

2. **核心需求**
   - 功能性需求
   - 非功能性需求（性能、可靠性等）

3. **技术实现范围**
   - 包含的功能
   - 不包含的功能
   - 关键的技术选择

4. **验收标准**
   - 功能验收
   - 性能验收
   - 测试覆盖率要求

5. **实现步骤**
   - 按天分解
   - 每个步骤的交付物
   - 关键技术难点

6. **测试策略**
   - 单元测试
   - 集成测试
   - 性能测试
   - 压力测试

7. **常见陷阱**
   - 容易出错的地方
   - 解决方案

---

## 代码文件结构规范

### Go代码包结构

```
ai-sandbox/
├── pkg/
│   └── sandbox/
│       ├── namespace.go      # 模块1.1
│       ├── overlayfs.go      # 模块1.2
│       ├── cgroups.go        # 模块1.3
│       ├── seccomp.go        # 模块2.1
│       ├── chroot.go         # 模块2.2
│       ├── snapshot.go       # 模块3.1
│       ├── orchestration.go  # 模块4.1
│       └── interfaces.go     # 公共接口
├── test/
│   ├── namespace_test.go
│   ├── overlayfs_test.go
│   └── integration_test.go
└── cmd/
    └── ai-sandbox/
        └── main.go
```

### 模块命名规范

- 文件名：`[模块功能].go`（小写，下划线分隔）
- 包名：`sandbox`
- 接口名：`[功能]Interface` 或 `[功能]er`
- 结构体名：`[功能][对象]` （大驼峰）

**示例**：
```go
// 模块1.1 Namespace隔离
package sandbox

type NamespaceManager struct {
    // ...
}

type NamespaceConfig struct {
    // ...
}

func (nm *NamespaceManager) CreatePIDNamespace() error {
    // ...
}
```

---

## 验收标准规范

### 标准格式

```markdown
## 验收标准

### 功能验收
- ✅ [具体功能1]
- ✅ [具体功能2]

### 性能验收

| 指标 | 目标 | 实际 |
|------|------|------|
| **指标名称** | 目标值 | 待测 |

### 测试验收
- ✅ 单元测试覆盖率 > 80%
- ✅ 集成测试全部通过
- ✅ 无resource leak
```

### 性能指标定义

**必需指标**：
- 延迟 (Latency)：创建时间、执行时间、清理时间
- 吞吐量 (Throughput)：并发数、QPS
- 资源占用：内存、CPU

**可选指标**：
- 可扩展性：随并发数增长的表现
- 可靠性：失败恢复时间

---

## 测试规范

### 单元测试

**位置**：`test/[模块]_test.go`

**命名**：
```go
func Test[功能]Success(t *testing.T) { }
func Test[功能]Failure(t *testing.T) { }
func Test[功能]Edge(t *testing.T) { }
```

**覆盖范围**：
- 正常情况
- 边界情况
- 错误情况
- 资源清理

**最低标准**：>80% 代码覆盖率

### 集成测试

**位置**：`test/integration_[功能]_test.go`

**范围**：
- 模块间的协作
- 完整的流程验证
- 端到端的功能测试

### 性能测试

**位置**：`test/benchmark_[功能]_test.go`

**格式**：
```go
func BenchmarkCreate(b *testing.B) {
    for i := 0; i < b.N; i++ {
        Create()
    }
}
```

---

## 文档规范

### 每个模块的完整文档

1. **README** (module-X.Y-name.md)
   - 模块概述
   - 核心需求
   - 验收标准
   - 实现步骤

2. **代码注释** (Go)
   - 包级注释
   - 函数级注释（公开函数）
   - 复杂逻辑的内部注释

3. **示例代码**
   - 如何使用本模块
   - 常见场景

### 文档链接规范

- 使用相对路径
- 格式：`[显示文本](./相对路径.md)`

**示例**：
```markdown
详见 [Namespace详细规划](./modules/module-1.1-namespace.md)
```

---

## 提交和Review规范

### 提交消息格式

```
[模块] 简短描述

详细说明：
- 做了什么
- 为什么这样做
- 可能的副影响

相关文档：
- DEVELOPMENT.md
- docs/development/phase-X-xxx.md
```

**示例**：
```
[模块1.1] 实现Namespace隔离和清理

- 实现PID/IPC/Mount/Network Namespace创建
- 支持进程在隔离环境中执行
- 完整的资源清理机制

验收标准：
- ✅ 单元测试覆盖率 > 80%
- ✅ 性能指标达标 (创建<50ms)
- ✅ 无资源泄漏

相关文档：
- docs/development/phase-1-isolation.md
- docs/development/modules/module-1.1-namespace.md
```

### Code Review检查清单

- ✅ 功能完整，符合验收标准
- ✅ 测试覆盖率 > 80%
- ✅ 代码风格一致（go fmt）
- ✅ 错误处理完善
- ✅ 无安全漏洞
- ✅ 文档完整

---

## 版本控制规范

### 分支命名

```
feature/phase-X-module-name
bugfix/module-name-issue
docs/update-xxx
```

**示例**：
```
feature/phase-1-namespace-isolation
feature/phase-2-seccomp-filter
bugfix/namespace-cleanup-leak
docs/development-plan-update
```

### 发布标签

```
v0.1.0-phase1    # 阶段1完成
v0.2.0-phase2    # 阶段2完成
v1.0.0           # 全部完成
```

---

## 常见问题解答

### Q: 模块文档应该多详细？

**A**: 足够让另一个开发者在没有口头交流的情况下完成实现。包括：
- 技术选择的原因
- 关键的陷阱和解决方案
- 具体的验收标准

### Q: 代码注释怎么写？

**A**: 注释应解释"为什么"，而不是"什么"。代码本身说明"什么"。

```go
// ❌ 不好的注释
i := i + 1  // 增加i

// ✅ 好的注释
// 当前页码递增，准备加载下一页数据
i := i + 1
```

### Q: 测试覆盖率有硬性要求吗？

**A**: 是的，最低80%。关键模块（安全相关）应达到90%以上。

### Q: 如何处理跨模块依赖？

**A**: 通过Go接口解耦。见`pkg/sandbox/interfaces.go`。

---

## 更新日期

2024-02-10

---

**本规范对所有开发人员强制执行**
