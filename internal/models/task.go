package models

import (
	"fmt"
	"time"
)

type Task struct {
	ID          string     `db:"id"           json:"id"`
	Title       string     `db:"title"         json:"title"`
	Description string     `db:"description"   json:"description"`
	WorkType    string     `db:"work_type"     json:"work_type"`
	Tier        string     `db:"tier"          json:"tier"`
	Direction   string     `db:"direction"     json:"direction"` // "blocked_on_me" | "blocked_on_them"
	PRURL       string     `db:"pr_url"        json:"pr_url"`
	Brief       string     `db:"brief"         json:"brief"`        // auto-analysis brief from agent
	BriefStatus string     `db:"brief_status"  json:"brief_status"` // "" | "pending" | "done" | "error"
	Link        string     `db:"link"          json:"link"`
	Done        bool       `db:"done"          json:"done"`
	Archived    bool       `db:"archived"      json:"archived"`
	Position    int        `db:"position"      json:"position"`
	CreatedAt   time.Time  `db:"created_at"    json:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"    json:"updated_at"`
	DoneAt      *time.Time `db:"done_at"       json:"done_at"`
	DueDate      *time.Time `db:"due_date"       json:"due_date"`      // optional due date (date only, no time)
	TimerStarted *time.Time `db:"timer_started"  json:"timer_started"` // non-nil when timer is running
	TimerTotal   int        `db:"timer_total"    json:"timer_total"`   // accumulated seconds (not counting current run)
	Scratchpad   string     `db:"scratchpad"     json:"scratchpad"`    // free-form notes/context for this task
	BlockedBy    string     `db:"blocked_by"     json:"blocked_by"`    // ID of the task blocking this one (empty = not blocked)
	Recurrence   string     `db:"recurrence"     json:"recurrence"`    // "" | "daily" | "weekly" | "biweekly" | "monthly"
	Priority     string     `db:"priority"       json:"priority"`      // "" | "p1" | "p2" | "p3"
	Effort       string     `db:"effort"         json:"effort"`        // "" | "xs" | "s" | "m" | "l" | "xl"
	Starred      bool       `db:"starred"        json:"starred"`       // pinned to top of board
	IsFocus      bool       `db:"is_focus"       json:"is_focus"`      // today's main focus task
	SnoozedUntil *time.Time `db:"snoozed_until"  json:"snoozed_until"` // hide from board until this time (nil = not snoozed)
	Tags         []string   `db:"-"              json:"tags"`          // populated by GetTask / ListTasks (not a column)
	CommentCount     int        `db:"-"              json:"comment_count"`      // populated by ListTasks (not a column)
	HasActiveSession bool       `db:"-"              json:"has_active_session"` // populated by ListTasks — true when a session is running/idle
	LastSessionAt    *time.Time  `db:"-"              json:"last_session_at"`    // populated by ListTasks — most recent session started_at across all sessions for this task
}

// IsBlocked returns true when the task has an active blocker set.
func (t *Task) IsBlocked() bool { return t.BlockedBy != "" }

// DaysInColumn returns the number of complete calendar days the task has been
// in its current column, measured from created_at to now (UTC, day-truncated).
// A task created earlier today returns 0.
func (t *Task) DaysInColumn() int {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	created := t.CreatedAt.UTC()
	start := time.Date(created.Year(), created.Month(), created.Day(), 0, 0, 0, 0, time.UTC)
	days := int(today.Sub(start).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// AgeLabel returns a compact age string: "0d"–"6d" for the first week, then
// whole weeks ("1w", "2w", …).
func (t *Task) AgeLabel() string {
	d := t.DaysInColumn()
	if d < 7 {
		return fmt.Sprintf("%dd", d)
	}
	return fmt.Sprintf("%dw", d/7)
}

// AgeClass returns a CSS class name reflecting how long the task has been in
// its column: "age-fresh" (< 2 days), "age-warn" (2–5 days), "age-stale" (≥ 6 days).
func (t *Task) AgeClass() string {
	switch d := t.DaysInColumn(); {
	case d < 2:
		return "age-fresh"
	case d <= 5:
		return "age-warn"
	default:
		return "age-stale"
	}
}

// IsRecurring returns true when the task has a recurrence schedule set.
func (t *Task) IsRecurring() bool { return t.Recurrence != "" }

func (t *Task) DirectionLabel() string {
	if t.Direction == "blocked_on_them" {
		return "On them"
	}
	return "On me"
}

// IsOverdue returns true if the task has a due date in the past and is not done.
func (t *Task) IsOverdue() bool {
	if t.DueDate == nil || t.Done {
		return false
	}
	// Compare dates only (truncate to day)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	due := time.Date(t.DueDate.Year(), t.DueDate.Month(), t.DueDate.Day(), 0, 0, 0, 0, now.Location())
	return due.Before(today)
}

// IsDueToday returns true if the task is due today.
func (t *Task) IsDueToday() bool {
	if t.DueDate == nil || t.Done {
		return false
	}
	now := time.Now()
	return t.DueDate.Year() == now.Year() && t.DueDate.Month() == now.Month() && t.DueDate.Day() == now.Day()
}

// IsDueSoon returns true if the task is due within 2 days (today or tomorrow).
// This drives the amber badge in the card header.
func (t *Task) IsDueSoon() bool {
	if t.DueDate == nil || t.Done {
		return false
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	due := time.Date(t.DueDate.Year(), t.DueDate.Month(), t.DueDate.Day(), 0, 0, 0, 0, now.Location())
	diff := int(due.Sub(today).Hours() / 24)
	return diff >= 0 && diff <= 1
}

// ElapsedSeconds returns total elapsed seconds including any current run.
func (t *Task) ElapsedSeconds() int {
	total := t.TimerTotal
	if t.TimerStarted != nil {
		total += int(time.Since(*t.TimerStarted).Seconds())
	}
	return total
}

// ElapsedLabel formats elapsed time as "1h 23m" or "45m" or "< 1m".
func (t *Task) ElapsedLabel() string {
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

// PriorityLabel returns a human-readable label for the task's priority.
func (t *Task) PriorityLabel() string {
	switch t.Priority {
	case "p1":
		return "P1"
	case "p2":
		return "P2"
	case "p3":
		return "P3"
	default:
		return ""
	}
}

// PriorityWeight returns a sort weight for priority (lower = higher priority).
// Used for board card ordering: p1=0, p2=1, p3=2, ""=3.
func (t *Task) PriorityWeight() int {
	switch t.Priority {
	case "p1":
		return 0
	case "p2":
		return 1
	case "p3":
		return 2
	default:
		return 3
	}
}

// EffortLabel returns a display label for the task's effort estimate.
func (t *Task) EffortLabel() string {
	switch t.Effort {
	case "xs":
		return "XS"
	case "s":
		return "S"
	case "m":
		return "M"
	case "l":
		return "L"
	case "xl":
		return "XL"
	default:
		return ""
	}
}

// EffortPoints returns a rough story-points equivalent for effort estimates.
// xs=1, s=2, m=3, l=5, xl=8 (Fibonacci-ish).
func (t *Task) EffortPoints() int {
	switch t.Effort {
	case "xs":
		return 1
	case "s":
		return 2
	case "m":
		return 3
	case "l":
		return 5
	case "xl":
		return 8
	default:
		return 0
	}
}

// EffortHours returns the expected hours for the effort estimate.
// XS=0.5h, S=1.5h, M=3h, L=6h, XL=12h.
func (t *Task) EffortHours() float64 {
	switch t.Effort {
	case "xs":
		return 0.5
	case "s":
		return 1.5
	case "m":
		return 3.0
	case "l":
		return 6.0
	case "xl":
		return 12.0
	default:
		return 0
	}
}

// EffortVsActual returns a human-readable comparison of estimated vs actual time.
// Returns "" if effort is unset or no time has been tracked.
// Examples: "on track (1h 12m / 1h 30m est)", "2.1× over (3h 8m / 1h 30m est)"
func (t *Task) EffortVsActual() string {
	if t.Effort == "" {
		return ""
	}
	actual := t.ElapsedSeconds()
	if actual == 0 {
		return ""
	}
	estHours := t.EffortHours()
	estSecs := int(estHours * 3600)
	if estSecs == 0 {
		return ""
	}

	actualLabel := t.ElapsedLabel()
	estMins := int(estHours * 60)
	var estLabel string
	if estMins >= 60 {
		estLabel = fmt.Sprintf("%dh %dm", estMins/60, estMins%60)
	} else {
		estLabel = fmt.Sprintf("%dm", estMins)
	}

	ratio := float64(actual) / float64(estSecs)
	switch {
	case ratio <= 0.75:
		return fmt.Sprintf("under est (%s / %s est)", actualLabel, estLabel)
	case ratio <= 1.25:
		return fmt.Sprintf("on track (%s / %s est)", actualLabel, estLabel)
	default:
		return fmt.Sprintf("%.1f× over (%s / %s est)", ratio, actualLabel, estLabel)
	}
}

// UrgencyScore returns a normalised urgency value in [0, 100].
// Formula: (priority_weight * age_days) / effort_hours, capped and scaled.
// Higher score = needs attention sooner.
func (t *Task) UrgencyScore() int {
	// Priority weight
	pw := map[string]float64{"p1": 4.0, "p2": 2.5, "p3": 1.5, "": 1.0}
	p := pw[t.Priority]
	if p == 0 {
		p = 1.0
	}

	// Age in days since task entered the current tier
	age := 0.0
	if !t.CreatedAt.IsZero() {
		age = time.Since(t.CreatedAt).Hours() / 24.0
	}

	// Effort hours (default 3h = Medium if not set)
	effort := t.EffortHours()
	if effort == 0 {
		effort = 3.0
	}

	raw := (p * (age + 1)) / effort // +1 so brand-new tasks aren't 0
	// Scale: 0–5 → 0–100, capped
	score := (raw / 5.0) * 100.0
	if score > 100 {
		score = 100
	}
	return int(score)
}

// UrgencyLabel returns "hot", "warm", or "cold" based on UrgencyScore.
func (t *Task) UrgencyLabel() string {
	s := t.UrgencyScore()
	switch {
	case s >= 60:
		return "hot"
	case s >= 25:
		return "warm"
	default:
		return "cold"
	}
}
