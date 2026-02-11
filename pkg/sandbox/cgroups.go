//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// CgroupsConfig 定义 cgroups v2 资源限制的配置。
type CgroupsConfig struct {
	Enabled   bool   // 是否启用
	CPUQuota  int    // CPU 配额（微秒/周期），0=不限制。100000=1核
	CPUPeriod int    // CPU 周期（微秒），默认 100000（100ms）
	MemoryMax int64  // 内存上限（字节），0=不限制。536870912=512MB
	PidsMax   int    // 最大进程数，0=不限制
	BaseDir   string // cgroup2 挂载点，默认 "/sys/fs/cgroup"
}

// DefaultCgroupsConfig 返回默认配置：1核 CPU、512MB 内存、512 进程。
func DefaultCgroupsConfig() CgroupsConfig {
	return CgroupsConfig{
		Enabled:   true,
		CPUQuota:  100000,
		CPUPeriod: 100000,
		MemoryMax: 536870912, // 512MB
		PidsMax:   512,
		BaseDir:   "/sys/fs/cgroup",
	}
}

// CgroupsV2Available 检测系统是否支持 cgroups v2。
// 通过检查 /sys/fs/cgroup/cgroup.controllers 文件是否存在来判断。
func CgroupsV2Available() bool {
	_, err := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	return err == nil
}

// CgroupsV2 管理单个沙箱的 cgroups v2 生命周期。
//
// 使用方式：
//
//	cg := sandbox.NewCgroupsV2(sandbox.DefaultCgroupsConfig())
//	if err := cg.Setup(); err != nil { ... }
//	defer cg.Cleanup()
//	ns.SetCgroupsV2(cg)
type CgroupsV2 struct {
	config    CgroupsConfig
	logger    *zap.Logger
	id        string // 唯一标识，复用 generateID()
	cgroupDir string // /sys/fs/cgroup/sandbox-<id>/
	setupDone bool
	mu        sync.Mutex
}

// NewCgroupsV2 创建 CgroupsV2 管理器实例。
func NewCgroupsV2(config CgroupsConfig) *CgroupsV2 {
	return &CgroupsV2{
		config: config,
	}
}

// SetLogger 设置此CgroupsV2的日志记录器。
func (cg *CgroupsV2) SetLogger(l *zap.Logger) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	cg.logger = l
}

// ID 返回 CgroupsV2 实例的唯一标识符。Setup() 之前返回空字符串。
func (cg *CgroupsV2) ID() string {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	return cg.id
}

// CgroupDir 返回 cgroup 目录路径。Setup() 之前返回空字符串。
func (cg *CgroupsV2) CgroupDir() string {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	return cg.cgroupDir
}

// Setup 创建 cgroup 目录、启用控制器并写入资源限制。
//
// 执行步骤：
//  1. 验证配置
//  2. 检测 cgroups v2 可用性
//  3. 生成 ID、创建 cgroup 目录
//  4. 启用所需控制器
//  5. 写入 cpu.max / memory.max / pids.max
func (cg *CgroupsV2) Setup() error {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	if cg.setupDone {
		return fmt.Errorf("cgroups: already set up")
	}

	if !cg.config.Enabled {
		return fmt.Errorf("cgroups: not enabled")
	}

	// 验证配置值
	if cg.config.CPUQuota < 0 {
		return fmt.Errorf("cgroups: cpu quota must be non-negative, got %d", cg.config.CPUQuota)
	}
	if cg.config.CPUPeriod < 0 {
		return fmt.Errorf("cgroups: cpu period must be non-negative, got %d", cg.config.CPUPeriod)
	}
	if cg.config.MemoryMax < 0 {
		return fmt.Errorf("cgroups: memory max must be non-negative, got %d", cg.config.MemoryMax)
	}
	if cg.config.PidsMax < 0 {
		return fmt.Errorf("cgroups: pids max must be non-negative, got %d", cg.config.PidsMax)
	}

	// 检测 cgroups v2 可用性
	baseDir := cg.config.BaseDir
	if baseDir == "" {
		baseDir = "/sys/fs/cgroup"
	}

	controllersPath := filepath.Join(baseDir, "cgroup.controllers")
	if _, err := os.Stat(controllersPath); err != nil {
		return fmt.Errorf("cgroups: v2 not available (cannot stat %s): %w", controllersPath, err)
	}

	// 确定需要启用的控制器
	var controllers []string
	if cg.config.CPUQuota > 0 {
		controllers = append(controllers, "cpu")
	}
	if cg.config.MemoryMax > 0 {
		controllers = append(controllers, "memory")
	}
	if cg.config.PidsMax > 0 {
		controllers = append(controllers, "pids")
	}

	// 启用控制器
	if len(controllers) > 0 {
		if err := enableControllers(baseDir, controllers); err != nil {
			return fmt.Errorf("cgroups: enable controllers: %w", err)
		}
	}

	// 生成 ID 和创建目录
	cg.id = generateID()
	cg.cgroupDir = filepath.Join(baseDir, "sandbox-"+cg.id)

	if err := os.Mkdir(cg.cgroupDir, 0755); err != nil {
		return fmt.Errorf("cgroups: mkdir %s: %w", cg.cgroupDir, err)
	}

	// 写入资源限制，失败时回滚
	if err := cg.writeLimits(); err != nil {
		os.Remove(cg.cgroupDir)
		return err
	}

	cg.setupDone = true

	if cg.logger != nil {
		cg.logger.Info("cgroup setup",
			zap.String("cgroup_id", cg.id),
			zap.Int("cpu_quota", cg.config.CPUQuota),
			zap.Int("cpu_period", cg.config.CPUPeriod),
			zap.Int64("memory_max", cg.config.MemoryMax),
			zap.Int("pids_max", cg.config.PidsMax),
		)
	}

	return nil
}

// writeLimits 写入 CPU、内存、进程数限制到 cgroup 控制文件。
func (cg *CgroupsV2) writeLimits() error {
	// CPU 限制：cpu.max 格式为 "quota period"
	if cg.config.CPUQuota > 0 {
		period := cg.config.CPUPeriod
		if period <= 0 {
			period = 100000
		}
		content := fmt.Sprintf("%d %d", cg.config.CPUQuota, period)
		if err := writeFile(filepath.Join(cg.cgroupDir, "cpu.max"), content); err != nil {
			return fmt.Errorf("cgroups: write cpu.max: %w", err)
		}
	}

	// 内存限制
	if cg.config.MemoryMax > 0 {
		content := strconv.FormatInt(cg.config.MemoryMax, 10)
		if err := writeFile(filepath.Join(cg.cgroupDir, "memory.max"), content); err != nil {
			return fmt.Errorf("cgroups: write memory.max: %w", err)
		}
	}

	// 进程数限制
	if cg.config.PidsMax > 0 {
		content := strconv.Itoa(cg.config.PidsMax)
		if err := writeFile(filepath.Join(cg.cgroupDir, "pids.max"), content); err != nil {
			return fmt.Errorf("cgroups: write pids.max: %w", err)
		}
	}

	return nil
}

// AddProcess 将指定 PID 写入 cgroup.procs，将进程加入此 cgroup。
// 失败是致命错误——资源限制失效意味着安全边界被突破。
func (cg *CgroupsV2) AddProcess(pid int) error {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	if !cg.setupDone {
		return fmt.Errorf("cgroups: not set up")
	}

	procsPath := filepath.Join(cg.cgroupDir, "cgroup.procs")
	if err := writeFile(procsPath, strconv.Itoa(pid)); err != nil {
		return err
	}

	if cg.logger != nil {
		cg.logger.Info("cgroup add process", zap.String("cgroup_id", cg.id), zap.Int("pid", pid))
	}
	return nil
}

// Cleanup 清理 cgroup 资源。
//
// 执行步骤：
//  1. 读取残留进程 PID
//  2. 迁移残留进程到父 cgroup
//  3. 删除 cgroup 目录（带单次重试，间隔 10ms）
func (cg *CgroupsV2) Cleanup() error {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	if !cg.setupDone {
		return nil
	}

	if cg.logger != nil {
		cg.logger.Info("cgroup cleanup", zap.String("cgroup_id", cg.id))
	}

	baseDir := cg.config.BaseDir
	if baseDir == "" {
		baseDir = "/sys/fs/cgroup"
	}

	// 读取残留进程并迁移到父 cgroup
	pids := readPids(cg.cgroupDir)
	if len(pids) > 0 {
		parentProcs := filepath.Join(baseDir, "cgroup.procs")
		for _, pid := range pids {
			// 迁移失败不阻塞清理（进程可能已退出）
			_ = writeFile(parentProcs, strconv.Itoa(pid))
		}
	}

	// 删除 cgroup 目录（只能用 os.Remove，不能用 os.RemoveAll）
	err := os.Remove(cg.cgroupDir)
	if err != nil && !os.IsNotExist(err) {
		// 单次重试：僵尸进程可能尚未完全回收
		time.Sleep(10 * time.Millisecond)
		err = os.Remove(cg.cgroupDir)
		if err != nil && !os.IsNotExist(err) {
			cg.setupDone = false
			return fmt.Errorf("cgroups: rmdir %s: %w", cg.cgroupDir, err)
		}
	}

	cg.setupDone = false
	return nil
}

// enableControllers 在 baseDir 的 cgroup.subtree_control 中启用指定控制器。
func enableControllers(baseDir string, controllers []string) error {
	subtreeCtl := filepath.Join(baseDir, "cgroup.subtree_control")

	// 构建 "+cpu +memory +pids" 格式
	var parts []string
	for _, c := range controllers {
		parts = append(parts, "+"+c)
	}
	content := strings.Join(parts, " ")

	return writeFile(subtreeCtl, content)
}

// writeFile 写入内容到 cgroup 控制文件。
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// readPids 读取 cgroup.procs 中的 PID 列表。
func readPids(cgroupDir string) []int {
	data, err := os.ReadFile(filepath.Join(cgroupDir, "cgroup.procs"))
	if err != nil {
		return nil
	}

	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}
