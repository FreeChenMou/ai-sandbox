//go:build linux

package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	// initSentinel 是reexec模式的命令行标记。
	// 当主程序检测到 os.Args[1] == initSentinel 时，进入init逻辑。
	initSentinel = "__sandbox_init__"

	// initPipeEnv 是传递管道fd的环境变量名。
	initPipeEnv = "_SANDBOX_INIT_PIPE"

	// initLogPipeEnv 是传递日志管道fd的环境变量名。
	initLogPipeEnv = "_SANDBOX_LOG_PIPE"
)

// MustReexecInit 检查当前进程是否是sandbox的init子进程。
// 如果是，执行Namespace初始化逻辑并exec用户命令（不会返回）。
//
// 必须在main()的第一行调用：
//
//	func main() {
//	    sandbox.MustReexecInit()
//	    // ... 正常业务逻辑
//	}
//
// 原理：父进程通过 /proc/self/exe reexec自身，并在命令行中附加
// initSentinel 标记。子进程启动后检测到该标记，进入init流程。
func MustReexecInit() {
	if len(os.Args) < 2 || os.Args[1] != initSentinel {
		return
	}
	if err := nsInit(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox init: %v\n", err)
		os.Exit(1)
	}
	// nsInit通过syscall.Exec替换进程，不应到达此处
	os.Exit(1)
}

// nsInit 是子进程的初始化入口。
//
// 执行流程：
//  1. 从管道读取父进程传递的 initConfig
//  2. 设置mount propagation为private（防止挂载事件泄漏到宿主机）
//  3. 挂载OverlayFS（文件系统隔离，致命错误）
//  4. 重新挂载/proc（使PID Namespace生效）
//  5. 设置hostname
//  6. 启动loopback网卡
//  7. syscall.Exec 替换为用户命令
func nsInit() error {
	// 1. 从管道读取配置
	pipeFdStr := os.Getenv(initPipeEnv)
	if pipeFdStr == "" {
		return fmt.Errorf("env %s not set", initPipeEnv)
	}
	pipeFd, err := strconv.Atoi(pipeFdStr)
	if err != nil {
		return fmt.Errorf("invalid pipe fd %q: %w", pipeFdStr, err)
	}

	pipeFile := os.NewFile(uintptr(pipeFd), "init-pipe")
	if pipeFile == nil {
		return fmt.Errorf("cannot open pipe fd %d", pipeFd)
	}
	defer pipeFile.Close()

	// 1b. 检测日志管道（可选），不存在时回退到 stderr
	var logWriter io.Writer = os.Stderr
	var logPipeFile *os.File
	if logPipeFdStr := os.Getenv(initLogPipeEnv); logPipeFdStr != "" {
		if logPipeFd, err := strconv.Atoi(logPipeFdStr); err == nil {
			if f := os.NewFile(uintptr(logPipeFd), "log-pipe"); f != nil {
				logPipeFile = f
				logWriter = f
			}
		}
	}

	var cfg initConfig
	if err := json.NewDecoder(pipeFile).Decode(&cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	// 2. 设置mount propagation为private
	// 必须在任何mount操作之前执行，防止挂载事件传播到宿主机
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		// 非致命：在某些环境中可能已经是private
		writeInitLog(logWriter, "warn", fmt.Sprintf("mount private: %v (non-fatal)", err))
	}

	// 3. 挂载OverlayFS（文件系统隔离）
	// 必须在mount propagation之后、/proc之前执行
	// 失败是致命错误：文件系统隔离失败意味着安全边界被突破
	if cfg.Overlay != nil {
		if err := mountOverlay(cfg.Overlay); err != nil {
			return fmt.Errorf("mount overlay: %w", err)
		}
	}

	// 4. 重新挂载/proc（PID Namespace需要）
	// 挂载后 ps/top 等工具才能正确显示Namespace内的进程
	if cfg.MountProc {
		if err := mountProc(); err != nil {
			writeInitLog(logWriter, "warn", fmt.Sprintf("mount /proc: %v (non-fatal)", err))
		}
	}

	// 5. 设置hostname
	if cfg.Hostname != "" {
		if err := syscall.Sethostname([]byte(cfg.Hostname)); err != nil {
			writeInitLog(logWriter, "warn", fmt.Sprintf("sethostname: %v (non-fatal)", err))
		}
	}

	// 6. 启动loopback网卡
	if cfg.SetupLoopback {
		if err := setupLoopback(); err != nil {
			writeInitLog(logWriter, "warn", fmt.Sprintf("setup lo: %v (non-fatal)", err))
		}
	}

	// 7. 关闭日志管道（exec 前关闭，防止泄漏给用户命令）
	if logPipeFile != nil {
		logPipeFile.Close()
	}

	// 8. 切换工作目录
	if cfg.WorkDir != "" {
		if err := syscall.Chdir(cfg.WorkDir); err != nil {
			return fmt.Errorf("chdir to %s: %w", cfg.WorkDir, err)
		}
	}

	// 9. 准备环境变量
	env := buildCleanEnv(cfg.Env)

	// 10. exec用户命令（替换当前进程映像）
	if cfg.Command == "" {
		return fmt.Errorf("no command specified")
	}
	binary, err := exec.LookPath(cfg.Command)
	if err != nil {
		return fmt.Errorf("command not found: %s: %w", cfg.Command, err)
	}

	argv := append([]string{cfg.Command}, cfg.Args...)
	return syscall.Exec(binary, argv, env)
}

// mountProc 在新的Mount Namespace中重新挂载/proc。
func mountProc() error {
	// 先卸载旧的/proc（来自宿主机的Mount Namespace）
	_ = syscall.Unmount("/proc", syscall.MNT_DETACH)
	// 挂载新的/proc（反映当前PID Namespace的进程）
	return syscall.Mount("proc", "/proc", "proc", 0, "")
}

// setupLoopback 在新的Network Namespace中启动lo接口。
// 新创建的Network Namespace默认只有lo但处于DOWN状态。
func setupLoopback() error {
	cmd := exec.Command("ip", "link", "set", "lo", "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// buildCleanEnv 构建传递给用户命令的环境变量。
// 移除sandbox内部使用的环境变量。
func buildCleanEnv(userEnv []string) []string {
	base := userEnv
	if len(base) == 0 {
		base = os.Environ()
	}

	clean := make([]string, 0, len(base))
	for _, e := range base {
		// 过滤掉sandbox内部环境变量
		if strings.HasPrefix(e, initPipeEnv+"=") {
			continue
		}
		if strings.HasPrefix(e, initLogPipeEnv+"=") {
			continue
		}
		clean = append(clean, e)
	}
	return clean
}
