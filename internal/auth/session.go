package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// Session duration — Plan B:
//   - Activity-based renewal: on every request we push out last_active_at. If
//     the gap between "now" and "last_active_at" exceeds ActivityWindow we
//     force a re-login.
//   - Absolute expiry: ExpiresAt on the session is fixed at login + MaxLifetime.
//     Once past that, no amount of activity can extend the session.
const (
	SessionCookieName = "gobivc_session"
	ActivityWindow    = 7 * 24 * time.Hour
	MaxLifetime       = 30 * 24 * time.Hour
)

// Manager coordinates sessions, users, whitelist and cookies.
type Manager struct {
	Store     *Store
	Whitelist *Whitelist
	OAuth     *OAuthClient

	// CookieSecure controls the Secure attribute on the session cookie.
	// Should be true in production (HTTPS).
	CookieSecure bool
}

// NewManager builds a Manager.
func NewManager(store *Store, wl *Whitelist, oc *OAuthClient, cookieSecure bool) *Manager {
	return &Manager{
		Store:        store,
		Whitelist:    wl,
		OAuth:        oc,
		CookieSecure: cookieSecure,
	}
}

// Login creates a user + session from a Feishu identity.
// Returns the session ID (caller sets the cookie).
func (m *Manager) Login(fu *FeishuUser) (*Session, *User, error) {
	ok, _ := m.Whitelist.Allow(fu.Email, fu.OpenID, fu.UnionID, fu.Mobile)
	if !ok {
		return nil, nil, fmt.Errorf("您的飞书账号不在白名单中，请联系管理员")
	}

	u, err := m.Store.UpsertUser(&User{
		OpenID:    fu.OpenID,
		UnionID:   fu.UnionID,
		Name:      pickName(fu),
		Email:     fu.Email,
		Mobile:    fu.Mobile,
		AvatarURL: fu.AvatarURL,
	})
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:           newSessionID(),
		UserID:       u.ID,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(MaxLifetime),
	}
	if err := m.Store.CreateSession(sess); err != nil {
		return nil, nil, err
	}
	return sess, u, nil
}

// Authenticate validates a session ID and returns the user. It also touches
// last_active_at. Returns nil, nil when the session is invalid, expired,
// or stale (no activity within ActivityWindow).
func (m *Manager) Authenticate(sessionID string) (*User, *Session, error) {
	if sessionID == "" {
		return nil, nil, fmt.Errorf("no session")
	}
	sess, err := m.Store.GetSession(sessionID)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	if now.After(sess.ExpiresAt) {
		_ = m.Store.DeleteSession(sess.ID)
		return nil, nil, fmt.Errorf("session expired")
	}
	if now.Sub(sess.LastActiveAt) > ActivityWindow {
		_ = m.Store.DeleteSession(sess.ID)
		return nil, nil, fmt.Errorf("session inactive too long")
	}
	u, err := m.Store.GetUserByID(sess.UserID)
	if err != nil {
		_ = m.Store.DeleteSession(sess.ID)
		return nil, nil, err
	}
	if err := m.Store.TouchSession(sess.ID, now); err != nil {
		// non-fatal; just log upstream
		return u, sess, nil
	}
	sess.LastActiveAt = now
	return u, sess, nil
}

// Logout deletes a session.
func (m *Manager) Logout(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return m.Store.DeleteSession(sessionID)
}

// SetSessionCookie writes the session cookie to the response.
func (m *Manager) SetSessionCookie(w http.ResponseWriter, sessionID string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

// ClearSessionCookie erases the cookie on the client.
func (m *Manager) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionIDFromRequest extracts the session ID from either the cookie or
// the ?session= query parameter (used by EventSource which can't set cookies
// on cross-origin domains, but same-origin cookies already work — query
// fallback is for completeness).
func SessionIDFromRequest(r *http.Request) string {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if q := r.URL.Query().Get("session"); q != "" {
		return q
	}
	return ""
}

func newSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read only fails on catastrophic OS errors.
		panic(fmt.Sprintf("rand.Read: %v", err))
	}
	return hex.EncodeToString(b)
}

func pickName(fu *FeishuUser) string {
	if fu.Name != "" {
		return fu.Name
	}
	if fu.EnName != "" {
		return fu.EnName
	}
	return "Feishu User"
}
