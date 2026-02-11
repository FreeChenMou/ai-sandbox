#!/bin/bash
# common.sh - 测试脚本公共函数和环境配置
# 被各模块测试脚本 source 引入，不单独运行

# 项目根目录（所有测试脚本通过此变量定位项目）
if [ -z "$PROJECT_DIR" ]; then
    echo "错误: 请在 source common.sh 前设置 PROJECT_DIR"
    exit 1
fi
cd "$PROJECT_DIR"

# sudo 下保留 go 环境
export HOME="${HOME:-/root}"
export PATH="$PATH:/home/xjh/go/bin"
if command -v go &>/dev/null; then
    export GOPATH="${GOPATH:-$(go env GOPATH)}"
    export PATH="$PATH:$GOPATH/bin"
fi

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-off}"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# 计数器
pass=0
fail=0
skip=0

log_pass() { echo -e "  ${GREEN}[PASS]${NC} $1"; pass=$((pass + 1)); }
log_fail() { echo -e "  ${RED}[FAIL]${NC} $1"; fail=$((fail + 1)); }
log_skip() { echo -e "  ${YELLOW}[SKIP]${NC} $1"; skip=$((skip + 1)); }
log_info() { echo -e "         $1"; }
section()  { echo -e "\n${CYAN}=== $1 ===${NC}"; }

# run_test 运行单个 Go 测试用例
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

# check_root 检查 root 权限
check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}错误: 需要 root 权限${NC}"
        echo "用法: sudo bash $0"
        exit 1
    fi
    log_pass "root 权限"
    echo -e "  内核: $(uname -r)"
}

# check_build 编译验证
check_build() {
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
}

# print_summary 打印测试汇总
print_summary() {
    local title="${1:-测试结果}"
    echo ""
    echo "============================================"
    echo "  $title"
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
}
