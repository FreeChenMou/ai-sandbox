#!/bin/bash
# test_pivotroot.sh - Pivot Root 目录禁锢测试
# 用法: sudo bash tests/permission/test_pivotroot.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/../common.sh"

echo "============================================"
echo "  Pivot Root 目录禁锢测试"
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
section "2. PivotRoot 单元测试（不需要 root）"
# -----------------------------------------------

run_test "TestDefaultPivotRootConfig"

# -----------------------------------------------
section "3. PivotRoot 集成测试（需要 root）"
# -----------------------------------------------

run_test "TestPivotRootWithOverlay"
run_test "TestPivotRootEscape"
run_test "TestPivotRootProcVisible"
run_test "TestPivotRootDevAvailable"
run_test "TestPivotRootWriteIsolation"
run_test "TestPivotRootWithSeccomp"
run_test "TestConcurrentPivotRoot"

# -----------------------------------------------
print_summary "Pivot Root 测试结果"
