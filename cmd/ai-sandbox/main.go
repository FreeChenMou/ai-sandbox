package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"aisandbox/pkg/sandbox"
)

const (
	ExitSuccess = 0 // 正常退出
	ExitFailure = 1 // 一般性错误
)

func main() {
	// 必须在第一行：检测是否是sandbox init子进程
	sandbox.MustReexecInit()

	os.Exit(run())
}

// run 执行主逻辑并返回退出码。
// 独立为函数以确保 defer 正常执行（os.Exit 会跳过 defer）。
func run() int {
	var (
		noPID        bool
		noIPC        bool
		noNet        bool
		noUTS        bool
		host         string
		overlay      bool
		overlayLower string
		overlaySize  string
		noCgroup     bool
		cpuQuota     int
		cpuPeriod    int
		memoryMax    string
		pidsMax      int
		logDir       string
		logLevel     string
	)

	flag.BoolVar(&noPID, "no-pid", false, "disable PID namespace isolation")
	flag.BoolVar(&noIPC, "no-ipc", false, "disable IPC namespace isolation")
	flag.BoolVar(&noNet, "no-net", false, "disable network namespace isolation")
	flag.BoolVar(&noUTS, "no-uts", false, "disable UTS namespace isolation")
	flag.StringVar(&host, "hostname", "sandbox", "hostname inside the sandbox")
	flag.BoolVar(&overlay, "overlay", false, "enable OverlayFS filesystem isolation")
	flag.StringVar(&overlayLower, "overlay-lower", "/", "lower directory for OverlayFS (read-only base)")
	flag.StringVar(&overlaySize, "overlay-size", "64m", "tmpfs size limit for OverlayFS upper layer")
	flag.BoolVar(&noCgroup, "no-cgroup", false, "disable cgroups v2 resource limits")
	flag.IntVar(&cpuQuota, "cpu-quota", 100000, "CPU quota in microseconds per period (0=unlimited)")
	flag.IntVar(&cpuPeriod, "cpu-period", 100000, "CPU period in microseconds")
	flag.StringVar(&memoryMax, "memory-max", "512m", "memory limit (supports k/m/g suffixes, 0=unlimited)")
	flag.IntVar(&pidsMax, "pids-max", 512, "maximum number of processes (0=unlimited)")
	flag.StringVar(&logDir, "log-dir", "/var/log/ai-sandbox", "log file storage directory")
	flag.StringVar(&logLevel, "log-level", "info", "log level: debug/info/warn/error")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: ai-sandbox [options] <command> [args...]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  ai-sandbox python agent.py")
		fmt.Fprintln(os.Stderr, "  ai-sandbox --no-net sh -c 'echo hello'")
		fmt.Fprintln(os.Stderr, "  ai-sandbox --overlay sh -c 'touch /tmp/test && ls /tmp/test'")
		fmt.Fprintln(os.Stderr, "  ai-sandbox --memory-max 1g --cpu-quota 50000 python agent.py")
		return ExitFailure
	}

	// 创建日志记录器
	logConfig := sandbox.LogConfig{
		Level:   logLevel,
		Dir:     logDir,
		Console: true,
	}
	slog, err := sandbox.NewSandboxLogger(logConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: create logger: %v\n", err)
		return ExitFailure
	}
	defer slog.Close()
	logger := slog.Logger()

	// 构建Namespace配置
	config := sandbox.DefaultNamespaceConfig()
	config.Hostname = host

	if noPID {
		config.PID = false
		config.MountProc = false
	}
	if noIPC {
		config.IPC = false
	}
	if noNet {
		config.Network = false
		config.SetupLoopback = false
	}
	if noUTS {
		config.UTS = false
	}

	// 创建Namespace并执行命令
	ns := sandbox.NewNamespace(config)
	ns.SetLogger(logger)
	defer ns.Cleanup()

	// 配置OverlayFS（如果启用）
	if overlay {
		ovConfig := sandbox.DefaultOverlayConfig(overlayLower)
		ovConfig.TmpfsSize = overlaySize
		ov := sandbox.NewOverlayFS(ovConfig)
		ov.SetLogger(logger)
		if err := ov.Setup(); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox: overlay setup: %v\n", err)
			return ExitFailure
		}
		ns.SetOverlayFS(ov)
	}

	// 配置CgroupsV2（默认启用）
	if !noCgroup {
		memBytes, err := parseMemorySize(memoryMax)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sandbox: invalid --memory-max %q: %v\n", memoryMax, err)
			return ExitFailure
		}

		cgConfig := sandbox.CgroupsConfig{
			Enabled:   true,
			CPUQuota:  cpuQuota,
			CPUPeriod: cpuPeriod,
			MemoryMax: memBytes,
			PidsMax:   pidsMax,
		}
		cg := sandbox.NewCgroupsV2(cgConfig)
		cg.SetLogger(logger)
		if err := cg.Setup(); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox: cgroup setup: %v\n", err)
			return ExitFailure
		}
		ns.SetCgroupsV2(cg)
	}

	result, err := ns.Execute(args[0], args[1:]...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: %v\n", err)
		return ExitFailure
	}

	return result.ExitCode
}

// parseMemorySize 解析带后缀的内存大小字符串。
// 支持 k/K（KB）、m/M（MB）、g/G（GB）后缀，纯数字视为字节。
// 例如："512m" → 536870912, "1g" → 1073741824, "0" → 0
func parseMemorySize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	// 检查后缀
	last := s[len(s)-1]
	switch last {
	case 'k', 'K':
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size: %s", s)
		}
		return n * 1024, nil
	case 'm', 'M':
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size: %s", s)
		}
		return n * 1024 * 1024, nil
	case 'g', 'G':
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size: %s", s)
		}
		return n * 1024 * 1024 * 1024, nil
	default:
		// 纯数字，视为字节
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size: %s", s)
		}
		return n, nil
	}
}
