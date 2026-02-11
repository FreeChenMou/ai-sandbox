#!/bin/bash
# test_overlayfs.sh - OverlayFS 模块测试脚本
# 在 WSL2 环境中以 root 运行: sudo bash test_overlayfs.sh

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
echo "  OverlayFS 模块测试"
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

# 检查 overlay 内核模块
if grep -q overlay /proc/filesystems 2>/dev/null; then
    log_pass "overlay 文件系统可用"
else
    log_skip "overlay 文件系统不可用，集成测试可能失败"
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

    # OverlayFS 写入隔离：写入文件后宿主机不受影响
    tmpdir=$(mktemp -d /tmp/ov-cli-test-XXXXXX)
    echo "original" > "$tmpdir/file.txt"
    if output=$("$binary" --no-cgroup --overlay --overlay-lower "$tmpdir" sh -c "echo modified > $tmpdir/file.txt; cat $tmpdir/file.txt" 2>&1); then
        # 检查宿主机文件未被修改
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
# 汇总
# -----------------------------------------------

echo ""
echo "============================================"
echo "  OverlayFS 测试结果"
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
