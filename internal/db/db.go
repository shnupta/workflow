package db

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/shnupta/workflow/internal/models"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id          TEXT PRIMARY KEY,
			title       TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			work_type   TEXT NOT NULL,
			tier        TEXT NOT NULL,
			direction   TEXT NOT NULL,
			pr_url      TEXT NOT NULL DEFAULT '',
			pr_summary  TEXT NOT NULL DEFAULT '',
			link        TEXT NOT NULL DEFAULT '',
			done        INTEGER NOT NULL DEFAULT 0,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			done_at     TEXT
		)
	`)
	return err
}

func (d *DB) CreateTask(t *models.Task) error {
	t.ID = uuid.New().String()
	t.CreatedAt = time.Now()
	t.UpdatedAt = time.Now()
	_, err := d.conn.Exec(`
		INSERT INTO tasks (id, title, description, work_type, tier, direction, pr_url, pr_summary, link, done, created_at, updated_at, done_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.PRSummary, t.Link, t.Done,
		t.CreatedAt.Format(time.RFC3339), t.UpdatedAt.Format(time.RFC3339), nil,
	)
	return err
}

func (d *DB) UpdateTask(t *models.Task) error {
	t.UpdatedAt = time.Now()
	var doneAt interface{}
	if t.DoneAt != nil {
		doneAt = t.DoneAt.Format(time.RFC3339)
	}
	_, err := d.conn.Exec(`
		UPDATE tasks SET title=?, description=?, work_type=?, tier=?, direction=?,
		pr_url=?, pr_summary=?, link=?, done=?, updated_at=?, done_at=?
		WHERE id=?`,
		t.Title, t.Description, t.WorkType, t.Tier, t.Direction,
		t.PRURL, t.PRSummary, t.Link, t.Done,
		t.UpdatedAt.Format(time.RFC3339), doneAt, t.ID,
	)
	return err
}

func (d *DB) GetTask(id string) (*models.Task, error) {
	row := d.conn.QueryRow(`SELECT id, title, description, work_type, tier, direction, pr_url, pr_summary, link, done, created_at, updated_at, done_at FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

func (d *DB) ListTasks(includeDone bool) ([]*models.Task, error) {
	q := `SELECT id, title, description, work_type, tier, direction, pr_url, pr_summary, link, done, created_at, updated_at, done_at FROM tasks`
	if !includeDone {
		q += ` WHERE done=0`
	}
	q += ` ORDER BY CASE tier WHEN 'today' THEN 1 WHEN 'this_week' THEN 2 ELSE 3 END, created_at ASC`
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
	now := time.Now().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET done=1, done_at=?, updated_at=? WHERE id=?`, now, now, id)
	return err
}

func (d *DB) UpdatePRSummary(id, summary string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := d.conn.Exec(`UPDATE tasks SET pr_summary=?, updated_at=? WHERE id=?`, summary, now, id)
	return err
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanTask(row *sql.Row) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt *string
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.PRSummary, &t.Link, &t.Done, &createdAt, &updatedAt, &doneAt)
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

func scanTaskRow(row *sql.Rows) (*models.Task, error) {
	var t models.Task
	var createdAt, updatedAt string
	var doneAt *string
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.WorkType, &t.Tier, &t.Direction,
		&t.PRURL, &t.PRSummary, &t.Link, &t.Done, &createdAt, &updatedAt, &doneAt)
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
