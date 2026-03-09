package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/shnupta/workflow/internal/db"
)

func (h *Handler) registerStandupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /standup", h.standupPage)
	mux.HandleFunc("GET /api/standup", h.standupAPI)
}

func (h *Handler) standupPage(w http.ResponseWriter, r *http.Request) {
	day := time.Now().UTC()
	if raw := r.URL.Query().Get("date"); raw != "" {
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			day = t
		}
	}

	done, inProgress, err := h.db.DailyStandup(day)
	if err != nil {
		http.Error(w, "failed to load standup: "+err.Error(), http.StatusInternalServerError)
		return
	}

	text := buildStandupText(day, done, inProgress)

	prevDay := day.AddDate(0, 0, -1).Format("2006-01-02")
	nextDay := day.AddDate(0, 0, 1).Format("2006-01-02")
	today := time.Now().UTC().Format("2006-01-02")
	isToday := day.Format("2006-01-02") == today

	h.render(w, "standup.html", map[string]interface{}{
		"Nav":        "standup",
		"Day":        day,
		"DayLabel":   day.Format("Monday, Jan 2"),
		"IsToday":    isToday,
		"PrevDay":    prevDay,
		"NextDay":    nextDay,
		"Done":       done,
		"InProgress": inProgress,
		"Text":       text,
	})
}

func (h *Handler) standupAPI(w http.ResponseWriter, r *http.Request) {
	day := time.Now().UTC()
	if raw := r.URL.Query().Get("date"); raw != "" {
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			day = t
		}
	}

	done, inProgress, err := h.db.DailyStandup(day)
	if err != nil {
		http.Error(w, `{"error":"failed to load standup"}`, http.StatusInternalServerError)
		return
	}

	text := buildStandupText(day, done, inProgress)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"date":        day.Format("2006-01-02"),
		"done":        done,
		"in_progress": inProgress,
		"text":        text,
	})
}

// buildStandupText formats a plain-text standup message from tasks.
func buildStandupText(day time.Time, done, inProgress []*db.StandupTask) string {
	var sb strings.Builder

	if len(done) == 0 && len(inProgress) == 0 {
		sb.WriteString("Yesterday: (nothing logged)\nToday: \nBlockers: none")
		return sb.String()
	}

	if len(done) > 0 {
		sb.WriteString("Yesterday:\n")
		for _, t := range done {
			sb.WriteString("  ✅ " + t.Title)
			if el := t.ElapsedLabel(); el != "" {
				sb.WriteString(" (" + el + ")")
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("Yesterday: (nothing completed)\n")
	}

	if len(inProgress) > 0 {
		sb.WriteString("Today:\n")
		for _, t := range inProgress {
			sb.WriteString("  🔄 " + t.Title)
			if el := t.ElapsedLabel(); el != "" {
				sb.WriteString(" (" + el + ")")
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("Today: \n")
	}

	sb.WriteString("Blockers: none")
	return sb.String()
}
