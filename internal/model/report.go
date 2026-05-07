package model

import "time"

// ReportType defines what kind of output to generate.
type ReportType string

const (
	TypePreDD          ReportType = "predd"
	TypeInvestmentMemo ReportType = "memo"
	TypeFinancial      ReportType = "financial"
	TypeComps          ReportType = "comps"     // 二级市场 Comps 分析
	TypeIndustryReport ReportType = "industry"
)

// ReportDepth defines the analysis depth.
type ReportDepth string

const (
	DepthBrief    ReportDepth = "brief"
	DepthStandard ReportDepth = "standard"
	DepthDeep     ReportDepth = "deep"
)

// ReportStatus tracks the lifecycle of a report.
type ReportStatus string

const (
	StatusPending    ReportStatus = "pending"
	StatusGenerating ReportStatus = "generating"
	StatusCompleted  ReportStatus = "completed"
	StatusFailed     ReportStatus = "failed"
)

// ReportSection represents one section of the generated report.
type ReportSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Order   int    `json:"order"`
}

// UploadedFile represents a file uploaded for a report.
type UploadedFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mime_type"`
	Text     string `json:"text,omitempty"`
}

// Assignee represents a person assigned to a report.
type Assignee struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// ProjectInfo holds structured project details for Pre-DD / Memo.
type ProjectInfo struct {
	CompanyName   string   `json:"company_name"`   // 公司全称
	Industry      string   `json:"industry"`       // 行业领域
	Round         string   `json:"round"`          // 融资轮次
	Amount        string   `json:"amount"`         // 融资金额
	Valuation     string   `json:"valuation"`      // 估值
	InvestAmount  string   `json:"invest_amount"`  // 拟投金额
	ShareRatio    string   `json:"share_ratio"`    // 拟占股比
	LeadInvestor  string   `json:"lead_investor"`  // 领投方
	CoInvestors   string   `json:"co_investors"`   // 跟投方
	FoundedYear   string   `json:"founded_year"`   // 成立年份
	Headquarters  string   `json:"headquarters"`   // 总部所在地
	EmployeeCount string   `json:"employee_count"` // 员工人数
	CoreProduct   string   `json:"core_product"`   // 核心产品/服务
	CoreTeam      string   `json:"core_team"`      // 核心团队背景
	InvestThesis  string   `json:"invest_thesis"`  // 核心投资逻辑
	DDFocus       []string `json:"dd_focus"`        // 尽调重点关注领域
	DealType      string   `json:"deal_type"`       // 交易类型
	KeyConcerns   string   `json:"key_concerns"`    // 已知关注/风险点
}

// FinancialInfo holds structured inputs for financial analysis.
type FinancialInfo struct {
	AnalysisPeriod string   `json:"analysis_period"` // 分析期间
	Currency       string   `json:"currency"`        // 币种
	PeerCompanies  string   `json:"peer_companies"`  // 对标公司
	FocusAreas     []string `json:"focus_areas"`     // 重点关注领域
	EnableComps    bool     `json:"enable_comps"`    // 启用二级市场Comps对比
	CompsSymbols   string   `json:"comps_symbols"`   // Comps股票代码（逗号分隔）
}

// IndustryInfo holds structured inputs for industry research.
type IndustryInfo struct {
	Region    string `json:"region"`     // 地域范围
	TimeRange string `json:"time_range"` // 时间范围
	SubFields string `json:"sub_fields"` // 细分领域
}

// ReportConfig holds user-specified parameters for report generation.
type ReportConfig struct {
	ReportType    ReportType     `json:"report_type"`
	Topic         string         `json:"topic"`
	Direction     string         `json:"direction"`
	Depth         ReportDepth    `json:"depth"`
	CustomNotes   string         `json:"custom_notes"`
	UseFeishu     bool           `json:"use_feishu"`
	Files         []UploadedFile `json:"files,omitempty"`
	Assignees     []Assignee     `json:"assignees,omitempty"`
	ProjectInfo   *ProjectInfo   `json:"project_info,omitempty"`
	FinancialInfo *FinancialInfo `json:"financial_info,omitempty"`
	IndustryInfo  *IndustryInfo  `json:"industry_info,omitempty"`
}

// Report is the core entity.
type Report struct {
	ID          string          `json:"id"`
	Config      ReportConfig    `json:"config"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Sections    []ReportSection `json:"sections"`
	Status      ReportStatus    `json:"status"`
	ErrorMsg    string          `json:"error_msg,omitempty"`
	Content     string          `json:"content"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// CreateReportRequest is the API request body for creating a new report.
type CreateReportRequest struct {
	ReportType    ReportType     `json:"report_type"`
	Topic         string         `json:"topic"`
	Direction     string         `json:"direction"`
	Depth         ReportDepth    `json:"depth"`
	CustomNotes   string         `json:"custom_notes"`
	UseFeishu     bool           `json:"use_feishu"`
	FileIDs       []string       `json:"file_ids"`
	Assignees     []Assignee     `json:"assignees"`
	ProjectInfo   *ProjectInfo   `json:"project_info,omitempty"`
	FinancialInfo *FinancialInfo `json:"financial_info,omitempty"`
	IndustryInfo  *IndustryInfo  `json:"industry_info,omitempty"`
}

// UpdateReportRequest is the API request body for updating a report.
type UpdateReportRequest struct {
	Title string `json:"title,omitempty"`
}

// Validate checks that required fields are present.
func (r *CreateReportRequest) Validate() string {
	if r.Topic == "" {
		return "topic is required"
	}
	switch r.ReportType {
	case TypePreDD, TypeInvestmentMemo, TypeFinancial, TypeComps, TypeIndustryReport:
	case "":
		r.ReportType = TypeIndustryReport
	default:
		return "report_type must be one of: predd, memo, financial, industry"
	}
	switch r.Depth {
	case DepthBrief, DepthStandard, DepthDeep:
	case "":
		r.Depth = DepthStandard
	default:
		return "depth must be one of: brief, standard, deep"
	}
	return ""
}

// ReportListItem is a lightweight representation for listing.
type ReportListItem struct {
	ID          string       `json:"id"`
	ReportType  ReportType   `json:"report_type"`
	Topic       string       `json:"topic"`
	Direction   string       `json:"direction"`
	Depth       ReportDepth  `json:"depth"`
	Status      ReportStatus `json:"status"`
	Title       string       `json:"title"`
	FileCount   int          `json:"file_count"`
	Assignees   []Assignee   `json:"assignees,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
}

// ToListItem converts a Report to a ReportListItem.
func (r *Report) ToListItem() ReportListItem {
	return ReportListItem{
		ID:          r.ID,
		ReportType:  r.Config.ReportType,
		Topic:       r.Config.Topic,
		Direction:   r.Config.Direction,
		Depth:       r.Config.Depth,
		Status:      r.Status,
		Title:       r.Title,
		FileCount:   len(r.Config.Files),
		Assignees:   r.Config.Assignees,
		CreatedAt:   r.CreatedAt,
		CompletedAt: r.CompletedAt,
	}
}
