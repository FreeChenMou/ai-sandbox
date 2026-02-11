//go:build linux

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"go.uber.org/zap"
)

// NamespaceType 标识Linux Namespace的类型，用于查询对应的procfs路径。
type NamespaceType int

const (
	NsPID NamespaceType = iota
	NsIPC
	NsMount
	NsNetwork
	NsUTS
	NsUser
)

// NamespaceConfig 定义创建沙箱时需要启用的Namespace及其初始化行为。
type NamespaceConfig struct {
	// 隔离开关
	PID     bool // 进程树隔离：Agent在新Namespace中PID为1
	IPC     bool // 进程间通信隔离：System V IPC / POSIX消息队列
	Mount   bool // 文件系统挂载隔离：为OverlayFS提供基础
	Network bool // 网络栈隔离：独立网卡、路由表、iptables
	UTS     bool // 主机名隔离

	// 初始化参数（子进程init阶段执行）
	Hostname      string // 设置UTS Namespace中的主机名
	MountProc     bool   // 在新Mount Namespace中重新挂载/proc
	SetupLoopback bool   // 在新Network Namespace中启动lo网卡
}

// DefaultNamespaceConfig 返回推荐的默认配置：启用所有Namespace隔离。
func DefaultNamespaceConfig() NamespaceConfig {
	return NamespaceConfig{
		PID:           true,
		IPC:           true,
		Mount:         true,
		Network:       true,
		UTS:           true,
		Hostname:      "sandbox",
		MountProc:     true,
		SetupLoopback: true,
	}
}

// MinimalNamespaceConfig 返回最小配置：仅启用PID和Mount隔离。
// 适用于不需要网络隔离的场景。
func MinimalNamespaceConfig() NamespaceConfig {
	return NamespaceConfig{
		PID:       true,
		Mount:     true,
		MountProc: true,
	}
}

// initConfig 通过管道传递给子进程的初始化配置。
// 子进程（reexec init）从管道读取此配置后执行初始化，然后exec用户命令。
type initConfig struct {
	Hostname      string             `json:"hostname,omitempty"`
	MountProc     bool               `json:"mount_proc,omitempty"`
	SetupLoopback bool               `json:"setup_loopback,omitempty"`
	Overlay       *overlayInitConfig `json:"overlay,omitempty"`
	Command       string             `json:"command"`
	Args          []string           `json:"args,omitempty"`
	Env           []string           `json:"env,omitempty"`
	WorkDir       string             `json:"work_dir,omitempty"`
}

// ExecResult 记录隔离进程的执行结果。
type ExecResult struct {
	ExitCode int
}

// Namespace 管理单个沙箱的Namespace生命周期。
//
// 使用方式:
//
//	ns := sandbox.NewNamespace(sandbox.DefaultNamespaceConfig())
//	defer ns.Cleanup()
//	result, err := ns.Execute("python", "agent.py")
type Namespace struct {
	config    NamespaceConfig
	overlayFS *OverlayFS
	cgroupsV2 *CgroupsV2
	logger    *zap.Logger
	cmd       *exec.Cmd
	pid       int
	running   bool
	done      chan struct{}
	mu        sync.Mutex

	// 外部可配置的IO（默认继承父进程）
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
	Env    []string // 传递给Agent的环境变量（空则继承父进程）
	Dir    string   // Agent的工作目录

	cleanups []func() error
}

// NewNamespace 创建Namespace管理器。
func NewNamespace(config NamespaceConfig) *Namespace {
	return &Namespace{
		config: config,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// SetOverlayFS 绑定OverlayFS到此Namespace。
// 必须在Start()之前调用。Setup()后的OverlayFS配置会自动注入到子进程初始化流程，
// 清理函数也会自动注册到Namespace的清理钩子中。
func (ns *Namespace) SetOverlayFS(ov *OverlayFS) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.overlayFS = ov
}

// SetCgroupsV2 绑定CgroupsV2到此Namespace。
// 必须在Start()之前调用。子进程fork后会被自动加入cgroup，
// 清理函数也会自动注册到Namespace的清理钩子中。
func (ns *Namespace) SetCgroupsV2(cg *CgroupsV2) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.cgroupsV2 = cg
}

// SetLogger 设置此Namespace的日志记录器。
// 必须在Start()之前调用。设置后会启用日志管道，将子进程日志转发到父进程。
func (ns *Namespace) SetLogger(l *zap.Logger) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.logger = l
}

// cloneFlags 根据配置组合syscall clone flags。
func (ns *Namespace) cloneFlags() uintptr {
	var flags uintptr
	if ns.config.PID {
		flags |= syscall.CLONE_NEWPID
	}
	if ns.config.IPC {
		flags |= syscall.CLONE_NEWIPC
	}
	if ns.config.Mount {
		flags |= syscall.CLONE_NEWNS
	}
	if ns.config.Network {
		flags |= syscall.CLONE_NEWNET
	}
	if ns.config.UTS {
		flags |= syscall.CLONE_NEWUTS
	}
	return flags
}

// Execute 在隔离环境中执行命令并阻塞等待完成。
// 这是最常用的同步接口。
func (ns *Namespace) Execute(command string, args ...string) (*ExecResult, error) {
	if err := ns.Start(command, args...); err != nil {
		return nil, err
	}
	return ns.Wait()
}

// Start 在隔离环境中启动命令（非阻塞）。
//
// 实现原理：
//  1. 创建管道，用于向子进程传递初始化配置
//  2. 通过 /proc/self/exe reexec 自身，带上 __sandbox_init__ 标记
//  3. 子进程在新的Namespace中执行init逻辑（见init_linux.go）
//  4. init完成后，子进程exec用户命令
func (ns *Namespace) Start(command string, args ...string) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	if ns.running {
		return fmt.Errorf("namespace: process already running (pid=%d)", ns.pid)
	}

	// 创建管道：父进程写入配置，子进程读取
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("namespace: create pipe: %w", err)
	}

	// 创建日志管道（可选，仅在设置了 logger 时）
	var logPipeR, logPipeW *os.File
	if ns.logger != nil {
		logPipeR, logPipeW, err = os.Pipe()
		if err != nil {
			pipeR.Close()
			pipeW.Close()
			return fmt.Errorf("namespace: create log pipe: %w", err)
		}
	}

	// reexec自身作为init进程
	cmd := exec.Command("/proc/self/exe", initSentinel)
	cmd.Stdin = ns.Stdin
	cmd.Stdout = ns.Stdout
	cmd.Stderr = ns.Stderr

	if logPipeW != nil {
		cmd.ExtraFiles = []*os.File{pipeR, logPipeW} // fd 3 = config pipe, fd 4 = log pipe
		cmd.Env = append(os.Environ(), initPipeEnv+"=3", initLogPipeEnv+"=4")
	} else {
		cmd.ExtraFiles = []*os.File{pipeR} // 子进程中为 fd 3
		cmd.Env = append(os.Environ(), initPipeEnv+"=3")
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: ns.cloneFlags(),
	}

	if err := cmd.Start(); err != nil {
		pipeR.Close()
		pipeW.Close()
		if logPipeR != nil {
			logPipeR.Close()
		}
		if logPipeW != nil {
			logPipeW.Close()
		}
		return fmt.Errorf("namespace: start process: %w", err)
	}

	// 子进程已fork，关闭其读取端
	pipeR.Close()
	// 关闭日志管道的写入端（父进程不写）
	if logPipeW != nil {
		logPipeW.Close()
	}

	// 启动日志管道读取 goroutine
	if logPipeR != nil {
		go readLogPipe(logPipeR, ns.logger)
	}

	// 添加子进程到 cgroup（必须在发送配置前，此时子进程阻塞在管道读取）
	if ns.cgroupsV2 != nil {
		if err := ns.cgroupsV2.AddProcess(cmd.Process.Pid); err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			pipeW.Close()
			if logPipeR != nil {
				logPipeR.Close()
			}
			return fmt.Errorf("namespace: add process to cgroup: %w", err)
		}
	}

	// 通过管道发送init配置
	cfg := initConfig{
		Hostname:      ns.config.Hostname,
		MountProc:     ns.config.MountProc,
		SetupLoopback: ns.config.SetupLoopback,
		Command:       command,
		Args:          args,
		Env:           ns.Env,
		WorkDir:       ns.Dir,
	}

	// 注入OverlayFS配置（如果已绑定）
	if ns.overlayFS != nil {
		cfg.Overlay = ns.overlayFS.InitConfig()
	}

	if err := json.NewEncoder(pipeW).Encode(&cfg); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		pipeW.Close()
		return fmt.Errorf("namespace: send init config: %w", err)
	}
	pipeW.Close()

	ns.cmd = cmd
	ns.pid = cmd.Process.Pid
	ns.running = true
	ns.done = make(chan struct{})

	if ns.logger != nil {
		ns.logger.Info("namespace started",
			zap.Int("pid", ns.pid),
			zap.Bool("pid_ns", ns.config.PID),
			zap.Bool("net_ns", ns.config.Network),
			zap.Bool("mount_ns", ns.config.Mount),
		)
	}

	// 自动注册OverlayFS清理钩子
	if ns.overlayFS != nil {
		ns.cleanups = append(ns.cleanups, ns.overlayFS.Cleanup)
	}

	// 自动注册CgroupsV2清理钩子
	if ns.cgroupsV2 != nil {
		ns.cleanups = append(ns.cleanups, ns.cgroupsV2.Cleanup)
	}

	return nil
}

// Wait 阻塞等待隔离进程完成，返回执行结果。
func (ns *Namespace) Wait() (*ExecResult, error) {
	ns.mu.Lock()
	if !ns.running || ns.cmd == nil {
		ns.mu.Unlock()
		return nil, fmt.Errorf("namespace: no running process")
	}
	cmd := ns.cmd
	ns.mu.Unlock()

	err := cmd.Wait()

	ns.mu.Lock()
	ns.running = false
	if ns.done != nil {
		close(ns.done)
	}
	ns.mu.Unlock()

	result := &ExecResult{}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("namespace: wait: %w", err)
		}
	}
	return result, nil
}

// Signal 向隔离进程发送信号。
func (ns *Namespace) Signal(sig syscall.Signal) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	if !ns.running || ns.cmd == nil || ns.cmd.Process == nil {
		return fmt.Errorf("namespace: no running process")
	}
	return ns.cmd.Process.Signal(sig)
}

// Cleanup 终止进程并清理所有Namespace资源。
// 清理函数按注册的逆序执行。
func (ns *Namespace) Cleanup() error {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	if ns.logger != nil {
		ns.logger.Info("namespace cleanup", zap.Int("pid", ns.pid))
	}

	var errs []error

	// 终止运行中的进程
	if ns.running && ns.cmd != nil && ns.cmd.Process != nil {
		if err := ns.cmd.Process.Kill(); err != nil {
			errs = append(errs, fmt.Errorf("kill process: %w", err))
		}
		ns.cmd.Wait()
		ns.running = false
		if ns.done != nil {
			close(ns.done)
			ns.done = nil
		}
	}

	// 逆序执行注册的清理函数
	for i := len(ns.cleanups) - 1; i >= 0; i-- {
		if err := ns.cleanups[i](); err != nil {
			errs = append(errs, err)
		}
	}
	ns.cleanups = nil

	if len(errs) > 0 {
		return fmt.Errorf("namespace cleanup errors: %v", errs)
	}
	return nil
}

// PID 返回隔离进程在宿主机上的PID。进程未启动时返回0。
func (ns *Namespace) PID() int {
	return ns.pid
}

// Running 返回隔离进程是否正在运行。
func (ns *Namespace) Running() bool {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.running
}

// Done 返回一个channel，在进程退出时被关闭。
// 可用于 select 等待。
func (ns *Namespace) Done() <-chan struct{} {
	return ns.done
}

// NsPath 返回指定类型Namespace在procfs中的路径。
// 例如 NsPID 返回 /proc/<pid>/ns/pid。
func (ns *Namespace) NsPath(nsType NamespaceType) string {
	if ns.pid == 0 {
		return ""
	}
	var name string
	switch nsType {
	case NsPID:
		name = "pid"
	case NsIPC:
		name = "ipc"
	case NsMount:
		name = "mnt"
	case NsNetwork:
		name = "net"
	case NsUTS:
		name = "uts"
	case NsUser:
		name = "user"
	default:
		return ""
	}
	return filepath.Join("/proc", fmt.Sprintf("%d", ns.pid), "ns", name)
}

// Config 返回当前Namespace配置的副本。
func (ns *Namespace) Config() NamespaceConfig {
	return ns.config
}

// AddCleanup 注册清理函数，在Cleanup()时按逆序执行。
// 用于外部模块（OverlayFS、Cgroups）注册自己的清理逻辑。
func (ns *Namespace) AddCleanup(fn func() error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.cleanups = append(ns.cleanups, fn)
}
