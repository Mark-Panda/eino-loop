#!/bin/bash
# 飞书配置连通性校验脚本
# 使用方法: bash scripts/check-feishu.sh

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "=========================================="
echo "  飞书配置连通性校验"
echo "=========================================="
echo ""

# 1. 检查 lark-cli 是否安装
echo -n "1. 检查 lark-cli 是否安装... "
if command -v lark-cli &> /dev/null; then
    echo -e "${GREEN}✓ 已安装${NC}"
    lark-cli --version 2>/dev/null || echo "   版本: 未知"
else
    echo -e "${RED}✗ 未安装${NC}"
    echo "   请安装: brew install lark-cli 或参考 https://github.com/larksuite/cli"
    exit 1
fi
echo ""

# 2. 检查授权状态
echo -n "2. 检查飞书授权状态... "
AUTH_OUTPUT=$(lark-cli auth status 2>&1)
TOKEN_STATUS=$(echo "$AUTH_OUTPUT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('tokenStatus','unknown'))" 2>/dev/null || echo "unknown")
USER_NAME=$(echo "$AUTH_OUTPUT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('userName','未知'))" 2>/dev/null || echo "未知")

if [ "$TOKEN_STATUS" = "valid" ]; then
    echo -e "${GREEN}✓ 已授权 (用户: $USER_NAME)${NC}"
else
    echo -e "${YELLOW}⚠ 未授权或授权已过期 (状态: $TOKEN_STATUS)${NC}"
    echo "   请执行: lark-cli auth login --domain wiki,contact --recommend"
    echo ""
    read -p "   是否现在授权? (y/n) " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        lark-cli auth login --domain wiki,contact --recommend
    else
        echo "   跳过授权，部分检查可能失败"
    fi
fi
echo ""

# 3. 显示当前用户信息
echo "3. 当前用户信息..."
echo "   ----------------------------------------"
echo "   用户名: $USER_NAME"
USER_OPEN_ID=$(echo "$AUTH_OUTPUT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('userOpenId','未知'))" 2>/dev/null || echo "未知")
EXPIRES_AT=$(echo "$AUTH_OUTPUT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('expiresAt','未知'))" 2>/dev/null || echo "未知")
echo "   OpenID: $USER_OPEN_ID"
echo "   授权过期: $EXPIRES_AT"
echo ""

# 4. 获取用户的 Wiki 空间列表
echo "4. 获取用户的 Wiki 空间列表..."
echo "   ----------------------------------------"
WIKI_SPACES=$(lark-cli api GET "/open-apis/wiki/v2/spaces" --params '{"page_size":50}' --as user 2>&1)
SPACE_COUNT=$(echo "$WIKI_SPACES" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('data',{}).get('items',[])))" 2>/dev/null || echo "0")

if [ "$SPACE_COUNT" != "0" ]; then
    echo -e "   ${GREEN}找到 $SPACE_COUNT 个知识空间:${NC}"
    echo ""
    echo "$WIKI_SPACES" | python3 -c "
import json, sys
data = json.load(sys.stdin)
items = data.get('data', {}).get('items', [])
for i, space in enumerate(items, 1):
    name = space.get('name', '未命名')
    space_id = space.get('space_id', '未知')
    desc = space.get('description', '')[:30]
    print(f'   [{i}] {name}')
    print(f'       空间ID: {space_id}')
    if desc:
        print(f'       描述: {desc}')
    print()
" 2>/dev/null || echo "   解析失败"
else
    echo -e "   ${YELLOW}未找到知识空间${NC}"
    echo "   可以使用个人知识库，请执行:"
    echo "   lark-cli api GET '/open-apis/wiki/v2/spaces' --params '{\"page_size\":50}' --as user"
fi
echo ""

# 5. 获取个人知识库
echo -n "5. 获取个人知识库... "
MY_LIBRARY=$(lark-cli api GET "/open-apis/wiki/v2/spaces" --params '{"page_size":50}' --as user 2>&1)
MY_LIB_ID=$(echo "$MY_LIBRARY" | python3 -c "
import json, sys
data = json.load(sys.stdin)
items = data.get('data', {}).get('items', [])
# 查找个人知识库（通常名称包含用户名或 '个人'）
for item in items:
    name = item.get('name', '')
    if '个人' in name or 'my' in name.lower():
        print(item['space_id'])
        sys.exit(0)
# 如果没有找到，使用第一个空间
if items:
    print(items[0]['space_id'])
else:
    print('无')
" 2>/dev/null || echo "获取失败")

if [ "$MY_LIB_ID" != "无" ] && [ "$MY_LIB_ID" != "获取失败" ]; then
    echo -e "${GREEN}✓ 可用空间ID: $MY_LIB_ID${NC}"
else
    echo -e "${YELLOW}⚠ 未找到可用空间${NC}"
fi
echo ""

# 6. 测试创建文档
echo "6. 测试创建飞书文档..."
echo "   ----------------------------------------"

# 确定使用哪个空间
TEST_SPACE=""
if [ "$MY_LIB_ID" != "无" ] && [ "$MY_LIB_ID" != "获取失败" ]; then
    TEST_SPACE="$MY_LIB_ID"
    echo "   使用个人知识库: $TEST_SPACE"
elif [ "$SPACE_COUNT" != "0" ]; then
    TEST_SPACE=$(echo "$WIKI_SPACES" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['data']['items'][0]['space_id'])" 2>/dev/null)
    echo "   使用第一个知识空间: $TEST_SPACE"
fi

if [ -n "$TEST_SPACE" ]; then
    # 创建测试文档
    TEST_TITLE="eino-loop 连通性测试 $(date '+%Y-%m-%d %H:%M:%S')"

    CREATE_RESULT=$(echo "# 连通性测试

这是一个自动创建的测试文档，验证飞书配置是否正常。

创建时间: $(date)" | lark-cli docs +create \
        --title "$TEST_TITLE" \
        --markdown - \
        --wiki-space "$TEST_SPACE" \
        --as user 2>&1)

    if echo "$CREATE_RESULT" | grep -q "doc_url"; then
        DOC_URL=$(echo "$CREATE_RESULT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('data',{}).get('doc_url',''))" 2>/dev/null)
        echo -e "   ${GREEN}✓ 文档创建成功${NC}"
        echo "   文档链接: $DOC_URL"
        echo ""
        echo "   请检查飞书中是否能看到此文档"
    else
        echo -e "   ${RED}✗ 文档创建失败${NC}"
        echo "   错误信息: $CREATE_RESULT" | head -5
    fi
else
    echo -e "   ${YELLOW}⚠ 无可用知识空间，跳过创建测试${NC}"
fi
echo ""

# 7. 测试消息发送
echo -n "7. 测试飞书消息发送... "
# 从 .env 读取 chat_id
CHAT_ID=""
if [ -f ".env" ]; then
    CHAT_ID=$(grep "EINO_LOOP_FEISHU_CHAT_ID" .env | cut -d'=' -f2 | tr -d '"' | tr -d "'")
fi

if [ -n "$CHAT_ID" ]; then
    MSG_RESULT=$(lark-cli message send \
        --chat-id "$CHAT_ID" \
        --type text \
        --content "🔧 eino-loop 飞书配置连通性测试" \
        2>&1)
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 消息发送成功${NC}"
    else
        echo -e "${RED}✗ 消息发送失败${NC}"
        echo "   错误: $MSG_RESULT" | head -3
    fi
else
    echo -e "${YELLOW}⚠ 未配置 EINO_LOOP_FEISHU_CHAT_ID，跳过${NC}"
fi
echo ""

# 总结
echo "=========================================="
echo "  校验完成"
echo "=========================================="
echo ""

# 输出推荐的 .env 配置
echo "推荐的 .env 配置:"
echo "----------------------------------------"
if [ -n "$MY_LIB_ID" ] && [ "$MY_LIB_ID" != "无" ]; then
    echo "EINO_LOOP_FEISHU_DOC_SPACE=$MY_LIB_ID  # 个人知识库"
elif [ -n "$TEST_SPACE" ]; then
    echo "EINO_LOOP_FEISHU_DOC_SPACE=$TEST_SPACE"
fi
echo "EINO_LOOP_FEISHU_ENABLED=true"
echo "EINO_LOOP_FEISHU_CLI_PATH=lark-cli"
if [ -n "$CHAT_ID" ]; then
    echo "EINO_LOOP_FEISHU_CHAT_ID=$CHAT_ID"
else
    echo "# EINO_LOOP_FEISHU_CHAT_ID=<你的飞书群聊ID>"
fi
echo "----------------------------------------"
echo ""
echo "获取飞书群聊ID的方法:"
echo "  1. 在飞书群设置中查看群号"
echo "  2. 或执行: lark-cli api GET '/open-apis/im/v1/chats' --as user"
