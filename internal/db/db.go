package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/models"
	_ "github.com/mattn/go-sqlite3" // requires CGO; build with: go build -tags fts5
)

type DB struct {
	conn *sql.DB
	fts5 bool // true if FTS5 is available (built with -tags fts5)
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
		`ALTER TABLE tasks ADD COLUMN due_date      TEXT`,
		`ALTER TABLE tasks ADD COLUMN timer_started TEXT`,
		`ALTER TABLE tasks ADD COLUMN timer_total   INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN scratchpad    TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN blocked_by    TEXT`,
		`ALTER TABLE tasks ADD COLUMN recurrence   TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = d.conn.Exec(col) // ignore "duplicate column" errors
	}

	// Brief versions table — stores each completed brief run
	_, _ = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS brief_versions (
			id         TEXT PRIMARY KEY,
			task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			content    TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)
	`)

	// Notes table
	_, _ = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS notes (
			id         TEXT PRIMARY KEY,
			task_id    TEXT NOT NULL DEFAULT '',
			title      TEXT NOT NULL DEFAULT '',
			content    TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)

	// Session migrations
	for _, col := range []string{
		`ALTER TABLE sessions ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN pinned   INTEGER NOT NULL DEFAULT 0`,
	} {
		_, _ = d.conn.Exec(col)
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
			archived         INTEGER NOT NULL DEFAULT 0,
			pinned           INTEGER NOT NULL DEFAULT 0,
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
	if err != nil {
		return err
	}

	// FTS5 index over message content for session search.
	// FTS5 requires SQLite to be built with -DSQLITE_ENABLE_FTS5 or the Go
	// driver built with `go build -tags fts5`. We attempt the migration but
	// don't fatal if FTS5 is unavailable — search just won't work.
	if _, ftsErr := d.conn.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content,
			session_id UNINDEXED,
			content='messages',
			content_rowid='rowid'
		)
	`); ftsErr != nil {
		// Not fatal — log and skip FTS setup
		log.Printf("db: FTS5 not available (%v) — session search disabled. Build with: go build -tags fts5", ftsErr)
		d.fts5 = false
	} else {
		d.fts5 = true

		// Triggers to keep FTS index in sync
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
				INSERT INTO messages_fts(rowid, content, session_id) VALUES (new.rowid, new.content, new.session_id);
			END
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
				INSERT INTO messages_fts(messages_fts, rowid, content, session_id) VALUES ('delete', old.rowid, old.content, old.session_id);
			END
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
				INSERT INTO messages_fts(messages_fts, rowid, content, session_id) VALUES ('delete', old.rowid, old.content, old.session_id);
				INSERT INTO messages_fts(rowid, content, session_id) VALUES (new.rowid, new.content, new.session_id);
			END
		`)

		// Backfill FTS for any existing messages (safe to run multiple times)
		_, _ = d.conn.Exec(`
			INSERT OR IGNORE INTO messages_fts(rowid, content, session_id)
			SELECT rowid, content, session_id FROM messages
		`)
	}

	return nil
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

	var dueDate interface{}
	if t.DueDate != nil {
		dueDate = t.DueDate.Format("2006-01-02")
	}
	var blockedBy interface{}
	if t.BlockedBy != "" {
		blockedBy = t.BlockedBy
	}
	_, err := d.conn.Exec(`
		INSERT INTO tasks (id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.Brief, t.BriefStatus, t.Link, t.Done, t.Position,
		t.CreatedAt.UTC().Format(time.RFC3339), t.UpdatedAt.UTC().Format(time.RFC3339), nil, dueDate, nil, 0, "", blockedBy, t.Recurrence,
	)
	return err
}

func (d *DB) UpdateTask(t *models.Task) error {
	t.UpdatedAt = time.Now()
	var doneAt interface{}
	if t.DoneAt != nil {
		doneAt = t.DoneAt.UTC().Format(time.RFC3339)
	}
	var dueDate interface{}
	if t.DueDate != nil {
		dueDate = t.DueDate.Format("2006-01-02")
	}
	var timerStarted interface{}
	if t.TimerStarted != nil {
		timerStarted = t.TimerStarted.UTC().Format(time.RFC3339)
	}
	var blockedBy interface{}
	if t.BlockedBy != "" {
		blockedBy = t.BlockedBy
	}
	_, err := d.conn.Exec(`
		UPDATE tasks SET title=?, description=?, work_type=?, tier=?, direction=?,
		pr_url=?, brief=?, brief_status=?, link=?, done=?, position=?, updated_at=?, done_at=?, due_date=?,
		timer_started=?, timer_total=?, scratchpad=?, blocked_by=?, recurrence=?
		WHERE id=?`,
		t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.Brief, t.BriefStatus, t.Link, t.Done, t.Position,
		t.UpdatedAt.UTC().Format(time.RFC3339), doneAt, dueDate,
		timerStarted, t.TimerTotal, t.Scratchpad, blockedBy, t.Recurrence, t.ID,
	)
	return err
}

// TimerToggle starts the timer if stopped, or stops it and accumulates elapsed time.
func (d *DB) TimerToggle(id string) (*models.Task, error) {
	t, err := d.GetTask(id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if t.TimerStarted != nil {
		// Stop: accumulate elapsed
		t.TimerTotal += int(now.Sub(*t.TimerStarted).Seconds())
		t.TimerStarted = nil
	} else {
		// Start
		t.TimerStarted = &now
	}
	t.UpdatedAt = now
	var timerStarted interface{}
	if t.TimerStarted != nil {
		timerStarted = t.TimerStarted.UTC().Format(time.RFC3339)
	}
	_, err = d.conn.Exec(`UPDATE tasks SET timer_started=?, timer_total=?, updated_at=? WHERE id=?`,
		timerStarted, t.TimerTotal, now.UTC().Format(time.RFC3339), id)
	return t, err
}

// TimerReset clears the timer entirely.
func (d *DB) TimerReset(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET timer_started=NULL, timer_total=0, updated_at=? WHERE id=?`, now, id)
	return err
}

func (d *DB) GetTask(id string) (*models.Task, error) {
	row := d.conn.QueryRow(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence
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
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence
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

// SearchTasks returns up to 20 non-done tasks whose title contains query (case-insensitive).
func (d *DB) SearchTasks(query string) ([]*models.Task, error) {
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence
		FROM tasks
		WHERE done=0 AND title LIKE ?
		ORDER BY updated_at DESC
		LIMIT 20`,
		"%"+query+"%",
	)
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

// GetTaskByPRURL returns the first non-done task matching the given PR URL, or nil if none found.
func (d *DB) GetTaskByPRURL(prURL string) (*models.Task, error) {
	row := d.conn.QueryRow(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence
		FROM tasks WHERE pr_url=? AND done=0 LIMIT 1`, prURL)
	t, err := scanTask(row)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

func (d *DB) MarkDone(id string) (cloned bool, err error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err = d.conn.Exec(`UPDATE tasks SET done=1, done_at=?, updated_at=? WHERE id=?`, now, now, id); err != nil {
		return false, err
	}
	// Clear the blocker on any tasks that were blocked by this one.
	if _, err = d.conn.Exec(`UPDATE tasks SET blocked_by=NULL, updated_at=? WHERE blocked_by=?`, now, id); err != nil {
		return false, err
	}
	// If the task is recurring, create the next occurrence in Backlog.
	t, err := d.GetTask(id)
	if err != nil {
		return false, fmt.Errorf("getting task after mark done: %w", err)
	}
	if t.IsRecurring() {
		if _, err = d.CloneTaskForRecurrence(id); err != nil {
			return false, fmt.Errorf("cloning recurring task: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// CloneTaskForRecurrence creates a new backlog task copying title, description,
// work_type, direction, and recurrence from the source task. All other fields
// (timers, brief, scratchpad, blocked_by, due_date) are reset to zero values.
// Returns the newly created task.
func (d *DB) CloneTaskForRecurrence(taskID string) (*models.Task, error) {
	src, err := d.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("getting source task: %w", err)
	}
	clone := &models.Task{
		Title:       src.Title,
		Description: src.Description,
		WorkType:    src.WorkType,
		Tier:        "backlog",
		Direction:   src.Direction,
		Recurrence:  src.Recurrence,
	}
	if err := d.CreateTask(clone); err != nil {
		return nil, fmt.Errorf("creating clone: %w", err)
	}
	return clone, nil
}

// SetBlockedBy records that taskID is blocked by blockerID.
func (d *DB) SetBlockedBy(taskID, blockerID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET blocked_by=?, updated_at=? WHERE id=?`, blockerID, now, taskID)
	return err
}

// ClearBlockedBy removes the blocker from taskID.
func (d *DB) ClearBlockedBy(taskID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET blocked_by=NULL, updated_at=? WHERE id=?`, now, taskID)
	return err
}

// GetBlockerTask returns the task that is blocking taskID, or nil if the task
// has no blocker or the blocker no longer exists.
func (d *DB) GetBlockerTask(taskID string) (*models.Task, error) {
	t, err := d.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("getting task: %w", err)
	}
	if t.BlockedBy == "" {
		return nil, nil
	}
	blocker, err := d.GetTask(t.BlockedBy)
	if err != nil {
		// Blocker row is gone — treat as no blocker (stale reference).
		return nil, nil //nolint:nilerr
	}
	return blocker, nil
}

func (d *DB) UpdateBrief(id, brief, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET brief=?, brief_status=?, updated_at=? WHERE id=?`, brief, status, now, id)
	if err == nil && status == "done" && brief != "" {
		// Store versioned copy
		versionID := uuid.New().String()
		_, _ = d.conn.Exec(`INSERT INTO brief_versions (id, task_id, content, created_at) VALUES (?, ?, ?, ?)`,
			versionID, id, brief, now)
	}
	return err
}

// BriefVersion is one historical brief run.
type BriefVersion struct {
	ID        string
	TaskID    string
	Content   string
	CreatedAt time.Time
}

func (d *DB) ListBriefVersions(taskID string) ([]*BriefVersion, error) {
	rows, err := d.conn.Query(`SELECT id, task_id, content, created_at FROM brief_versions WHERE task_id=? ORDER BY created_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BriefVersion
	for rows.Next() {
		var v BriefVersion
		var createdAt string
		if err := rows.Scan(&v.ID, &v.TaskID, &v.Content, &createdAt); err != nil {
			return nil, err
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &v)
	}
	return out, rows.Err()
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

func parseTaskScanned(t *models.Task, createdAt, updatedAt string, doneAt, dueDate, timerStarted *string) {
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if doneAt != nil {
		da, _ := time.Parse(time.RFC3339, *doneAt)
		t.DoneAt = &da
	}
	if dueDate != nil {
		dd, _ := time.Parse("2006-01-02", *dueDate)
		t.DueDate = &dd
	}
	if timerStarted != nil {
		ts, _ := time.Parse(time.RFC3339, *timerStarted)
		t.TimerStarted = &ts
	}
}

func scanTask(row *sql.Row) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt, dueDate, timerStarted, blockedBy *string
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position, &createdAt, &updatedAt, &doneAt, &dueDate, &timerStarted, &t.TimerTotal, &t.Scratchpad, &blockedBy, &t.Recurrence)
	if err != nil {
		return nil, err
	}
	parseTaskScanned(&t, createdAt, updatedAt, doneAt, dueDate, timerStarted)
	if blockedBy != nil {
		t.BlockedBy = *blockedBy
	}
	return &t, nil
}

func scanTaskRow(rows *sql.Rows) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt, dueDate, timerStarted, blockedBy *string
	err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position, &createdAt, &updatedAt, &doneAt, &dueDate, &timerStarted, &t.TimerTotal, &t.Scratchpad, &blockedBy, &t.Recurrence)
	if err != nil {
		return nil, err
	}
	parseTaskScanned(&t, createdAt, updatedAt, doneAt, dueDate, timerStarted)
	if blockedBy != nil {
		t.BlockedBy = *blockedBy
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

func (d *DB) UpdateSessionName(id, name string) error {
	_, err := d.conn.Exec(`UPDATE sessions SET name=?, updated_at=? WHERE id=?`,
		name, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (d *DB) GetSession(id string) (*models.Session, error) {
	row := d.conn.QueryRow(`
		SELECT id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, archived, pinned, created_at, updated_at
		FROM sessions WHERE id=?`, id)
	return scanSession(row)
}

func (d *DB) ListSessions(taskID string) ([]*models.Session, error) {
	rows, err := d.conn.Query(`
		SELECT id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, archived, pinned, created_at, updated_at
		FROM sessions WHERE task_id=? AND name != '[brief]' AND archived=0
		ORDER BY pinned DESC, updated_at DESC`, taskID)
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

func (d *DB) ArchiveSession(id string, archived bool) error {
	v := 0
	if archived {
		v = 1
	}
	_, err := d.conn.Exec(`UPDATE sessions SET archived=?, updated_at=? WHERE id=?`,
		v, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (d *DB) PinSession(id string, pinned bool) error {
	v := 0
	if pinned {
		v = 1
	}
	_, err := d.conn.Exec(`UPDATE sessions SET pinned=?, updated_at=? WHERE id=?`,
		v, time.Now().UTC().Format(time.RFC3339), id)
	return err
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
		&s.AgentProvider, &agentSessionID, &s.ErrorMessage, &s.Archived, &s.Pinned, &createdAt, &updatedAt,
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

// ListAllSessions returns all non-brief, non-archived sessions across all tasks, joined with task title.
// If showArchived is true, archived sessions are included too.
func (d *DB) ListAllSessions(showArchived bool) ([]*models.SessionWithTask, error) {
	archiveClause := "AND s.archived=0"
	if showArchived {
		archiveClause = ""
	}
	q := fmt.Sprintf(`
		SELECT s.id, s.task_id, s.parent_id, s.name, s.mode, s.status,
		       s.agent_provider, s.agent_session_id, s.error_message, s.archived, s.pinned, s.created_at, s.updated_at,
		       t.title
		FROM sessions s
		JOIN tasks t ON t.id = s.task_id
		WHERE s.name != '[brief]' %s
		ORDER BY s.pinned DESC, s.updated_at DESC
	`, archiveClause)
	rows, err := d.conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.SessionWithTask
	for rows.Next() {
		var sw models.SessionWithTask
		var parentID, agentSessionID sql.NullString
		var createdAt, updatedAt string
		err := rows.Scan(
			&sw.ID, &sw.TaskID, &parentID, &sw.Name, &sw.Mode, &sw.Status,
			&sw.AgentProvider, &agentSessionID, &sw.ErrorMessage, &sw.Archived, &sw.Pinned, &createdAt, &updatedAt,
			&sw.TaskTitle,
		)
		if err != nil {
			return nil, err
		}
		if parentID.Valid {
			sw.ParentID = &parentID.String
		}
		if agentSessionID.Valid {
			sw.AgentSessionID = &agentSessionID.String
		}
		sw.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		sw.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, &sw)
	}
	return out, rows.Err()
}

// SearchResult is a session hit from full-text search, with a snippet of the matching message.
type SearchResult struct {
	models.SessionWithTask
	Snippet string
}

// SearchSessions searches message content (and session names) for the given query.
// Returns up to 20 results, deduplicated by session, with a content snippet.
func (d *DB) SearchSessions(query string) ([]*SearchResult, error) {
	if query == "" {
		return nil, nil
	}
	if !d.fts5 {
		return nil, fmt.Errorf("search unavailable: rebuild with `go build -tags fts5`")
	}
	rows, err := d.conn.Query(`
		SELECT
			s.id, s.task_id, s.parent_id, s.name, s.mode, s.status,
			s.agent_provider, s.agent_session_id, s.error_message, s.archived, s.pinned, s.created_at, s.updated_at,
			t.title,
			snippet(messages_fts, 0, '<mark>', '</mark>', '…', 16) AS snippet
		FROM messages_fts
		JOIN messages m ON m.rowid = messages_fts.rowid
		JOIN sessions s ON s.id = m.session_id
		JOIN tasks t ON t.id = s.task_id
		WHERE messages_fts.content MATCH ? AND s.name != '[brief]'
		GROUP BY s.id
		ORDER BY rank
		LIMIT 20
	`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SearchResult
	for rows.Next() {
		var sr SearchResult
		var parentID, agentSessionID sql.NullString
		var createdAt, updatedAt string
		err := rows.Scan(
			&sr.ID, &sr.TaskID, &parentID, &sr.Name, &sr.Mode, &sr.Status,
			&sr.AgentProvider, &agentSessionID, &sr.ErrorMessage, &sr.Archived, &sr.Pinned, &createdAt, &updatedAt,
			&sr.TaskTitle, &sr.Snippet,
		)
		if err != nil {
			return nil, err
		}
		if parentID.Valid {
			sr.ParentID = &parentID.String
		}
		if agentSessionID.Valid {
			sr.AgentSessionID = &agentSessionID.String
		}
		sr.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		sr.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, &sr)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────
// Notes
// ─────────────────────────────────────────────────────────

func (d *DB) CreateNote(n *models.Note) error {
	n.ID = uuid.New().String()
	n.CreatedAt = time.Now()
	n.UpdatedAt = time.Now()
	_, err := d.conn.Exec(`
		INSERT INTO notes (id, task_id, title, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		n.ID, n.TaskID, n.Title, n.Content,
		n.CreatedAt.UTC().Format(time.RFC3339),
		n.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) UpdateNote(n *models.Note) error {
	n.UpdatedAt = time.Now()
	_, err := d.conn.Exec(`UPDATE notes SET title=?, content=?, updated_at=? WHERE id=?`,
		n.Title, n.Content, n.UpdatedAt.UTC().Format(time.RFC3339), n.ID)
	return err
}

func (d *DB) GetNote(id string) (*models.Note, error) {
	row := d.conn.QueryRow(`SELECT id, task_id, title, content, created_at, updated_at FROM notes WHERE id=?`, id)
	return scanNote(row)
}

func (d *DB) ListNotes(taskID string) ([]*models.Note, error) {
	var rows *sql.Rows
	var err error
	if taskID == "" {
		rows, err = d.conn.Query(`SELECT id, task_id, title, content, created_at, updated_at FROM notes WHERE task_id='' ORDER BY updated_at DESC`)
	} else {
		rows, err = d.conn.Query(`SELECT id, task_id, title, content, created_at, updated_at FROM notes WHERE task_id=? ORDER BY updated_at DESC`, taskID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *DB) DeleteNote(id string) error {
	_, err := d.conn.Exec(`DELETE FROM notes WHERE id=?`, id)
	return err
}

type noteScanner interface {
	Scan(dest ...any) error
}

func scanNote(row noteScanner) (*models.Note, error) {
	var n models.Note
	var createdAt, updatedAt string
	err := row.Scan(&n.ID, &n.TaskID, &n.Title, &n.Content, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	n.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	n.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &n, nil
}

// ─── Weekly Digest ────────────────────────────────────────────────────────────

// DigestTask is a task summary used in the weekly digest.
type DigestTask struct {
	ID           string
	Title        string
	WorkType     string
	Tier         string
	Done         bool
	DoneAt       *time.Time
	CreatedAt    time.Time
	TimerTotal   int
	TimerStarted *time.Time
	SessionCount int
}

func (t *DigestTask) ElapsedSeconds() int {
	total := t.TimerTotal
	if t.TimerStarted != nil {
		total += int(time.Since(*t.TimerStarted).Seconds())
	}
	return total
}

func (t *DigestTask) ElapsedLabel() string {
	secs := t.ElapsedSeconds()
	if secs < 60 {
		if secs == 0 {
			return ""
		}
		return "< 1m"
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// DigestWeek holds all data for a given ISO week.
type DigestWeek struct {
	WeekStart   time.Time // Monday 00:00 UTC
	WeekEnd     time.Time // Sunday 23:59 UTC
	Done        []*DigestTask
	InProgress  []*DigestTask
	TotalTimeSecs int
	SessionCount  int
}

func (d *DB) WeeklyDigest(weekStart time.Time) (*DigestWeek, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)

	// Tasks completed this week
	rows, err := d.conn.Query(`
		SELECT t.id, t.title, t.work_type, t.tier, t.done, t.done_at, t.created_at,
		       t.timer_total, t.timer_started,
		       COUNT(s.id) AS session_count
		FROM tasks t
		LEFT JOIN sessions s ON s.task_id = t.id AND s.archived = 0
		WHERE t.done = 1 AND t.done_at >= ? AND t.done_at < ?
		GROUP BY t.id
		ORDER BY t.done_at DESC
	`, weekStart.Format(time.RFC3339), weekEnd.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dw := &DigestWeek{WeekStart: weekStart, WeekEnd: weekEnd.Add(-time.Second)}
	for rows.Next() {
		var dt DigestTask
		var doneAt, createdAt, timerStarted sql.NullString
		if err := rows.Scan(&dt.ID, &dt.Title, &dt.WorkType, &dt.Tier, &dt.Done,
			&doneAt, &createdAt, &dt.TimerTotal, &timerStarted, &dt.SessionCount); err != nil {
			return nil, err
		}
		if doneAt.Valid {
			t, _ := time.Parse(time.RFC3339, doneAt.String)
			dt.DoneAt = &t
		}
		if timerStarted.Valid {
			t, _ := time.Parse(time.RFC3339, timerStarted.String)
			dt.TimerStarted = &t
		}
		dt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
		dw.Done = append(dw.Done, &dt)
		dw.TotalTimeSecs += dt.ElapsedSeconds()
	}
	rows.Close()

	// In-progress tasks (not done, created or updated this week)
	rows2, err := d.conn.Query(`
		SELECT t.id, t.title, t.work_type, t.tier, t.done, t.done_at, t.created_at,
		       t.timer_total, t.timer_started,
		       COUNT(s.id) AS session_count
		FROM tasks t
		LEFT JOIN sessions s ON s.task_id = t.id AND s.archived = 0
		WHERE t.done = 0 AND (t.created_at >= ? OR t.updated_at >= ?)
		GROUP BY t.id
		ORDER BY t.updated_at DESC
		LIMIT 20
	`, weekStart.Format(time.RFC3339), weekStart.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var dt DigestTask
		var doneAt, createdAt, timerStarted sql.NullString
		if err := rows2.Scan(&dt.ID, &dt.Title, &dt.WorkType, &dt.Tier, &dt.Done,
			&doneAt, &createdAt, &dt.TimerTotal, &timerStarted, &dt.SessionCount); err != nil {
			return nil, err
		}
		if timerStarted.Valid {
			t, _ := time.Parse(time.RFC3339, timerStarted.String)
			dt.TimerStarted = &t
		}
		dt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
		dw.InProgress = append(dw.InProgress, &dt)
		dw.TotalTimeSecs += dt.ElapsedSeconds()
	}

	// Session count for the week
	var sc int
	d.conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE created_at >= ? AND created_at < ?`,
		weekStart.Format(time.RFC3339), weekEnd.Format(time.RFC3339)).Scan(&sc)
	dw.SessionCount = sc

	return dw, nil
}
