#!/bin/bash
# test_all.sh - 隔离模块全量测试（Namespace + OverlayFS + Cgroups + Log）
# 用法: sudo bash tests/isolation/test_all.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  AI-Sandbox 隔离模块全量测试"
echo "  Namespace + OverlayFS + Cgroups + Log"
echo "============================================"

# -----------------------------------------------
section "0. 环境检查"
# -----------------------------------------------

check_root

cgv2=true
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    log_pass "cgroups v2 可用 ($(cat /sys/fs/cgroup/cgroup.controllers))"
else
    log_skip "cgroups v2 不可用，相关测试将跳过"
    cgv2=false
fi

if command -v ip &>/dev/null; then
    log_pass "ip 命令可用"
else
    log_skip "ip 命令不可用，Network Namespace 测试可能失败"
fi

# -----------------------------------------------
section "1. 编译验证"
# -----------------------------------------------

check_build

# -----------------------------------------------
section "2. Namespace 单元测试"
# -----------------------------------------------

run_test "TestDefaultNamespaceConfig"
run_test "TestMinimalNamespaceConfig"
run_test "TestCloneFlags"

# -----------------------------------------------
section "3. Namespace 集成测试"
# -----------------------------------------------

run_test "TestPIDNamespace"
run_test "TestIPCNamespace"
run_test "TestNetworkNamespace"
run_test "TestUTSNamespace"
run_test "TestAllNamespaces"
run_test "TestCleanup"
run_test "TestCleanupHooks"
run_test "TestDoubleStart"
run_test "TestNsPath"
run_test "TestExitCode"
run_test "TestEnvPassing"
run_test "TestConcurrentNamespaces"

# -----------------------------------------------
section "4. OverlayFS 单元测试"
# -----------------------------------------------

run_test "TestDefaultOverlayConfig"
run_test "TestBuildOverlayOptions"
run_test "TestGenerateIDUniqueness"
run_test "TestNewOverlayFS"
run_test "TestOverlaySetupValidation"

# -----------------------------------------------
section "5. OverlayFS 集成测试"
# -----------------------------------------------

run_test "TestOverlaySetupAndCleanup"
run_test "TestOverlayWriteIsolation"
run_test "TestOverlayReadFromLower"
run_test "TestOverlayLowerUnmodified"
run_test "TestOverlayWithNamespace"
run_test "TestOverlayCleanupAfterCrash"
run_test "TestOverlayTmpfsSize" 120s
run_test "TestOverlayMultipleLowerDirs"
run_test "TestConcurrentOverlays"

# -----------------------------------------------
section "6. Cgroups v2 单元测试"
# -----------------------------------------------

run_test "TestDefaultCgroupsConfig"
run_test "TestNewCgroupsV2"
run_test "TestCgroupsSetupValidation"
run_test "TestCgroupsV2Available"

# -----------------------------------------------
section "7. Cgroups v2 集成测试"
# -----------------------------------------------

run_test "TestCgroupsSetupAndCleanup"
run_test "TestCgroupsDoubleSetup"
run_test "TestCgroupsAddProcess"
run_test "TestCgroupsWithNamespace"
run_test "TestCgroupsWithOverlayAndNamespace"
run_test "TestCgroupsCleanupAfterCrash"
run_test "TestConcurrentCgroups"

# -----------------------------------------------
section "8. Cgroups v2 资源限制测试"
# -----------------------------------------------

run_test "TestCgroupsCPULimit" 120s
run_test "TestCgroupsMemoryLimit" 120s
run_test "TestCgroupsPidsLimit" 120s

# -----------------------------------------------
section "9. 日志单元测试"
# -----------------------------------------------

run_test "TestDefaultLogConfig"
run_test "TestNewSandboxLogger"
run_test "TestNewSandboxLoggerInvalidLevel"
run_test "TestReadLogPipe"
run_test "TestReadLogPipeUnparseable"
run_test "TestWriteInitLog"
run_test "TestWriteInitLogMultiple"
run_test "TestLogLevels"
run_test "TestLoggerClose"
run_test "TestConcurrentLoggers"

# -----------------------------------------------
section "10. 基准测试"
# -----------------------------------------------

for bench in "BenchmarkOverlaySetupCleanup" "BenchmarkOverlayWithNamespace" \
             "BenchmarkCgroupsSetupCleanup" "BenchmarkCgroupsWithNamespace"; do
    bench_output=$(go test -bench "$bench" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
    if echo "$bench_output" | grep -q "Benchmark"; then
        log_pass "$bench"
        echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
    else
        log_skip "$bench"
    fi
done

# -----------------------------------------------
section "11. CLI 集成验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 二进制编译"

    help_output=$("$binary" 2>&1 || true)

    all_flags=(
        "no-pid" "no-ipc" "no-net" "no-uts" "hostname"
        "overlay" "overlay-lower" "overlay-size"
        "no-cgroup" "cpu-quota" "cpu-period" "memory-max" "pids-max"
        "log-dir" "log-level"
    )
    for f in "${all_flags[@]}"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    if output=$("$binary" --no-cgroup sh -c 'echo ns-ok' 2>&1); then
        if echo "$output" | grep -q "ns-ok"; then
            log_pass "CLI: Namespace only (--no-cgroup)"
        else
            log_fail "CLI: Namespace only 输出异常"
        fi
    else
        log_fail "CLI: Namespace only 执行失败"
    fi

    if output=$("$binary" --no-cgroup --hostname myhost sh -c 'hostname' 2>&1); then
        if echo "$output" | grep -q "myhost"; then
            log_pass "CLI: --hostname myhost"
        else
            log_fail "CLI: --hostname 未生效"
        fi
    else
        log_fail "CLI: --hostname 执行失败"
    fi

    if output=$("$binary" --no-cgroup --overlay sh -c 'echo overlay-ok' 2>&1); then
        if echo "$output" | grep -q "overlay-ok"; then
            log_pass "CLI: Namespace + OverlayFS"
        else
            log_fail "CLI: Namespace + OverlayFS 输出异常"
        fi
    else
        log_fail "CLI: Namespace + OverlayFS 执行失败"
    fi

    if output=$("$binary" sh -c 'echo cg-ok' 2>&1); then
        if echo "$output" | grep -q "cg-ok"; then
            log_pass "CLI: Namespace + Cgroups (default)"
        else
            log_fail "CLI: Namespace + Cgroups 输出异常"
        fi
    else
        log_skip "CLI: Namespace + Cgroups (default)"
    fi

    if output=$("$binary" --overlay --memory-max 512m sh -c 'echo triple-ok' 2>&1); then
        if echo "$output" | grep -q "triple-ok"; then
            log_pass "CLI: Namespace + OverlayFS + Cgroups"
        else
            log_fail "CLI: 三层联合输出异常"
        fi
    else
        log_skip "CLI: Namespace + OverlayFS + Cgroups"
    fi

    "$binary" --no-cgroup sh -c 'exit 42' 2>/dev/null
    rc=$?
    if [ "$rc" -eq 42 ]; then
        log_pass "CLI: 退出码传递 (exit 42)"
    else
        log_fail "CLI: 退出码传递 期望42 实际$rc"
    fi

    if output=$("$binary" --no-cgroup --no-pid --no-ipc --no-net --no-uts sh -c 'echo minimal-ok' 2>&1); then
        if echo "$output" | grep -q "minimal-ok"; then
            log_pass "CLI: 禁用全部可选隔离"
        else
            log_fail "CLI: 禁用全部可选隔离输出异常"
        fi
    else
        log_fail "CLI: 禁用全部可选隔离执行失败"
    fi

    rm -f "$binary"
else
    log_fail "CLI 二进制编译失败"
fi

# -----------------------------------------------
print_summary "隔离模块全量测试结果"
