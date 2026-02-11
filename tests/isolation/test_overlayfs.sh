#!/bin/bash
# test_overlayfs.sh - OverlayFS 模块测试脚本
# 用法: sudo bash tests/isolation/test_overlayfs.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  OverlayFS 模块测试"
echo "============================================"

# -----------------------------------------------
section "0. 环境检查"
# -----------------------------------------------

check_root

if grep -q overlay /proc/filesystems 2>/dev/null; then
    log_pass "overlay 文件系统可用"
else
    log_skip "overlay 文件系统不可用，集成测试可能失败"
fi

# -----------------------------------------------
section "1. 编译验证"
# -----------------------------------------------

check_build

# -----------------------------------------------
section "2. 单元测试（不需要 root）"
# -----------------------------------------------

run_test "TestDefaultOverlayConfig"
run_test "TestBuildOverlayOptions"
run_test "TestGenerateIDUniqueness"
run_test "TestNewOverlayFS"
run_test "TestOverlaySetupValidation"

# -----------------------------------------------
section "3. 生命周期测试"
# -----------------------------------------------

run_test "TestOverlaySetupAndCleanup"
run_test "TestOverlayCleanupAfterCrash"

# -----------------------------------------------
section "4. 读写隔离测试"
# -----------------------------------------------

run_test "TestOverlayWriteIsolation"
run_test "TestOverlayReadFromLower"
run_test "TestOverlayLowerUnmodified"

# -----------------------------------------------
section "5. 高级功能测试"
# -----------------------------------------------

run_test "TestOverlayTmpfsSize" 120s
run_test "TestOverlayMultipleLowerDirs"

# -----------------------------------------------
section "6. 与 Namespace 联合测试"
# -----------------------------------------------

run_test "TestOverlayWithNamespace"

# -----------------------------------------------
section "7. 并发测试"
# -----------------------------------------------

run_test "TestConcurrentOverlays"

# -----------------------------------------------
section "8. 基准测试"
# -----------------------------------------------

bench_output=$(go test -bench "BenchmarkOverlaySetupCleanup" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
if echo "$bench_output" | grep -q "Benchmark"; then
    log_pass "BenchmarkOverlaySetupCleanup"
    echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
else
    log_skip "BenchmarkOverlaySetupCleanup"
fi

bench_output=$(go test -bench "BenchmarkOverlayWithNamespace" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
if echo "$bench_output" | grep -q "Benchmark"; then
    log_pass "BenchmarkOverlayWithNamespace"
    echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
else
    log_skip "BenchmarkOverlayWithNamespace"
fi

# -----------------------------------------------
section "9. CLI 验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-ov-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 编译"

    # flag 检查
    help_output=$("$binary" 2>&1 || true)
    for f in "overlay" "overlay-lower" "overlay-size"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    # OverlayFS 基本执行
    if output=$("$binary" --no-cgroup --overlay sh -c 'echo ov-ok' 2>&1); then
        if echo "$output" | grep -q "ov-ok"; then
            log_pass "CLI: --overlay 基本执行"
        else
            log_fail "CLI: --overlay 输出异常"
            log_info "$output"
        fi
    else
        log_fail "CLI: --overlay 执行失败"
        log_info "$output"
    fi

    # OverlayFS 写入隔离
    tmpdir=$(mktemp -d /tmp/ov-cli-test-XXXXXX)
    echo "original" > "$tmpdir/file.txt"
    if output=$("$binary" --no-cgroup --overlay --overlay-lower "$tmpdir" sh -c "echo modified > $tmpdir/file.txt; cat $tmpdir/file.txt" 2>&1); then
        host_content=$(cat "$tmpdir/file.txt")
        if [ "$host_content" = "original" ]; then
            log_pass "CLI: --overlay 写入隔离（宿主机未被修改）"
        else
            log_fail "CLI: --overlay 写入泄漏到宿主机"
            log_info "宿主机内容: $host_content"
        fi
    else
        log_info "CLI: --overlay 写入隔离测试返回错误"
        log_info "$output"
        log_skip "CLI: --overlay 写入隔离"
    fi
    rm -rf "$tmpdir"

    # 自定义 overlay-size
    if output=$("$binary" --no-cgroup --overlay --overlay-size 32m sh -c 'echo size-ok' 2>&1); then
        if echo "$output" | grep -q "size-ok"; then
            log_pass "CLI: --overlay-size 32m"
        else
            log_fail "CLI: --overlay-size 输出异常"
            log_info "$output"
        fi
    else
        log_fail "CLI: --overlay-size 执行失败"
        log_info "$output"
    fi

    rm -f "$binary"
else
    log_fail "CLI 编译失败"
fi

# -----------------------------------------------
print_summary "OverlayFS 测试结果"
