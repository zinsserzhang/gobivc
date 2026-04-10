package api

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// Middleware is an HTTP handler wrapper.
type Middleware func(http.Handler) http.Handler

// Chain applies a list of middlewares to a handler in order.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// LoggingMiddleware logs each HTTP request with its method, path, status and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		log.Printf("[HTTP] %s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

// CORSMiddleware adds CORS headers. Origin defaults to * unless ALLOWED_ORIGIN is set.
func CORSMiddleware(allowedOrigin string) Middleware {
	if allowedOrigin == "" {
		allowedOrigin = "*"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AuthMiddleware checks for a bearer token on /api routes. If apiToken is empty, auth is disabled.
func AuthMiddleware(apiToken string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only protect API routes
			if !strings.HasPrefix(r.URL.Path, "/api/") || apiToken == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Allow SSE streams to also use token via query parameter
			// (EventSource doesn't support custom headers)
			token := ""
			if auth := r.Header.Get("Authorization"); auth != "" {
				token = strings.TrimPrefix(auth, "Bearer ")
			} else if qt := r.URL.Query().Get("token"); qt != "" {
				token = qt
			}

			if subtle.ConstantTimeCompare([]byte(token), []byte(apiToken)) != 1 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RecoverMiddleware recovers from panics in handlers.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %s %s: %v", r.Method, r.URL.Path, err)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush supports SSE through the wrapped writer.
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
