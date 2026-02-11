# AI Sandbox 开发文档导航

欢迎来到 ai-sandbox 的开发文档。本目录包含了完整的项目规划、技术设计和实现指南。

## 📚 快速导航

### 🎯 我想快速了解项目进度
👉 [DEVELOPMENT.md](../../DEVELOPMENT.md) - 整个项目的开发计划和时间表

### 🔍 我想了解具体某个阶段
- [阶段1：基础隔离](./phase-1-isolation.md) - Namespace、OverlayFS、Cgroups
- [阶段2：权限控制](./phase-2-permission.md) - Seccomp、Chroot、API审计
- [阶段3：快照恢复](./phase-3-snapshot.md) - CRIU、热启动
- [阶段4：Workflow编排](./phase-4-orchestration.md) - DAG、调度器、恢复
- [阶段5：可选增强](./phase-5-enhancement.md) - eBPF、异常检测

### 📋 我想了解某个具体模块
**阶段1模块**：
- [模块1.1 - Namespace隔离](./modules/module-1.1-namespace.md) (已完成)
- [模块1.2 - OverlayFS文件系统](./modules/module-1.2-overlayfs.md) (已完成)
- [模块1.3 - Cgroups v2资源限制](./modules/module-1.3-cgroups.md) (已完成)

**阶段2模块**：
- [模块2.1 - Seccomp-BPF](./modules/module-2.1-seccomp.md) (已完成)
- [模块2.2 - Pivot Root](./modules/module-2.2-pivotroot.md) (已完成)
- 模块2.3 - Sidecar Proxy (待创建)

**后续模块**：(待创建)
- 模块3.1 - CRIU快照集成
- 模块4.1 - DAG执行引擎
- 等等...

### 📖 我想了解开发规范
👉 [STANDARDS.md](./STANDARDS.md) - 代码、文档、测试、提交等规范

### 📊 我想看性能目标
👉 [性能目标](./references/performance-targets.md) (待创建)

### 🧪 我想了解测试策略
👉 [测试指南](./testing/) 目录 (待创建)

---

## 📂 目录结构

```
docs/development/
├── DEVELOPMENT.md              # 主入口，项目总体规划
├── README.md                   # 本文件
├── STANDARDS.md                # 开发规范
│
├── phase-1-isolation.md        # 阶段1：基础隔离
├── phase-2-permission.md       # 阶段2：权限控制
├── phase-3-snapshot.md         # 阶段3：快照恢复
├── phase-4-orchestration.md    # 阶段4：Workflow编排
├── phase-5-enhancement.md      # 阶段5：可选增强
│
├── modules/                    # 详细的模块规划
│   ├── module-1.1-namespace.md
│   ├── module-1.2-overlayfs.md
│   ├── module-1.3-cgroups.md
│   ├── module-2.1-seccomp.md
│   ├── module-2.2-pivotroot.md
│   └── ... (更多模块)
│
├── references/                 # 技术参考
│   ├── performance-targets.md (待创建)
│   ├── syscalls-namespace.md (待创建)
│   └── ... (参考资料)
│
└── testing/                    # 测试指南
    ├── unit-testing.md (待创建)
    ├── integration-testing.md (待创建)
    └── performance-testing.md (待创建)
```

---

## 🗓️ 项目里程碑

| 时间 | 里程碑 | 交付物 | 状态 |
|------|--------|--------|------|
| **第2周末** | 基础隔离MVP | 能创建和运行隔离的Agent沙箱 | ✅ 已完成 |
| **第4周末** | 安全隔离 | 能安全执行代码，拦截恶意syscall | ✅ 已完成 |
| **第6周末** | 快照恢复 | 支持Agent快照和故障恢复 | 📋 规划中 |
| **第9周末** | 完整系统 | 支持顺序/并行/并发Workflow编排 | 📋 规划中 |
| **第10周+** | 高级能力 | eBPF监控和异常检测（可选） | 📋 规划中 |

---

## 🚀 快速开始

### 1. 理解总体计划（5分钟）
```
阅读 DEVELOPMENT.md 中的"快速导航"和"开发时间表"
```

### 2. 选择你的工作模块（10分钟）
```
打开相应的阶段文档，了解模块的目标和验收标准
```

### 3. 开始开发（参考模块文档）
```
按照模块文档的"实现步骤"逐步完成
遵循 STANDARDS.md 的规范
```

### 4. 进行测试和验收
```
按照验收标准进行测试
达到性能目标
提交review
```

---

## 📝 常见问题

### Q: 我应该从哪个模块开始？
**A**: 如果是新加入，从[模块1.1 - Namespace隔离](./modules/module-1.1-namespace.md)开始。这是整个系统的基础。

### Q: 如何知道我的工作是否完成？
**A**: 查看模块文档中的"验收标准"部分。所有项都通过✅才算完成。

### Q: 代码应该写在哪里？
**A**: 见[STANDARDS.md](./STANDARDS.md)的"代码文件结构规范"部分。

### Q: 如何提交我的代码？
**A**: 见[STANDARDS.md](./STANDARDS.md)的"提交和Review规范"部分。

### Q: 遇到问题怎么办？
**A**:
1. 查看模块文档的"常见陷阱"部分
2. 查看相关的参考文档
3. 联系项目技术负责人

---

## 📊 项目统计

| 指标 | 数值 |
|------|------|
| **总模块数** | 13个 |
| **阶段数** | 5个 |
| **预计周期** | 10-12周 |
| **预计代码行数** | 10K+ |
| **测试覆盖率目标** | >80% |

---

## 🔗 其他资源

- [项目README](../../README.md) - 项目概述和技术架构
- [主DEVELOPMENT文件](../../DEVELOPMENT.md) - 完整的项目规划
- 代码仓库：[待补充]
- 问题跟踪：[待补充]

---

## 📞 联系方式

- **项目技术负责人**：[待补充]
- **架构设计**：[待补充]
- **问题报告**：使用GitHub Issues

---

## 📄 文档版本

| 版本 | 日期 | 更新内容 |
|------|------|---------|
| 1.0 | 2024-02-10 | 初版发布 |

---

**最后更新**: 2024-02-10

**开始你的开发之旅吧！** 🚀
