package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
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

// Conn exposes the underlying *sql.DB for test helpers that need raw SQL access.
func (d *DB) Conn() *sql.DB { return d.conn }

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
			done_at      TEXT,
			priority     TEXT NOT NULL DEFAULT ''
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
		`ALTER TABLE tasks ADD COLUMN priority     TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN effort       TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN starred      INTEGER NOT NULL DEFAULT 0`,
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

	// Task templates table
	_, _ = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS task_templates (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			work_type   TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			recurrence  TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL
		)
	`)
	// Seed default templates when the table is empty.
	if err := d.seedDefaultTemplates(); err != nil {
		log.Printf("db: seeding default templates: %v", err)
	}

	// Task comments table
	_, _ = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS task_comments (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id    TEXT    NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			body       TEXT    NOT NULL,
			created_at TEXT    NOT NULL DEFAULT (datetime('now'))
		)
	`)

	// Task tags table
	_, _ = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS task_tags (
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			tag     TEXT NOT NULL,
			PRIMARY KEY (task_id, tag)
		)
	`)

	// Task reminders table
	_, _ = d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS task_reminders (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id    TEXT    NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			remind_at  TEXT    NOT NULL,
			note       TEXT    NOT NULL DEFAULT '',
			sent       INTEGER NOT NULL DEFAULT 0,
			created_at TEXT    NOT NULL DEFAULT (datetime('now'))
		)
	`)

	// Session migrations
	for _, col := range []string{
		`ALTER TABLE sessions ADD COLUMN archived  INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN pinned    INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN feedback  TEXT    NOT NULL DEFAULT ''`,
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
			feedback         TEXT    NOT NULL DEFAULT '',
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

		// ── tasks_fts: FTS5 index over task title, description, and scratchpad ──

		_, _ = d.conn.Exec(`
			CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(
				title,
				description,
				scratchpad,
				content='tasks',
				content_rowid='rowid'
			)
		`)

		// Triggers to keep tasks_fts in sync with tasks.
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS tasks_fts_insert AFTER INSERT ON tasks BEGIN
				INSERT INTO tasks_fts(rowid, title, description, scratchpad)
				VALUES (new.rowid, new.title, new.description, new.scratchpad);
			END
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS tasks_fts_delete AFTER DELETE ON tasks BEGIN
				INSERT INTO tasks_fts(tasks_fts, rowid, title, description, scratchpad)
				VALUES ('delete', old.rowid, old.title, old.description, old.scratchpad);
			END
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS tasks_fts_update AFTER UPDATE ON tasks BEGIN
				INSERT INTO tasks_fts(tasks_fts, rowid, title, description, scratchpad)
				VALUES ('delete', old.rowid, old.title, old.description, old.scratchpad);
				INSERT INTO tasks_fts(rowid, title, description, scratchpad)
				VALUES (new.rowid, new.title, new.description, new.scratchpad);
			END
		`)

		// Backfill any existing tasks (INSERT OR IGNORE is safe on re-runs
		// because the FTS rowid acts as the dedup key here; FTS5 content tables
		// don't enforce UNIQUE on rowid inserts, so we use a DELETE+INSERT
		// pattern instead — but for the initial backfill a plain INSERT is fine
		// since tasks_fts will be empty on a fresh DB, and on an upgrade the
		// trigger will keep it current going forward).
		_, _ = d.conn.Exec(`
			INSERT INTO tasks_fts(rowid, title, description, scratchpad)
			SELECT rowid, title, description, scratchpad FROM tasks
		`)

		// ── notes_fts: FTS5 index over note title and content ──────────────
		_, _ = d.conn.Exec(`
			CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
				title,
				content,
				content='notes',
				content_rowid='rowid'
			)
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS notes_fts_insert AFTER INSERT ON notes BEGIN
				INSERT INTO notes_fts(rowid, title, content)
				VALUES (new.rowid, new.title, new.content);
			END
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes BEGIN
				INSERT INTO notes_fts(notes_fts, rowid, title, content)
				VALUES ('delete', old.rowid, old.title, old.content);
			END
		`)
		_, _ = d.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS notes_fts_update AFTER UPDATE ON notes BEGIN
				INSERT INTO notes_fts(notes_fts, rowid, title, content)
				VALUES ('delete', old.rowid, old.title, old.content);
				INSERT INTO notes_fts(rowid, title, content)
				VALUES (new.rowid, new.title, new.content);
			END
		`)
		_, _ = d.conn.Exec(`
			INSERT OR IGNORE INTO notes_fts(rowid, title, content)
			SELECT rowid, title, content FROM notes
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
	starred := 0
	if t.Starred {
		starred = 1
	}
	_, err := d.conn.Exec(`
		INSERT INTO tasks (id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, starred)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.Brief, t.BriefStatus, t.Link, t.Done, t.Position,
		t.CreatedAt.UTC().Format(time.RFC3339), t.UpdatedAt.UTC().Format(time.RFC3339), nil, dueDate, nil, 0, "", blockedBy, t.Recurrence, t.Priority, t.Effort, starred,
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
	starred := 0
	if t.Starred {
		starred = 1
	}
	_, err := d.conn.Exec(`
		UPDATE tasks SET title=?, description=?, work_type=?, tier=?, direction=?,
		pr_url=?, brief=?, brief_status=?, link=?, done=?, position=?, updated_at=?, done_at=?, due_date=?,
		timer_started=?, timer_total=?, scratchpad=?, blocked_by=?, recurrence=?, priority=?, effort=?, starred=?
		WHERE id=?`,
		t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.Brief, t.BriefStatus, t.Link, t.Done, t.Position,
		t.UpdatedAt.UTC().Format(time.RFC3339), doneAt, dueDate,
		timerStarted, t.TimerTotal, t.Scratchpad, blockedBy, t.Recurrence, t.Priority, t.Effort, starred, t.ID,
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
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks WHERE id=?`, id)
	t, err := scanTask(row)
	if err != nil {
		return nil, err
	}
	t.Tags, err = d.ListTags(t.ID)
	return t, err
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
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
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
	if err := d.populateTagsForTasks(tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (d *DB) DeleteTask(id string) error {
	_, err := d.conn.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

// RecentTasks returns the n most recently updated non-done tasks.
func (d *DB) RecentTasks(n int) ([]*models.Task, error) {
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks WHERE done=0
		ORDER BY updated_at DESC
		LIMIT ?`, n)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.populateTagsForTasks(tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// ListTasksWithDueDates returns all non-done tasks that have a due_date set,
// ordered by due_date ASC. Tags are populated on each returned task.
func (d *DB) ListTasksWithDueDates() ([]*models.Task, error) {
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status,
		       link, done, position, created_at, updated_at, done_at, due_date,
		       timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks
		WHERE done = 0 AND due_date IS NOT NULL AND due_date != ''
		ORDER BY due_date ASC`)
	if err != nil {
		return nil, fmt.Errorf("list tasks with due dates: %w", err)
	}
	defer rows.Close()

	var tasks []*models.Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task with due date: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.populateTagsForTasks(tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// ListAllTasks returns every task (all tiers, done and not done) ordered by
// created_at DESC. Intended for export — no pagination, no filtering.
func (d *DB) ListAllTasks() ([]*models.Task, error) {
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks
		ORDER BY created_at DESC`)
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
	if rerr := rows.Err(); rerr != nil {
		return nil, rerr
	}
	if err := d.populateTagsForTasks(tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// TaskSearchResult is a task hit from FTS5 full-text search with a snippet of
// the matching field content.
type TaskSearchResult struct {
	Task    *models.Task
	Snippet string
}

// SearchTasks performs a full-text search over task title, description, and
// scratchpad using the tasks_fts FTS5 index. Results are ranked by relevance
// and limited to 50. Falls back to a LIKE search on title when FTS5 is
// unavailable (e.g. tests run without the fts5 build tag).
func (d *DB) SearchTasks(query string) ([]*TaskSearchResult, error) {
	if query == "" {
		return nil, nil
	}
	if !d.fts5 {
		return d.searchTasksFallback(query)
	}
	rows, err := d.conn.Query(`
		SELECT
			t.id, t.title, t.description, t.work_type, t.tier, t.direction,
			t.pr_url, t.brief, t.brief_status, t.link, t.done, t.position,
			t.created_at, t.updated_at, t.done_at, t.due_date,
			t.timer_started, t.timer_total, t.scratchpad, t.blocked_by, t.recurrence, t.priority, t.effort,
			COALESCE(t.starred,0),
			snippet(tasks_fts, 0, '<mark>', '</mark>', '…', 12) AS snippet
		FROM tasks_fts
		JOIN tasks t ON t.rowid = tasks_fts.rowid
		WHERE tasks_fts MATCH ? AND t.done = 0
		ORDER BY rank
		LIMIT 50
	`, query)
	if err != nil {
		// FTS5 MATCH syntax errors surface here; return a descriptive error.
		return nil, fmt.Errorf("task search: %w", err)
	}
	defer rows.Close()

	var out []*TaskSearchResult
	var tasks []*models.Task
	for rows.Next() {
		var sr TaskSearchResult
		var snippet string
		t, err := scanTaskRowWithExtra(rows, &snippet)
		if err != nil {
			return nil, err
		}
		sr.Task = t
		sr.Snippet = snippet
		out = append(out, &sr)
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.populateTagsForTasks(tasks); err != nil {
		return nil, err
	}
	return out, nil
}

// scanTaskRowWithExtra scans a task row that has one additional trailing column
// (e.g. a snippet string from FTS5). The extra value is written to *extra.
func scanTaskRowWithExtra(rows *sql.Rows, extra *string) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt, dueDate, timerStarted, blockedBy *string
	var starred int
	err := rows.Scan(
		&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position,
		&createdAt, &updatedAt, &doneAt, &dueDate,
		&timerStarted, &t.TimerTotal, &t.Scratchpad, &blockedBy, &t.Recurrence, &t.Priority, &t.Effort, &starred,
		extra,
	)
	if err != nil {
		return nil, err
	}
	parseTaskScanned(&t, createdAt, updatedAt, doneAt, dueDate, timerStarted)
	if blockedBy != nil {
		t.BlockedBy = *blockedBy
	}
	t.Starred = starred == 1
	return &t, nil
}

// searchTasksFallback is a simple LIKE-based title search used when FTS5 is
// not available (e.g. tests built without -tags fts5).
func (d *DB) searchTasksFallback(query string) ([]*TaskSearchResult, error) {
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status,
		       link, done, position, created_at, updated_at, done_at, due_date,
		       timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks
		WHERE done=0 AND (title LIKE ? OR description LIKE ? OR scratchpad LIKE ?)
		ORDER BY updated_at DESC
		LIMIT 50`,
		"%"+query+"%", "%"+query+"%", "%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*TaskSearchResult
	var tasks []*models.Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, &TaskSearchResult{Task: t})
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.populateTagsForTasks(tasks); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTaskByPRURL returns the first non-done task matching the given PR URL, or nil if none found.
func (d *DB) GetTaskByPRURL(prURL string) (*models.Task, error) {
	row := d.conn.QueryRow(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
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
	var starred int
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position, &createdAt, &updatedAt, &doneAt, &dueDate, &timerStarted, &t.TimerTotal, &t.Scratchpad, &blockedBy, &t.Recurrence, &t.Priority, &t.Effort, &starred)
	if err != nil {
		return nil, err
	}
	parseTaskScanned(&t, createdAt, updatedAt, doneAt, dueDate, timerStarted)
	if blockedBy != nil {
		t.BlockedBy = *blockedBy
	}
	t.Starred = starred == 1
	return &t, nil
}

func scanTaskRow(rows *sql.Rows) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt, dueDate, timerStarted, blockedBy *string
	var starred int
	err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.Brief, &t.BriefStatus, &t.Link, &t.Done, &t.Position, &createdAt, &updatedAt, &doneAt, &dueDate, &timerStarted, &t.TimerTotal, &t.Scratchpad, &blockedBy, &t.Recurrence, &t.Priority, &t.Effort, &starred)
	if err != nil {
		return nil, err
	}
	parseTaskScanned(&t, createdAt, updatedAt, doneAt, dueDate, timerStarted)
	if blockedBy != nil {
		t.BlockedBy = *blockedBy
	}
	t.Starred = starred == 1
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
		SELECT id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, archived, pinned, feedback, created_at, updated_at
		FROM sessions WHERE id=?`, id)
	return scanSession(row)
}

func (d *DB) ListSessions(taskID string) ([]*models.Session, error) {
	rows, err := d.conn.Query(`
		SELECT id, task_id, parent_id, name, mode, status, agent_provider, agent_session_id, error_message, archived, pinned, feedback, created_at, updated_at
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

func (d *DB) SetSessionFeedback(id, feedback string) error {
	_, err := d.conn.Exec(`UPDATE sessions SET feedback=?, updated_at=? WHERE id=?`,
		feedback, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func scanSession(row sessionScanner) (*models.Session, error) {
	var s models.Session
	var parentID, agentSessionID sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(
		&s.ID, &s.TaskID, &parentID, &s.Name, &s.Mode, &s.Status,
		&s.AgentProvider, &agentSessionID, &s.ErrorMessage, &s.Archived, &s.Pinned, &s.Feedback, &createdAt, &updatedAt,
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
		       s.agent_provider, s.agent_session_id, s.error_message, s.archived, s.pinned, s.feedback, s.created_at, s.updated_at,
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
			&sw.AgentProvider, &agentSessionID, &sw.ErrorMessage, &sw.Archived, &sw.Pinned, &sw.Feedback, &createdAt, &updatedAt,
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

// NoteSearchResult is a note hit from full-text search.
type NoteSearchResult struct {
	models.Note
	Snippet string `json:"snippet"`
}

// SearchNotes searches notes by full-text query. Falls back to LIKE on title/content.
func (d *DB) SearchNotes(query string) ([]*NoteSearchResult, error) {
	if query == "" {
		return nil, nil
	}
	var results []*NoteSearchResult
	if d.fts5 {
		rows, err := d.conn.Query(`
			SELECT n.id, n.task_id, n.title, n.content, n.created_at, n.updated_at,
			       snippet(notes_fts, 1, '<mark>', '</mark>', '…', 20) AS snip
			FROM notes_fts
			JOIN notes n ON notes_fts.rowid = n.rowid
			WHERE notes_fts MATCH ?
			ORDER BY rank
			LIMIT 50
		`, query+"*")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var r NoteSearchResult
				var createdAt, updatedAt string
				if err := rows.Scan(&r.ID, &r.TaskID, &r.Title, &r.Content, &createdAt, &updatedAt, &r.Snippet); err != nil {
					continue
				}
				r.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
				r.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
				results = append(results, &r)
			}
			return results, nil
		}
	}
	// LIKE fallback
	like := "%" + query + "%"
	rows, err := d.conn.Query(`
		SELECT id, task_id, title, content, created_at, updated_at
		FROM notes WHERE title LIKE ? OR content LIKE ?
		ORDER BY updated_at DESC LIMIT 50
	`, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r NoteSearchResult
		var createdAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Title, &r.Content, &createdAt, &updatedAt); err != nil {
			continue
		}
		r.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		r.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		results = append(results, &r)
	}
	return results, nil
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

// ─────────────────────────────────────────────────────────
// Task Templates
// ─────────────────────────────────────────────────────────

var defaultTemplates = []struct{ name, workType, description, recurrence string }{
	{"PR Review", "pr_review", "Review PR, check diff, leave comments.", ""},
	{"Deployment", "deployment", "Deploy to production. Verify health checks after.", ""},
	{"Weekly Sync", "meeting", "Weekly team sync — review priorities, blockers, updates.", "weekly"},
	{"Design Review", "design", "Review design proposal. Check consistency, feasibility, feedback.", ""},
}

// seedDefaultTemplates inserts the built-in templates when the table is empty.
// Safe to call on every startup — no-ops if rows exist.
func (d *DB) seedDefaultTemplates() error {
	var count int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM task_templates`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	for _, t := range defaultTemplates {
		id := uuid.New().String()
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := d.conn.Exec(
			`INSERT INTO task_templates (id, name, work_type, description, recurrence, created_at) VALUES (?,?,?,?,?,?)`,
			id, t.name, t.workType, t.description, t.recurrence, now,
		); err != nil {
			return err
		}
	}
	return nil
}

// CreateTemplate inserts a new task template and returns it with its generated ID.
// recurrence must already be sanitized by the caller (empty string = no recurrence).
func (d *DB) CreateTemplate(name, workType, description, recurrence string) (*models.TaskTemplate, error) {
	tmpl := &models.TaskTemplate{
		ID:          uuid.New().String(),
		Name:        name,
		WorkType:    workType,
		Description: description,
		Recurrence:  recurrence,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	_, err := d.conn.Exec(
		`INSERT INTO task_templates (id, name, work_type, description, recurrence, created_at) VALUES (?,?,?,?,?,?)`,
		tmpl.ID, tmpl.Name, tmpl.WorkType, tmpl.Description, tmpl.Recurrence, tmpl.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

// ListTemplates returns all templates ordered by name.
func (d *DB) ListTemplates() ([]*models.TaskTemplate, error) {
	rows, err := d.conn.Query(
		`SELECT id, name, work_type, description, recurrence, created_at FROM task_templates ORDER BY name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.TaskTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate returns a single template by ID.
func (d *DB) GetTemplate(id string) (*models.TaskTemplate, error) {
	row := d.conn.QueryRow(
		`SELECT id, name, work_type, description, recurrence, created_at FROM task_templates WHERE id=?`, id,
	)
	return scanTemplate(row)
}

// DeleteTemplate removes a template by ID.
func (d *DB) DeleteTemplate(id string) error {
	_, err := d.conn.Exec(`DELETE FROM task_templates WHERE id=?`, id)
	return err
}

type templateScanner interface {
	Scan(dest ...any) error
}

func scanTemplate(row templateScanner) (*models.TaskTemplate, error) {
	var t models.TaskTemplate
	err := row.Scan(&t.ID, &t.Name, &t.WorkType, &t.Description, &t.Recurrence, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
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
	Priority     string // P1, P2, P3, or ""
	Effort       string // XS, S, M, L, XL, or ""
	DaysInColumn int    // only set for WaitingOnOthers
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
// WorkTypeTime holds time-tracked seconds for a single work type.
type WorkTypeTime struct {
	WorkType string
	Seconds  int
	Label    string // e.g. "2h 30m"
}

type DigestWeek struct {
	WeekStart        time.Time // Monday 00:00 UTC
	WeekEnd          time.Time // Sunday 23:59 UTC
	Done             []*DigestTask
	InProgress       []*DigestTask
	WaitingOnOthers  []*DigestTask // direction='blocked_on_them', not done, age >= 2 days
	TotalTimeSecs    int
	SessionCount     int
	WorkTypeBreakdown []WorkTypeTime // sorted by seconds desc, only types with time > 0
	DoneLastWeek     int    // count of tasks completed in the prior week (for velocity comparison)
	TimeDeltaPct     int    // % change in total time vs last week (+/-); 0 if no prior data
	AvgCycleDays     float64           // average days from created_at → done_at for tasks done this week
	CycleByType      []CycleTimeEntry  // per-work-type cycle time, sorted by avg days desc
}

// CycleTimeEntry holds cycle-time data for one work type.
type CycleTimeEntry struct {
	WorkType string
	AvgDays  float64
	Count    int
}

func (d *DB) WeeklyDigest(weekStart time.Time) (*DigestWeek, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)

	// Tasks completed this week
	rows, err := d.conn.Query(`
		SELECT t.id, t.title, t.work_type, t.tier, t.done, t.done_at, t.created_at,
		       t.timer_total, t.timer_started,
		       COUNT(s.id) AS session_count,
		       COALESCE(t.priority, '') AS priority,
		       COALESCE(t.effort, '') AS effort
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
			&doneAt, &createdAt, &dt.TimerTotal, &timerStarted, &dt.SessionCount,
			&dt.Priority, &dt.Effort); err != nil {
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
		       COUNT(s.id) AS session_count,
		       COALESCE(t.priority, '') AS priority,
		       COALESCE(t.effort, '') AS effort
		FROM tasks t
		LEFT JOIN sessions s ON s.task_id = t.id AND s.archived = 0
		WHERE t.done = 0 AND (t.created_at >= ? OR t.updated_at >= ?)
		GROUP BY t.id
		ORDER BY
		  CASE t.priority WHEN 'P1' THEN 1 WHEN 'P2' THEN 2 WHEN 'P3' THEN 3 ELSE 4 END,
		  t.updated_at DESC
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
			&doneAt, &createdAt, &dt.TimerTotal, &timerStarted, &dt.SessionCount,
			&dt.Priority, &dt.Effort); err != nil {
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

	// Work type breakdown: aggregate timer_total across all tasks touched this week.
	// "Touched" = done this week OR still in progress (created before week end).
	typeRows, err := d.conn.Query(`
		SELECT work_type, SUM(timer_total) as total_secs
		FROM tasks
		WHERE work_type != ''
		  AND timer_total > 0
		  AND (
		        (done = 1 AND done_at >= ? AND done_at < ?)
		     OR (done = 0 AND created_at < ?)
		      )
		GROUP BY work_type
		ORDER BY total_secs DESC`,
		weekStart.Format(time.RFC3339), weekEnd.Format(time.RFC3339),
		weekEnd.Format(time.RFC3339),
	)
	if err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var wt string
			var secs int
			if typeRows.Scan(&wt, &secs) == nil && secs > 0 {
				wtt := WorkTypeTime{WorkType: wt, Seconds: secs}
				h := secs / 3600
				m := (secs % 3600) / 60
				if h > 0 {
					wtt.Label = fmt.Sprintf("%dh %dm", h, m)
				} else {
					wtt.Label = fmt.Sprintf("%dm", m)
				}
				dw.WorkTypeBreakdown = append(dw.WorkTypeBreakdown, wtt)
			}
		}
	}

	// Tasks waiting on others: direction='blocked_on_them', not done, created 2+ days ago
	cutoff := time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339)
	waitRows, err := d.conn.Query(`
		SELECT t.id, t.title, t.work_type, t.tier, t.done, t.done_at, t.created_at,
		       t.timer_total, t.timer_started,
		       COUNT(s.id) AS session_count,
		       COALESCE(t.priority, '') AS priority,
		       COALESCE(t.effort, '') AS effort
		FROM tasks t
		LEFT JOIN sessions s ON s.task_id = t.id AND s.archived = 0
		WHERE t.done = 0 AND t.direction = 'blocked_on_them' AND t.created_at <= ?
		GROUP BY t.id
		ORDER BY t.created_at ASC
	`, cutoff)
	if err == nil {
		defer waitRows.Close()
		for waitRows.Next() {
			var dt DigestTask
			var doneAt, createdAt *string
			var timerStarted *string
			if waitRows.Scan(&dt.ID, &dt.Title, &dt.WorkType, &dt.Tier, &dt.Done,
				&doneAt, &createdAt, &dt.TimerTotal, &timerStarted, &dt.SessionCount,
				&dt.Priority, &dt.Effort) == nil {
				if createdAt != nil {
					if t, err := time.Parse(time.RFC3339, *createdAt); err == nil {
						dt.CreatedAt = t
						dt.DaysInColumn = int(time.Since(t).Hours() / 24)
					}
				}
				if timerStarted != nil {
					if t, err := time.Parse(time.RFC3339, *timerStarted); err == nil {
						dt.TimerStarted = &t
					}
				}
				dw.WaitingOnOthers = append(dw.WaitingOnOthers, &dt)
			}
		}
	}

	// Velocity: count tasks done + total time in prior week for comparison
	prevStart := weekStart.AddDate(0, 0, -7)
	prevEnd := weekStart
	var prevDoneCount int
	var prevTimeSecs int
	d.conn.QueryRow(`SELECT COUNT(*), COALESCE(SUM(timer_total),0) FROM tasks WHERE done=1 AND done_at >= ? AND done_at < ?`,
		prevStart.Format(time.RFC3339), prevEnd.Format(time.RFC3339)).Scan(&prevDoneCount, &prevTimeSecs)
	dw.DoneLastWeek = prevDoneCount
	if prevTimeSecs > 0 && dw.TotalTimeSecs > 0 {
		dw.TimeDeltaPct = int(float64(dw.TotalTimeSecs-prevTimeSecs) / float64(prevTimeSecs) * 100)
	}

	// ── Cycle time: average days creation → done, for tasks completed this week ──
	ctRows, err := d.conn.Query(`
		SELECT work_type,
		       AVG(CAST((julianday(done_at) - julianday(created_at)) AS REAL)) as avg_days,
		       COUNT(*) as cnt
		FROM tasks
		WHERE done=1 AND done_at >= ? AND done_at < ?
		  AND created_at IS NOT NULL AND done_at IS NOT NULL
		GROUP BY work_type
		ORDER BY avg_days DESC
	`, weekStart.Format(time.RFC3339), weekEnd.Format(time.RFC3339))
	if err == nil {
		defer ctRows.Close()
		var totalDays float64
		var totalCount int
		for ctRows.Next() {
			var entry CycleTimeEntry
			if err := ctRows.Scan(&entry.WorkType, &entry.AvgDays, &entry.Count); err == nil {
				dw.CycleByType = append(dw.CycleByType, entry)
				totalDays += entry.AvgDays * float64(entry.Count)
				totalCount += entry.Count
			}
		}
		if totalCount > 0 {
			dw.AvgCycleDays = totalDays / float64(totalCount)
		}
	}

	return dw, nil
}

// ─────────────────────────────────────────────────────────
// Daily standup
// ─────────────────────────────────────────────────────────

// StandupTask is a task entry in the daily standup.
type StandupTask struct {
	ID           string
	Title        string
	WorkType     string
	Done         bool
	DoneAt       *time.Time
	TimerTotal   int
	TimerStarted *time.Time
	SessionCount int
}

func (t *StandupTask) ElapsedLabel() string {
	total := t.TimerTotal
	if t.TimerStarted != nil {
		total += int(time.Since(*t.TimerStarted).Seconds())
	}
	if total < 60 {
		return ""
	}
	h := total / 3600
	m := (total % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// DailyStandup returns tasks completed and in-progress for a given day (UTC).
func (d *DB) DailyStandup(day time.Time) (done []*StandupTask, inProgress []*StandupTask, err error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)

	// Tasks completed today
	rows, err := d.conn.Query(`
		SELECT t.id, t.title, t.work_type, t.done, t.done_at,
		       t.timer_total, t.timer_started,
		       COUNT(s.id) AS session_count
		FROM tasks t
		LEFT JOIN sessions s ON s.task_id = t.id AND s.archived = 0
		WHERE t.done = 1 AND t.done_at >= ? AND t.done_at < ?
		GROUP BY t.id
		ORDER BY t.done_at DESC
	`, dayStart.Format(time.RFC3339), dayEnd.Format(time.RFC3339))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var st StandupTask
		var doneAt, timerStarted sql.NullString
		if err := rows.Scan(&st.ID, &st.Title, &st.WorkType, &st.Done, &doneAt,
			&st.TimerTotal, &timerStarted, &st.SessionCount); err != nil {
			return nil, nil, err
		}
		if doneAt.Valid {
			t, _ := time.Parse(time.RFC3339, doneAt.String)
			st.DoneAt = &t
		}
		if timerStarted.Valid {
			t, _ := time.Parse(time.RFC3339, timerStarted.String)
			st.TimerStarted = &t
		}
		done = append(done, &st)
	}
	rows.Close()

	// Tasks touched today but not done (created or updated today, not done)
	rows2, err := d.conn.Query(`
		SELECT t.id, t.title, t.work_type, t.done, t.done_at,
		       t.timer_total, t.timer_started,
		       COUNT(s.id) AS session_count
		FROM tasks t
		LEFT JOIN sessions s ON s.task_id = t.id AND s.archived = 0
		WHERE t.done = 0 AND (t.updated_at >= ? AND t.updated_at < ?)
		GROUP BY t.id
		ORDER BY t.updated_at DESC
	`, dayStart.Format(time.RFC3339), dayEnd.Format(time.RFC3339))
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var st StandupTask
		var doneAt, timerStarted sql.NullString
		if err := rows2.Scan(&st.ID, &st.Title, &st.WorkType, &st.Done, &doneAt,
			&st.TimerTotal, &timerStarted, &st.SessionCount); err != nil {
			return nil, nil, err
		}
		if doneAt.Valid {
			t, _ := time.Parse(time.RFC3339, doneAt.String)
			st.DoneAt = &t
		}
		if timerStarted.Valid {
			t, _ := time.Parse(time.RFC3339, timerStarted.String)
			st.TimerStarted = &t
		}
		inProgress = append(inProgress, &st)
	}

	return done, inProgress, nil
}

// ─────────────────────────────────────────────────────────
// Task comments
// ─────────────────────────────────────────────────────────

// CreateComment inserts a new comment for the given task and populates c.ID
// and c.CreatedAt from the inserted row.
func (d *DB) CreateComment(taskID, body string) (*models.Comment, error) {
	res, err := d.conn.Exec(
		`INSERT INTO task_comments (task_id, body) VALUES (?, ?)`,
		taskID, body,
	)
	if err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create comment: last insert id: %w", err)
	}
	// Re-fetch so created_at is populated from the DB default.
	return d.getComment(id)
}

func (d *DB) getComment(id int64) (*models.Comment, error) {
	row := d.conn.QueryRow(
		`SELECT id, task_id, body, created_at FROM task_comments WHERE id = ?`, id,
	)
	return scanComment(row)
}

// ListComments returns all comments for a task ordered oldest-first.
func (d *DB) ListComments(taskID string) ([]*models.Comment, error) {
	rows, err := d.conn.Query(
		`SELECT id, task_id, body, created_at FROM task_comments
		 WHERE task_id = ? ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var comments []*models.Comment
	for rows.Next() {
		c, err := scanCommentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list comments: scan: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// DeleteComment removes a comment by its ID. Deleting a non-existent comment
// is not an error.
func (d *DB) DeleteComment(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM task_comments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete comment %d: %w", id, err)
	}
	return nil
}

func scanComment(row *sql.Row) (*models.Comment, error) {
	var c models.Comment
	var createdAt string
	if err := row.Scan(&c.ID, &c.TaskID, &c.Body, &createdAt); err != nil {
		return nil, err
	}
	c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	if c.CreatedAt.IsZero() {
		c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	}
	return &c, nil
}

func scanCommentRow(rows *sql.Rows) (*models.Comment, error) {
	var c models.Comment
	var createdAt string
	if err := rows.Scan(&c.ID, &c.TaskID, &c.Body, &createdAt); err != nil {
		return nil, err
	}
	c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	if c.CreatedAt.IsZero() {
		c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	}
	return &c, nil
}

// ─────────────────────────────────────────────────────────
// Task tags
// ─────────────────────────────────────────────────────────

// DuplicateTask creates a copy of the given task (title, description, work_type,
// direction, pr_url, link, recurrence, tags) placed at the top of the same tier.
// Timer state, brief, scratchpad, done status, and comments are NOT copied.
func (d *DB) DuplicateTask(id string) (*models.Task, error) {
	src, err := d.GetTask(id)
	if err != nil {
		return nil, fmt.Errorf("source task: %w", err)
	}
	dup := &models.Task{
		Title:      "Copy of " + src.Title,
		WorkType:   src.WorkType,
		Description: src.Description,
		Direction:  src.Direction,
		PRURL:      src.PRURL,
		Link:       src.Link,
		Recurrence: src.Recurrence,
		Tier:       src.Tier,
	}
	if err := d.CreateTask(dup); err != nil {
		return nil, err
	}
	for _, tag := range src.Tags {
		_ = d.AddTag(dup.ID, tag)
	}
	dup.Tags = src.Tags
	return dup, nil
}

// AddTag associates tag with the given task. If the tag already exists the
// call is a no-op (INSERT OR IGNORE).
// tag is normalised to lowercase and trimmed before storage.
func (d *DB) AddTag(taskID, tag string) error {
	tag = normaliseTag(tag)
	if tag == "" {
		return fmt.Errorf("add tag: tag must not be blank")
	}
	_, err := d.conn.Exec(
		`INSERT OR IGNORE INTO task_tags (task_id, tag) VALUES (?, ?)`,
		taskID, tag,
	)
	if err != nil {
		return fmt.Errorf("add tag %q to task %s: %w", tag, taskID, err)
	}
	return nil
}

// RemoveTag removes a tag from a task. Removing a tag that does not exist is
// not an error.
func (d *DB) RemoveTag(taskID, tag string) error {
	tag = normaliseTag(tag)
	_, err := d.conn.Exec(
		`DELETE FROM task_tags WHERE task_id=? AND tag=?`,
		taskID, tag,
	)
	if err != nil {
		return fmt.Errorf("remove tag %q from task %s: %w", tag, taskID, err)
	}
	return nil
}

// ListTags returns all tags for a task, sorted alphabetically.
func (d *DB) ListTags(taskID string) ([]string, error) {
	rows, err := d.conn.Query(
		`SELECT tag FROM task_tags WHERE task_id=? ORDER BY tag ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tags for task %s: %w", taskID, err)
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("list tags scan: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// ListAllTags returns every distinct tag in use across all tasks, sorted
// alphabetically. Intended for autocomplete.
func (d *DB) ListAllTags() ([]string, error) {
	rows, err := d.conn.Query(
		`SELECT DISTINCT tag FROM task_tags ORDER BY tag ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all tags: %w", err)
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("list all tags scan: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// populateTagsForTasks bulk-fetches tags for a slice of tasks in a single
// query and populates t.Tags on each one. This avoids N individual queries
// while keeping the approach simple.
func (d *DB) populateTagsForTasks(tasks []*models.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	// Build id→index map.
	idx := make(map[string]int, len(tasks))
	for i, t := range tasks {
		idx[t.ID] = i
		tasks[i].Tags = []string{} // ensure non-nil slice
	}

	// Build a parameterised IN clause.
	placeholders := make([]string, len(tasks))
	args := make([]interface{}, len(tasks))
	for i, t := range tasks {
		placeholders[i] = "?"
		args[i] = t.ID
	}
	q := fmt.Sprintf(
		`SELECT task_id, tag FROM task_tags WHERE task_id IN (%s) ORDER BY task_id, tag ASC`,
		strings.Join(placeholders, ","),
	)
	rows, err := d.conn.Query(q, args...)
	if err != nil {
		return fmt.Errorf("populate tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, tag string
		if err := rows.Scan(&taskID, &tag); err != nil {
			return fmt.Errorf("populate tags scan: %w", err)
		}
		if i, ok := idx[taskID]; ok {
			tasks[i].Tags = append(tasks[i].Tags, tag)
		}
	}
	return rows.Err()
}

// normaliseTag lowercases and trims a tag value.
func normaliseTag(tag string) string {
	return strings.ToLower(strings.TrimSpace(tag))
}

// ─────────────────────────────────────────────────────────
// Task reminders
// ─────────────────────────────────────────────────────────

// CreateReminder inserts a new reminder for taskID scheduled for remindAt.
// note is optional free text shown in the Telegram notification.
// sqliteDateTime is the format used by SQLite's datetime() function.
// We store remind_at in this format so that SQLite string comparisons with
// datetime('now') work correctly in ListDueReminders.
const sqliteDateTime = "2006-01-02 15:04:05"

func (d *DB) CreateReminder(taskID string, remindAt time.Time, note string) (*models.Reminder, error) {
	res, err := d.conn.Exec(
		`INSERT INTO task_reminders (task_id, remind_at, note)
		 VALUES (?, ?, ?)`,
		taskID,
		remindAt.UTC().Format(sqliteDateTime),
		note,
	)
	if err != nil {
		return nil, fmt.Errorf("create reminder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create reminder last insert id: %w", err)
	}
	return d.getReminder(id)
}

func (d *DB) getReminder(id int64) (*models.Reminder, error) {
	row := d.conn.QueryRow(
		`SELECT id, task_id, remind_at, note, sent, created_at
		 FROM task_reminders WHERE id = ?`, id,
	)
	return scanReminderRow(row)
}

// ListDueReminders returns all unsent reminders whose remind_at is in the
// past, ordered oldest-first. Intended for the check_reminders script.
func (d *DB) ListDueReminders() ([]*models.Reminder, error) {
	rows, err := d.conn.Query(
		`SELECT id, task_id, remind_at, note, sent, created_at
		 FROM task_reminders
		 WHERE sent = 0 AND remind_at <= datetime('now')
		 ORDER BY remind_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list due reminders: %w", err)
	}
	defer rows.Close()
	return scanReminderRows(rows)
}

// DueReminderRow is a due reminder joined with its task title, used by the
// in-app notification API.
type DueReminderRow struct {
	ID                int64
	TaskID            string
	TaskTitle         string
	Note              string
	RemindAt          time.Time
	RemindAtFormatted string
}

// ListDueRemindersWithTask returns unsent due reminders joined with their task
// title, ordered oldest-first. This is the backend for GET /api/reminders/due.
func (d *DB) ListDueRemindersWithTask() ([]*DueReminderRow, error) {
	rows, err := d.conn.Query(
		`SELECT r.id, r.task_id, t.title, r.note, r.remind_at
		 FROM task_reminders r
		 JOIN tasks t ON t.id = r.task_id
		 WHERE r.sent = 0 AND r.remind_at <= datetime('now')
		 ORDER BY r.remind_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list due reminders with task: %w", err)
	}
	defer rows.Close()

	var out []*DueReminderRow
	for rows.Next() {
		var dr DueReminderRow
		var remindAtStr string
		if err := rows.Scan(&dr.ID, &dr.TaskID, &dr.TaskTitle, &dr.Note, &remindAtStr); err != nil {
			return nil, fmt.Errorf("scan due reminder: %w", err)
		}
		dr.RemindAt = parseReminderTime(remindAtStr)
		dr.RemindAtFormatted = (&models.Reminder{RemindAt: dr.RemindAt}).RemindAtFormatted()
		out = append(out, &dr)
	}
	return out, rows.Err()
}

// ListRemindersForTask returns all reminders for taskID ordered by
// remind_at ASC (both sent and unsent).
func (d *DB) ListRemindersForTask(taskID string) ([]*models.Reminder, error) {
	rows, err := d.conn.Query(
		`SELECT id, task_id, remind_at, note, sent, created_at
		 FROM task_reminders
		 WHERE task_id = ?
		 ORDER BY remind_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("list reminders for task %s: %w", taskID, err)
	}
	defer rows.Close()
	return scanReminderRows(rows)
}

// MarkReminderSent marks a reminder as sent. Calling it on an already-sent
// reminder is idempotent.
func (d *DB) MarkReminderSent(id int64) error {
	_, err := d.conn.Exec(`UPDATE task_reminders SET sent = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("mark reminder sent %d: %w", id, err)
	}
	return nil
}

// DeleteReminder removes a reminder by ID. Deleting a non-existent reminder
// is not an error.
func (d *DB) DeleteReminder(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM task_reminders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete reminder %d: %w", id, err)
	}
	return nil
}

// parseReminderTime attempts RFC3339 then SQLite's datetime() format.
func parseReminderTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	return t
}

func scanReminderRow(row *sql.Row) (*models.Reminder, error) {
	var r models.Reminder
	var remindAt, createdAt string
	var sent int
	if err := row.Scan(&r.ID, &r.TaskID, &remindAt, &r.Note, &sent, &createdAt); err != nil {
		return nil, err
	}
	r.RemindAt = parseReminderTime(remindAt)
	r.CreatedAt = parseReminderTime(createdAt)
	r.Sent = sent != 0
	return &r, nil
}

func scanReminderRows(rows *sql.Rows) ([]*models.Reminder, error) {
	var out []*models.Reminder
	for rows.Next() {
		var r models.Reminder
		var remindAt, createdAt string
		var sent int
		if err := rows.Scan(&r.ID, &r.TaskID, &remindAt, &r.Note, &sent, &createdAt); err != nil {
			return nil, fmt.Errorf("scan reminder row: %w", err)
		}
		r.RemindAt = parseReminderTime(remindAt)
		r.CreatedAt = parseReminderTime(createdAt)
		r.Sent = sent != 0
		out = append(out, &r)
	}
	return out, rows.Err()
}

// PatchTaskFields updates only the title and description of a task.
func (d *DB) PatchTaskFields(id, title, description string) (*models.Task, error) {
	_, err := d.conn.Exec(
		`UPDATE tasks SET title=?, description=?, updated_at=? WHERE id=?`,
		title, description, time.Now().UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return nil, err
	}
	return d.GetTask(id)
}

// StarTask toggles the starred state of a task and returns the new value.
func (d *DB) StarTask(id string) (bool, error) {
	var current int
	err := d.conn.QueryRow(`SELECT COALESCE(starred,0) FROM tasks WHERE id=?`, id).Scan(&current)
	if err != nil {
		return false, err
	}
	newVal := 1
	if current == 1 {
		newVal = 0
	}
	_, err = d.conn.Exec(`UPDATE tasks SET starred=?, updated_at=? WHERE id=?`,
		newVal, time.Now().UTC().Format(time.RFC3339), id)
	return newVal == 1, err
}

// ListStarredTasks returns all non-done starred tasks ordered by updated_at desc.
func (d *DB) ListStarredTasks() ([]*models.Task, error) {
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status,
		       link, done, position, created_at, updated_at, done_at, due_date,
		       timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks WHERE done=0 AND starred=1
		ORDER BY updated_at DESC`)
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
	return tasks, rows.Err()
}

// RecentlyDone returns the most recently completed tasks (up to limit), for the "done today" strip.
func (d *DB) RecentlyDone(limit int) ([]*models.Task, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	rows, err := d.conn.Query(`
		SELECT id, title, description, work_type, tier, direction, pr_url, brief, brief_status, link, done, position, created_at, updated_at, done_at, due_date, timer_started, timer_total, scratchpad, blocked_by, recurrence, priority, effort, COALESCE(starred,0)
		FROM tasks
		WHERE done=1 AND done_at >= ?
		ORDER BY done_at DESC
		LIMIT ?
	`, cutoff, limit)
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

// ── Activity feed ─────────────────────────────────────────────────────────────

// ActivityEvent represents a single item in the global activity feed.
type ActivityEvent struct {
	Time     time.Time
	Kind     string // "task_created", "task_done", "task_moved", "session_started", "session_done", "comment"
	TaskID   string
	TaskTitle string
	Detail   string // extra context (tier name, session name, comment snippet, etc.)
}

// ListActivityFeed returns up to limit events ordered newest-first, covering
// the past numDays days. It unions across:
//   - tasks created
//   - tasks completed
//   - sessions created (started)
//   - sessions with status=done (completed)
//   - task comments
func (d *DB) ListActivityFeed(numDays, limit int) ([]*ActivityEvent, error) {
	since := time.Now().UTC().AddDate(0, 0, -numDays).Format(time.RFC3339)

	q := fmt.Sprintf(`
		SELECT ts, kind, task_id, task_title, detail FROM (
			-- tasks created
			SELECT created_at AS ts, 'task_created' AS kind, id AS task_id, title AS task_title,
			       work_type || '|' || tier AS detail
			FROM tasks
			WHERE created_at >= %q

			UNION ALL

			-- tasks done
			SELECT done_at AS ts, 'task_done' AS kind, id AS task_id, title AS task_title,
			       work_type AS detail
			FROM tasks
			WHERE done=1 AND done_at IS NOT NULL AND done_at >= %q

			UNION ALL

			-- sessions created
			SELECT s.created_at AS ts, 'session_started' AS kind, s.task_id, t.title AS task_title,
			       s.name AS detail
			FROM sessions s JOIN tasks t ON t.id = s.task_id
			WHERE s.name != '[brief]' AND s.created_at >= %q

			UNION ALL

			-- sessions completed
			SELECT s.updated_at AS ts, 'session_done' AS kind, s.task_id, t.title AS task_title,
			       s.name AS detail
			FROM sessions s JOIN tasks t ON t.id = s.task_id
			WHERE s.name != '[brief]' AND s.status='done' AND s.updated_at >= %q

			UNION ALL

			-- comments
			SELECT c.created_at AS ts, 'comment' AS kind, c.task_id, t.title AS task_title,
			       c.body AS detail
			FROM task_comments c JOIN tasks t ON t.id = c.task_id
			WHERE c.created_at >= %q
		)
		ORDER BY ts DESC
		LIMIT %d
	`, since, since, since, since, since, limit)

	rows, err := d.conn.Query(q)
	if err != nil {
		return nil, fmt.Errorf("activity feed: %w", err)
	}
	defer rows.Close()

	var events []*ActivityEvent
	for rows.Next() {
		var e ActivityEvent
		var ts string
		if err := rows.Scan(&ts, &e.Kind, &e.TaskID, &e.TaskTitle, &e.Detail); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339, ts)
		events = append(events, &e)
	}
	return events, rows.Err()
}
