核心技术架构 (Technical Architecture)
本项目不依赖厚重的虚拟机，而是通过 Go 语言直接操作底层 Linux 命名空间（Namespace）与控制组（Cgroups），实现了三大核心支柱：

🛡️ 模块一：多维资源隔离 (Multi-Layer Resource Isolation)
目标： 防止 Agent 任务出现“吵闹邻居”效应，确保宿主机稳定性。 技术实现：

计算与内存围栏： 基于 Cgroups v2 实现 CPU 份额（CPU Shares）的硬限制与内存带宽控制，杜绝 OOM 连锁反应。

临时文件系统（Ephemeral FS）： 利用 OverlayFS 构建写时复制（CoW）的文件层。每个 Agent 启动时挂载独立的只读根目录，所有写入操作仅发生在内存层的 UpperDir 中，任务结束即销毁，确保环境“无状态”且不仅用磁盘 I/O。

网络命名空间： 默认构建隔离的 Network Namespace，通过 veth pair 虚拟网卡桥接，仅允许特定端口的出站流量。

🔐 模块二：内核级权限控制 (Kernel-Level Permission Control)
目标： 实现从应用层到内核层的纵深防御，防止恶意代码逃逸。 技术实现：

系统调用过滤 (Seccomp-BPF)： 预加载严格的 Seccomp 白名单 profile。仅开放 read, write, futex 等基础 syscall，直接在内核态拦截并阻断 socket (监听), ptrace (调试), mount (挂载) 等危险调用。

动态路径锚定： 利用 Chroot/Pivot_root 将进程监狱化在特定目录下。

API 流量审计： 在用户态实现了一层透明代理（Sidecar Proxy），解析 Agent 发出的 HTTP 请求，基于预设策略拦截未授权的域名访问或敏感数据传输。

📸 模块三：即时快照与恢复 (Instant Snapshot & Restore)
目标： 解决复杂 Agent 任务的长耗时问题，支持“时间旅行”调试。 技术实现：

用户态检查点 (CRIU Integration)： 集成 CRIU (Checkpoint/Restore In Userspace) 技术，能够冻结正在运行的 Python/Node.js 解释器。

内存页转储： 将进程的虚拟内存空间、文件描述符表（FD Table）、寄存器状态序列化为镜像文件。

热启动优化： 支持从“预热好的快照”启动新沙箱（Fork from Snapshot）。相比传统的 docker run，将环境初始化时间从 1s+ 降低至 50ms 以内，极大提升了 Agent 的交互响应速度。