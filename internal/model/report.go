package model

import "time"

// ReportDepth defines the analysis depth of a report.
type ReportDepth string

const (
	DepthBrief    ReportDepth = "brief"    // 概览级别 ~2000字
	DepthStandard ReportDepth = "standard" // 标准分析 ~5000字
	DepthDeep     ReportDepth = "deep"     // 深度研究 ~10000字
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

// ReportConfig holds user-specified parameters for report generation.
type ReportConfig struct {
	Topic       string      `json:"topic"`       // 研究主题，如 "新能源汽车"
	Direction   string      `json:"direction"`   // 研究方向，如 "市场规模与增长趋势"
	Depth       ReportDepth `json:"depth"`        // 报告深度
	CustomNotes string      `json:"custom_notes"` // 用户自定义备注/要求
}

// Report is the core entity representing an industry research report.
type Report struct {
	ID          string          `json:"id"`
	Config      ReportConfig    `json:"config"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Sections    []ReportSection `json:"sections"`
	Status      ReportStatus    `json:"status"`
	ErrorMsg    string          `json:"error_msg,omitempty"`
	Content     string          `json:"content"`      // full markdown content
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// CreateReportRequest is the API request body for creating a new report.
type CreateReportRequest struct {
	Topic       string      `json:"topic"`
	Direction   string      `json:"direction"`
	Depth       ReportDepth `json:"depth"`
	CustomNotes string      `json:"custom_notes"`
}

// Validate checks that required fields are present and depth is valid.
func (r *CreateReportRequest) Validate() string {
	if r.Topic == "" {
		return "topic is required"
	}
	switch r.Depth {
	case DepthBrief, DepthStandard, DepthDeep:
		// valid
	case "":
		r.Depth = DepthStandard // default
	default:
		return "depth must be one of: brief, standard, deep"
	}
	return ""
}

// ReportListItem is a lightweight representation for listing reports.
type ReportListItem struct {
	ID          string       `json:"id"`
	Topic       string       `json:"topic"`
	Direction   string       `json:"direction"`
	Depth       ReportDepth  `json:"depth"`
	Status      ReportStatus `json:"status"`
	Title       string       `json:"title"`
	CreatedAt   time.Time    `json:"created_at"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
}

// ToListItem converts a Report to a ReportListItem.
func (r *Report) ToListItem() ReportListItem {
	return ReportListItem{
		ID:          r.ID,
		Topic:       r.Config.Topic,
		Direction:   r.Config.Direction,
		Depth:       r.Config.Depth,
		Status:      r.Status,
		Title:       r.Title,
		CreatedAt:   r.CreatedAt,
		CompletedAt: r.CompletedAt,
	}
}
