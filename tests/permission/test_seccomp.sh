#!/bin/bash
# test_seccomp.sh - Seccomp-BPF 权限控制测试
# 用法: sudo bash tests/permission/test_seccomp.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  Seccomp-BPF 权限控制测试"
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
section "2. Seccomp 单元测试（不需要 root）"
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
section "3. Seccomp 集成测试（需要 root）"
# -----------------------------------------------

run_test "TestSeccompBlockedSyscall"
run_test "TestSeccompAllowedSyscall"
run_test "TestSeccompWithNamespace"
run_test "TestSeccompLogMode"
run_test "TestSeccompExitCode"
run_test "TestConcurrentSeccomp"

# -----------------------------------------------
print_summary "Seccomp 测试结果"
