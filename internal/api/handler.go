package api

import (
	"encoding/json"
	"net/http"
	"strings"

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

// CreateReport handles POST /api/reports
func (h *Handler) CreateReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
	if r.Method != http.MethodGet {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
	if r.Method != http.MethodGet {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	reports, err := h.svc.ListReports()
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
	if r.Method != http.MethodDelete {
		errorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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

// extractID extracts the ID portion from a URL path given a prefix.
func extractID(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	// Remove trailing slash if present
	id = strings.TrimSuffix(id, "/")
	return id
}
