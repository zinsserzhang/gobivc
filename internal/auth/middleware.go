package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// ctxKey is a private type for request-context keys.
type ctxKey string

const (
	ctxUserKey    ctxKey = "user"
	ctxSessionKey ctxKey = "session"
)

// UserFromContext returns the authenticated user, or nil.
func UserFromContext(ctx context.Context) *User {
	if v, ok := ctx.Value(ctxUserKey).(*User); ok {
		return v
	}
	return nil
}

// SessionFromContext returns the active session, or nil.
func SessionFromContext(ctx context.Context) *Session {
	if v, ok := ctx.Value(ctxSessionKey).(*Session); ok {
		return v
	}
	return nil
}

// publicPaths are reachable without a session.
func isPublicPath(p string) bool {
	switch p {
	case "/health", "/api/health",
		"/login",
		"/api/auth/login-url",
		"/api/auth/callback",
		"/api/auth/me",
		"/api/auth/logout":
		return true
	}
	if strings.HasPrefix(p, "/static/") {
		return true
	}
	return false
}

// RequireSession is middleware that blocks /api/* calls without a valid session.
// It also sets the user on the request context for handlers that want it.
//
// A service-account Bearer token (apiToken) is still accepted for
// backend-to-backend calls when the header is present.
func (m *Manager) RequireSession(apiToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Non-API page routes: redirect to /login if not authenticated.
			if !strings.HasPrefix(path, "/api/") {
				if isPublicPath(path) {
					next.ServeHTTP(w, r)
					return
				}
				if _, _, err := m.Authenticate(SessionIDFromRequest(r)); err != nil {
					// Preserve original destination so we can return post-login.
					to := "/login"
					if r.URL.Path != "/" {
						to += "?next=" + r.URL.Path
					}
					http.Redirect(w, r, to, http.StatusSeeOther)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// API routes:
			if isPublicPath(path) {
				next.ServeHTTP(w, r)
				return
			}

			// Accept service-account bearer token first (backend callers).
			if apiToken != "" {
				token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if token == "" {
					token = r.URL.Query().Get("token")
				}
				if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(apiToken)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
			}

			user, sess, err := m.Authenticate(SessionIDFromRequest(r))
			if err != nil {
				writeUnauthorized(w, "请先登录")
				return
			}
			ctx := context.WithValue(r.Context(), ctxUserKey, user)
			ctx = context.WithValue(ctx, ctxSessionKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// StartCleanupLoop runs a background goroutine that purges expired sessions.
func (m *Manager) StartCleanupLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := m.Store.CleanupExpiredSessions()
				if err != nil {
					log.Printf("WARNING: session cleanup: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("INFO: cleaned %d expired sessions", n)
				}
			}
		}
	}()
}
