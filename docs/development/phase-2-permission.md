# 阶段2：权限控制 (第3-4周)

**关键里程碑**：在基础隔离上增加权限控制，能安全地执行代码

**核心目标**：
- ✅ 系统调用被严格过滤（Seccomp-BPF）
- ✅ 进程被限制在特定目录（Chroot/Pivot Root）
- ✅ API流量被审计和控制（可选）
- ✅ 能阻止常见的恶意行为（ptrace、mount、socket等）

**阶段验收标准**：
- 恶意系统调用被内核拦截
- Agent因违反规则被正确kill
- 日志记录所有违规行为
- 双层防护：Seccomp + Chroot
- 无安全漏洞

---

## 模块列表

| 模块 | 周数 | 难度 | 关键度 | 完整规划 |
|------|------|------|--------|---------|
| [2.1 Seccomp-BPF](./modules/module-2.1-seccomp.md) | 1周 | ★★★★ | 🔴 关键 | [详情](./modules/module-2.1-seccomp.md) |
| [2.2 Chroot/Pivot Root](./modules/module-2.2-chroot.md) | 0.5周 | ★★ | 🔴 关键 | [详情](./modules/module-2.2-chroot.md) |
| [2.3 Sidecar Proxy](./modules/module-2.3-proxy.md) | 1周 | ★★★ | 🟡 可选 | [详情](./modules/module-2.3-proxy.md) |

---

## 关键决策

### Seccomp规则白名单 vs 黑名单
- **决策**：使用白名单（更安全）
- **理由**：黑名单容易遗漏新的危险syscall

### 何时启用Seccomp
- **决策**：Agent启动时立即加载
- **理由**：越早拦截越好

### 故障处理
- **决策**：违反Seccomp规则的进程被SECCOMP_RET_KILL
- **理由**：保证系统安全，不允许继续执行

---

## 验收标准（必须全部通过）

### Seccomp模块
- ✅ 白名单syscall正常执行
- ✅ 黑名单syscall被拦截（返回EACCES）
- ✅ Agent因违规被kill，无信息泄露
- ✅ dmesg中有seccomp kill日志
- ✅ 性能开销<2%

### Chroot模块
- ✅ 进程getcwd()返回正确相对位置
- ✅ 尝试../../../etc失败
- ✅ readlink(/proc/self/root)显示新root

### 集成验证
- ✅ 运行恶意脚本被拦截
- ✅ 正常脚本能继续执行
- ✅ 多层防护（Seccomp + Chroot）

---

## 周任务分解

### 第1周：Seccomp-BPF

**关键任务**：
1. 定义白名单系统调用列表（参考libseccomp或seccomp profile）
2. 编译BPF程序
3. 加载和验证Seccomp规则
4. 测试：允许列表、禁止列表、边界情况

### 第2周：Chroot + 集成

**关键任务**：
1. 实现Chroot/Pivot Root
2. 与Namespace和OverlayFS集成
3. 性能和压力测试
4. 可选：Sidecar Proxy基础实现

---

## 相比阶段1的改进

| 能力 | 阶段1后 | 阶段2后 |
|------|---------|---------|
| **隔离强度** | 进程树隔离 | 进程树+权限隔离 |
| **恶意代码防护** | 基础（资源限制） | 完整（syscall拦截） |
| **可信度** | 低（能执行危险操作） | 高（内核级保护） |
| **安全等级** | 开发级 | 生产级 |

---

**更新日期**：2024-02-10
