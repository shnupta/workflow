package models

// TaskTemplate is a reusable task blueprint — captures work_type, description,
// and recurrence so a new task can be pre-filled from it.
type TaskTemplate struct {
	ID          string `db:"id"          json:"id"`
	Name        string `db:"name"        json:"name"`
	WorkType    string `db:"work_type"   json:"work_type"`
	Description string `db:"description" json:"description"`
	Recurrence  string `db:"recurrence"  json:"recurrence"`
	CreatedAt   string `db:"created_at"  json:"created_at"`
}
