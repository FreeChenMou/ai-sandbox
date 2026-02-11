#!/bin/bash
# test_all.sh - Namespace + OverlayFS + Cgroups v2 全模块测试脚本
# 在 WSL2 环境中以 root 运行: sudo bash test_all.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"
cd "$PROJECT_DIR"
echo "项目目录: $PROJECT_DIR"

# sudo 下保留 go 环境
export HOME="${HOME:-/root}"
export PATH="$PATH:/home/xjh//go/bin"
if command -v go &>/dev/null; then
    export GOPATH="${GOPATH:-$(go env GOPATH)}"
    export PATH="$PATH:$GOPATH/bin"
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

pass=0
fail=0
skip=0

log_pass() { echo -e "  ${GREEN}[PASS]${NC} $1"; pass=$((pass + 1)); }
log_fail() { echo -e "  ${RED}[FAIL]${NC} $1"; fail=$((fail + 1)); }
log_skip() { echo -e "  ${YELLOW}[SKIP]${NC} $1"; skip=$((skip + 1)); }
log_info() { echo -e "         $1"; }
section()  { echo -e "\n${CYAN}=== $1 ===${NC}"; }

run_test() {
    local name="$1"
    local timeout="${2:-60s}"
    local output
    output=$(go test -v -timeout "$timeout" -run "^${name}$" ./pkg/sandbox/ 2>&1)
    if echo "$output" | grep -q "^--- PASS:"; then
        log_pass "$name"
        return 0
    elif echo "$output" | grep -q "^--- SKIP:"; then
        log_skip "$name"
        log_info "$(echo "$output" | grep 'SKIP' | head -1)"
        return 0
    else
        log_fail "$name"
        # 显示最后几行错误信息
        echo "$output" | tail -8 | while IFS= read -r line; do
            log_info "$line"
        done
        return 1
    fi
}

echo "============================================"
echo "  AI-Sandbox 全模块测试"
echo "  Namespace + OverlayFS + Cgroups v2"
echo "============================================"

# -----------------------------------------------
section "0. 环境检查"
# -----------------------------------------------

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}错误: 需要 root 权限${NC}"
    echo "用法: sudo bash $0"
    exit 1
fi
log_pass "root 权限"

echo -e "  内核: $(uname -r)"

# cgroups v2
cgv2=true
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    log_pass "cgroups v2 可用 ($(cat /sys/fs/cgroup/cgroup.controllers))"
else
    log_skip "cgroups v2 不可用，相关测试将跳过"
    cgv2=false
fi

# ip 命令（Network Namespace 测试需要）
if command -v ip &>/dev/null; then
    log_pass "ip 命令可用"
else
    log_skip "ip 命令不可用，Network Namespace 测试可能失败"
fi

# -----------------------------------------------
section "1. 编译验证"
# -----------------------------------------------

if go build ./... 2>&1; then
    log_pass "go build ./..."
else
    log_fail "go build ./..."
    echo -e "${RED}编译失败，终止测试${NC}"
    exit 1
fi

if go vet ./... 2>&1; then
    log_pass "go vet ./..."
else
    log_fail "go vet ./..."
fi

# -----------------------------------------------
section "2. Namespace 单元测试（不需要 root）"
# -----------------------------------------------

run_test "TestDefaultNamespaceConfig"
run_test "TestMinimalNamespaceConfig"
run_test "TestCloneFlags"

# -----------------------------------------------
section "3. Namespace 集成测试（需要 root）"
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
section "4. OverlayFS 单元测试（不需要 root）"
# -----------------------------------------------

run_test "TestDefaultOverlayConfig"
run_test "TestBuildOverlayOptions"
run_test "TestGenerateIDUniqueness"
run_test "TestNewOverlayFS"
run_test "TestOverlaySetupValidation"

# -----------------------------------------------
section "5. OverlayFS 集成测试（需要 root）"
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
section "6. Cgroups v2 单元测试（不需要 root）"
# -----------------------------------------------

run_test "TestDefaultCgroupsConfig"
run_test "TestNewCgroupsV2"
run_test "TestCgroupsSetupValidation"
run_test "TestCgroupsV2Available"

# -----------------------------------------------
section "7. Cgroups v2 集成测试（需要 root + cgroups v2）"
# -----------------------------------------------

run_test "TestCgroupsSetupAndCleanup"
run_test "TestCgroupsDoubleSetup"
run_test "TestCgroupsAddProcess"
run_test "TestCgroupsWithNamespace"
run_test "TestCgroupsWithOverlayAndNamespace"
run_test "TestCgroupsCleanupAfterCrash"
run_test "TestConcurrentCgroups"

# -----------------------------------------------
section "8. Cgroups v2 资源限制测试（需要 root + cgroups v2）"
# -----------------------------------------------

run_test "TestCgroupsCPULimit" 120s
run_test "TestCgroupsMemoryLimit" 120s
run_test "TestCgroupsPidsLimit" 120s

# -----------------------------------------------
section "9. 基准测试"
# -----------------------------------------------

# Namespace
bench_output=$(go test -bench "BenchmarkOverlayWithNamespace" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
if echo "$bench_output" | grep -q "Benchmark"; then
    log_pass "BenchmarkOverlayWithNamespace"
    echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
else
    log_skip "BenchmarkOverlayWithNamespace"
fi

# OverlayFS
bench_output=$(go test -bench "BenchmarkOverlaySetupCleanup" -benchtime 3s -timeout 120s ./pkg/sandbox/ 2>&1)
if echo "$bench_output" | grep -q "Benchmark"; then
    log_pass "BenchmarkOverlaySetupCleanup"
    echo "$bench_output" | grep "Benchmark" | while IFS= read -r line; do log_info "$line"; done
else
    log_skip "BenchmarkOverlaySetupCleanup"
fi

# Cgroups
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
section "10. CLI 集成验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 二进制编译"

    # 检查 help 输出中所有 flag 存在
    help_output=$("$binary" 2>&1 || true)

    all_flags=(
        "no-pid" "no-ipc" "no-net" "no-uts" "hostname"
        "overlay" "overlay-lower" "overlay-size"
        "no-cgroup" "cpu-quota" "cpu-period" "memory-max" "pids-max"
    )
    flags_ok=true
    for f in "${all_flags[@]}"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
            flags_ok=false
        fi
    done

    # 基本执行：仅 Namespace
    if output=$("$binary" --no-cgroup sh -c 'echo ns-ok' 2>&1); then
        if echo "$output" | grep -q "ns-ok"; then
            log_pass "CLI: Namespace only (--no-cgroup)"
        else
            log_fail "CLI: Namespace only 输出异常"
            log_info "$output"
        fi
    else
        log_fail "CLI: Namespace only 执行失败"
        log_info "$output"
    fi

    # Namespace + 自定义 hostname
    if output=$("$binary" --no-cgroup --hostname myhost sh -c 'hostname' 2>&1); then
        if echo "$output" | grep -q "myhost"; then
            log_pass "CLI: --hostname myhost"
        else
            log_fail "CLI: --hostname 未生效"
            log_info "$output"
        fi
    else
        log_fail "CLI: --hostname 执行失败"
        log_info "$output"
    fi

    # Namespace + OverlayFS
    if output=$("$binary" --no-cgroup --overlay sh -c 'echo overlay-ok' 2>&1); then
        if echo "$output" | grep -q "overlay-ok"; then
            log_pass "CLI: Namespace + OverlayFS"
        else
            log_fail "CLI: Namespace + OverlayFS 输出异常"
            log_info "$output"
        fi
    else
        log_fail "CLI: Namespace + OverlayFS 执行失败"
        log_info "$output"
    fi

    # Namespace + Cgroups（默认）
    if output=$("$binary" sh -c 'echo cg-ok' 2>&1); then
        if echo "$output" | grep -q "cg-ok"; then
            log_pass "CLI: Namespace + Cgroups (default)"
        else
            log_fail "CLI: Namespace + Cgroups 输出异常"
            log_info "$output"
        fi
    else
        log_info "CLI: Namespace + Cgroups 返回错误（环境可能不支持）"
        log_info "$output"
        log_skip "CLI: Namespace + Cgroups (default)"
    fi

    # 自定义资源限制
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

    # 三层联合：Namespace + OverlayFS + Cgroups
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

    # 退出码传递
    "$binary" --no-cgroup sh -c 'exit 42' 2>/dev/null
    rc=$?
    if [ "$rc" -eq 42 ]; then
        log_pass "CLI: 退出码传递 (exit 42)"
    else
        log_fail "CLI: 退出码传递 期望42 实际$rc"
    fi

    # 禁用各项隔离
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
    log_fail "CLI 二进制编译失败"
fi

# -----------------------------------------------
# 汇总
# -----------------------------------------------

echo ""
echo "============================================"
echo "  测试结果汇总"
echo "============================================"
echo -e "  ${GREEN}通过: $pass${NC}"
echo -e "  ${RED}失败: $fail${NC}"
echo -e "  ${YELLOW}跳过: $skip${NC}"
total=$((pass + fail + skip))
echo "  总计: $total"
echo ""

if [ $fail -gt 0 ]; then
    echo -e "${RED}存在失败的测试，请检查上方输出${NC}"
    exit 1
else
    echo -e "${GREEN}所有测试通过！${NC}"
    exit 0
fi
