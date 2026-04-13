package model

import "time"

// ReportType defines what kind of output to generate.
type ReportType string

const (
	TypePreDD          ReportType = "predd"
	TypeInvestmentMemo ReportType = "memo"
	TypeFinancial      ReportType = "financial"
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
	ID     string `json:"id"`     // Feishu open_id or user_id
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// ReportConfig holds user-specified parameters for report generation.
type ReportConfig struct {
	ReportType  ReportType     `json:"report_type"`
	Topic       string         `json:"topic"`
	Direction   string         `json:"direction"`
	Depth       ReportDepth    `json:"depth"`
	CustomNotes string         `json:"custom_notes"`
	UseFeishu   bool           `json:"use_feishu"`
	Files       []UploadedFile `json:"files,omitempty"`
	Assignees   []Assignee     `json:"assignees,omitempty"`
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
	ReportType  ReportType  `json:"report_type"`
	Topic       string      `json:"topic"`
	Direction   string      `json:"direction"`
	Depth       ReportDepth `json:"depth"`
	CustomNotes string      `json:"custom_notes"`
	UseFeishu   bool        `json:"use_feishu"`
	FileIDs     []string    `json:"file_ids"`
	Assignees   []Assignee  `json:"assignees"`
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
	case TypePreDD, TypeInvestmentMemo, TypeFinancial, TypeIndustryReport:
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
