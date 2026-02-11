#!/bin/bash
# test_namespace.sh - Namespace 模块测试脚本
# 用法: sudo bash tests/isolation/test_namespace.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  Namespace 模块测试"
echo "============================================"

# -----------------------------------------------
section "0. 环境检查"
# -----------------------------------------------

check_root

if command -v ip &>/dev/null; then
    log_pass "ip 命令可用"
else
    log_skip "ip 命令不可用，NetworkNamespace 测试可能失败"
fi

# -----------------------------------------------
section "1. 编译验证"
# -----------------------------------------------

check_build

# -----------------------------------------------
section "2. 单元测试（不需要 root）"
# -----------------------------------------------

run_test "TestDefaultNamespaceConfig"
run_test "TestMinimalNamespaceConfig"
run_test "TestCloneFlags"

# -----------------------------------------------
section "3. Namespace 隔离测试"
# -----------------------------------------------

run_test "TestPIDNamespace"
run_test "TestIPCNamespace"
run_test "TestNetworkNamespace"
run_test "TestUTSNamespace"
run_test "TestAllNamespaces"

# -----------------------------------------------
section "4. 生命周期测试"
# -----------------------------------------------

run_test "TestCleanup"
run_test "TestCleanupHooks"
run_test "TestDoubleStart"
run_test "TestNsPath"

# -----------------------------------------------
section "5. 功能测试"
# -----------------------------------------------

run_test "TestExitCode"
run_test "TestEnvPassing"

# -----------------------------------------------
section "6. 并发测试"
# -----------------------------------------------

run_test "TestConcurrentNamespaces"

# -----------------------------------------------
section "7. CLI 验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-ns-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 编译"

    # flag 检查
    help_output=$("$binary" 2>&1 || true)
    for f in "no-pid" "no-ipc" "no-net" "no-uts" "hostname"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    # PID 隔离
    if output=$("$binary" --no-cgroup sh -c 'echo pid=$$' 2>&1); then
        if echo "$output" | grep -q "pid=1"; then
            log_pass "CLI: PID Namespace (pid=1)"
        else
            log_fail "CLI: PID Namespace 期望 pid=1"
            log_info "$output"
        fi
    else
        log_fail "CLI: PID Namespace 执行失败"
        log_info "$output"
    fi

    # hostname 隔离
    if output=$("$binary" --no-cgroup --hostname mybox sh -c 'hostname' 2>&1); then
        if echo "$output" | grep -q "mybox"; then
            log_pass "CLI: --hostname mybox"
        else
            log_fail "CLI: --hostname 未生效"
            log_info "$output"
        fi
    else
        log_fail "CLI: --hostname 执行失败"
        log_info "$output"
    fi

    # 退出码传递
    "$binary" --no-cgroup sh -c 'exit 42' 2>/dev/null
    rc=$?
    if [ "$rc" -eq 42 ]; then
        log_pass "CLI: 退出码传递 (exit 42)"
    else
        log_fail "CLI: 退出码传递 期望42 实际$rc"
    fi

    # 禁用全部可选隔离
    if output=$("$binary" --no-cgroup --no-pid --no-ipc --no-net --no-uts sh -c 'echo minimal-ok' 2>&1); then
        if echo "$output" | grep -q "minimal-ok"; then
            log_pass "CLI: 禁用全部可选隔离"
        else
            log_fail "CLI: 禁用全部可选隔离输出异常"
            log_info "$output"
        fi
    else
        log_fail "CLI: 禁用全部可选隔离执行失败"
        log_info "$output"
    fi

    rm -f "$binary"
else
    log_fail "CLI 编译失败"
fi

# -----------------------------------------------
print_summary "Namespace 测试结果"
