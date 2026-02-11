#!/bin/bash
# test_all.sh - 权限控制模块全量测试（Seccomp + PivotRoot）
# 用法: sudo bash tests/permission/test_all.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  AI-Sandbox 权限控制模块全量测试"
echo "  Seccomp-BPF + Pivot Root"
echo "============================================"

# -----------------------------------------------
section "0. 环境检查"
# -----------------------------------------------

check_root

# -----------------------------------------------
section "1. 编译验证"
# -----------------------------------------------

check_build

# -----------------------------------------------
section "2. Seccomp 单元测试"
# -----------------------------------------------

run_test "TestDefaultSeccompConfig"
run_test "TestDefaultBlockedSyscallsNotEmpty"
run_test "TestDefaultBlockedSocketFamiliesNotEmpty"
run_test "TestResolveBlocklist"
run_test "TestResolveBlocklistDedup"
run_test "TestResolveBlocklistInvalid"
run_test "TestResolveBlocklistEmpty"
run_test "TestResolveBlocklistAllDefaults"
run_test "TestSyscallMapComplete"
run_test "TestBuildBPFProgramBlockedOnly"
run_test "TestBuildBPFProgramWithSocketFilter"
run_test "TestBuildBPFProgramSocketOnly"
run_test "TestBuildBPFProgramEmpty"
run_test "TestBuildBPFProgramLogDenied"
run_test "TestSeccompAvailable"
run_test "TestApplySeccompNil"
run_test "TestApplySeccompEmpty"

# -----------------------------------------------
section "3. Seccomp 集成测试"
# -----------------------------------------------

run_test "TestSeccompBlockedSyscall"
run_test "TestSeccompAllowedSyscall"
run_test "TestSeccompWithNamespace"
run_test "TestSeccompLogMode"
run_test "TestSeccompExitCode"
run_test "TestConcurrentSeccomp"

# -----------------------------------------------
section "4. PivotRoot 单元测试"
# -----------------------------------------------

run_test "TestDefaultPivotRootConfig"

# -----------------------------------------------
section "5. PivotRoot 集成测试"
# -----------------------------------------------

run_test "TestPivotRootWithOverlay"
run_test "TestPivotRootEscape"
run_test "TestPivotRootProcVisible"
run_test "TestPivotRootDevAvailable"
run_test "TestPivotRootWriteIsolation"
run_test "TestPivotRootWithSeccomp"
run_test "TestConcurrentPivotRoot"

# -----------------------------------------------
section "6. CLI 权限控制参数验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 二进制编译"

    help_output=$("$binary" 2>&1 || true)

    # 验证新增 CLI 参数存在
    new_flags=("no-seccomp" "seccomp-log" "no-pivot-root" "rootfs" "no-overlay")
    for f in "${new_flags[@]}"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    # 验证 seccomp + pivot_root 默认开启下正常命令能执行
    if output=$("$binary" --no-cgroup sh -c 'echo perm-ok' 2>&1); then
        if echo "$output" | grep -q "perm-ok"; then
            log_pass "CLI: 默认权限控制（seccomp + pivot_root + overlay）"
        else
            log_fail "CLI: 默认权限控制输出异常"
        fi
    else
        log_fail "CLI: 默认权限控制执行失败"
    fi

    # 验证 --no-seccomp 可禁用
    if output=$("$binary" --no-cgroup --no-seccomp sh -c 'echo nosec-ok' 2>&1); then
        if echo "$output" | grep -q "nosec-ok"; then
            log_pass "CLI: --no-seccomp"
        else
            log_fail "CLI: --no-seccomp 输出异常"
        fi
    else
        log_fail "CLI: --no-seccomp 执行失败"
    fi

    # 验证 --no-pivot-root 可禁用
    if output=$("$binary" --no-cgroup --no-pivot-root sh -c 'echo nopivot-ok' 2>&1); then
        if echo "$output" | grep -q "nopivot-ok"; then
            log_pass "CLI: --no-pivot-root"
        else
            log_fail "CLI: --no-pivot-root 输出异常"
        fi
    else
        log_fail "CLI: --no-pivot-root 执行失败"
    fi

    rm -f "$binary"
else
    log_fail "CLI 二进制编译失败"
fi

# -----------------------------------------------
print_summary "权限控制模块全量测试结果"
