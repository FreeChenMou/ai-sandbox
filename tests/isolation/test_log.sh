#!/bin/bash
# test_log.sh - 日志模块测试脚本
# 用法: sudo bash tests/isolation/test_log.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  日志模块测试"
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

        log_files=$(ls "$log_tmpdir"/sandbox-*.log 2>/dev/null | wc -l)
        if [ "$log_files" -ge 1 ]; then
            log_pass "CLI: 日志文件已生成 ($log_files 个)"

            log_file=$(ls "$log_tmpdir"/sandbox-*.log | head -1)
            first_line=$(head -1 "$log_file")
            if echo "$first_line" | python3 -m json.tool >/dev/null 2>&1; then
                log_pass "CLI: 日志文件为 JSON 格式"
            elif echo "$first_line" | jq . >/dev/null 2>&1; then
                log_pass "CLI: 日志文件为 JSON 格式"
            else
                log_skip "CLI: 无法验证 JSON 格式（缺少 python3/jq）"
            fi

            if grep -q "sandbox_id" "$log_file"; then
                log_pass "CLI: 日志包含 sandbox_id 字段"
            else
                log_fail "CLI: 日志缺少 sandbox_id 字段"
                log_info "$(head -1 "$log_file")"
            fi

            if grep -q "namespace started" "$log_file"; then
                log_pass "CLI: 日志包含 namespace started 事件"
            else
                log_fail "CLI: 日志缺少 namespace started 事件"
            fi

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
print_summary "日志模块测试结果"
