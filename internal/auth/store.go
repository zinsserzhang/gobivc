package auth

import (
	"database/sql"
	"fmt"
	"time"
)

// User represents an authenticated Feishu user.
type User struct {
	ID         int64     `json:"id"`
	OpenID     string    `json:"open_id"`
	UnionID    string    `json:"union_id,omitempty"`
	Name       string    `json:"name"`
	Email      string    `json:"email,omitempty"`
	Mobile     string    `json:"mobile,omitempty"`
	AvatarURL  string    `json:"avatar_url,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// Session represents an active login session.
type Session struct {
	ID           string
	UserID       int64
	CreatedAt    time.Time
	LastActiveAt time.Time
	ExpiresAt    time.Time // absolute expiry (Plan B: 30 days from creation)
}

// Store persists users and sessions to SQLite.
type Store struct {
	db *sql.DB
}

// NewStore initializes the auth store and migrates tables.
// It accepts an existing *sql.DB so users and sessions share the main DB file.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("auth migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS users (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		open_id      TEXT NOT NULL UNIQUE,
		union_id     TEXT NOT NULL DEFAULT '',
		name         TEXT NOT NULL DEFAULT '',
		email        TEXT NOT NULL DEFAULT '',
		mobile       TEXT NOT NULL DEFAULT '',
		avatar_url   TEXT NOT NULL DEFAULT '',
		created_at   DATETIME NOT NULL,
		last_seen_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

	CREATE TABLE IF NOT EXISTS sessions (
		id             TEXT PRIMARY KEY,
		user_id        INTEGER NOT NULL,
		created_at     DATETIME NOT NULL,
		last_active_at DATETIME NOT NULL,
		expires_at     DATETIME NOT NULL,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
	`)
	return err
}

// UpsertUser inserts or updates a user by open_id, returning the full row.
func (s *Store) UpsertUser(u *User) (*User, error) {
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO users (open_id, union_id, name, email, mobile, avatar_url, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(open_id) DO UPDATE SET
			union_id = excluded.union_id,
			name = excluded.name,
			email = excluded.email,
			mobile = excluded.mobile,
			avatar_url = excluded.avatar_url,
			last_seen_at = excluded.last_seen_at`,
		u.OpenID, u.UnionID, u.Name, u.Email, u.Mobile, u.AvatarURL, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return s.GetUserByOpenID(u.OpenID)
}

func (s *Store) GetUserByOpenID(openID string) (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, open_id, union_id, name, email, mobile, avatar_url, created_at, last_seen_at
		FROM users WHERE open_id = ?`, openID)
	return scanUser(row)
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, open_id, union_id, name, email, mobile, avatar_url, created_at, last_seen_at
		FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// CreateSession persists a new session row.
func (s *Store) CreateSession(sess *Session) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, user_id, created_at, last_active_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.CreatedAt, sess.LastActiveAt, sess.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession looks up a session by ID.
func (s *Store) GetSession(id string) (*Session, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, created_at, last_active_at, expires_at
		FROM sessions WHERE id = ?`, id)
	sess := &Session{}
	err := row.Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastActiveAt, &sess.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	return sess, nil
}

// TouchSession updates last_active_at to the given time.
func (s *Store) TouchSession(id string, t time.Time) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_active_at = ? WHERE id = ?`, t, id)
	return err
}

// DeleteSession removes a session by ID.
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// CleanupExpiredSessions deletes any session past its expires_at.
func (s *Store) CleanupExpiredSessions() (int64, error) {
	result, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func scanUser(row *sql.Row) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.OpenID, &u.UnionID, &u.Name, &u.Email, &u.Mobile, &u.AvatarURL, &u.CreatedAt, &u.LastSeenAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

// Sentinel errors.
var (
	ErrUserNotFound    = fmt.Errorf("user not found")
	ErrSessionNotFound = fmt.Errorf("session not found")
)
