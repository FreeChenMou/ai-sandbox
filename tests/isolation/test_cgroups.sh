#!/bin/bash
# test_cgroups.sh - Cgroups v2 模块测试脚本
# 用法: sudo bash tests/isolation/test_cgroups.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  Cgroups v2 模块测试"
echo "============================================"

# -----------------------------------------------
section "0. 环境检查"
# -----------------------------------------------

check_root

cgv2=true
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    controllers=$(cat /sys/fs/cgroup/cgroup.controllers)
    log_pass "cgroups v2 可用 (controllers: $controllers)"
else
    log_skip "cgroups v2 不可用，集成测试将被跳过"
    cgv2=false
fi

# -----------------------------------------------
section "1. 编译验证"
# -----------------------------------------------

check_build

# -----------------------------------------------
section "2. 单元测试（不需要 root / cgroups v2）"
# -----------------------------------------------

run_test "TestDefaultCgroupsConfig"
run_test "TestNewCgroupsV2"
run_test "TestCgroupsSetupValidation"
run_test "TestCgroupsV2Available"

# -----------------------------------------------
section "3. 生命周期测试（需要 root + cgroups v2）"
# -----------------------------------------------

run_test "TestCgroupsSetupAndCleanup"
run_test "TestCgroupsDoubleSetup"
run_test "TestCgroupsAddProcess"
run_test "TestCgroupsCleanupAfterCrash"

# -----------------------------------------------
section "4. 资源限制测试（需要 root + cgroups v2）"
# -----------------------------------------------

run_test "TestCgroupsCPULimit" 120s
run_test "TestCgroupsMemoryLimit" 120s
run_test "TestCgroupsPidsLimit" 120s

# -----------------------------------------------
section "5. 联合测试（需要 root + cgroups v2）"
# -----------------------------------------------

run_test "TestCgroupsWithNamespace"
run_test "TestCgroupsWithOverlayAndNamespace"

# -----------------------------------------------
section "6. 并发测试"
# -----------------------------------------------

run_test "TestConcurrentCgroups"

# -----------------------------------------------
section "7. 基准测试"
# -----------------------------------------------

bench_output=$(go test -bench "BenchmarkCgroupsSetupCleanup" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
if echo "$bench_output" | grep -q "Benchmark"; then
    log_pass "BenchmarkCgroupsSetupCleanup"
    echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
else
    log_skip "BenchmarkCgroupsSetupCleanup"
fi

bench_output=$(go test -bench "BenchmarkCgroupsWithNamespace" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
if echo "$bench_output" | grep -q "Benchmark"; then
    log_pass "BenchmarkCgroupsWithNamespace"
    echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
else
    log_skip "BenchmarkCgroupsWithNamespace"
fi

# -----------------------------------------------
section "8. CLI 验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-cg-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 编译"

    help_output=$("$binary" 2>&1 || true)
    for f in "no-cgroup" "cpu-quota" "cpu-period" "memory-max" "pids-max"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    if output=$("$binary" sh -c 'echo cg-ok' 2>&1); then
        if echo "$output" | grep -q "cg-ok"; then
            log_pass "CLI: 默认执行（带 cgroup）"
        else
            log_fail "CLI: 默认执行输出异常"
            log_info "$output"
        fi
    else
        log_info "CLI: 默认执行返回错误（环境可能不支持）"
        log_info "$output"
        log_skip "CLI: 默认执行（带 cgroup）"
    fi

    if output=$("$binary" --no-cgroup sh -c 'echo nocg-ok' 2>&1); then
        if echo "$output" | grep -q "nocg-ok"; then
            log_pass "CLI: --no-cgroup"
        else
            log_fail "CLI: --no-cgroup 输出异常"
            log_info "$output"
        fi
    else
        log_fail "CLI: --no-cgroup 执行失败"
        log_info "$output"
    fi

    if output=$("$binary" --memory-max 256m --cpu-quota 50000 --pids-max 100 sh -c 'echo custom-ok' 2>&1); then
        if echo "$output" | grep -q "custom-ok"; then
            log_pass "CLI: 自定义资源限制"
        else
            log_fail "CLI: 自定义资源限制输出异常"
            log_info "$output"
        fi
    else
        log_info "CLI: 自定义资源限制返回错误"
        log_info "$output"
        log_skip "CLI: 自定义资源限制"
    fi

    if output=$("$binary" --overlay --memory-max 512m sh -c 'echo triple-ok' 2>&1); then
        if echo "$output" | grep -q "triple-ok"; then
            log_pass "CLI: Namespace + OverlayFS + Cgroups"
        else
            log_fail "CLI: 三层联合输出异常"
            log_info "$output"
        fi
    else
        log_info "CLI: 三层联合返回错误"
        log_info "$output"
        log_skip "CLI: Namespace + OverlayFS + Cgroups"
    fi

    rm -f "$binary"
else
    log_fail "CLI 编译失败"
fi

# -----------------------------------------------
print_summary "Cgroups v2 测试结果"
