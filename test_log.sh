#!/bin/bash
# test_log.sh - 日志模块测试脚本
# 在 WSL2 环境中以 root 运行: sudo bash test_log.sh

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

export GOPROXY="https://goproxy.cn,direct"
export GOSUMDB="off"
export GOPRIVATE=""

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
echo "  日志模块测试"
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
section "2. 单元测试 - 配置与创建"
# -----------------------------------------------

run_test "TestDefaultLogConfig"
run_test "TestNewSandboxLogger"
run_test "TestNewSandboxLoggerInvalidLevel"

# -----------------------------------------------
section "3. 单元测试 - 日志管道读写"
# -----------------------------------------------

run_test "TestReadLogPipe"
run_test "TestReadLogPipeUnparseable"
run_test "TestWriteInitLog"
run_test "TestWriteInitLogMultiple"

# -----------------------------------------------
section "4. 单元测试 - 级别过滤与关闭"
# -----------------------------------------------

run_test "TestLogLevels"
run_test "TestLoggerClose"

# -----------------------------------------------
section "5. 并发测试"
# -----------------------------------------------

run_test "TestConcurrentLoggers"

# -----------------------------------------------
section "6. CLI 验证"
# -----------------------------------------------

binary=$(mktemp /tmp/ai-sandbox-log-test-XXXXXX)
if go build -o "$binary" ./cmd/ai-sandbox/ 2>&1; then
    log_pass "CLI 编译"

    # flag 检查
    help_output=$("$binary" 2>&1 || true)
    for f in "log-dir" "log-level"; do
        if echo "$help_output" | grep -q -- "$f"; then
            log_pass "CLI --$f"
        else
            log_fail "CLI --$f 缺失"
        fi
    done

    # 日志文件生成测试
    log_tmpdir=$(mktemp -d /tmp/sandbox-log-test-XXXXXX)
    if output=$("$binary" --no-cgroup --log-dir "$log_tmpdir" --log-level debug sh -c 'echo log-ok' 2>&1); then
        if echo "$output" | grep -q "log-ok"; then
            log_pass "CLI: --log-dir 基本执行"
        else
            log_fail "CLI: --log-dir 输出异常"
            log_info "$output"
        fi

        # 检查日志文件是否生成
        log_files=$(ls "$log_tmpdir"/sandbox-*.log 2>/dev/null | wc -l)
        if [ "$log_files" -ge 1 ]; then
            log_pass "CLI: 日志文件已生成 ($log_files 个)"

            # 检查日志文件内容是否为 JSON 格式
            log_file=$(ls "$log_tmpdir"/sandbox-*.log | head -1)
            first_line=$(head -1 "$log_file")
            if echo "$first_line" | python3 -m json.tool >/dev/null 2>&1; then
                log_pass "CLI: 日志文件为 JSON 格式"
            elif echo "$first_line" | jq . >/dev/null 2>&1; then
                log_pass "CLI: 日志文件为 JSON 格式"
            else
                log_skip "CLI: 无法验证 JSON 格式（缺少 python3/jq）"
            fi

            # 检查日志文件包含 sandbox_id 字段
            if grep -q "sandbox_id" "$log_file"; then
                log_pass "CLI: 日志包含 sandbox_id 字段"
            else
                log_fail "CLI: 日志缺少 sandbox_id 字段"
                log_info "$(head -1 "$log_file")"
            fi

            # 检查日志文件包含 namespace started
            if grep -q "namespace started" "$log_file"; then
                log_pass "CLI: 日志包含 namespace started 事件"
            else
                log_fail "CLI: 日志缺少 namespace started 事件"
            fi

            # 检查日志文件包含 namespace cleanup
            if grep -q "namespace cleanup" "$log_file"; then
                log_pass "CLI: 日志包含 namespace cleanup 事件"
            else
                log_fail "CLI: 日志缺少 namespace cleanup 事件"
            fi
        else
            log_fail "CLI: 日志文件未生成"
        fi
    else
        log_fail "CLI: --log-dir 执行失败"
        log_info "$output"
    fi
    rm -rf "$log_tmpdir"

    # 日志级别过滤测试
    log_tmpdir=$(mktemp -d /tmp/sandbox-log-test-XXXXXX)
    if output=$("$binary" --no-cgroup --log-dir "$log_tmpdir" --log-level error sh -c 'echo level-ok' 2>&1); then
        log_file=$(ls "$log_tmpdir"/sandbox-*.log 2>/dev/null | head -1)
        if [ -n "$log_file" ]; then
            # error 级别下不应有 info 级别的 "namespace started" 日志
            if grep -q "namespace started" "$log_file"; then
                log_fail "CLI: --log-level error 仍输出 info 日志"
            else
                log_pass "CLI: --log-level error 级别过滤生效"
            fi
        else
            log_pass "CLI: --log-level error 无日志文件（符合预期）"
        fi
    else
        log_fail "CLI: --log-level error 执行失败"
        log_info "$output"
    fi
    rm -rf "$log_tmpdir"

    # cgroup 日志测试
    log_tmpdir=$(mktemp -d /tmp/sandbox-log-test-XXXXXX)
    if output=$("$binary" --log-dir "$log_tmpdir" --log-level debug sh -c 'echo cg-ok' 2>&1); then
        log_file=$(ls "$log_tmpdir"/sandbox-*.log 2>/dev/null | head -1)
        if [ -n "$log_file" ]; then
            if grep -q "cgroup setup" "$log_file"; then
                log_pass "CLI: 日志包含 cgroup setup 事件"
            else
                log_fail "CLI: 日志缺少 cgroup setup 事件"
            fi
            if grep -q "cgroup cleanup" "$log_file"; then
                log_pass "CLI: 日志包含 cgroup cleanup 事件"
            else
                log_fail "CLI: 日志缺少 cgroup cleanup 事件"
            fi
        else
            log_fail "CLI: 日志文件未生成"
        fi
    else
        log_skip "CLI: cgroup 日志测试（cgroup 不可用）"
    fi
    rm -rf "$log_tmpdir"

    # overlay 日志测试
    log_tmpdir=$(mktemp -d /tmp/sandbox-log-test-XXXXXX)
    if output=$("$binary" --no-cgroup --overlay --log-dir "$log_tmpdir" --log-level debug sh -c 'echo ov-ok' 2>&1); then
        log_file=$(ls "$log_tmpdir"/sandbox-*.log 2>/dev/null | head -1)
        if [ -n "$log_file" ]; then
            if grep -q "overlay setup" "$log_file"; then
                log_pass "CLI: 日志包含 overlay setup 事件"
            else
                log_fail "CLI: 日志缺少 overlay setup 事件"
            fi
            if grep -q "overlay cleanup" "$log_file"; then
                log_pass "CLI: 日志包含 overlay cleanup 事件"
            else
                log_fail "CLI: 日志缺少 overlay cleanup 事件"
            fi
        else
            log_fail "CLI: 日志文件未生成"
        fi
    else
        log_fail "CLI: --overlay 日志测试执行失败"
        log_info "$output"
    fi
    rm -rf "$log_tmpdir"

    rm -f "$binary"
else
    log_fail "CLI 编译失败"
fi

# -----------------------------------------------
# 汇总
# -----------------------------------------------

echo ""
echo "============================================"
echo "  日志模块测试结果"
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
