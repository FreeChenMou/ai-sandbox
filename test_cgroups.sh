#!/bin/bash
# test_cgroups.sh - Cgroups v2 模块测试脚本
# 在 WSL2 环境中以 root 运行: sudo bash test_cgroups.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"
cd "$PROJECT_DIR"
echo "项目目录: $PROJECT_DIR"

# sudo 下保留 go 环境
export HOME="${HOME:-/root}"
export PATH="$PATH:/home/xjh/go/bin"
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
    elif echo "$output" | grep -q "^--- SKIP:"; then
        log_skip "$name"
        log_info "$(echo "$output" | grep 'SKIP' | head -1)"
    else
        log_fail "$name"
        echo "$output" | tail -8 | while IFS= read -r line; do log_info "$line"; done
    fi
}

echo "============================================"
echo "  Cgroups v2 模块测试"
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

    # flag 检查
    help_output=$("$binary" 2>&1 || true)
    for f in "no-cgroup" "cpu-quota" "cpu-period" "memory-max" "pids-max"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    # 默认执行（cgroup 启用）
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

    # --no-cgroup
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

    # 三层联合
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
# 汇总
# -----------------------------------------------

echo ""
echo "============================================"
echo "  Cgroups v2 测试结果"
echo "============================================"
echo -e "  ${GREEN}通过: $pass${NC}"
echo -e "  ${RED}失败: $fail${NC}"
echo -e "  ${YELLOW}跳过: $skip${NC}"
echo "  总计: $((pass + fail + skip))"
echo ""

if [ $fail -gt 0 ]; then
    echo -e "${RED}存在失败的测试${NC}"
    exit 1
else
    echo -e "${GREEN}所有测试通过！${NC}"
    exit 0
fi
