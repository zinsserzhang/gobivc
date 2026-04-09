package api

import (
	"net/http"
	"strings"
)

// NewRouter creates the HTTP router (mux) for the application.
func NewRouter(handler *Handler) http.Handler {
	mux := http.NewServeMux()

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

	// Serve index.html for the root path and any non-API paths (SPA fallback)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/static/") {
			http.ServeFile(w, r, "web/templates/index.html")
			return
		}
		if r.URL.Path == "/" {
			http.ServeFile(w, r, "web/templates/index.html")
			return
		}
	})

	return mux
}
