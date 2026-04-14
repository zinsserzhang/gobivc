package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/zinsserzhang/gobivc/internal/feishu"
	"github.com/zinsserzhang/gobivc/internal/model"
	"github.com/zinsserzhang/gobivc/internal/qveris"
	"github.com/zinsserzhang/gobivc/internal/store"
)

// ReportService handles the business logic for report operations.
type ReportService struct {
	store     store.ReportStore
	generator AIGenerator
	feishu    *feishu.Client
	qveris    *qveris.Client

	mu          sync.RWMutex
	subscribers map[string][]chan string
}

// NewReportService creates a new ReportService.
func NewReportService(st store.ReportStore, generator AIGenerator, feishuClient *feishu.Client, qverisClient *qveris.Client) *ReportService {
	return &ReportService{
		store:       st,
		generator:   generator,
		feishu:      feishuClient,
		qveris:      qverisClient,
		subscribers: make(map[string][]chan string),
	}
}

// CreateReport creates a new report and starts async generation.
func (s *ReportService) CreateReport(config model.ReportConfig) (*model.Report, error) {
	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}

	report := &model.Report{
		ID:        id,
		Config:    config,
		Status:    model.StatusPending,
		CreatedAt: time.Now(),
	}

	if err := s.store.Save(report); err != nil {
		return nil, fmt.Errorf("failed to save report: %w", err)
	}

	// Start async generation
	go s.generateReport(report.ID)

	return report, nil
}

// GetReport retrieves a report by ID.
func (s *ReportService) GetReport(id string) (*model.Report, error) {
	return s.store.Get(id)
}

// ListReports returns all reports.
func (s *ReportService) ListReports() ([]*model.Report, error) {
	return s.store.List()
}

// SearchReports searches reports by keyword (delegates to SQLiteStore if available).
func (s *ReportService) SearchReports(query string) ([]*model.Report, error) {
	if searcher, ok := s.store.(interface {
		Search(string) ([]*model.Report, error)
	}); ok {
		return searcher.Search(query)
	}
	// Fallback: return all
	return s.store.List()
}

// UpdateReport updates a report in the store.
func (s *ReportService) UpdateReport(report *model.Report) error {
	return s.store.Update(report)
}

// DeleteReport deletes a report by ID.
func (s *ReportService) DeleteReport(id string) error {
	return s.store.Delete(id)
}

// Subscribe registers a channel to receive streaming chunks for a report.
func (s *ReportService) Subscribe(reportID string) chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan string, 64)
	s.subscribers[reportID] = append(s.subscribers[reportID], ch)
	return ch
}

// Unsubscribe removes a channel from streaming.
func (s *ReportService) Unsubscribe(reportID string, ch chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subscribers[reportID]
	for i, sub := range subs {
		if sub == ch {
			s.subscribers[reportID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(s.subscribers[reportID]) == 0 {
		delete(s.subscribers, reportID)
	}
	close(ch)
}

// broadcast sends a chunk to all subscribers of a report.
func (s *ReportService) broadcast(reportID string, chunk string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.subscribers[reportID] {
		select {
		case ch <- chunk:
		default:
			// Drop if channel full
		}
	}
}

// broadcastDone signals completion to all subscribers.
func (s *ReportService) broadcastDone(reportID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.subscribers[reportID] {
		close(ch)
	}
	delete(s.subscribers, reportID)
}

func (s *ReportService) generateReport(id string) {
	report, err := s.store.Get(id)
	if err != nil {
		log.Printf("ERROR: failed to get report %s for generation: %v", id, err)
		return
	}

	// Update status to generating
	report.Status = model.StatusGenerating
	if err := s.store.Update(report); err != nil {
		log.Printf("ERROR: failed to update report %s status: %v", id, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Gather Feishu reference materials if requested
	if report.Config.UseFeishu && s.feishu != nil && s.feishu.IsConfigured() {
		refs, err := s.feishu.GatherReferences(ctx, report.Config.Topic, 5)
		if err != nil {
			log.Printf("WARNING: failed to gather Feishu references for report %s: %v", id, err)
		} else if len(refs) > 0 {
			var refText strings.Builder
			refText.WriteString("\n\n以下是来自企业内部知识库的参考材料，请在撰写报告时参考整合这些信息：\n\n")
			for i, ref := range refs {
				refText.WriteString(fmt.Sprintf("--- 参考材料 %d: %s ---\n", i+1, ref.Title))
				refText.WriteString(ref.Content)
				refText.WriteString("\n\n")
			}
			report.Config.CustomNotes += refText.String()
			log.Printf("INFO: injected %d Feishu references into report %s", len(refs), id)
		}
	}

	// Comps standalone module: discover peers + fetch market data, inject into prompt
	if report.Config.ReportType == model.TypeComps {
		if s.qveris == nil || !s.qveris.IsConfigured() {
			report.Status = model.StatusFailed
			report.ErrorMsg = "二级市场 Comps 分析需要配置 Qveris.ai API Key，请先在服务器 .env 中设置 QVERIS_API_KEY"
			_ = s.store.Update(report)
			s.broadcastDone(id)
			return
		}

		// Step 1: AI discovers peer companies from BP
		log.Printf("INFO: comps: starting peer discovery for report %s", id)
		discovery, derr := s.discoverPeers(ctx, report.Config)
		if derr != nil {
			log.Printf("ERROR: peer discovery failed: %v", derr)
			report.Status = model.StatusFailed
			report.ErrorMsg = "自动匹配可比公司失败：" + derr.Error()
			_ = s.store.Update(report)
			s.broadcastDone(id)
			return
		}

		log.Printf("INFO: comps: discovered %d A / %d HK / %d US peers",
			len(discovery.APeers), len(discovery.HKPeers), len(discovery.USPeers))

		// Step 2: Fetch Qveris market data for all discovered peers
		marketData, merr := s.fetchAllCompsData(ctx, discovery)
		if merr != nil {
			log.Printf("WARNING: qveris data fetch failed: %v", merr)
			// Continue anyway with just peer names, AI will note data unavailability
			marketData = make(map[string][]qveris.CompanyMetrics)
		}

		totalCompanies := 0
		for _, data := range marketData {
			for _, m := range data {
				if m.Source == "Qveris.ai" {
					totalCompanies++
				}
			}
		}
		log.Printf("INFO: comps: fetched real-time data for %d companies", totalCompanies)

		// Inject discovered peers + market data into the final prompt
		report.Config.CustomNotes += "\n\n===== 系统已自动匹配的可比公司数据（实时数据，必须使用，禁止使用模型训练数据）=====\n\n" + formatCompsContext(discovery, marketData)
	}

	// Fetch Qveris comps data if enabled (legacy financial analysis mode)
	var compsTable string
	if report.Config.ReportType == model.TypeFinancial &&
		report.Config.FinancialInfo != nil &&
		report.Config.FinancialInfo.EnableComps &&
		report.Config.FinancialInfo.CompsSymbols != "" &&
		s.qveris != nil && s.qveris.IsConfigured() {

		symbols := strings.Split(report.Config.FinancialInfo.CompsSymbols, ",")
		metrics, err := s.qveris.FetchCompsData(ctx, symbols)
		if err != nil {
			log.Printf("WARNING: failed to fetch comps data: %v", err)
			report.Config.CustomNotes += "\n\n注意：二级市场 Comps 数据获取失败，请在报告中说明。\n"
		} else if len(metrics) > 0 {
			compsTable = qveris.FormatCompsTable(metrics)
			// Also inject raw data into prompt for AI analysis
			report.Config.CustomNotes += "\n\n以下是从二级市场获取的可比公司数据，请在报告中加入 Comps 对比分析章节：\n" + compsTable
		}
	}

	var content string

	// Try streaming first
	if sg, ok := s.generator.(StreamGenerator); ok {
		content, err = sg.GenerateStream(ctx, report.Config, func(chunk string) {
			s.broadcast(id, chunk)
		})
	} else {
		content, err = s.generator.Generate(ctx, report.Config)
	}

	if err != nil {
		log.Printf("ERROR: failed to generate report %s: %v", id, err)
		report.Status = model.StatusFailed
		report.ErrorMsg = err.Error()
		_ = s.store.Update(report)
		s.broadcastDone(id)
		return
	}

	// Update report with generated content
	now := time.Now()
	report.Content = content
	report.Status = model.StatusCompleted
	report.CompletedAt = &now
	report.Title = buildTitle(report.Config)

	if err := s.store.Update(report); err != nil {
		log.Printf("ERROR: failed to update report %s with content: %v", id, err)
	}

	s.broadcastDone(id)
	log.Printf("INFO: report %s generated successfully", id)
}

// buildTitle generates a title from project name + report type.
func buildTitle(config model.ReportConfig) string {
	typeName := "行业研究报告"
	switch config.ReportType {
	case model.TypePreDD:
		typeName = "Pre-DD 尽调"
	case model.TypeInvestmentMemo:
		typeName = "立项报告"
	case model.TypeFinancial:
		typeName = "财务分析"
	case model.TypeComps:
		typeName = "二级市场Comps分析"
	}
	return config.Topic + " - " + typeName
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func generateID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
