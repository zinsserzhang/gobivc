package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
	"github.com/zinsserzhang/gobivc/internal/model"
)

// SQLiteStore is a SQLite-backed implementation of ReportStore.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates and initializes a new SQLite store.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS reports (
		id           TEXT PRIMARY KEY,
		config_json  TEXT NOT NULL,
		title        TEXT NOT NULL DEFAULT '',
		summary      TEXT NOT NULL DEFAULT '',
		content      TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL DEFAULT 'pending',
		error_msg    TEXT NOT NULL DEFAULT '',
		created_at   DATETIME NOT NULL,
		completed_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_reports_status ON reports(status);
	CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at);
	`
	_, err := s.db.Exec(query)
	return err
}

func (s *SQLiteStore) Save(report *model.Report) error {
	configJSON, err := json.Marshal(report.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO reports (id, config_json, title, summary, content, status, error_msg, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.ID, string(configJSON), report.Title, report.Summary,
		report.Content, string(report.Status), report.ErrorMsg,
		report.CreatedAt, report.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert report: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Get(id string) (*model.Report, error) {
	row := s.db.QueryRow(`
		SELECT id, config_json, title, summary, content, status, error_msg, created_at, completed_at
		FROM reports WHERE id = ?`, id)
	return s.scanReport(row)
}

func (s *SQLiteStore) List() ([]*model.Report, error) {
	rows, err := s.db.Query(`
		SELECT id, config_json, title, summary, content, status, error_msg, created_at, completed_at
		FROM reports ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query reports: %w", err)
	}
	defer rows.Close()

	var reports []*model.Report
	for rows.Next() {
		r, err := s.scanReportRows(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// Search returns reports matching the query string in topic, direction, title, or content.
func (s *SQLiteStore) Search(query string) ([]*model.Report, error) {
	pattern := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT id, config_json, title, summary, content, status, error_msg, created_at, completed_at
		FROM reports
		WHERE title LIKE ? OR content LIKE ? OR config_json LIKE ?
		ORDER BY created_at DESC`, pattern, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to search reports: %w", err)
	}
	defer rows.Close()

	var reports []*model.Report
	for rows.Next() {
		r, err := s.scanReportRows(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

func (s *SQLiteStore) Delete(id string) error {
	result, err := s.db.Exec("DELETE FROM reports WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete report: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("report %s not found", id)
	}
	return nil
}

func (s *SQLiteStore) Update(report *model.Report) error {
	configJSON, err := json.Marshal(report.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	result, err := s.db.Exec(`
		UPDATE reports SET config_json=?, title=?, summary=?, content=?, status=?, error_msg=?, completed_at=?
		WHERE id=?`,
		string(configJSON), report.Title, report.Summary,
		report.Content, string(report.Status), report.ErrorMsg,
		report.CompletedAt, report.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update report: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("report %s not found", report.ID)
	}
	return nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// DB exposes the underlying *sql.DB so other packages (e.g. auth) can share
// the same database file without opening a second handle.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// scanner interface shared by sql.Row and sql.Rows
type scanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteStore) scanReport(row *sql.Row) (*model.Report, error) {
	r := &model.Report{}
	var configJSON string
	var status string
	var completedAt sql.NullTime

	err := row.Scan(&r.ID, &configJSON, &r.Title, &r.Summary, &r.Content,
		&status, &r.ErrorMsg, &r.CreatedAt, &completedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("report not found")
		}
		return nil, fmt.Errorf("failed to scan report: %w", err)
	}

	if err := json.Unmarshal([]byte(configJSON), &r.Config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	r.Status = model.ReportStatus(status)
	if completedAt.Valid {
		t := completedAt.Time
		r.CompletedAt = &t
	}
	return r, nil
}

func (s *SQLiteStore) scanReportRows(rows *sql.Rows) (*model.Report, error) {
	r := &model.Report{}
	var configJSON string
	var status string
	var completedAt sql.NullTime

	err := rows.Scan(&r.ID, &configJSON, &r.Title, &r.Summary, &r.Content,
		&status, &r.ErrorMsg, &r.CreatedAt, &completedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to scan report: %w", err)
	}

	if err := json.Unmarshal([]byte(configJSON), &r.Config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	r.Status = model.ReportStatus(status)
	if completedAt.Valid {
		t := completedAt.Time
		r.CompletedAt = &t
	}
	return r, nil
}

// ListByStatus returns reports filtered by status.
func (s *SQLiteStore) ListByStatus(status model.ReportStatus) ([]*model.Report, error) {
	rows, err := s.db.Query(`
		SELECT id, config_json, title, summary, content, status, error_msg, created_at, completed_at
		FROM reports WHERE status = ? ORDER BY created_at DESC`, string(status))
	if err != nil {
		return nil, fmt.Errorf("failed to query reports: %w", err)
	}
	defer rows.Close()

	var reports []*model.Report
	for rows.Next() {
		r, err := s.scanReportRows(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// RecoverPendingReports finds reports stuck in pending/generating state (e.g., from a crash)
// and marks them as failed.
func (s *SQLiteStore) RecoverPendingReports() error {
	now := time.Now()
	_, err := s.db.Exec(`
		UPDATE reports SET status = 'failed', error_msg = '服务重启，报告生成中断', completed_at = ?
		WHERE status IN ('pending', 'generating')`, now)
	return err
}
