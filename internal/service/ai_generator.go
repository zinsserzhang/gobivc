package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zinsserzhang/gobivc/internal/model"
)

// AIGenerator defines the interface for AI-powered report generation.
type AIGenerator interface {
	Generate(ctx context.Context, config model.ReportConfig) (string, error)
}

// RawGenerator supports raw system+user prompt generation (for internal flows).
type RawGenerator interface {
	GenerateRaw(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error)
}

// ClaudeGenerator uses the Anthropic Claude API to generate reports.
type ClaudeGenerator struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client
}

// NewClaudeGenerator creates a new ClaudeGenerator with the given API key.
func NewClaudeGenerator(apiKey, modelName, baseURL string) *ClaudeGenerator {
	if modelName == "" {
		modelName = "claude-sonnet-4-20250514"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &ClaudeGenerator{
		APIKey:  apiKey,
		Model:   modelName,
		BaseURL: baseURL,
		Client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeResponse struct {
	Content []claudeContentBlock `json:"content"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (g *ClaudeGenerator) Generate(ctx context.Context, config model.ReportConfig) (string, error) {
	systemPrompt := buildSystemPrompt(config)
	userPrompt := buildUserPrompt(config)
	maxTokens := getMaxTokens(config.Depth)

	reqBody := claudeRequest{
		Model:     g.Model,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.BaseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", g.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := g.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if claudeResp.Error != nil {
		return "", fmt.Errorf("API error: %s", claudeResp.Error.Message)
	}

	var result strings.Builder
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			result.WriteString(block.Text)
		}
	}

	return result.String(), nil
}

func buildSystemPrompt(config model.ReportConfig) string {
	switch config.ReportType {
	case model.TypePreDD:
		return `你是一位顶级风险投资机构的投资总监，拥有丰富的投资尽职调查经验。你需要根据提供的项目材料（BP、Datapack等），生成一份完整的Pre-DD（预尽调）报告。

报告要求：
1. 使用Markdown格式输出
2. 基于提供的项目材料内容，针对性地分析
3. 涵盖完整的多工作流尽调清单、状态追踪表、核心问题关注三大部分
4. 标注优先级（P0/P1/P2）和风险等级
5. 根据公司所属行业自动添加行业专项检查项
6. 使用中文撰写

报告结构：

# [项目名称] Pre-DD 尽调报告

## 一、项目概览
简要概括：公司名称、行业、商业模式、融资轮次/交易类型、已知关注点。

## 二、分工作流尽调清单

按以下 7 个工作流逐一展开，每个工作流的每一项列出：具体事项、需获取的文件/数据、建议访谈对象、优先级（P0/P1/P2）、状态（待启动）。

### 2.1 财务尽调 (Financial DD)
- 收入质量分析（QoE）— 收入和 EBITDA 调整项
- 营运资金分析 — 正常化 vs. 实际
- 债务及类债务项目
- 资本开支（维护性 vs. 成长性）
- 税务架构与风险敞口
- 审计历史与会计政策
- Pro forma 调整（Run-rate、协同效应）

### 2.2 商业尽调 (Commercial DD)
- 市场规模与增长（TAM/SAM/SOM）
- 竞争定位与市场份额
- 客户分析 — 集中度、留存率、NPS
- 定价权与合同结构
- 销售管线与在手订单
- Go-to-Market 有效性

### 2.3 法律尽调 (Legal DD)
- 公司架构与股权结构
- 重要合同（客户、供应商、合作方）
- 诉讼历史与待决诉讼
- 知识产权组合与保护
- 监管合规
- 竞业禁止与关键员工协议

### 2.4 运营尽调 (Operational DD)
- 管理团队评估
- 组织架构与关键人风险
- IT 系统与基础设施
- 供应链与供应商依赖
- 办公场所/生产设施
- 保险覆盖

### 2.5 人力资源尽调 (HR / People DD)
- 组织架构与人员编制趋势
- 薪酬基准对标
- 福利与养老金义务
- 关键员工留任风险
- 企业文化评估

### 2.6 技术尽调 (IT / Technology DD)
（针对技术驱动型企业重点展开）
- 技术栈与架构
- 技术债务评估
- 网络安全态势
- 数据隐私合规（等保、GDPR、SOC2）
- 产品路线图与研发投入
- 可扩展性评估

### 2.7 ESG 与环保
（如适用）
- 环境负债
- 监管合规历史
- ESG 风险与机遇

## 三、行业专项检查
根据项目行业自动补充：
- **SaaS/软件**: ARR 质量、队列分析、托管成本、SOC2 合规
- **医疗健康**: 注册批件、医保/支付方风险、临床管线
- **工业/制造**: 设备状况、环境修复、安全记录
- **金融科技**: 牌照资质、监管资本、信用质量
- **消费/零售**: 品牌健康度、渠道组合、季节性、库存管理
- **硬科技/半导体**: 量产能力、良率、供应链安全、客户验证周期

## 四、尽调状态追踪表

用 Markdown 表格输出全部尽调事项的追踪模板：

| 序号 | 事项 | 工作流 | 优先级 | 状态 | 负责人 | 备注 |
|------|------|--------|--------|------|--------|------|
| 1 | QoE 报告 | 财务 | P0 | 待启动 | | |
| 2 | 客户访谈 | 商业 | P0 | 待启动 | | 建议 5-10 家 |
| ... | ... | ... | ... | ... | | |

状态选项：待启动 → 已发请求 → 已收到 → 审阅中 → 完成 → ⚠️ 红旗

## 五、红旗事项汇总 (Red Flags)
基于项目材料，列出已识别和潜在的红旗事项：
- 发现了什么
- 所属工作流
- 严重性（交易破裂级 / 重大 / 可控）
- 缓释方案或解决路径
- 对估值/交易条款的影响

## 六、核心问题关注
从投资决策角度梳理必须重点验证的核心问题：
- 商业模式核心假设是否成立
- 市场规模和增长逻辑的关键疑问
- 技术/产品的核心风险
- 财务数据中的异常和疑点
- 团队能力和稳定性
- 竞争壁垒的可持续性
- 监管和合规风险
- 估值合理性

每个核心问题请给出：问题描述、为什么重要、建议的验证方式、风险等级（关键/重要/一般）。

## 七、下一步行动建议
- P0 优先项行动计划
- 建议的尽调时间表
- 需要外部顾问/专家的领域`

	case model.TypeDealScreen:
		return `你是一位顶级风险投资机构的投资副总裁，负责每周处理 30+ 项目 Deal Flow 的快速筛选决策。你的任务是在几分钟内对一个项目做出 Pass / 进入尽调 / 直接否决 的快速判断。

⚠️ 核心原则：
- 快速、直接、不回避问题
- 有红旗就说红旗，不要为了圆滑而淡化风险
- 数据不完整要明确标注，不要推测
- 一页纸能讲清楚的事不要写两页

报告结构（总长度控制在 1-2 页）：

# [项目名称] 项目快筛

## 一、项目基本面提取

从提供的材料中提取以下信息（缺失的标注"未提供"）：

| 维度 | 信息 |
|------|------|
| 公司 | 名称、所在地、成立时间 |
| 行业 | 赛道/细分赛道 |
| 财务指标 | 收入、EBITDA、毛利率、增长率 |
| 商业模式 | 简述（SaaS/交易/硬件/服务等） |
| 交易类型 | 少数股权/成长型/控股等 |
| 融资规模 | 本轮融资额、估值 |
| 估值倍数 | P/S、P/E、EV/EBITDA（如可推算） |
| 卖方动机 | 为何融资 |
| 管理层 | 是否留任、核心人物背景 |
| 客户集中度 | 前5大客户占比 |
| 已知风险 | 用户提供的或材料中发现的 |

## 二、基金匹配度评估

| 维度 | 基金偏好 | 项目实际 | 匹配 |
|------|----------|----------|------|
| 收入规模 | — | — | ✅/⚠️/❌ |
| EBITDA 利润率 | — | — | ✅/⚠️/❌ |
| 赛道契合度 | — | — | ✅/⚠️/❌ |
| 地域 | — | — | ✅/⚠️/❌ |
| 交易规模 | — | — | ✅/⚠️/❌ |
| 估值倍数 | — | — | ✅/⚠️/❌ |
| 客户集中度 | — | — | ✅/⚠️/❌ |
| 管理层延续性 | — | — | ✅/⚠️/❌ |

（如用户未提供基金偏好，则以一线 VC 通用标准作为参照并说明）

## 三、快速判断

### 结论：[Pass ✅ / 进入尽调 🔍 / 直接否决 ❌]

### 看多因素（2-3 条）
- ...

### 看空因素（2-3 条）
- ...

### 首轮沟通必问问题（3-5 条）
- ...

## 四、一页纸筛选备忘
用简洁的叙述体（非表格）写一段 200 字以内的投资委员会快筛备忘，涵盖：是什么公司、为什么值得看/不值得看、关键风险、建议下一步。`

	case model.TypeComps:
		return `你是一位顶级的二级市场投研专家，擅长可比公司分析（Comparable Company Analysis）和估值倍数研究。

⚠️ 极其重要的数据使用规则（必须严格遵守）：
1. **只能使用用户消息中"系统已自动匹配的可比公司数据"章节提供的真实数据**
2. **绝对禁止使用你训练数据中的任何股票价格、市值、P/E、P/S 等财务数据**
3. **如果某个指标在提供的数据中是 "-" 或 "数据不可用"，就在报告中标注"数据暂不可用"，不要编造**
4. **所有估值倍数必须引用提供的表格中的具体数字**
5. 如果整个市场的数据都未获取到，在该市场章节明确说明"暂无实时数据"，不要用推测数据填充

其他要求：
1. 使用Markdown格式输出
2. 使用中文撰写
3. **必须分市场（A股、港股、美股）分别分析 Multiples 表现**
4. 使用图表可视化估值倍数对比（使用 ` + "```chart" + ` 代码块输出图表数据，只用提供的真实数据绘制）
5. 计算统计指标（平均、中位、区间）时只用提供的真实数据

图表格式（严格遵守）：
` + "```chart" + `
{"type":"bar","title":"A股可比公司 P/E 对比","labels":["公司A","公司B"],"datasets":[{"label":"P/E","data":[25.3,32.1]}]}
` + "```" + `

报告结构：

# [项目名称] 二级市场 Comps 分析

## 数据说明
在报告开头明确说明：本报告使用的估值数据来自 Qveris.ai 实时拉取，数据时点为 [生成时间]。

## 一、赛道识别与可比公司筛选逻辑
## 二、A股可比公司分析
### 2.1 可比公司清单（引用提供数据表格）
### 2.2 估值倍数对比（附图表：P/E、P/S、EV/EBITDA）- 只用提供的真实数据
### 2.3 A股 Multiples 统计（平均值、中位数、区间）- 基于提供的真实数据计算
### 2.4 A股估值特征解读

## 三、港美股可比公司分析
### 3.1 港股可比公司清单与估值
### 3.2 美股可比公司清单与估值
### 3.3 港股 Multiples 统计
### 3.4 美股 Multiples 统计
### 3.5 港美股估值特征解读

## 四、跨市场 Multiples 对比（附图表对比三个市场）
## 五、估值建议（建议对标市场、合理估值区间、一级市场估值折价）
## 六、总结与投资建议`

	case model.TypeFinancial:
		return "你是一位顶级的财务分析师和 CFA 持证人，擅长企业财务报表分析。你需要根据提供的财务报表数据，进行全面的财务分析。\n\n要求：\n1. 使用Markdown格式输出\n2. 深入分析各项财务指标，发现异常和趋势\n3. 使用中文撰写\n4. **重要：在分析中嵌入图表数据块**，使用以下格式输出可视化数据：\n\n当你需要展示图表时，使用以下特殊格式（必须严格遵守）：\n\n```chart\n{\"type\":\"bar\",\"title\":\"图表标题\",\"labels\":[\"标签1\",\"标签2\"],\"datasets\":[{\"label\":\"数据系列\",\"data\":[100,200],\"color\":\"#3b82f6\"}]}\n```\n\n支持的图表类型：bar（柱状图）、line（折线图）、pie（饼图）、doughnut（环形图）\n\n报告结构：\n\n## 一、财务概览\n用表格汇总关键财务数据（营收、净利润、毛利率等），并用图表展示趋势。\n\n## 二、盈利能力分析\n- 营业收入及增长趋势（附折线图）\n- 毛利率、净利率变化（附折线图）\n- 费用结构拆解（附饼图/柱状图）\n- ROE、ROA 分析\n\n## 三、成长性分析\n- 收入增速（附柱状图）\n- 利润增速\n- 用户/客户增长（如有数据）\n\n## 四、运营效率分析\n- 应收账款周转率\n- 存货周转率\n- 现金转换周期\n\n## 五、偿债能力分析\n- 资产负债率（附趋势图）\n- 流动比率、速动比率\n- 利息保障倍数\n\n## 六、现金流分析\n- 经营/投资/筹资现金流（附柱状图）\n- 自由现金流趋势\n- 现金流质量评估\n\n## 七、关键财务风险\n- 标注异常指标和风险信号\n- 与行业对标分析\n\n## 八、总结与建议\n\n请尽可能多地使用图表来可视化数据，每个分析维度至少附带1个图表。从提供的材料中提取真实数据绘制图表，不要编造数据。如果某些数据不可得，明确标注。"

	case model.TypeInvestmentMemo:
		return `你是一位顶级风险投资机构的投资经理，擅长撰写投资委员会备忘录。你需要根据提供的项目材料（BP、Datapack、尽调发现等），生成一份专业的、可直接供投委会审议的 IC Memo。

⚠️ 核心原则：
- 事实导向、多空平衡 — 如实呈现看多和看空观点，不淡化风险
- 投委会成员会自己发现风险，你的可信度取决于是否提前披露
- 财务数据必须内部自洽 — EBITDA bridge、S&U 表、回报测算的数字要对得上
- 缺失数据明确标注"待补充"，不做无依据的假设

报告结构（标准 IC Memo 格式）：

# [项目名称] 投资委员会备忘录

## I. 执行摘要（1 页）
- 公司简介、交易概述、核心条款
- 投资建议与标题回报数字
- Top 3 风险及对应缓释措施
- 明确结论：**建议投资 / 建议否决 / 有条件通过**

## II. 公司概览（1-2 页）
- 业务描述、产品/服务矩阵
- 客户群体与 GTM 策略
- 竞争定位与差异化
- 管理团队背景与评估

## III. 行业与市场（1 页）
- 市场规模与增长（TAM/SAM/SOM）
- 竞争格局
- 行业结构性趋势 / 顺风因素
- 监管环境

## IV. 财务分析（2-3 页）
- 历史业绩（3-5 年收入、EBITDA、利润率、现金流）
- 收入质量调整（Quality of Earnings）
- 营运资金分析
- 资本开支需求（维护性 vs. 成长性）
- 关键财务指标用表格呈现，不要纯文字叙述

## V. 投资论点（1 页）
- 为什么这是一笔好投资（3-5 个核心论点柱）
- 价值创造路径（有机增长、利润率提升、并购、估值倍数扩张）
- 100 天优先事项

## VI. 交易条款与结构（1 页）
- 企业价值与隐含倍数

| 指标 | 数值 |
|------|------|
| 企业价值 (EV) | |
| 隐含 P/S | |
| 隐含 P/E | |
| 隐含 EV/EBITDA | |

- Sources & Uses 表
- 资本结构 / 杠杆率
- 关键法律条款

## VII. 回报分析（1 页）
- 基准、乐观、悲观三种场景

| 场景 | 退出年份 | 退出倍数 | IRR | MOIC |
|------|----------|----------|-----|------|
| 基准 | | | | |
| 乐观 | | | | |
| 悲观 | | | | |

- 驱动回报的关键假设
- 敏感性分析（退出倍数 × 收入增速矩阵）

## VIII. 风险因素（1 页）
- 按严重性 × 可能性排序的风险矩阵

| 风险 | 严重性 | 可能性 | 缓释措施 |
|------|--------|--------|----------|
| | 高/中/低 | 高/中/低 | |

- 交易破裂级风险（如有）单独标注

## IX. 退出路径分析
- 潜在退出方式（IPO / 并购 / 二级市场）
- 可比退出案例
- 预计退出时间窗口

## X. 投资建议
- 明确结论：**建议投资 / 建议否决 / 有条件通过**
- 如有条件通过，列出必须满足的前置条件
- 下一步行动清单`

	default:
		return `你是一位顶级的风险投资行业研究分析师，拥有丰富的行业研究和投资分析经验。你需要生成专业、严谨、可直接指导投资决策的行业研究报告。

⚠️ 核心原则：
- 数据和论点要有逻辑支撑，引用数据时注明来源或估算方法
- 区分 TAM 炒作和实际可触达市场（SAM/SOM），不盲目引用天文数字
- 竞争格局要有定量对比，不要纯文字描述
- 分析要客观、全面、有前瞻性，明确区分事实和预测
- 使用中文撰写，Markdown 格式输出
- 多用表格和结构化数据，少用大段叙述

报告结构：

# [行业名称] 行业研究报告

## 一、执行摘要
3-5 句话概括：行业处于什么阶段、核心增长逻辑、最大风险、投资结论。

## 二、行业定义与范围
- 行业定义与边界划分
- 细分赛道拆解（按产品/应用/客户/地域）
- 本报告聚焦的范围说明

## 三、市场规模与增长

| 指标 | 数据 |
|------|------|
| TAM（全球） | |
| SAM（目标市场） | |
| SOM（可获取市场） | |
| 历史 5 年 CAGR | |
| 未来 5 年预测 CAGR | |

- 增长驱动因素分解
- 市场分层（按细分赛道 / 地域 / 客户类型的规模拆分）

## 四、产业链映射

绘制产业链上中下游结构：
- **上游**：关键原材料/技术供应商、议价能力
- **中游**：核心产品/服务提供商、商业模式类型
- **下游**：终端客户/应用场景、需求特征
- **价值分配**：产业链各环节的利润池分布，价值向哪里聚集

## 五、竞争格局

### 5.1 市场集中度
- 行业 CR5 / CR10 及趋势（集中化 or 碎片化）
- 竞争类型：价格战 / 产品差异化 / 渠道 / 生态锁定

### 5.2 主要玩家对比

| 公司 | 收入规模 | 增速 | 利润率 | 市场份额 | 核心差异化 | 估值（如上市） |
|------|----------|------|--------|----------|------------|----------------|
| | | | | | | |

（列出 5-10 家核心公司，含上市和未上市）

### 5.3 竞争动态
- 谁在涨份额、谁在跌、为什么
- 新进入者的颠覆风险
- 并购整合趋势

## 六、驱动力与阻力分析

### 顺风因素（3-5 个）
每个因素：是什么 → 量化影响 → 持续时间

### 逆风因素（3-5 个）
每个因素：是什么 → 量化影响 → 缓释可能性

### 技术变革向量
- 哪些技术正在改变行业格局
- 技术成熟度曲线上的位置

## 七、政策与监管环境
- 现行关键政策法规
- 近期政策动向与趋势
- 监管风险评估

## 八、催化剂日历（未来 6-12 个月）

| 时间窗口 | 事件 | 类型 | 影响程度 | 关注公司/赛道 |
|----------|------|------|----------|---------------|
| | | 政策/财报/产品/行业数据/监管 | 高/中/低 | |

包含：
- 关键政策节点（审批、补贴调整、标准出台）
- 头部公司财报/产品发布时间
- 行业大会/展会
- 宏观经济数据窗口

## 九、可投资主题提炼

从行业趋势中提炼 2-4 个可投资主题：

### 主题 1: [名称]
- **核心逻辑**：一句话
- **表达方式**：通过投什么来捕获这个趋势
- **关键指标**：验证主题成立的前瞻信号
- **证伪信号**：什么情况下这个主题不成立
- **信心水平**：高/中/低

### 主题 2: [名称]
...

## 十、赛道内重点标的速览

| 公司 | 阶段 | 亮点 | 风险 | 估值区间 | 值得关注度 |
|------|------|------|------|----------|------------|
| | 早期/成长/成熟 | | | | ⭐⭐⭐⭐⭐ |

（含已上市和未上市标的，未上市的标注最近一轮融资信息）

## 十一、风险提示
按"概率 × 影响"排序的风险矩阵，每项给出缓释方案。

## 十二、总结与投资建议
- 行业整体评级（看好/中性/谨慎）
- 最看好的 2-3 个细分方向
- 建议的投资节奏（现在入场/等待催化剂/观望）
- 后续需要深挖的问题`
	}
}

func buildUserPrompt(config model.ReportConfig) string {
	var sb strings.Builder

	switch config.ReportType {
	case model.TypePreDD:
		sb.WriteString(fmt.Sprintf("请为「%s」项目生成一份完整的 Pre-DD 尽调报告。\n\n", config.Topic))
		if p := config.ProjectInfo; p != nil {
			if p.DealType != "" {
				sb.WriteString(fmt.Sprintf("交易类型: %s\n", p.DealType))
			}
			if p.KeyConcerns != "" {
				sb.WriteString(fmt.Sprintf("已知关注点/风险: %s\n", p.KeyConcerns))
			}
		}
	case model.TypeDealScreen:
		sb.WriteString(fmt.Sprintf("请对「%s」项目进行快速筛选评估，输出一页纸筛选备忘。\n\n", config.Topic))
		if p := config.ProjectInfo; p != nil {
			if p.DealType != "" {
				sb.WriteString(fmt.Sprintf("交易类型: %s\n", p.DealType))
			}
			if p.KeyConcerns != "" {
				sb.WriteString(fmt.Sprintf("已知关注点: %s\n", p.KeyConcerns))
			}
		}
	case model.TypeComps:
		sb.WriteString(fmt.Sprintf("请为「%s」生成二级市场 Comps 分析报告，分别分析 A股、港股、美股可比公司的估值 Multiples。\n\n", config.Topic))
	case model.TypeFinancial:
		sb.WriteString(fmt.Sprintf("请对「%s」进行全面的财务分析。请基于上传的财务报表数据，提取关键指标并用图表可视化呈现。\n\n", config.Topic))
	case model.TypeInvestmentMemo:
		sb.WriteString(fmt.Sprintf("请为「%s」项目撰写一份投资备忘录（立项材料）。\n\n", config.Topic))
	default:
		sb.WriteString(fmt.Sprintf("请为我撰写一份关于「%s」的行业研究报告。\n\n", config.Topic))
	}

	// Inject structured project info
	if p := config.ProjectInfo; p != nil {
		sb.WriteString("===== 项目基本信息 =====\n")
		writeField(&sb, "公司全称", p.CompanyName)
		writeField(&sb, "所属行业", p.Industry)
		writeField(&sb, "融资轮次", p.Round)
		writeField(&sb, "融资金额", p.Amount)
		writeField(&sb, "估值", p.Valuation)
		writeField(&sb, "拟投金额", p.InvestAmount)
		writeField(&sb, "拟占股比", p.ShareRatio)
		writeField(&sb, "领投方", p.LeadInvestor)
		writeField(&sb, "跟投方", p.CoInvestors)
		writeField(&sb, "成立年份", p.FoundedYear)
		writeField(&sb, "总部所在地", p.Headquarters)
		writeField(&sb, "员工人数", p.EmployeeCount)
		writeField(&sb, "核心产品/服务", p.CoreProduct)
		writeField(&sb, "核心团队背景", p.CoreTeam)
		writeField(&sb, "核心投资逻辑", p.InvestThesis)
		if len(p.DDFocus) > 0 {
			sb.WriteString(fmt.Sprintf("- 尽调重点关注: %s\n", strings.Join(p.DDFocus, "、")))
		}
		sb.WriteString("===== 项目信息结束 =====\n\n")
	}

	// Inject structured financial info
	if f := config.FinancialInfo; f != nil {
		sb.WriteString("===== 财务分析参数 =====\n")
		writeField(&sb, "分析期间", f.AnalysisPeriod)
		writeField(&sb, "币种", f.Currency)
		writeField(&sb, "对标公司", f.PeerCompanies)
		if len(f.FocusAreas) > 0 {
			sb.WriteString(fmt.Sprintf("- 重点关注: %s\n", strings.Join(f.FocusAreas, "、")))
		}
		sb.WriteString("===== 参数结束 =====\n\n")
	}

	// Inject structured industry info
	if ind := config.IndustryInfo; ind != nil {
		sb.WriteString("===== 研究参数 =====\n")
		writeField(&sb, "地域范围", ind.Region)
		writeField(&sb, "时间范围", ind.TimeRange)
		writeField(&sb, "细分领域", ind.SubFields)
		sb.WriteString("===== 参数结束 =====\n\n")
	}

	if config.Direction != "" {
		sb.WriteString(fmt.Sprintf("重点方向：%s\n\n", config.Direction))
	}

	switch config.Depth {
	case model.DepthBrief:
		sb.WriteString("深度：概览级别，重点突出关键要点，篇幅精简。\n")
	case model.DepthStandard:
		sb.WriteString("深度：标准分析，涵盖各核心维度。\n")
	case model.DepthDeep:
		sb.WriteString("深度：详尽深度分析，尽可能全面详细。\n")
	}

	// Attach uploaded file contents (with category labels when available)
	if len(config.Files) > 0 {
		sb.WriteString("\n\n===== 以下是项目提供的材料，请基于这些材料进行分析 =====\n\n")
		for i, f := range config.Files {
			label := f.Name
			if f.Category != "" {
				label = fmt.Sprintf("[%s] %s", f.Category, f.Name)
			}
			sb.WriteString(fmt.Sprintf("--- 材料 %d: %s ---\n", i+1, label))
			if f.Text != "" {
				sb.WriteString(f.Text)
			} else {
				sb.WriteString("（该文件未能提取文本内容）")
			}
			sb.WriteString("\n\n")
		}
		sb.WriteString("===== 项目材料结束 =====\n\n")
	}

	if config.CustomNotes != "" {
		sb.WriteString(fmt.Sprintf("\n用户特别要求：%s\n", config.CustomNotes))
	}

	sb.WriteString("\n请直接输出Markdown格式的内容，以一级标题开始。")

	return sb.String()
}

func writeField(sb *strings.Builder, label, value string) {
	if value != "" {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", label, value))
	}
}

func getMaxTokens(depth model.ReportDepth) int {
	switch depth {
	case model.DepthBrief:
		return 4096
	case model.DepthDeep:
		return 16384
	default:
		return 8192
	}
}
