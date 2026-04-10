package api

import (
	"net/http"
	"strings"
)

// NewRouter creates the HTTP router (mux) for the application.
func NewRouter(handler *Handler) http.Handler {
	mux := http.NewServeMux()

	// Health check (unauthenticated)
	mux.HandleFunc("/health", handler.Health)
	mux.HandleFunc("/api/health", handler.Health)

	// API routes
	mux.HandleFunc("/api/reports", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.ListReports(w, r)
		case http.MethodPost:
			handler.CreateReport(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/reports/", func(w http.ResponseWriter, r *http.Request) {
		// Stream endpoint: /api/reports/{id}/stream
		if strings.HasSuffix(r.URL.Path, "/stream") {
			handler.StreamReport(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			handler.GetReport(w, r)
		case http.MethodDelete:
			handler.DeleteReport(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Serve index.html for the root and non-API/non-static paths
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || (!strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/static/") && r.URL.Path != "/health") {
			http.ServeFile(w, r, "web/templates/index.html")
			return
		}
	})

	return mux
}
