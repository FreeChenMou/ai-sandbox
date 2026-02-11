#!/bin/bash
# test_namespace.sh - Namespace 模块测试脚本
# 在 WSL2 环境中以 root 运行: sudo bash test_namespace.sh

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
echo "  Namespace 模块测试"
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

if command -v ip &>/dev/null; then
    log_pass "ip 命令可用"
else
    log_skip "ip 命令不可用，NetworkNamespace 测试可能失败"
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
# 汇总
# -----------------------------------------------

echo ""
echo "============================================"
echo "  Namespace 测试结果"
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
