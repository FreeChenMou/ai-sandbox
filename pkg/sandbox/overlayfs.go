//go:build linux

package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// OverlayConfig 定义OverlayFS文件系统隔离的配置。
type OverlayConfig struct {
	Enabled   bool     // 是否启用OverlayFS隔离
	LowerDirs []string // 只读底层目录（支持多层，高优先级在前）
	MergeDir  string   // 合并挂载点（默认自动生成）
	TmpfsSize string   // tmpfs大小限制，如 "64m"、"256m"（默认 "64m"）
	BaseDir   string   // 临时目录父路径（默认 "/tmp"）
	ReadOnly  bool     // true时无UpperDir，完全只读
}

// DefaultOverlayConfig 返回默认的OverlayFS配置。
// lowerDir 指定只读底层目录（通常是宿主机的根文件系统或特定目录）。
func DefaultOverlayConfig(lowerDir string) OverlayConfig {
	return OverlayConfig{
		Enabled:   true,
		LowerDirs: []string{lowerDir},
		TmpfsSize: "64m",
		BaseDir:   "/tmp",
	}
}

// overlayInitConfig 通过管道传递给子进程的overlay配置。
// 子进程使用此配置在新Namespace中挂载OverlayFS。
type overlayInitConfig struct {
	LowerDirs []string `json:"lower_dirs"`
	UpperDir  string   `json:"upper_dir"`
	WorkDir   string   `json:"work_dir"`
	MergeDir  string   `json:"merge_dir"`
	ReadOnly  bool     `json:"read_only,omitempty"`
}

// OverlayFS 管理单个沙箱的OverlayFS生命周期。
//
// 使用方式：
//
//	ov := sandbox.NewOverlayFS(sandbox.DefaultOverlayConfig("/"))
//	if err := ov.Setup(); err != nil { ... }
//	defer ov.Cleanup()
//	ns.SetOverlayFS(ov)
type OverlayFS struct {
	config    OverlayConfig
	logger    *zap.Logger
	id        string // 唯一标识，用于目录命名
	baseDir   string // /tmp/sandbox-overlay-<id>/
	upperDir  string // baseDir/upper
	workDir   string // baseDir/work
	mergeDir  string // baseDir/merged 或用户指定
	setupDone bool
	mu        sync.Mutex
}

// NewOverlayFS 创建OverlayFS管理器实例。
func NewOverlayFS(config OverlayConfig) *OverlayFS {
	return &OverlayFS{
		config: config,
	}
}

// SetLogger 设置此OverlayFS的日志记录器。
func (ov *OverlayFS) SetLogger(l *zap.Logger) {
	ov.mu.Lock()
	defer ov.mu.Unlock()
	ov.logger = l
}

// generateID 生成唯一标识符：纳秒时间戳 + 4字节随机数。
func generateID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

// buildOverlayOptions 构建OverlayFS mount选项字符串。
// 格式: "lowerdir=dir1:dir2,upperdir=...,workdir=..."
func buildOverlayOptions(cfg *overlayInitConfig) string {
	opts := "lowerdir=" + strings.Join(cfg.LowerDirs, ":")
	if !cfg.ReadOnly {
		opts += ",upperdir=" + cfg.UpperDir
		opts += ",workdir=" + cfg.WorkDir
	}
	return opts
}

// Setup 在父进程侧准备OverlayFS所需的目录和tmpfs挂载。
//
// 执行步骤：
//  1. 验证配置
//  2. 生成唯一ID
//  3. 创建基础目录
//  4. 挂载tmpfs（限制大小）
//  5. 创建upper/work/merged子目录
func (ov *OverlayFS) Setup() error {
	ov.mu.Lock()
	defer ov.mu.Unlock()

	if ov.setupDone {
		return fmt.Errorf("overlayfs: already set up")
	}

	// 验证配置
	if !ov.config.Enabled {
		return fmt.Errorf("overlayfs: not enabled")
	}
	if len(ov.config.LowerDirs) == 0 {
		return fmt.Errorf("overlayfs: no lower dirs specified")
	}
	for _, d := range ov.config.LowerDirs {
		if _, err := os.Stat(d); err != nil {
			return fmt.Errorf("overlayfs: lower dir %q: %w", d, err)
		}
	}

	// 生成唯一ID和路径
	ov.id = generateID()
	baseDir := ov.config.BaseDir
	if baseDir == "" {
		baseDir = "/tmp"
	}
	ov.baseDir = filepath.Join(baseDir, "sandbox-overlay-"+ov.id)

	// 创建基础目录
	if err := os.MkdirAll(ov.baseDir, 0700); err != nil {
		return fmt.Errorf("overlayfs: mkdir base: %w", err)
	}

	// 挂载tmpfs
	tmpfsSize := ov.config.TmpfsSize
	if tmpfsSize == "" {
		tmpfsSize = "64m"
	}
	mountOpts := fmt.Sprintf("size=%s,mode=0700", tmpfsSize)
	if err := syscall.Mount("tmpfs", ov.baseDir, "tmpfs", 0, mountOpts); err != nil {
		os.Remove(ov.baseDir)
		return fmt.Errorf("overlayfs: mount tmpfs: %w", err)
	}

	// 创建子目录
	ov.upperDir = filepath.Join(ov.baseDir, "upper")
	ov.workDir = filepath.Join(ov.baseDir, "work")
	if ov.config.MergeDir != "" {
		ov.mergeDir = ov.config.MergeDir
	} else {
		ov.mergeDir = filepath.Join(ov.baseDir, "merged")
	}

	for _, dir := range []string{ov.upperDir, ov.workDir, ov.mergeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			// 回滚：卸载tmpfs并删除基础目录
			syscall.Unmount(ov.baseDir, syscall.MNT_DETACH)
			os.RemoveAll(ov.baseDir)
			return fmt.Errorf("overlayfs: mkdir %s: %w", dir, err)
		}
	}

	ov.setupDone = true

	if ov.logger != nil {
		ov.logger.Info("overlay setup",
			zap.String("overlay_id", ov.id),
			zap.Strings("lower_dirs", ov.config.LowerDirs),
			zap.String("tmpfs_size", ov.config.TmpfsSize),
		)
	}

	return nil
}

// InitConfig 返回传递给子进程的overlay配置。
// 必须在Setup()之后调用。
func (ov *OverlayFS) InitConfig() *overlayInitConfig {
	ov.mu.Lock()
	defer ov.mu.Unlock()

	if !ov.setupDone {
		return nil
	}
	return &overlayInitConfig{
		LowerDirs: ov.config.LowerDirs,
		UpperDir:  ov.upperDir,
		WorkDir:   ov.workDir,
		MergeDir:  ov.mergeDir,
		ReadOnly:  ov.config.ReadOnly,
	}
}

// Cleanup 清理OverlayFS资源。
//
// 执行步骤（防御性，忽略已卸载的错误）：
//  1. 卸载overlay合并挂载点
//  2. 卸载tmpfs
//  3. 删除基础目录
func (ov *OverlayFS) Cleanup() error {
	ov.mu.Lock()
	defer ov.mu.Unlock()

	if !ov.setupDone {
		return nil
	}

	if ov.logger != nil {
		ov.logger.Info("overlay cleanup", zap.String("overlay_id", ov.id))
	}

	var errs []error

	// 1. 卸载overlay合并点（可能由子进程挂载，也可能未挂载）
	if ov.mergeDir != "" {
		if err := syscall.Unmount(ov.mergeDir, syscall.MNT_DETACH); err != nil {
			// 非致命：合并点可能未被挂载（子进程在自己的namespace中挂载）
			_ = err
		}
	}

	// 2. 卸载tmpfs
	if err := syscall.Unmount(ov.baseDir, syscall.MNT_DETACH); err != nil {
		errs = append(errs, fmt.Errorf("unmount tmpfs %s: %w", ov.baseDir, err))
	}

	// 3. 删除基础目录
	if err := os.RemoveAll(ov.baseDir); err != nil {
		errs = append(errs, fmt.Errorf("remove %s: %w", ov.baseDir, err))
	}

	ov.setupDone = false

	if len(errs) > 0 {
		return fmt.Errorf("overlayfs cleanup errors: %v", errs)
	}
	return nil
}

// mountOverlay 在子进程中执行OverlayFS挂载。
// 此函数在新Namespace内运行，由nsInit()调用。
func mountOverlay(cfg *overlayInitConfig) error {
	if cfg == nil {
		return nil
	}

	// 确保合并目录存在
	if err := os.MkdirAll(cfg.MergeDir, 0700); err != nil {
		return fmt.Errorf("mkdir merge dir %s: %w", cfg.MergeDir, err)
	}

	opts := buildOverlayOptions(cfg)
	if err := syscall.Mount("overlay", cfg.MergeDir, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay on %s (opts=%s): %w", cfg.MergeDir, opts, err)
	}
	return nil
}

// MergeDir 返回合并挂载点路径。Setup()之前返回空字符串。
func (ov *OverlayFS) MergeDir() string {
	ov.mu.Lock()
	defer ov.mu.Unlock()
	return ov.mergeDir
}

// UpperDir 返回上层可写目录路径。Setup()之前返回空字符串。
func (ov *OverlayFS) UpperDir() string {
	ov.mu.Lock()
	defer ov.mu.Unlock()
	return ov.upperDir
}

// ID 返回OverlayFS实例的唯一标识符。Setup()之前返回空字符串。
func (ov *OverlayFS) ID() string {
	ov.mu.Lock()
	defer ov.mu.Unlock()
	return ov.id
}
