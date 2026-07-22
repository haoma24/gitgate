// Package daemon provides the GitGate background daemon, its SQLite state store,
// and OS-specific service management.
package daemon

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// RunStatus represents the lifecycle state of a pipeline run.
type RunStatus string

const (
	RunStatusPending RunStatus = "pending"
	RunStatusRunning RunStatus = "running"
	RunStatusWaiting RunStatus = "waiting" // awaiting user input
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
	RunStatusAborted RunStatus = "aborted"
	RunStatusCrash   RunStatus = "crash" // daemon restarted mid-run
)

// Run holds metadata about a single pipeline execution.
type Run struct {
	ID           string    `json:"id"`
	BareRepo     string    `json:"bare_repo"`
	Ref          string    `json:"ref"`
	Branch       string    `json:"branch"`
	OldSHA       string    `json:"old_sha"`
	NewSHA       string    `json:"new_sha"`
	Status       RunStatus `json:"status"`
	CurrentStep  string    `json:"current_step"`
	Intent       string    `json:"intent"`
	WorktreePath string    `json:"worktree_path"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// LogEntry holds a single log line from a run.
type LogEntry struct {
	ID        int64
	RunID     string
	Timestamp time.Time
	Level     string
	Step      string
	Message   string
}

// Finding holds a single AI review finding.
type Finding struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id"`
	Severity    string    `json:"severity"` // critical, high, medium, low
	File        string    `json:"file"`
	Line        int       `json:"line"`
	Description string    `json:"description"`
	Action      string    `json:"action"` // auto-fix, ask-user
	Status      string    `json:"status"` // pending, applied, skipped
	CreatedAt   time.Time `json:"created_at"`
}

// DB wraps the SQLite connection and provides typed query methods.
type DB struct {
	sql *sql.DB
}

// OpenDB opens (or creates) the GitGate SQLite database.
func OpenDB(ggHome string) (*DB, error) {
	dbDir := filepath.Join(ggHome, "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dbPath := filepath.Join(dbDir, "gitgate.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL mode for better concurrent access
	if _, err := sqlDB.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	db := &DB{sql: sqlDB}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.sql.Close()
}

// migrate applies the database schema.
func (db *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS runs (
    id           TEXT PRIMARY KEY,
    bare_repo    TEXT NOT NULL,
    ref          TEXT NOT NULL,
    branch       TEXT NOT NULL,
    old_sha      TEXT NOT NULL,
    new_sha      TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    current_step TEXT NOT NULL DEFAULT '',
    intent       TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC);

CREATE TABLE IF NOT EXISTS findings (
    id           TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    severity     TEXT NOT NULL,
    file         TEXT NOT NULL,
    line         INTEGER NOT NULL DEFAULT 0,
    description  TEXT NOT NULL,
    action       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    created_at   DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_findings_run ON findings(run_id);

CREATE TABLE IF NOT EXISTS logs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       TEXT REFERENCES runs(id) ON DELETE CASCADE,
    timestamp    DATETIME NOT NULL,
    level        TEXT NOT NULL,
    step         TEXT NOT NULL DEFAULT '',
    message      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_logs_run ON logs(run_id);
CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp DESC);
`
	_, err := db.sql.Exec(schema)
	return err
}

// CreateRun inserts a new run record into the database.
func (db *DB) CreateRun(run *Run) error {
	now := time.Now().UTC()
	run.CreatedAt = now
	run.UpdatedAt = now
	run.Status = RunStatusPending

	_, err := db.sql.Exec(`
		INSERT INTO runs (id, bare_repo, ref, branch, old_sha, new_sha, status, current_step, intent, worktree_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.BareRepo, run.Ref, run.Branch, run.OldSHA, run.NewSHA,
		run.Status, run.CurrentStep, run.Intent, run.WorktreePath,
		run.CreatedAt, run.UpdatedAt,
	)
	return err
}

// UpdateRun updates mutable fields of a run.
func (db *DB) UpdateRun(run *Run) error {
	run.UpdatedAt = time.Now().UTC()
	_, err := db.sql.Exec(`
		UPDATE runs SET status=?, current_step=?, intent=?, worktree_path=?, updated_at=?
		WHERE id=?`,
		run.Status, run.CurrentStep, run.Intent, run.WorktreePath, run.UpdatedAt, run.ID,
	)
	return err
}

// GetRun fetches a single run by ID.
func (db *DB) GetRun(id string) (*Run, error) {
	row := db.sql.QueryRow(`
		SELECT id, bare_repo, ref, branch, old_sha, new_sha, status, current_step, intent, worktree_path, created_at, updated_at
		FROM runs WHERE id=?`, id)
	return scanRun(row)
}

// ListRuns returns the N most recent runs.
func (db *DB) ListRuns(limit int) ([]*Run, error) {
	rows, err := db.sql.Query(`
		SELECT id, bare_repo, ref, branch, old_sha, new_sha, status, current_step, intent, worktree_path, created_at, updated_at
		FROM runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ListActiveRuns returns all runs that are currently in a non-terminal state.
func (db *DB) ListActiveRuns() ([]*Run, error) {
	rows, err := db.sql.Query(`
		SELECT id, bare_repo, ref, branch, old_sha, new_sha, status, current_step, intent, worktree_path, created_at, updated_at
		FROM runs WHERE status IN ('pending', 'running', 'waiting')
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// MarkCrashedRuns marks all running/pending runs as crashed (called on daemon startup).
func (db *DB) MarkCrashedRuns() error {
	_, err := db.sql.Exec(`
		UPDATE runs SET status='crash', updated_at=?
		WHERE status IN ('running', 'pending')`,
		time.Now().UTC(),
	)
	return err
}

// AddFinding inserts a finding for a run.
func (db *DB) AddFinding(f *Finding) error {
	now := time.Now().UTC()
	f.CreatedAt = now
	f.Status = "pending"

	_, err := db.sql.Exec(`
		INSERT INTO findings (id, run_id, severity, file, line, description, action, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.RunID, f.Severity, f.File, f.Line, f.Description, f.Action, f.Status, f.CreatedAt,
	)
	return err
}

// UpdateFindingStatus updates the status of a finding.
func (db *DB) UpdateFindingStatus(findingID, status string) error {
	_, err := db.sql.Exec(`UPDATE findings SET status=? WHERE id=?`, status, findingID)
	return err
}

// GetFindings returns all findings for a run.
func (db *DB) GetFindings(runID string) ([]*Finding, error) {
	rows, err := db.sql.Query(`
		SELECT id, run_id, severity, file, line, description, action, status, created_at
		FROM findings WHERE run_id=? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []*Finding
	for rows.Next() {
		f := &Finding{}
		if err := rows.Scan(&f.ID, &f.RunID, &f.Severity, &f.File, &f.Line,
			&f.Description, &f.Action, &f.Status, &f.CreatedAt); err != nil {
			return nil, err
		}
		findings = append(findings, f)
	}
	return findings, rows.Err()
}

// AddLog appends a log entry.
func (db *DB) AddLog(runID, level, step, message string) error {
	_, err := db.sql.Exec(`
		INSERT INTO logs (run_id, timestamp, level, step, message)
		VALUES (?, ?, ?, ?, ?)`,
		runID, time.Now().UTC(), level, step, message,
	)
	return err
}

// GetLogs returns recent log entries, optionally filtered by runID.
func (db *DB) GetLogs(runID string, limit int) ([]*LogEntry, error) {
	var rows *sql.Rows
	var err error

	if runID != "" {
		rows, err = db.sql.Query(`
			SELECT id, run_id, timestamp, level, step, message
			FROM logs WHERE run_id=? ORDER BY timestamp DESC LIMIT ?`, runID, limit)
	} else {
		rows, err = db.sql.Query(`
			SELECT id, run_id, timestamp, level, step, message
			FROM logs ORDER BY timestamp DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*LogEntry
	for rows.Next() {
		e := &LogEntry{}
		if err := rows.Scan(&e.ID, &e.RunID, &e.Timestamp, &e.Level, &e.Step, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// scanRun scans a row into a Run struct.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (*Run, error) {
	r := &Run{}
	err := row.Scan(
		&r.ID, &r.BareRepo, &r.Ref, &r.Branch, &r.OldSHA, &r.NewSHA,
		&r.Status, &r.CurrentStep, &r.Intent, &r.WorktreePath,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}
