package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerCalendarRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /calendar", h.calendarPage)
}

// WeekDay is one column in the calendar view.
type WeekDay struct {
	Date    time.Time
	Label   string // "Mon 9", "Tue 10", …
	DateKey string // "2006-01-02" — used to look up tasks
	IsToday bool
}

func (h *Handler) calendarPage(w http.ResponseWriter, r *http.Request) {
	// Parse ?week= offset (integer, default 0 = current week).
	weekOffset := 0
	if raw := r.URL.Query().Get("week"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			weekOffset = n
		}
	}

	// Compute Monday of the requested week (in local time, day-truncated).
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Roll back to Monday of the current week, then advance by weekOffset weeks.
	daysFromMonday := int(today.Weekday()) - int(time.Monday)
	if daysFromMonday < 0 {
		daysFromMonday += 7 // Sunday: Weekday()=0, so 0-1=-1 → 6
	}
	monday := today.AddDate(0, 0, -daysFromMonday+weekOffset*7)

	// Build the seven WeekDay descriptors (Mon … Sun).
	weekDays := make([]WeekDay, 7)
	for i := 0; i < 7; i++ {
		d := monday.AddDate(0, 0, i)
		weekDays[i] = WeekDay{
			Date:    d,
			Label:   d.Format("Mon 2"),
			DateKey: d.Format("2006-01-02"),
			IsToday: d.Equal(today),
		}
	}

	// Load all non-done tasks that have a due date.
	allTasks, err := h.db.ListTasksWithDueDates()
	if err != nil {
		http.Error(w, "failed to load tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Bucket tasks by due_date key. A task falls into this week's view if its
	// due date matches one of the seven day keys.
	tasksByDay := make(map[string][]*models.Task)
	for _, t := range allTasks {
		if t.DueDate == nil {
			continue
		}
		key := t.DueDate.Format("2006-01-02")
		tasksByDay[key] = append(tasksByDay[key], t)
	}

	// Count tasks that are before this week's Monday (overdue but not shown
	// in the current view). We'll surface them in a banner.
	var overdueBefore []*models.Task
	weekStart := monday.Format("2006-01-02")
	for _, t := range allTasks {
		if t.DueDate == nil {
			continue
		}
		if t.DueDate.Format("2006-01-02") < weekStart {
			overdueBefore = append(overdueBefore, t)
		}
	}

	prevWeek := weekOffset - 1
	nextWeek := weekOffset + 1

	// Month label for the header: "March 2026" or "Feb – Mar 2026" if the
	// week spans two months.
	monthLabel := monday.Format("January 2006")
	sunday := monday.AddDate(0, 0, 6)
	if monday.Month() != sunday.Month() {
		monthLabel = monday.Format("Jan") + " – " + sunday.Format("Jan 2006")
	}

	h.render(w, "calendar.html", map[string]interface{}{
		"Nav":           "calendar",
		"WeekDays":      weekDays,
		"TasksByDay":    tasksByDay,
		"OverdueBefore": overdueBefore,
		"WeekOffset":    weekOffset,
		"PrevWeek":      prevWeek,
		"NextWeek":      nextWeek,
		"MonthLabel":    monthLabel,
		"Today":         today,
	})
}
