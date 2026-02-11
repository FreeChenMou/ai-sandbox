#!/bin/bash
# run_all.sh - 全模块测试入口
# 按模块依次运行所有测试脚本
# 用法: sudo bash tests/run_all.sh [模块名...]
#
# 示例:
#   sudo bash tests/run_all.sh                 # 运行所有已实现模块
#   sudo bash tests/run_all.sh isolation       # 仅运行隔离模块
#   sudo bash tests/run_all.sh isolation permission  # 运行指定模块

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# 所有模块（按开发阶段排列）
ALL_MODULES=(
    "isolation"       # 阶段1：基础隔离（Namespace + OverlayFS + Cgroups + Log）
    "permission"      # 阶段2：权限控制（Seccomp + Chroot + Proxy）
    "snapshot"        # 阶段3：快照恢复（CRIU + 热启动）
    "orchestration"   # 阶段4：Workflow编排（DAG + 调度 + 故障恢复）
    "enhancement"     # 阶段5：可选增强（eBPF + 异常检测）
)

# 确定要运行的模块
if [ $# -gt 0 ]; then
    MODULES=("$@")
else
    MODULES=("${ALL_MODULES[@]}")
fi

echo "============================================"
echo "  AI-Sandbox 全模块测试"
echo "============================================"
echo ""

total_pass=0
total_fail=0
module_results=()

for module in "${MODULES[@]}"; do
    module_dir="$SCRIPT_DIR/$module"
    test_all="$module_dir/test_all.sh"

    if [ ! -d "$module_dir" ]; then
        echo -e "${YELLOW}[SKIP]${NC} $module - 目录不存在"
        module_results+=("$module: SKIP (目录不存在)")
        continue
    fi

    if [ ! -f "$test_all" ]; then
        echo -e "${YELLOW}[SKIP]${NC} $module - 尚未实现 (test_all.sh 不存在)"
        module_results+=("$module: SKIP (未实现)")
        continue
    fi

    echo -e "\n${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}  模块: $module${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"

    if bash "$test_all"; then
        module_results+=("$module: ${GREEN}PASS${NC}")
        total_pass=$((total_pass + 1))
    else
        module_results+=("$module: ${RED}FAIL${NC}")
        total_fail=$((total_fail + 1))
    fi
done

# 汇总
echo ""
echo "============================================"
echo "  全模块测试汇总"
echo "============================================"
for result in "${module_results[@]}"; do
    echo -e "  $result"
done
echo ""
echo -e "  模块通过: ${GREEN}$total_pass${NC}"
echo -e "  模块失败: ${RED}$total_fail${NC}"
echo ""

if [ $total_fail -gt 0 ]; then
    echo -e "${RED}存在失败的模块${NC}"
    exit 1
else
    echo -e "${GREEN}所有模块测试通过！${NC}"
    exit 0
fi
