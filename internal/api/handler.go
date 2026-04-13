package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zinsserzhang/gobivc/internal/model"
	"github.com/zinsserzhang/gobivc/internal/service"
)

// Handler holds HTTP handler methods for the report API.
type Handler struct {
	svc *service.ReportService
}

// NewHandler creates a new Handler.
func NewHandler(svc *service.ReportService) *Handler {
	return &Handler{svc: svc}
}

// jsonResponse writes a JSON response with the given status code.
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// errorResponse writes a JSON error response.
func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

// FeishuEnabled tracks whether Feishu integration is active.
var FeishuEnabled bool

// Health handles GET /health and /api/health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"time":          time.Now().UTC().Format(time.RFC3339),
		"feishu_enabled": FeishuEnabled,
	})
}

// CreateReport handles POST /api/reports
func (h *Handler) CreateReport(w http.ResponseWriter, r *http.Request) {
	var req model.CreateReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if msg := req.Validate(); msg != "" {
		errorResponse(w, http.StatusBadRequest, msg)
		return
	}

	config := model.ReportConfig{
		Topic:       req.Topic,
		Direction:   req.Direction,
		Depth:       req.Depth,
		CustomNotes: req.CustomNotes,
		UseFeishu:   req.UseFeishu,
	}

	report, err := h.svc.CreateReport(config)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create report")
		return
	}

	jsonResponse(w, http.StatusCreated, report)
}

// GetReport handles GET /api/reports/{id}
func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/reports/")
	if id == "" {
		errorResponse(w, http.StatusBadRequest, "report ID is required")
		return
	}

	report, err := h.svc.GetReport(id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "report not found")
		return
	}

	jsonResponse(w, http.StatusOK, report)
}

// ListReports handles GET /api/reports
func (h *Handler) ListReports(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")

	var reports []*model.Report
	var err error

	if query != "" {
		reports, err = h.svc.SearchReports(query)
	} else {
		reports, err = h.svc.ListReports()
	}

	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list reports")
		return
	}

	items := make([]model.ReportListItem, 0, len(reports))
	for _, r := range reports {
		items = append(items, r.ToListItem())
	}

	jsonResponse(w, http.StatusOK, items)
}

// DeleteReport handles DELETE /api/reports/{id}
func (h *Handler) DeleteReport(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/reports/")
	if id == "" {
		errorResponse(w, http.StatusBadRequest, "report ID is required")
		return
	}

	if err := h.svc.DeleteReport(id); err != nil {
		errorResponse(w, http.StatusNotFound, "report not found")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"message": "report deleted"})
}

// StreamReport handles GET /api/reports/{id}/stream (SSE)
func (h *Handler) StreamReport(w http.ResponseWriter, r *http.Request) {
	// Extract ID: path is /api/reports/{id}/stream
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/reports/")
	path = strings.TrimSuffix(path, "/stream")
	id := path

	if id == "" {
		errorResponse(w, http.StatusBadRequest, "report ID is required")
		return
	}

	// Check the report exists
	report, err := h.svc.GetReport(id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "report not found")
		return
	}

	// If already completed, send content directly
	if report.Status == model.StatusCompleted {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]string{"type": "content", "text": report.Content}))
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]string{"type": "done"}))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	if report.Status == model.StatusFailed {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]string{"type": "error", "message": report.ErrorMsg}))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Subscribe to streaming updates
	ch := h.svc.Subscribe(id)
	defer h.svc.Unsubscribe(id, ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		errorResponse(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Send initial event
	fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]string{"type": "start"}))
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				// Channel closed = generation done
				fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]string{"type": "done"}))
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]string{"type": "chunk", "text": chunk}))
			flusher.Flush()
		}
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// extractID extracts the ID portion from a URL path given a prefix.
func extractID(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(id, "/")
	// Don't return if it contains sub-paths
	if strings.Contains(id, "/") {
		return ""
	}
	return id
}
