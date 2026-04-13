#!/bin/bash
# ================================================
# GobiVC 一键部署脚本 (阿里云 ECS / Ubuntu)
# 用法: curl -fsSL <script_url> | bash
# 或者: bash deploy.sh
# ================================================

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[GobiVC]${NC} $1"; }
warn() { echo -e "${YELLOW}[警告]${NC} $1"; }
err()  { echo -e "${RED}[错误]${NC} $1"; exit 1; }

# ================================================
# 1. 检查环境
# ================================================
log "========================================="
log "  GobiVC 行业研究报告系统 - 一键部署"
log "========================================="
echo ""

if [ "$(id -u)" -ne 0 ]; then
    err "请使用 root 用户运行此脚本: sudo bash deploy.sh"
fi

# ================================================
# 2. 收集配置信息
# ================================================
echo ""
log "请提供以下配置信息："
echo ""

echo "  支持的 AI 服务商："
echo "  1) MiniMax  (默认，api.minimax.chat)"
echo "  2) DeepSeek (api.deepseek.com)"
echo "  3) Claude   (api.anthropic.com)"
echo "  4) 其他 OpenAI 兼容接口"
echo ""
read -r -p "$(echo -e ${YELLOW}'[1/5]'${NC}) 选择 AI 服务商 [1-4] (直接回车选1): " PROVIDER_CHOICE
PROVIDER_CHOICE=${PROVIDER_CHOICE:-1}

case "$PROVIDER_CHOICE" in
    1)
        AI_PROVIDER="openai"
        DEFAULT_MODEL="MiniMax-Text-01"
        DEFAULT_BASE_URL="https://api.minimax.chat/v1"
        PROVIDER_NAME="MiniMax"
        ;;
    2)
        AI_PROVIDER="openai"
        DEFAULT_MODEL="deepseek-chat"
        DEFAULT_BASE_URL="https://api.deepseek.com/v1"
        PROVIDER_NAME="DeepSeek"
        ;;
    3)
        AI_PROVIDER="claude"
        DEFAULT_MODEL="claude-sonnet-4-20250514"
        DEFAULT_BASE_URL="https://api.anthropic.com"
        PROVIDER_NAME="Claude"
        ;;
    4)
        AI_PROVIDER="openai"
        DEFAULT_MODEL=""
        DEFAULT_BASE_URL=""
        PROVIDER_NAME="自定义"
        ;;
    *)
        err "无效选择"
        ;;
esac

read -r -p "$(echo -e ${YELLOW}'[2/5]'${NC}) 请输入 ${PROVIDER_NAME} API Key: " AI_API_KEY
if [ -z "$AI_API_KEY" ]; then
    err "API Key 不能为空"
fi

read -r -p "$(echo -e ${YELLOW}'[3/5]'${NC}) 请输入模型名称 (直接回车使用 ${DEFAULT_MODEL:-'需要填写'}): " AI_MODEL
AI_MODEL=${AI_MODEL:-$DEFAULT_MODEL}
if [ -z "$AI_MODEL" ]; then
    err "模型名称不能为空"
fi

if [ "$PROVIDER_CHOICE" = "4" ]; then
    read -r -p "$(echo -e ${YELLOW}'[3.5/5]'${NC}) 请输入 API Base URL (如 https://api.example.com/v1): " AI_BASE_URL
    if [ -z "$AI_BASE_URL" ]; then
        err "Base URL 不能为空"
    fi
else
    AI_BASE_URL="$DEFAULT_BASE_URL"
fi

read -r -p "$(echo -e ${YELLOW}'[4/5]'${NC}) 请输入你的域名 (没有则留空，使用IP访问): " DOMAIN_NAME

# 自动生成 API Token
API_TOKEN=$(openssl rand -hex 32)

echo ""
log "配置确认："
echo "  - 服务商:   ${PROVIDER_NAME}"
echo "  - API Key:  ${AI_API_KEY:0:12}..."
echo "  - 模型:     ${AI_MODEL}"
echo "  - Base URL: ${AI_BASE_URL}"
echo "  - 域名:     ${DOMAIN_NAME:-'无 (使用 IP 访问)'}"
echo "  - API Token: ${API_TOKEN:0:16}..."
echo ""
read -r -p "确认开始部署? (y/N): " CONFIRM
if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    log "已取消"
    exit 0
fi

# ================================================
# 3. 安装系统依赖
# ================================================
echo ""
log "[1/6] 安装系统依赖..."
apt-get update -qq
apt-get install -y -qq git curl wget nginx certbot python3-certbot-nginx > /dev/null 2>&1
log "系统依赖安装完成"

# ================================================
# 4. 安装 Docker
# ================================================
log "[2/6] 安装 Docker..."
if command -v docker &> /dev/null; then
    log "Docker 已安装，跳过"
else
    curl -fsSL https://get.docker.com | bash > /dev/null 2>&1
    systemctl enable --now docker
    log "Docker 安装完成"
fi

# 安装 docker compose plugin
if ! docker compose version &> /dev/null; then
    apt-get install -y -qq docker-compose-plugin > /dev/null 2>&1
fi

# ================================================
# 5. 克隆项目 & 配置
# ================================================
log "[3/6] 克隆项目代码..."
INSTALL_DIR="/opt/gobivc"

if [ -d "$INSTALL_DIR" ]; then
    warn "目录 $INSTALL_DIR 已存在，备份旧数据..."
    if [ -d "$INSTALL_DIR/data" ]; then
        cp -r "$INSTALL_DIR/data" "/tmp/gobivc-data-backup-$(date +%F-%H%M%S)"
    fi
    rm -rf "$INSTALL_DIR"
fi

git clone https://github.com/zinsserzhang/gobivc.git "$INSTALL_DIR"
cd "$INSTALL_DIR"
git checkout claude/industry-report-generator-qIVST

log "[4/6] 写入配置文件..."
cat > "$INSTALL_DIR/.env" << ENVEOF
AI_PROVIDER=${AI_PROVIDER}
AI_API_KEY=${AI_API_KEY}
AI_MODEL=${AI_MODEL}
AI_BASE_URL=${AI_BASE_URL}
API_TOKEN=${API_TOKEN}
PORT=8080
DATA_DIR=/app/data
ENVEOF

# 恢复旧数据
if [ -d "/tmp/gobivc-data-backup-"* ] 2>/dev/null; then
    LATEST_BACKUP=$(ls -td /tmp/gobivc-data-backup-* | head -1)
    cp -r "$LATEST_BACKUP/"* "$INSTALL_DIR/data/" 2>/dev/null || true
    log "已恢复之前的数据"
fi

# ================================================
# 6. 启动 Docker 容器
# ================================================
log "[5/6] 构建并启动服务..."
cd "$INSTALL_DIR"
docker compose up -d --build

# 等待服务就绪
echo -n "等待服务启动"
for i in $(seq 1 30); do
    if curl -sf http://127.0.0.1:8080/health > /dev/null 2>&1; then
        echo ""
        log "服务已启动"
        break
    fi
    echo -n "."
    sleep 2
done

if ! curl -sf http://127.0.0.1:8080/health > /dev/null 2>&1; then
    echo ""
    warn "服务启动超时，请检查日志: docker compose -f $INSTALL_DIR/docker-compose.yml logs"
fi

# ================================================
# 7. 配置 Nginx 反向代理
# ================================================
log "[6/6] 配置 Nginx 反向代理..."

# 生成 Nginx 配置
if [ -n "$DOMAIN_NAME" ]; then
    SERVER_NAME="$DOMAIN_NAME"
else
    SERVER_NAME="_"
fi

cat > /etc/nginx/sites-available/gobivc << NGINXEOF
server {
    listen 80;
    server_name ${SERVER_NAME};

    client_max_body_size 10M;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # SSE 流式输出支持
        proxy_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;
        proxy_read_timeout 900s;
        proxy_send_timeout 900s;
    }
}
NGINXEOF

# 禁用默认站点，启用 gobivc
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/gobivc /etc/nginx/sites-enabled/gobivc

nginx -t > /dev/null 2>&1 && systemctl reload nginx
log "Nginx 配置完成"

# 配置 HTTPS (有域名时)
if [ -n "$DOMAIN_NAME" ]; then
    log "配置 HTTPS 证书..."
    certbot --nginx -d "$DOMAIN_NAME" --non-interactive --agree-tos --register-unsafely-without-email || {
        warn "HTTPS 证书配置失败，可能域名还未解析到此服务器"
        warn "请确认 DNS 解析后手动运行: certbot --nginx -d $DOMAIN_NAME"
    }
fi

# ================================================
# 8. 配置自动备份
# ================================================
log "配置每日自动备份..."
mkdir -p /opt/gobivc-backups

cat > /etc/cron.daily/gobivc-backup << 'CRONEOF'
#!/bin/bash
BACKUP_DIR="/opt/gobivc-backups"
DATE=$(date +%F)
cp /opt/gobivc/data/gobivc.db "$BACKUP_DIR/gobivc-$DATE.db" 2>/dev/null
# 保留最近 30 天的备份
find "$BACKUP_DIR" -name "*.db" -mtime +30 -delete
CRONEOF
chmod +x /etc/cron.daily/gobivc-backup
log "自动备份已配置 (每日备份，保留30天)"

# ================================================
# 9. 配置 systemd 开机自启
# ================================================
cat > /etc/systemd/system/gobivc.service << SVCEOF
[Unit]
Description=GobiVC Industry Report Generator
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/gobivc
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=300

[Install]
WantedBy=multi-user.target
SVCEOF

systemctl daemon-reload
systemctl enable gobivc > /dev/null 2>&1
log "开机自启已配置"

# ================================================
# 完成
# ================================================
echo ""
echo ""
log "========================================="
log "  部署完成!"
log "========================================="
echo ""

PUBLIC_IP=$(curl -sf http://ifconfig.me 2>/dev/null || echo "你的服务器IP")

if [ -n "$DOMAIN_NAME" ]; then
    echo -e "  访问地址:  ${GREEN}https://${DOMAIN_NAME}${NC}"
else
    echo -e "  访问地址:  ${GREEN}http://${PUBLIC_IP}${NC}"
fi

echo ""
echo -e "  ${YELLOW}重要: 请保存以下信息${NC}"
echo -e "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "  API Token:  ${GREEN}${API_TOKEN}${NC}"
echo -e "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "  首次使用步骤："
echo "  1. 浏览器打开上方地址"
echo "  2. 点击左侧「API Token」按钮"
echo "  3. 粘贴上面的 API Token"
echo "  4. 开始生成行业研究报告"
echo ""
echo "  常用命令："
echo "  查看日志:   cd /opt/gobivc && docker compose logs -f"
echo "  重启服务:   cd /opt/gobivc && docker compose restart"
echo "  更新版本:   cd /opt/gobivc && git pull && docker compose up -d --build"
echo ""

# 保存部署信息到文件
cat > /opt/gobivc/DEPLOY_INFO << INFOEOF
部署时间: $(date)
访问地址: ${DOMAIN_NAME:-$PUBLIC_IP}
API Token: ${API_TOKEN}
AI 服务商: ${PROVIDER_NAME}
AI 模型:   ${AI_MODEL}
API 地址:  ${AI_BASE_URL}
数据目录: /opt/gobivc/data
备份目录: /opt/gobivc-backups
INFOEOF

log "部署信息已保存到 /opt/gobivc/DEPLOY_INFO"
