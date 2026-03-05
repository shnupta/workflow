package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/models"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	dsn := path + "?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000"
	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(10)
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id           TEXT PRIMARY KEY,
			title        TEXT NOT NULL,
			description  TEXT NOT NULL DEFAULT '',
			work_type    TEXT NOT NULL,
			tier         TEXT NOT NULL,
			direction    TEXT NOT NULL,
			pr_url       TEXT NOT NULL DEFAULT '',
			brief        TEXT NOT NULL DEFAULT '',
			brief_status TEXT NOT NULL DEFAULT '',
			link         TEXT NOT NULL DEFAULT '',
			done         INTEGER NOT NULL DEFAULT 0,
			position     INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL,
			done_at      TEXT
		)
	`)
	if err != nil {
		return err
	}
	// Safe migrations for existing databases
	for _, col := range []string{
		`ALTER TABLE tasks ADD COLUMN position     INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN brief        TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN brief_status TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = d.conn.Exec(col) // ignore "duplicate column" errors
	}

	// Migrate old pr_summary → brief for existing rows
	_, _ = d.conn.Exec(`
		UPDATE tasks SET brief = pr_summary, brief_status = 'done'
		WHERE brief = '' AND pr_summary != '' AND pr_summary IS NOT NULL
	`)

	_, err = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id               TEXT PRIMARY KEY,
			task_id          TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			parent_id        TEXT REFERENCES sessions(id) ON DELETE SET NULL,
			name             TEXT NOT NULL DEFAULT '',
			mode             TEXT NOT NULL DEFAULT 'interactive',
			status           TEXT NOT NULL DEFAULT 'pending',
			agent_provider   TEXT NOT NULL DEFAULT 'claude_local',
			agent_session_id TEXT,
			error_message    TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL
		)
	`)
	if err != nil {
		return err
	}

	_, err = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id         TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role       TEXT NOT NULL,
			kind       TEXT NOT NULL DEFAULT 'text',
			content    TEXT NOT NULL DEFAULT '',
			tool_name  TEXT NOT NULL DEFAULT '',
			metadata   TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)
	`)
	return err
}

// ─────────────────────────────────────────────────────────
// Tasks
// ─────────────────────────────────────────────────────────

func (d *DB) CreateTask(t *models.Task) error {
	t.ID = uuid.New().String()
	t.CreatedAt = time.Now()
	t.UpdatedAt = time.Now()

	var maxPos int
	d.conn.QueryRow(`SELECT COALESCE(MAX(position), -1) FROM tasks WHERE tier=? AND done=0`, t.Tier).Scan(&maxPos)
	t.Position = maxPos + 1

	_, err := d.conn.Exec(`
		INSERT INTO tasks (id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.Brief, t.BriefStatus, t.Link, t.Done, t.Position,
		t.CreatedAt.UTC().Format(time.RFC3339), t.UpdatedAt.UTC().Format(time.RFC3339), nil,
	)
	return err
}

func (d *DB) UpdateTask(t *models.Task) error {
	t.UpdatedAt = time.Now()
	var doneAt interface{}
	if t.DoneAt != nil {
		doneAt = t.DoneAt.UTC().Format(time.RFC3339)
	}
	_, err := d.conn.Exec(`
		UPDATE tasks SET title=?, description=?, work_type=?, tier=?, direction=?,
		pr_url=?, brief=?, brief_status=?, link=?, done=?, position=?, updated_at=?, done_at=?
		WHERE id=?`,
		t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.Brief, t.BriefStatus, t.Link, t.Done, t.Position,
		t.UpdatedAt.UTC().Format(time.RFC3339), doneAt, t.ID,
	)
	return err
}

func (d *DB) GetTask(id string) (*models.Task, error) {
	row := d.conn.QueryRow(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at
		FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

func (d *DB) ListTasks(includeDone bool, cfg *config.Config) ([]*models.Task, error) {
	tierOrder := ""
	for _, t := range cfg.Tiers {
		tierOrder += fmt.Sprintf("WHEN '%s' THEN %d ", t.Key, t.Order)
	}
	tierOrder += "ELSE 99"

	where := "WHERE done=0"
	if includeDone {
		where = ""
	}

	q := fmt.Sprintf(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at
		FROM tasks %s
		ORDER BY CASE tier %s END, position ASC, updated_at DESC`, where, tierOrder)

	rows, err := d.conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*models.Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (d *DB) DeleteTask(id string) error {
	_, err := d.conn.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

func (d *DB) MarkDone(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET done=1, done_at=?, updated_at=? WHERE id=?`, now, now, id)
	return err
}

func (d *DB) UpdateBrief(id, brief, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET brief=?, brief_status=?, updated_at=? WHERE id=?`, brief, status, now, id)
	return err
}

func (d *DB) MoveTask(id, tier, beforeID string) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	var targetPos int
	if beforeID == "" {
		tx.QueryRow(`SELECT COALESCE(MAX(position), -1) FROM tasks WHERE tier=? AND done=0 AND id!=?`, tier, id).Scan(&targetPos)
		targetPos++
	} else {
		if err := tx.QueryRow(`SELECT position FROM tasks WHERE id=?`, beforeID).Scan(&targetPos); err != nil {
			return fmt.Errorf("before task not found: %w", err)
		}
		_, err = tx.Exec(`UPDATE tasks SET position=position+1, updated_at=? WHERE tier=? AND done=0 AND id!=? AND position>=?`,
			now, tier, id, targetPos)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE tasks SET tier=?, position=?, updated_at=? WHERE id=?`, tier, targetPos, now, id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func scanTask(row *sql.Row) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt *string
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position, &createdAt, &updatedAt, &doneAt)
	if err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if doneAt != nil {
		da, _ := time.Parse(time.RFC3339, *doneAt)
		t.DoneAt = &da
	}
	return &t, nil
}

func scanTaskRow(rows *sql.Rows) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt *string
	err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position, &createdAt, &updatedAt, &doneAt)
	if err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if doneAt != nil {
		da, _ := time.Parse(time.RFC3339, *doneAt)
		t.DoneAt = &da
	}
	return &t, nil
}

// ─────────────────────────────────────────────────────────
// Sessions
// ─────────────────────────────────────────────────────────

func (d *DB) CreateSession(s *models.Session) error {
	s.ID = uuid.New().String()
	s.CreatedAt = time.Now()
	s.UpdatedAt = time.Now()
	if s.Status == "" {
		s.Status = models.SessionStatusPending
	}
	if s.AgentProvider == "" {
		s.AgentProvider = "claude_local"
	}
	var parentID interface{}
	if s.ParentID != nil {
		parentID = *s.ParentID
	}
	var agentSessionID interface{}
	if s.AgentSessionID != nil {
		agentSessionID = *s.AgentSessionID
	}
	_, err := d.conn.Exec(`
		INSERT INTO sessions (id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TaskID, parentID, s.Name, s.Mode, s.Status, s.AgentProvider,
		agentSessionID, s.ErrorMessage,
		s.CreatedAt.UTC().Format(time.RFC3339), s.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) UpdateSessionStatus(id string, status models.SessionStatus, errMsg string) error {
	_, err := d.conn.Exec(`UPDATE sessions SET status=?, error_message=?, updated_at=? WHERE id=?`,
		status, errMsg, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (d *DB) UpdateSessionAgentID(id, agentSessionID string) error {
	_, err := d.conn.Exec(`UPDATE sessions SET agent_session_id=?, updated_at=? WHERE id=?`,
		agentSessionID, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (d *DB) GetSession(id string) (*models.Session, error) {
	row := d.conn.QueryRow(`
		SELECT id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, created_at, updated_at
		FROM sessions WHERE id=?`, id)
	return scanSession(row)
}

func (d *DB) ListSessions(taskID string) ([]*models.Session, error) {
	rows, err := d.conn.Query(`
		SELECT id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, created_at, updated_at
		FROM sessions WHERE task_id=? AND name != '[brief]' ORDER BY updated_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanSession(row sessionScanner) (*models.Session, error) {
	var s models.Session
	var parentID, agentSessionID sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(
		&s.ID, &s.TaskID, &parentID, &s.Name, &s.Mode, &s.Status,
		&s.AgentProvider, &agentSessionID, &s.ErrorMessage, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if parentID.Valid {
		s.ParentID = &parentID.String
	}
	if agentSessionID.Valid {
		s.AgentSessionID = &agentSessionID.String
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &s, nil
}

// ─────────────────────────────────────────────────────────
// Messages
// ─────────────────────────────────────────────────────────

func (d *DB) CreateMessage(m *models.Message) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	_, err := d.conn.Exec(`
		INSERT INTO messages (id, session_id, role, kind, content, tool_name, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.Role, m.Kind, m.Content, m.ToolName, m.Metadata,
		m.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) ListMessages(sessionID string) ([]*models.Message, error) {
	rows, err := d.conn.Query(`
		SELECT id, session_id, role, kind, content, tool_name, metadata, created_at
		FROM messages WHERE session_id=? ORDER BY rowid ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (d *DB) ListMessagesSince(sessionID, afterID string) ([]*models.Message, error) {
	var afterRowid int64
	if afterID != "" {
		d.conn.QueryRow(`SELECT rowid FROM messages WHERE id=?`, afterID).Scan(&afterRowid)
	}
	var rows *sql.Rows
	var err error
	if afterRowid > 0 {
		rows, err = d.conn.Query(`
			SELECT id, session_id, role, kind, content, tool_name, metadata, created_at
			FROM messages WHERE session_id=? AND rowid > ? ORDER BY rowid ASC`, sessionID, afterRowid)
	} else {
		rows, err = d.conn.Query(`
			SELECT id, session_id, role, kind, content, tool_name, metadata, created_at
			FROM messages WHERE session_id=? ORDER BY rowid ASC`, sessionID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func scanMessages(rows *sql.Rows) ([]*models.Message, error) {
	var out []*models.Message
	for rows.Next() {
		var m models.Message
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Kind, &m.Content, &m.ToolName, &m.Metadata, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}
