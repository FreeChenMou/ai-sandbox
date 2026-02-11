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
| [2.2 Pivot Root](./modules/module-2.2-pivotroot.md) | 0.5周 | ★★ | 🔴 关键 | [详情](./modules/module-2.2-pivotroot.md) |
| [2.3 Sidecar Proxy](./modules/module-2.3-proxy.md) | 1周 | ★★★ | 🟡 可选 | [详情](./modules/module-2.3-proxy.md) |

---

## 关键决策

### Seccomp规则白名单 vs 黑名单
- **决策**：使用黑名单模式
- **理由**：Namespace + OverlayFS 已限制 Agent 的可见域，Seccomp 只需禁止内核级危险操作。黑名单模式对 Agent 兼容性更好。

### 何时启用Seccomp
- **决策**：在 nsInit() 中所有特权操作完成后、exec 前加载
- **理由**：mount、pivot_root、sethostname 等操作需要在 seccomp 之前完成

### 故障处理
- **决策**：违反Seccomp规则的进程被SECCOMP_RET_KILL_PROCESS
- **理由**：保证系统安全，不允许继续执行

### Chroot vs Pivot Root
- **决策**：使用 pivot_root 而非 chroot
- **理由**：pivot_root 彻底卸载旧根文件系统，比 chroot 更安全（chroot 可被逃逸）

---

## 验收标准（必须全部通过）

### Seccomp模块
- ✅ 黑名单syscall被拦截（进程被KILL_PROCESS）
- ✅ 不安全socket协议族被拦截（AF_NETLINK、AF_PACKET等）
- ✅ Agent因违规被kill，退出码非零
- ✅ 正常操作（echo、ls、网络）不受影响
- ✅ 日志模式可选（LogDenied）
- ✅ 性能开销<2%

### Pivot Root模块
- ✅ pivot_root后 / 是overlay merged目录
- ✅ /.pivot_old被卸载和删除
- ✅ 尝试../../../etc 逃逸失败
- ✅ /dev/null、/dev/zero、/dev/urandom可用
- ✅ /proc正确挂载

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
