package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPHandler wraps the HTTP endpoints for the auth flow.
type HTTPHandler struct {
	Manager *Manager

	// oauth-state cache: state -> expiry. Protects against CSRF on callback.
	mu     sync.Mutex
	states map[string]stateEntry
}

type stateEntry struct {
	expiresAt time.Time
	next      string // optional post-login redirect path
}

// NewHTTPHandler creates a handler.
func NewHTTPHandler(m *Manager) *HTTPHandler {
	h := &HTTPHandler{Manager: m, states: map[string]stateEntry{}}
	go h.gcStates()
	return h
}

func (h *HTTPHandler) gcStates() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		h.mu.Lock()
		for k, v := range h.states {
			if now.After(v.expiresAt) {
				delete(h.states, k)
			}
		}
		h.mu.Unlock()
	}
}

// LoginURL returns the Feishu authorize URL for the client to redirect to.
//   GET /api/auth/login-url?next=/somewhere
// Response: {"url": "https://open.feishu.cn/...", "state": "..."}
func (h *HTTPHandler) LoginURL(w http.ResponseWriter, r *http.Request) {
	if !h.Manager.OAuth.IsConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "飞书登录未配置"})
		return
	}
	state := newState()
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") { // must be same-site relative path
		next = ""
	}
	h.mu.Lock()
	h.states[state] = stateEntry{expiresAt: time.Now().Add(10 * time.Minute), next: next}
	h.mu.Unlock()

	url := h.Manager.OAuth.AuthorizeURL(state)
	writeJSON(w, http.StatusOK, map[string]string{"url": url, "state": state})
}

// Callback handles the redirect from Feishu after the user scans.
//   GET /api/auth/callback?code=xxx&state=xxx
// On success it sets the session cookie and 303-redirects to "/".
func (h *HTTPHandler) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		h.redirectLogin(w, r, "missing code/state")
		return
	}

	h.mu.Lock()
	entry, ok := h.states[state]
	if ok {
		delete(h.states, state)
	}
	h.mu.Unlock()
	if !ok || time.Now().After(entry.expiresAt) {
		h.redirectLogin(w, r, "state expired, please try again")
		return
	}

	fu, err := h.Manager.OAuth.ExchangeCode(r.Context(), code)
	if err != nil {
		log.Printf("WARNING: feishu exchange: %v", err)
		h.redirectLogin(w, r, "飞书验证失败")
		return
	}

	sess, user, err := h.Manager.Login(fu)
	if err != nil {
		log.Printf("INFO: login rejected for %s (open_id=%s): %v", fu.Email, fu.OpenID, err)
		h.redirectLogin(w, r, err.Error())
		return
	}
	log.Printf("INFO: login ok user=%s email=%s open_id=%s", user.Name, user.Email, user.OpenID)

	h.Manager.SetSessionCookie(w, sess.ID, sess.ExpiresAt)

	dst := "/"
	if entry.next != "" {
		dst = entry.next
	}
	http.Redirect(w, r, dst, http.StatusSeeOther)
}

// Me returns the current user's profile, or 401.
func (h *HTTPHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, _, err := h.Manager.Authenticate(SessionIDFromRequest(r))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not logged in"})
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// Logout clears the session.
func (h *HTTPHandler) Logout(w http.ResponseWriter, r *http.Request) {
	sid := SessionIDFromRequest(r)
	_ = h.Manager.Logout(sid)
	h.Manager.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (h *HTTPHandler) redirectLogin(w http.ResponseWriter, r *http.Request, msg string) {
	q := ""
	if msg != "" {
		q = "?error=" + urlEscape(msg)
	}
	http.Redirect(w, r, "/login"+q, http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func newState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// urlEscape is a tiny query-string escaper limited to what we emit.
func urlEscape(s string) string {
	// Keep it simple: replace the handful of chars that break query strings.
	r := strings.NewReplacer(
		" ", "%20", "&", "%26", "?", "%3F", "#", "%23",
		"=", "%3D", "+", "%2B", "%", "%25",
	)
	return r.Replace(s)
}
