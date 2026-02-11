//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// PivotRootConfig 定义 pivot_root 目录禁锢的配置（父进程侧）。
type PivotRootConfig struct {
	Enabled bool   // 是否启用 pivot_root
	RootDir string // 新的根目录路径（未启用 overlay 时使用）
}

// DefaultPivotRootConfig 返回默认的 PivotRoot 配置：启用、RootDir 为空（依赖 overlay 提供）。
func DefaultPivotRootConfig() PivotRootConfig {
	return PivotRootConfig{
		Enabled: true,
		RootDir: "",
	}
}

// pivotRootConfig 通过管道传递给子进程的 pivot_root 配置。
type pivotRootConfig struct {
	RootDir string `json:"root_dir"` // 新 root 路径（无 overlay 时使用）
}

// doPivotRoot 执行完整的 pivot_root 流程。
//
// 步骤：
//  1. 将 newRoot bind mount 到自身（pivot_root 要求 newRoot 是挂载点）
//  2. 在 newRoot 内创建 .pivot_old 目录
//  3. 调用 pivot_root(newRoot, pivotDir)
//  4. chdir("/")
//  5. 以 MNT_DETACH 卸载旧 root（/.pivot_old）
//  6. 删除 .pivot_old 目录
func doPivotRoot(newRoot string) error {
	// pivot_root 要求 newRoot 必须是一个挂载点。
	// 通过 bind mount 到自身来满足此要求。
	if err := syscall.Mount(newRoot, newRoot, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount %s: %w", newRoot, err)
	}

	// 创建旧 root 的挂载点目录
	pivotDir := filepath.Join(newRoot, ".pivot_old")
	if err := os.MkdirAll(pivotDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", pivotDir, err)
	}

	// 执行 pivot_root：将 newRoot 作为新的 /，旧 root 挂载到 pivotDir
	if err := syscall.PivotRoot(newRoot, pivotDir); err != nil {
		return fmt.Errorf("pivot_root(%s, %s): %w", newRoot, pivotDir, err)
	}

	// 切换到新的根目录
	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir /: %w", err)
	}

	// 卸载旧的 root 文件系统（现在位于 /.pivot_old）
	// 使用 MNT_DETACH 确保即使有文件被占用也能卸载
	if err := syscall.Unmount("/.pivot_old", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount /.pivot_old: %w", err)
	}

	// 删除旧 root 挂载点目录
	if err := os.Remove("/.pivot_old"); err != nil {
		// 非致命：目录可能在 unmount 后已不存在
		_ = err
	}

	return nil
}

// setupMinimalDev 在新 root 中创建最小的 /dev 目录。
// 包含基本设备节点和必要的符号链接。
//
// 设备列表：
//   - /dev/null   - 丢弃写入的数据
//   - /dev/zero   - 读取返回零字节
//   - /dev/urandom - 伪随机数生成器
//   - /dev/fd     -> /proc/self/fd（文件描述符）
//   - /dev/stdin  -> /proc/self/fd/0
//   - /dev/stdout -> /proc/self/fd/1
//   - /dev/stderr -> /proc/self/fd/2
func setupMinimalDev(rootDir string) error {
	devDir := filepath.Join(rootDir, "dev")
	if err := os.MkdirAll(devDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", devDir, err)
	}

	// bind mount 必要的设备节点
	devices := []struct {
		src string
		dst string
	}{
		{"/dev/null", filepath.Join(devDir, "null")},
		{"/dev/zero", filepath.Join(devDir, "zero")},
		{"/dev/urandom", filepath.Join(devDir, "urandom")},
	}

	for _, d := range devices {
		// 创建目标文件（bind mount 需要目标文件存在）
		f, err := os.OpenFile(d.dst, os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			return fmt.Errorf("create %s: %w", d.dst, err)
		}
		f.Close()

		// bind mount 设备
		if err := syscall.Mount(d.src, d.dst, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind mount %s -> %s: %w", d.src, d.dst, err)
		}
	}

	// 创建符号链接（这些链接在 /proc 挂载后才能正常工作）
	symlinks := []struct {
		target string
		link   string
	}{
		{"/proc/self/fd", filepath.Join(devDir, "fd")},
		{"/proc/self/fd/0", filepath.Join(devDir, "stdin")},
		{"/proc/self/fd/1", filepath.Join(devDir, "stdout")},
		{"/proc/self/fd/2", filepath.Join(devDir, "stderr")},
	}

	for _, s := range symlinks {
		// 如果链接已存在则跳过
		if _, err := os.Lstat(s.link); err == nil {
			continue
		}
		if err := os.Symlink(s.target, s.link); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", s.link, s.target, err)
		}
	}

	return nil
}
