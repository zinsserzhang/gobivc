# GobiVC - 行业研究报告生成系统

面向风险投资团队的 AI 驱动行业研究报告生成工具。用户可自定义研究主题、方向、深度，系统自动生成专业的行业研究报告。

## 功能特性

- **自定义主题** - 输入任意行业/领域作为研究对象
- **方向聚焦** - 指定报告重点分析方向（市场规模、竞争格局、技术趋势等）
- **三档深度** - 概览(~2000字) / 标准(~5000字) / 深度(~10000字)
- **异步生成** - 后台生成报告，实时轮询状态
- **报告管理** - 查看、复制、下载(Markdown)、删除报告
- **专业结构** - 涵盖摘要、市场分析、竞争格局、投资建议、风险提示等

## 技术栈

- **后端**: Go (标准库 `net/http`)
- **前端**: 原生 HTML/CSS/JavaScript (无框架依赖)
- **AI引擎**: Anthropic Claude API
- **存储**: SQLite (WAL模式，持久化存储)

## 快速开始

### 1. 环境要求

- Go 1.21+
- Anthropic API Key

### 2. 配置

```bash
cp .env.example .env
# 编辑 .env 文件，填入你的 ANTHROPIC_API_KEY
```

### 3. 运行

```bash
# 加载环境变量
export $(cat .env | xargs)

# 启动服务
go run cmd/server/main.go
```

访问 http://localhost:8080

### 4. 构建

```bash
go build -o gobivc cmd/server/main.go
./gobivc
```

## 项目结构

```
├── cmd/server/          # 应用入口
│   └── main.go
├── internal/
│   ├── api/             # HTTP handlers & router
│   ├── config/          # 配置管理
│   ├── model/           # 数据模型
│   ├── service/         # 业务逻辑 & AI生成
│   └── store/           # 数据存储层
├── web/
│   ├── templates/       # HTML模板
│   └── static/          # CSS & JavaScript
├── .env.example         # 环境变量示例
└── go.mod
```

## API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/reports` | 创建报告 |
| GET | `/api/reports` | 获取报告列表 |
| GET | `/api/reports?q=关键词` | 搜索报告 |
| GET | `/api/reports/:id` | 获取报告详情 |
| GET | `/api/reports/:id/stream` | SSE流式获取生成内容 |
| DELETE | `/api/reports/:id` | 删除报告 |

### 创建报告请求示例

```json
{
  "topic": "新能源汽车",
  "direction": "中国市场竞争格局",
  "depth": "standard",
  "custom_notes": "请重点分析比亚迪和特斯拉的竞争态势"
}
```

## 配置项

| 环境变量 | 必填 | 默认值 | 说明 |
|---------|------|--------|------|
| `ANTHROPIC_API_KEY` | 是 | - | Anthropic API密钥 |
| `ANTHROPIC_MODEL` | 否 | `claude-sonnet-4-20250514` | 使用的AI模型 |
| `ANTHROPIC_BASE_URL` | 否 | `https://api.anthropic.com` | API地址(支持代理) |
| `PORT` | 否 | `8080` | 服务端口 |
| `DATA_DIR` | 否 | `./data` | SQLite数据库目录 |

## Docker 部署

```bash
# 使用 docker-compose
cp .env.example .env
# 编辑 .env，填入 ANTHROPIC_API_KEY
docker compose up -d

# 或直接构建运行
docker build -t gobivc .
docker run -p 8080:8080 -e ANTHROPIC_API_KEY=sk-ant-xxxxx -v gobivc-data:/app/data gobivc
```
