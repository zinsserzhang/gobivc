# 云端部署指南

GobiVC 是一个纯 Go 构建的单二进制文件应用，无 CGO 依赖，可在任意云平台快速部署。

## 部署前准备

1. **Anthropic API Key** - 从 https://console.anthropic.com 获取
2. **API Token** (推荐) - 生成一个随机字符串用于保护 API（例如 `openssl rand -hex 32`）
3. **域名** (可选) - 用于 HTTPS 访问

## 环境变量

| 变量 | 必填 | 说明 |
|------|------|------|
| `ANTHROPIC_API_KEY` | 是 | Claude API 密钥 |
| `API_TOKEN` | 推荐 | 设置后所有 `/api/*` 请求需要 `Authorization: Bearer <token>` |
| `ANTHROPIC_MODEL` | 否 | AI 模型名称 |
| `ANTHROPIC_BASE_URL` | 否 | 自定义 API 地址（支持代理） |
| `ALLOWED_ORIGIN` | 否 | CORS 允许的来源域名 |
| `DATA_DIR` | 否 | SQLite 数据目录（默认 `/app/data`） |
| `PORT` | 否 | 监听端口（默认 `8080`） |

---

## 方案一：Docker + VPS (最简单)

适用于阿里云 ECS、腾讯云 CVM、AWS EC2 等 VPS。

```bash
# 1. 在服务器上克隆代码
git clone https://github.com/zinsserzhang/gobivc.git
cd gobivc

# 2. 配置环境变量
cp .env.example .env
# 编辑 .env，填入 ANTHROPIC_API_KEY 和 API_TOKEN

# 3. 启动服务
docker compose up -d

# 4. 查看日志
docker compose logs -f
```

访问 `http://服务器IP:8080`。

建议前面加 Nginx 反向代理并配置 HTTPS (Let's Encrypt)。

---

## 方案二：Kubernetes (推荐生产环境)

适用于阿里云 ACK、腾讯云 TKE、AWS EKS、GCP GKE 等托管 K8s。

```bash
# 1. 创建命名空间
kubectl apply -f deploy/k8s/namespace.yaml

# 2. 创建 Secret（填入真实密钥）
kubectl create secret generic gobivc-secrets -n gobivc \
  --from-literal=anthropic-api-key=sk-ant-xxxxx \
  --from-literal=api-token=$(openssl rand -hex 32)

# 3. 创建 PVC、Deployment、Service
kubectl apply -f deploy/k8s/pvc.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml

# 4. 配置 Ingress（修改 ingress.yaml 中的 host）
kubectl apply -f deploy/k8s/ingress.yaml

# 或使用 kustomize 一次部署所有资源
kubectl apply -k deploy/k8s
```

查看状态：
```bash
kubectl -n gobivc get pods
kubectl -n gobivc logs -f deployment/gobivc
```

**注意**：使用的是 SQLite，必须单副本运行 (`replicas: 1`, `strategy: Recreate`)。如需多副本，请改用外部数据库。

---

## 方案三：阿里云 Serverless (SAE / Container Service)

1. **构建镜像并推送到阿里云 ACR**
   ```bash
   docker build -t registry.cn-hangzhou.aliyuncs.com/<namespace>/gobivc:latest .
   docker push registry.cn-hangzhou.aliyuncs.com/<namespace>/gobivc:latest
   ```

2. **在 SAE 创建应用**
   - 镜像地址：填入上面的镜像
   - 端口：8080
   - 健康检查：`/health`
   - 环境变量：`ANTHROPIC_API_KEY`, `API_TOKEN`
   - 挂载 NAS 存储到 `/app/data` (持久化)

3. **配置 SLB + 域名 + SSL**

---

## 方案四：腾讯云 Cloud Run / 云托管

1. 在云托管控制台选择 **GitHub 仓库代码部署**
2. 选择 `Dockerfile` 构建
3. 配置环境变量与上表一致
4. 挂载 CFS 文件存储到 `/app/data`
5. 云托管会自动分配 HTTPS 域名

---

## 方案五：AWS ECS Fargate

```bash
# 构建镜像并推送到 ECR
aws ecr get-login-password | docker login --username AWS --password-stdin <account>.dkr.ecr.<region>.amazonaws.com
docker build -t gobivc .
docker tag gobivc:latest <account>.dkr.ecr.<region>.amazonaws.com/gobivc:latest
docker push <account>.dkr.ecr.<region>.amazonaws.com/gobivc:latest
```

在 ECS 控制台创建任务定义：
- 镜像：ECR 地址
- 端口映射：8080
- 环境变量：`ANTHROPIC_API_KEY`, `API_TOKEN`
- 挂载 EFS 到 `/app/data`
- 健康检查：`/health`

前面放 ALB，配置 HTTPS + 域名。

---

## 方案六：Google Cloud Run

```bash
# 构建并部署
gcloud run deploy gobivc \
  --source . \
  --region asia-east1 \
  --platform managed \
  --allow-unauthenticated \
  --set-env-vars ANTHROPIC_API_KEY=sk-ant-xxxxx,API_TOKEN=your-token \
  --memory 512Mi \
  --timeout 900
```

**注意**：Cloud Run 容器文件系统临时，SQLite 数据会丢失。生产环境请挂载 Cloud Filestore 或改用 Cloud SQL。

---

## 安全建议

1. **务必设置 `API_TOKEN`** - 否则 API 公开可访问，任何人都能消耗你的 Claude 额度
2. **启用 HTTPS** - 使用 Let's Encrypt / Cloud SSL 证书
3. **限制 CORS 来源** - 设置 `ALLOWED_ORIGIN=https://your-domain.com`
4. **定期备份 `data/` 目录** - 包含所有历史报告
5. **设置资源限制** - 防止恶意请求耗尽资源
6. **监控 Claude API 消耗** - 在 Anthropic 控制台设置预算告警

## 故障排查

```bash
# 检查健康状态
curl http://localhost:8080/health

# 带 token 调用 API
curl -H "Authorization: Bearer <your-token>" http://localhost:8080/api/reports

# 查看容器日志
docker logs gobivc-gobivc-1

# 查看 K8s 日志
kubectl -n gobivc logs -f deployment/gobivc
```
